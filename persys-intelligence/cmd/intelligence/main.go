package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/config"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/features"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/httpapi"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/inference"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/service"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	var provider inference.Provider
	switch cfg.ModelProvider {
	case "mock":
		provider = inference.MockProvider{}
	case "disabled":
		provider = inference.DisabledProvider{}
	case "openai", "local", "fine-tuned":
		provider = inference.OpenAICompatibleProvider{
			Endpoint: cfg.ModelEndpoint,
			APIKey:   cfg.ModelAPIKey,
			Model:    cfg.ModelName,
		}
	default:
		log.Fatalf("unsupported model provider: %s", cfg.ModelProvider)
	}

	minInterval := time.Second / time.Duration(cfg.InferenceRateLimitPerSec)
	infer := inference.New(provider, inference.EngineConfig{
		Timeout:          cfg.InferenceTimeout,
		MinInterval:      minInterval,
		FailureThreshold: cfg.InferenceFailureThreshold,
		Cooldown:         cfg.InferenceCooldown,
	})

	collector := metrics.New()
	analyzer := inference.NewAnalyzer(
		cfg.ModelProvider,
		cfg.ModelEndpoint,
		cfg.ModelAPIKey,
		cfg.ModelName,
		cfg.InferenceTimeout,
	)
	svc := service.New(
		store.NewMemoryStore(),
		features.NewStaticExtractor(cfg.DefaultWorkload),
		infer,
		analyzer,
		collector,
		cfg.Mode,
		cfg.PolicyMinConfidence,
		cfg.PolicyMaxRisk,
	)
	handler := httpapi.New(svc, collector)

	apiServer := &http.Server{
		Addr:    net.JoinHostPort(cfg.HTTPAddr, strconv.Itoa(cfg.HTTPPort)),
		Handler: handler.API(),
	}
	metricsServer := &http.Server{
		Addr:    net.JoinHostPort(cfg.MetricsAddr, strconv.Itoa(cfg.MetricsPort)),
		Handler: handler.Metrics(),
	}
	if cfg.ServerTLS {
		tlsCfg, err := loadServerTLS(cfg)
		if err != nil {
			log.Fatalf("load server TLS: %v", err)
		}
		apiServer.TLSConfig = tlsCfg
		metricsServer.TLSConfig = tlsCfg
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("intelligence API listening on %s", apiServer.Addr)
		if cfg.ServerTLS {
			if err := apiServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("api server failed: %w", err)
			}
			return
		}
		if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("api server failed: %w", err)
		}
	}()
	go func() {
		log.Printf("intelligence metrics listening on %s", metricsServer.Addr)
		if cfg.ServerTLS {
			if err := metricsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("metrics server failed: %w", err)
			}
			return
		}
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("api shutdown error: %v", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics shutdown error: %v", err)
	}
}

func loadServerTLS(cfg *config.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.ServerCertPath, cfg.ServerKeyPath)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}, nil
}
