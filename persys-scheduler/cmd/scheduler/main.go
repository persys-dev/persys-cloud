package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/auth"
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

	metricspkg.Register()

	otelShutdown, err := telemetry.SetupOpenTelemetry(context.Background(), "persys-scheduler")
	if err != nil {
		logger.WithError(err).Fatal("failed to initialize OpenTelemetry")
	}

	caFile := os.Getenv("CA_FILE")
	cfsslURL := os.Getenv("CFSSL_API_URL")
	commonName := os.Getenv("CERT_COMMON_NAME")
	organization := os.Getenv("CERT_ORGANIZATION")

	if caFile == "" {
		caFile = "/etc/prow/certs/ca.pem"
	}
	if cfsslURL == "" {
		cfsslURL = "https://persys-cfssl:8888"
	}
	if commonName == "" {
		commonName = "persys-scheduler"
	}
	if organization == "" {
		organization = "persys"
	}

	var tlsConfig *tls.Config
	if !*insecure {
		certManager := auth.NewCertificateManager(caFile, cfsslURL, commonName, organization)
		if err := certManager.EnsureCertificate(); err != nil {
			logger.WithError(err).Fatal("failed to ensure certificate")
		}
		cert, err := tls.LoadX509KeyPair(certManager.CertPath, certManager.KeyPath)
		if err != nil {
			logger.WithError(err).Fatal("failed to load certificate pair")
		}
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			logger.WithError(err).WithField("ca_file", caFile).Fatal("failed to read CA cert")
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			logger.Fatal("failed to append CA cert to pool")
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientCAs:    caCertPool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS12,
		}
	} else {
		logger.Warn("starting scheduler in insecure mode: mTLS disabled")
	}

	sched, err := scheduler.NewScheduler()
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

	grpcPort := os.Getenv("GRPC_PORT")
	if grpcPort == "" {
		grpcPort = "8085"
	}
	lis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		logger.WithError(err).WithField("port", grpcPort).Fatal("failed to listen on gRPC port")
	}

	grpcOpts := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.UnaryInterceptor(metricspkg.GRPCUnaryServerInterceptor()),
		grpc.StreamInterceptor(metricspkg.GRPCStreamServerInterceptor()),
	}
	var grpcServer *grpc.Server
	if *insecure {
		grpcServer = grpc.NewServer(grpcOpts...)
	} else {
		grpcServer = grpc.NewServer(append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))...)
	}
	controlv1.RegisterAgentControlServer(grpcServer, grpcapi.NewService(sched))
	reflection.Register(grpcServer)

	metricsPort := os.Getenv("METRICS_PORT")
	if metricsPort == "" {
		metricsPort = "8084"
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", gootelhttp.NewHandler(promhttp.Handler(), "scheduler.metrics"))
	metricsMux.Handle("/health", gootelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}), "scheduler.health"))
	metricsServer := &http.Server{Addr: ":" + metricsPort, Handler: metricsMux}

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
	if err := otelShutdown(shutdownCtx); err != nil {
		logger.WithError(err).Warn("OpenTelemetry shutdown error")
	}

	if ok := sched.WaitForBackground(5 * time.Second); !ok {
		logger.Warn("timed out waiting for scheduler background workers to stop")
	}
}
