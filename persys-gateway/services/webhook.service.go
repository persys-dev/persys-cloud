package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-gateway/internal/forgeryv1"
	"github.com/persys-dev/persys-cloud/persys-gateway/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type WebhookService interface {
	Start(ctx context.Context)
	HandleGitHubWebhook(ctx context.Context, headers http.Header, body []byte) (int, string)
}

type webhookService struct {
	cfg            *config.Config
	tlsConfig      *tls.Config
	collection     *mongo.Collection
	replayTTL      time.Duration
	baseBackoff    time.Duration
	retries        int
	cacheMu        sync.Mutex
	deliverySeenAt map[string]time.Time
	jobs           chan forwardJob
}

type forwardJob struct {
	deliveryID string
	eventName  string
	repo       string
	clusterID  string
	body       []byte
	attempt    int
}

type githubPushEnvelope struct {
	Ref    string `json:"ref"`
	Before string `json:"before"`
	After  string `json:"after"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

func NewWebhookService(cfg *config.Config, tlsClient *tls.Config, collection *mongo.Collection) (WebhookService, error) {
	replayTTL, err := time.ParseDuration(cfg.Webhook.ReplayTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook.replay_ttl: %w", err)
	}
	baseBackoff, err := time.ParseDuration(cfg.Webhook.ForwardBaseBackoff)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook.forward_base_backoff: %w", err)
	}
	if strings.TrimSpace(cfg.Forgery.GRPCAddr) == "" {
		return nil, fmt.Errorf("forgery.grpc_addr is required")
	}

	return &webhookService{
		cfg:            cfg,
		tlsConfig:      tlsClient,
		collection:     collection,
		replayTTL:      replayTTL,
		baseBackoff:    baseBackoff,
		retries:        cfg.Webhook.ForwardRetries,
		deliverySeenAt: map[string]time.Time{},
		jobs:           make(chan forwardJob, 1024),
	}, nil
}

func (w *webhookService) Start(ctx context.Context) {
	for i := 0; i < 2; i++ {
		go w.runWorker(ctx)
	}
}

func (w *webhookService) runWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-w.jobs:
			w.processJob(ctx, job)
		}
	}
}

func (w *webhookService) processJob(ctx context.Context, job forwardJob) {
	attempt := job.attempt + 1
	err := w.forwardGRPC(ctx, job.eventName, job.repo, job.clusterID, job.body, job.deliveryID)
	if err == nil {
		w.persist(ctx, models.WebhookEvent{DeliveryID: job.deliveryID, EventName: job.eventName, Repository: job.repo, ClusterID: job.clusterID, Verified: true, Status: "forwarded", Attempts: attempt, LastUpdatedAt: time.Now().UTC()})
		return
	}

	if attempt >= w.retries {
		w.persist(ctx, models.WebhookEvent{DeliveryID: job.deliveryID, EventName: job.eventName, Repository: job.repo, ClusterID: job.clusterID, Verified: true, Status: "failed", Attempts: attempt, LastError: err.Error(), LastUpdatedAt: time.Now().UTC()})
		log.Printf("webhook forwarding failed permanently delivery=%s repo=%s err=%v", job.deliveryID, job.repo, err)
		return
	}

	next := time.Now().UTC().Add(w.backoffForAttempt(attempt))
	w.persist(ctx, models.WebhookEvent{DeliveryID: job.deliveryID, EventName: job.eventName, Repository: job.repo, ClusterID: job.clusterID, Verified: true, Status: "retrying", Attempts: attempt, LastError: err.Error(), NextRetryAt: next, LastUpdatedAt: time.Now().UTC()})

	go func(retryJob forwardJob, wait time.Duration) {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			retryJob.attempt = attempt
			select {
			case <-ctx.Done():
				return
			case w.jobs <- retryJob:
			}
		}
	}(job, w.backoffForAttempt(attempt))
}

func (w *webhookService) backoffForAttempt(attempt int) time.Duration {
	backoff := w.baseBackoff
	for i := 1; i < attempt; i++ {
		backoff *= 2
	}
	return backoff
}

func (w *webhookService) HandleGitHubWebhook(ctx context.Context, headers http.Header, body []byte) (int, string) {
	eventName := strings.TrimSpace(headers.Get("X-GitHub-Event"))
	sig := strings.TrimSpace(headers.Get("X-Hub-Signature-256"))
	deliveryID := strings.TrimSpace(headers.Get("X-GitHub-Delivery"))
	contentType := strings.ToLower(strings.TrimSpace(headers.Get("Content-Type")))

	if eventName == "" || sig == "" || deliveryID == "" {
		return http.StatusBadRequest, "missing required GitHub webhook headers"
	}
	if !strings.Contains(contentType, "application/json") {
		return http.StatusBadRequest, "content-type must be application/json"
	}

	if !w.reserveDelivery(deliveryID) {
		return http.StatusConflict, "duplicate delivery id"
	}

	repo, err := extractRepository(body)
	if err != nil {
		return http.StatusBadRequest, "invalid webhook payload"
	}

	secret := w.secretForRepository(repo)
	if !validateSignature(secret, sig, body) {
		return http.StatusUnauthorized, "invalid webhook signature"
	}

	clusterID := w.resolveClusterForRepository(repo)
	w.persist(ctx, models.WebhookEvent{DeliveryID: deliveryID, EventName: eventName, Repository: repo, ClusterID: clusterID, Verified: true, Status: "accepted", Attempts: 0, ReceivedAt: time.Now().UTC(), LastUpdatedAt: time.Now().UTC()})

	job := forwardJob{deliveryID: deliveryID, eventName: eventName, repo: repo, clusterID: clusterID, body: body}
	select {
	case w.jobs <- job:
	default:
		w.persist(ctx, models.WebhookEvent{DeliveryID: deliveryID, EventName: eventName, Repository: repo, ClusterID: clusterID, Verified: true, Status: "buffer_full", Attempts: 0, LastError: "retry buffer full", LastUpdatedAt: time.Now().UTC()})
	}

	return http.StatusOK, "ok"
}

func (w *webhookService) persist(ctx context.Context, event models.WebhookEvent) {
	if w.collection == nil {
		return
	}
	now := time.Now().UTC()
	if event.LastUpdatedAt.IsZero() {
		event.LastUpdatedAt = now
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = now
	}

	update := bson.M{"$set": event, "$setOnInsert": bson.M{"delivery_id": event.DeliveryID, "received_at": event.ReceivedAt}}
	_, err := w.collection.UpdateOne(ctx, bson.M{"delivery_id": event.DeliveryID}, update, options.Update().SetUpsert(true))
	if err != nil {
		log.Printf("failed to persist webhook metadata delivery=%s err=%v", event.DeliveryID, err)
	}
}

func (w *webhookService) reserveDelivery(deliveryID string) bool {
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()

	now := time.Now().UTC()
	for k, seenAt := range w.deliverySeenAt {
		if now.Sub(seenAt) > w.replayTTL {
			delete(w.deliverySeenAt, k)
		}
	}

	if _, exists := w.deliverySeenAt[deliveryID]; exists {
		return false
	}
	w.deliverySeenAt[deliveryID] = now
	return true
}

func (w *webhookService) secretForRepository(repo string) string {
	if secret := strings.TrimSpace(w.cfg.Webhook.RepositorySecrets[repo]); secret != "" {
		return secret
	}
	return w.cfg.GitHub.DefaultSecret
}

func validateSignature(secret, receivedSignature string, body []byte) bool {
	if !strings.HasPrefix(receivedSignature, "sha256=") {
		return false
	}
	receivedHex := strings.TrimPrefix(receivedSignature, "sha256=")
	received, err := hex.DecodeString(receivedHex)
	if err != nil {
		return false
	}

	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write(body)
	expected := h.Sum(nil)
	return hmac.Equal(expected, received)
}

func extractRepository(body []byte) (string, error) {
	var payload githubPushEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Repository.FullName) != "" {
		return payload.Repository.FullName, nil
	}
	if payload.Repository.Owner.Login != "" && payload.Repository.Name != "" {
		return payload.Repository.Owner.Login + "/" + payload.Repository.Name, nil
	}
	return "", fmt.Errorf("repository metadata missing")
}

func (w *webhookService) resolveClusterForRepository(repo string) string {
	if clusterID := strings.TrimSpace(w.cfg.Scheduler.RepositoryClusterMap[repo]); clusterID != "" {
		return clusterID
	}
	return w.cfg.Scheduler.DefaultClusterID
}

func (w *webhookService) forwardGRPC(ctx context.Context, eventName, repo, clusterID string, body []byte, deliveryID string) error {
	meta := githubPushEnvelope{}
	if err := json.Unmarshal(body, &meta); err != nil {
		return fmt.Errorf("parse webhook body: %w", err)
	}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, w.cfg.Forgery.GRPCAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(w.tlsConfig)),
		grpc.WithBlock(),
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := forgeryv1.NewForgeryControlClient(conn)
	resp, err := client.ForwardWebhook(ctx, &forgeryv1.ForwardWebhookRequest{
		DeliveryId:  deliveryID,
		EventType:   eventName,
		Repository:  repo,
		ClusterId:   clusterID,
		Sender:      meta.Sender.Login,
		Ref:         meta.Ref,
		Before:      meta.Before,
		After:       meta.After,
		PayloadJson: string(body),
		Verified:    true,
	})
	if err != nil {
		return err
	}
	if !resp.GetAccepted() {
		return fmt.Errorf("forgery rejected webhook: %s", resp.GetMessage())
	}
	return nil
}
