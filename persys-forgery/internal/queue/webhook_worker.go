package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
	"github.com/persys-dev/persys-cloud/persys-forgery/utils"
	"github.com/redis/go-redis/v9"
)

type VerifiedWebhookEvent struct {
	DeliveryID string                 `json:"delivery_id"`
	EventType  string                 `json:"event_type"`
	Repository string                 `json:"repository"`
	ClusterID  string                 `json:"cluster_id"`
	Sender     string                 `json:"sender"`
	Ref        string                 `json:"ref"`
	Before     string                 `json:"before"`
	After      string                 `json:"after"`
	Payload    map[string]interface{} `json:"payload"`
	ReceivedAt time.Time              `json:"received_at"`
}

type PipelineStatusEvent struct {
	DeliveryID string    `json:"delivery_id"`
	Repository string    `json:"repository"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	Timestamp  time.Time `json:"timestamp"`
}

func StartWebhookWorker(cfg *utils.Config) {
	rdb := redis.NewClient(&redis.Options{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB})
	ctx := context.Background()

	for {
		res, err := rdb.BLPop(ctx, 0, cfg.Redis.WebhookQueueKey).Result()
		if err != nil {
			log.Println("Webhook worker Redis error:", err)
			continue
		}
		if len(res) < 2 {
			continue
		}

		var event VerifiedWebhookEvent
		if err := json.Unmarshal([]byte(res[1]), &event); err != nil {
			log.Println("Failed to unmarshal webhook event:", err)
			continue
		}

		publishPipelineStatus(ctx, rdb, cfg.Redis.PipelineStatusQueue, PipelineStatusEvent{
			DeliveryID: event.DeliveryID,
			Repository: event.Repository,
			Status:     "webhook_received",
			Message:    "Webhook event accepted by forgery",
			Timestamp:  time.Now().UTC(),
		})

		buildReq := buildRequestFromWebhook(event)
		payload, err := json.Marshal(buildReq)
		if err != nil {
			publishPipelineStatus(ctx, rdb, cfg.Redis.PipelineStatusQueue, PipelineStatusEvent{
				DeliveryID: event.DeliveryID,
				Repository: event.Repository,
				Status:     "webhook_failed",
				Message:    fmt.Sprintf("Failed to encode build request: %v", err),
				Timestamp:  time.Now().UTC(),
			})
			continue
		}

		if err := rdb.LPush(ctx, cfg.Redis.BuildQueueKey, payload).Err(); err != nil {
			publishPipelineStatus(ctx, rdb, cfg.Redis.PipelineStatusQueue, PipelineStatusEvent{
				DeliveryID: event.DeliveryID,
				Repository: event.Repository,
				Status:     "webhook_failed",
				Message:    fmt.Sprintf("Failed to enqueue build: %v", err),
				Timestamp:  time.Now().UTC(),
			})
			continue
		}

		publishPipelineStatus(ctx, rdb, cfg.Redis.PipelineStatusQueue, PipelineStatusEvent{
			DeliveryID: event.DeliveryID,
			Repository: event.Repository,
			Status:     "build_enqueued",
			Message:    "Build request enqueued from webhook",
			Timestamp:  time.Now().UTC(),
		})
	}
}

func buildRequestFromWebhook(event VerifiedWebhookEvent) models.BuildRequest {
	branch := strings.TrimPrefix(event.Ref, "refs/heads/")
	if branch == "" {
		branch = extractString(event.Payload, "ref")
		branch = strings.TrimPrefix(branch, "refs/heads/")
	}
	source := event.Repository
	if !strings.Contains(source, "://") && strings.Contains(source, "/") {
		source = "https://github.com/" + source + ".git"
	}
	commitSHA := strings.TrimSpace(event.After)
	if commitSHA == "" {
		commitSHA = extractString(event.Payload, "after")
	}
	webhookDataMap := map[string]interface{}{
		"event_type": event.EventType,
		"repository": event.Repository,
		"sender":     event.Sender,
		"ref":        event.Ref,
		"before":     event.Before,
		"after":      event.After,
	}

	webhookData := models.WebhookData{
		EventType:  event.EventType,
		Repository: event.Repository,
		Sender:     event.Sender,
		Ref:        event.Ref,
		Before:     event.Before,
		After:      event.After,
	}
	if pr := extractPullRequest(event.Payload); pr != nil {
		webhookData.PullRequest = pr
		webhookDataMap["pull_request"] = pr
	}

	return models.BuildRequest{
		ID:          event.DeliveryID,
		ProjectName: event.Repository,
		Type:        models.BuildTypeDockerfile,
		Source:      source,
		CommitHash:  commitSHA,
		Branch:      branch,
		Strategy:    "local",
		WebhookData: webhookDataMap,
		Metadata: map[string]interface{}{
			"cluster_id":  event.ClusterID,
			"delivery_id": event.DeliveryID,
		},
		CreatedAt: time.Now().UTC(),
	}
}

func extractString(payload map[string]interface{}, key string) string {
	if payload == nil {
		return ""
	}
	val, ok := payload[key]
	if !ok {
		return ""
	}
	s, ok := val.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func extractPullRequest(payload map[string]interface{}) *struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
} {
	if payload == nil {
		return nil
	}
	rawPR, ok := payload["pull_request"]
	if !ok {
		return nil
	}
	prObj, ok := rawPR.(map[string]interface{})
	if !ok {
		return nil
	}

	number := 0
	if v, ok := prObj["number"].(float64); ok {
		number = int(v)
	}
	title, _ := prObj["title"].(string)
	state, _ := prObj["state"].(string)
	if number == 0 && strings.TrimSpace(title) == "" && strings.TrimSpace(state) == "" {
		return nil
	}
	return &struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
	}{
		Number: number,
		Title:  strings.TrimSpace(title),
		State:  strings.TrimSpace(state),
	}
}

func publishPipelineStatus(ctx context.Context, rdb *redis.Client, queue string, evt PipelineStatusEvent) {
	payload, err := json.Marshal(evt)
	if err != nil {
		log.Printf("failed to marshal pipeline status event: %v", err)
		return
	}
	if err := rdb.LPush(ctx, queue, payload).Err(); err != nil {
		log.Printf("failed to publish pipeline status event: %v", err)
	}
}
