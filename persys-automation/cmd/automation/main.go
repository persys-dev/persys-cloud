package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	automationv1 "github.com/persys-dev/persys-cloud/persys-automation/internal/automationv1"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/config"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/engine"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/forgery"
	promclient "github.com/persys-dev/persys-cloud/persys-automation/internal/prometheus"
	schedulerclient "github.com/persys-dev/persys-cloud/persys-automation/internal/scheduler"
	"github.com/persys-dev/persys-cloud/persys-automation/internal/store"
	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	certCtx, certCancel := context.WithCancel(context.Background())
	defer certCancel()
	if err := initCertificates(certCtx, cfg); err != nil {
		log.Fatalf("init certificates: %v", err)
	}

	sched, err := schedulerclient.New(schedulerclient.Config{
		Address:          cfg.SchedulerAddr,
		TLSEnabled:       cfg.SchedulerTLS,
		CAPath:           cfg.SchedulerCAPath,
		ClientCertPath:   cfg.ClientCertPath,
		ClientKeyPath:    cfg.ClientKeyPath,
		InsecureSkipCert: cfg.InsecureSkipTLS,
	})
	if err != nil {
		log.Fatalf("init scheduler client: %v", err)
	}
	defer sched.Close()

	policyStore, pgDB, err := initStore(context.Background(), cfg)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}
	defer policyStore.Close()

	prom := promclient.New(cfg.PrometheusURL)
	var pipelineReader engine.PipelineStatusReader
	var forgeryCollector *forgery.Collector
	if cfg.ForgeryRedisEnabled {
		forgeryCollector = forgery.NewCollector(cfg.ForgeryRedisAddr, cfg.ForgeryRedisPass, cfg.ForgeryRedisDB, cfg.ForgeryPipelineKey)
		pipelineReader = forgeryCollector
		log.Printf("forgery pipeline collector enabled key=%s redis=%s", cfg.ForgeryPipelineKey, cfg.ForgeryRedisAddr)
	}
	eng := engine.New(policyStore, prom, sched, pipelineReader)

	grpcServer := newGRPCServer(cfg)
	automationv1.RegisterAutomationControlServer(grpcServer, engine.NewGRPCService(policyStore, eng))

	grpcLis, err := net.Listen("tcp", net.JoinHostPort(cfg.GRPCAddr, strconv.Itoa(cfg.GRPCPort)))
	if err != nil {
		log.Fatalf("listen grpc: %v", err)
	}

	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(engine.MetricsText()))
	})
	metricsMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	metricsServer := &http.Server{Addr: net.JoinHostPort(cfg.GRPCAddr, strconv.Itoa(cfg.MetricsPort)), Handler: metricsMux}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if forgeryCollector != nil {
		go forgeryCollector.Start(ctx)
	}

	var (
		evalCancel context.CancelFunc
	)
	startEvaluator := func() {
		if evalCancel != nil {
			return
		}
		runCtx, runCancel := context.WithCancel(ctx)
		evalCancel = runCancel
		go engine.StartPeriodicEvaluation(runCtx, eng, cfg.EvalInterval)
		log.Printf("automation evaluator started (leader=true)")
	}
	stopEvaluator := func() {
		if evalCancel == nil {
			return
		}
		evalCancel()
		evalCancel = nil
		log.Printf("automation evaluator stopped (leader=false)")
	}

	if cfg.LeaderElectionEnabled && pgDB != nil {
		elector := store.NewPostgresLeaderElector(pgDB, cfg.LeaderElectionLockID, cfg.LeaderElectionPollInterval)
		leadership := elector.Start(ctx)
		go func() {
			for isLeader := range leadership {
				if isLeader {
					startEvaluator()
					continue
				}
				stopEvaluator()
			}
		}()
		log.Printf("leader election enabled lock_id=%d poll_interval=%s", cfg.LeaderElectionLockID, cfg.LeaderElectionPollInterval)
	} else {
		startEvaluator()
		log.Printf("leader election disabled")
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("automation gRPC listening on %s:%d", cfg.GRPCAddr, cfg.GRPCPort)
		if err := grpcServer.Serve(grpcLis); err != nil {
			errCh <- fmt.Errorf("grpc server failed: %w", err)
		}
	}()
	go func() {
		log.Printf("automation metrics listening on %s:%d", cfg.GRPCAddr, cfg.MetricsPort)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("metrics server failed: %w", err)
		}
	}()

	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	select {
	case <-sigCtx.Done():
	case err := <-errCh:
		log.Printf("server error: %v", err)
	}

	cancel()
	stopEvaluator()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer shutdownCancel()
	grpcServer.GracefulStop()
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics shutdown error: %v", err)
	}
	certCancel()
}

func initStore(ctx context.Context, cfg *config.Config) (store.PolicyStore, *sql.DB, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.StoreBackend)) {
	case "memory":
		return store.NewMemoryStore(), nil, nil
	case "postgres":
		pgStore, err := store.NewPostgresStore(ctx, cfg.PostgresDSN)
		if err != nil {
			return nil, nil, err
		}
		return pgStore, pgStore.DB(), nil
	default:
		return nil, nil, fmt.Errorf("unsupported store backend %q", cfg.StoreBackend)
	}
}

func initCertificates(ctx context.Context, cfg *config.Config) error {
	tlsEnabled := cfg.SchedulerTLS || cfg.ServerTLS
	certCfg := certmanager.Config{
		TLSEnabled:  tlsEnabled,
		TLSCertPath: cfg.ClientCertPath,
		TLSKeyPath:  cfg.ClientKeyPath,
		TLSCAPath:   cfg.SchedulerCAPath,

		VaultEnabled:       cfg.VaultEnabled,
		VaultAddr:          cfg.VaultAddr,
		VaultAuthMethod:    cfg.VaultAuthMethod,
		VaultToken:         cfg.VaultToken,
		VaultAppRoleID:     cfg.VaultAppRoleID,
		VaultAppSecretID:   cfg.VaultAppSecretID,
		VaultPKIMount:      cfg.VaultPKIMount,
		VaultPKIRole:       cfg.VaultPKIRole,
		VaultCertTTL:       cfg.VaultCertTTL,
		VaultServiceName:   cfg.VaultServiceName,
		VaultServiceDomain: cfg.VaultServiceDomain,
		VaultRetryInterval: cfg.VaultRetryInterval,

		BindHost: cfg.GRPCAddr,
	}
	logger := logrus.New()
	manager := certmanager.NewManager(certCfg, logger)
	return manager.Start(ctx)
}

func newGRPCServer(cfg *config.Config) *grpc.Server {
	if !cfg.ServerTLS {
		return grpc.NewServer()
	}
	tlsCfg, err := loadServerTLS(cfg)
	if err != nil {
		log.Fatalf("load server TLS: %v", err)
	}
	return grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
}

func loadServerTLS(cfg *config.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.ServerCertPath, cfg.ServerKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load server cert/key: %w", err)
	}
	caBytes, err := os.ReadFile(cfg.ServerCAPath)
	if err != nil {
		return nil, fmt.Errorf("read server CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("append server CA failed")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}
