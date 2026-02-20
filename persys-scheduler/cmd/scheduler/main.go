package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/auth"
	cfgpkg "github.com/persys-dev/persys-cloud/persys-scheduler/internal/config"
	controlv1 "github.com/persys-dev/persys-cloud/persys-scheduler/internal/controlv1"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/grpcapi"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	metricspkg "github.com/persys-dev/persys-cloud/persys-scheduler/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/scheduler"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	gootelhttp "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
)

func main() {
	logger := logging.C("cmd.scheduler")
	insecure := flag.Bool("insecure", false, "run scheduler gRPC without mTLS (testing only)")
	flag.Parse()

	cfg, err := cfgpkg.Load(*insecure)
	if err != nil {
		logger.WithError(err).Fatal("failed to load scheduler configuration")
	}

	metricspkg.Register()

	otelShutdown, err := telemetry.SetupOpenTelemetry(context.Background(), "persys-scheduler", cfg)
	if err != nil {
		logger.WithError(err).Fatal("failed to initialize OpenTelemetry")
	}

	var tlsConfig *tls.Config
	var certCancel context.CancelFunc
	if !cfg.Insecure {
		certCfg := auth.Config{
			TLSEnabled:  cfg.TLSEnabled,
			ExternalIP:  cfg.ExternalIP,
			TLSCertPath: cfg.TLSCertPath,
			TLSKeyPath:  cfg.TLSKeyPath,
			TLSCAPath:   cfg.TLSCAPath,

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
		certMgr := auth.NewManager(certCfg, logger.Logger)
		certCtx, cancel := context.WithCancel(context.Background())
		certCancel = cancel
		if err := certMgr.Start(certCtx); err != nil {
			logger.WithError(err).Fatal("failed to initialize certificate manager")
		}

		provider := &dynamicTLSProvider{
			certPath: certCfg.TLSCertPath,
			keyPath:  certCfg.TLSKeyPath,
			caPath:   certCfg.TLSCAPath,
		}
		if _, err := provider.getConfig(); err != nil {
			logger.WithError(err).Fatal("failed to load TLS config")
		}
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				return provider.getConfig()
			},
		}
	} else {
		logger.Warn("starting scheduler in insecure mode: mTLS disabled")
	}

	sched, err := scheduler.NewScheduler(cfg)
	if err != nil {
		logger.WithError(err).Fatal("failed to initialize scheduler")
	}
	defer sched.Close()
	if err := sched.RefreshStateMetrics(); err != nil {
		logger.WithError(err).Warn("failed to initialize scheduler state metrics")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched.StartMonitoring(ctx)
	sched.StartReconciliation(ctx)

	grpcPort := strconv.Itoa(cfg.GRPCPort)
	if err := sched.RegisterSchedulerSelfInCoreDNS(cfg.GRPCPort); err != nil {
		logger.WithError(err).Warn("failed to self-register scheduler in CoreDNS")
	}

	lis, err := net.Listen("tcp", net.JoinHostPort(cfg.GRPCAddr, grpcPort))
	if err != nil {
		logger.WithError(err).WithField("port", grpcPort).Fatal("failed to listen on gRPC port")
	}

	grpcOpts := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.UnaryInterceptor(metricspkg.GRPCUnaryServerInterceptor()),
		grpc.StreamInterceptor(metricspkg.GRPCStreamServerInterceptor()),
	}
	var grpcServer *grpc.Server
	if cfg.Insecure {
		grpcServer = grpc.NewServer(grpcOpts...)
	} else {
		grpcServer = grpc.NewServer(append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))...)
	}
	controlv1.RegisterAgentControlServer(grpcServer, grpcapi.NewService(sched))
	reflection.Register(grpcServer)

	metricsPort := strconv.Itoa(cfg.MetricsPort)
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", gootelhttp.NewHandler(promhttp.Handler(), "scheduler.metrics"))
	metricsMux.Handle("/health", gootelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		mode, reason, changedAt := sched.ModeSnapshot()
		payload, _ := json.Marshal(map[string]string{
			"status":        "ok",
			"mode":          string(mode),
			"reason":        reason,
			"modeChangedAt": changedAt.Format(time.RFC3339),
		})
		_, _ = w.Write(payload)
	}), "scheduler.health"))
	metricsServer := &http.Server{Addr: net.JoinHostPort(cfg.GRPCAddr, metricsPort), Handler: metricsMux}

	serverErrCh := make(chan error, 2)

	go func() {
		logger.WithField("port", grpcPort).Info("starting scheduler gRPC server")
		if err := grpcServer.Serve(lis); err != nil {
			serverErrCh <- fmt.Errorf("gRPC server failed: %w", err)
		}
	}()
	go func() {
		logger.WithField("port", metricsPort).Info("starting scheduler metrics server")
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErrCh <- fmt.Errorf("metrics server failed: %w", err)
		}
	}()

	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	select {
	case <-sigCtx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErrCh:
		logger.WithError(err).Error("server exited unexpectedly")
	}

	logger.Info("shutting down scheduler servers")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	grpcStopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcStopped)
	}()
	select {
	case <-grpcStopped:
	case <-shutdownCtx.Done():
		logger.Warn("gRPC graceful stop timed out; forcing stop")
		grpcServer.Stop()
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.WithError(err).Error("metrics server shutdown error")
	}
	if certCancel != nil {
		certCancel()
	}
	if err := otelShutdown(shutdownCtx); err != nil {
		logger.WithError(err).Warn("OpenTelemetry shutdown error")
	}

	if ok := sched.WaitForBackground(5 * time.Second); !ok {
		logger.Warn("timed out waiting for scheduler background workers to stop")
	}
}

type dynamicTLSProvider struct {
	certPath string
	keyPath  string
	caPath   string

	mu          sync.RWMutex
	cached      *tls.Config
	certModTime time.Time
	keyModTime  time.Time
	caModTime   time.Time
}

func (d *dynamicTLSProvider) getConfig() (*tls.Config, error) {
	certInfo, err := os.Stat(d.certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat server certificate: %w", err)
	}
	keyInfo, err := os.Stat(d.keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat server key: %w", err)
	}
	caInfo, err := os.Stat(d.caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat CA certificate: %w", err)
	}

	d.mu.RLock()
	cached := d.cached
	certUnchanged := d.certModTime.Equal(certInfo.ModTime())
	keyUnchanged := d.keyModTime.Equal(keyInfo.ModTime())
	caUnchanged := d.caModTime.Equal(caInfo.ModTime())
	d.mu.RUnlock()
	if cached != nil && certUnchanged && keyUnchanged && caUnchanged {
		return cached, nil
	}

	keyPair, err := tls.LoadX509KeyPair(d.certPath, d.keyPath)
	if err != nil {
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}
	caCert, err := os.ReadFile(d.caPath)
	if err != nil {
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	updated := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{keyPair},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}

	d.mu.Lock()
	d.cached = updated
	d.certModTime = certInfo.ModTime()
	d.keyModTime = keyInfo.ModTime()
	d.caModTime = caInfo.ModTime()
	d.mu.Unlock()
	return updated, nil
}
