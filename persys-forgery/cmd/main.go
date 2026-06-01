package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"strconv"
	"syscall"
	"time"

	"github.com/persys-dev/persys-cloud/persys-forgery/internal/build"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/certmanager"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/db"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-forgery/internal/forgeryv1"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/grpcapi"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/operator"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/queue"
	"github.com/persys-dev/persys-cloud/persys-forgery/utils"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := utils.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	shutdownTracer := setupTracer()
	defer shutdownTracer()

	if err := db.InitMySQL(cfg.MySQLDSN); err != nil {
		log.Fatalf("failed to connect to MySQL: %v", err)
	}

	workspaceDir := cfg.Build.Workspace
	orchestrator := build.NewOrchestrator(workspaceDir)
	orchestrator.SetOperatorClient(&operator.Client{})
	orchestrator.SetDockerClient(&build.Docker{})

	go queue.StartRedisWorker(cfg, orchestrator)
	go queue.StartWebhookWorker(cfg)

	certMgr, err := certmanager.NewFromConfig(cfg, logrus.New())
	if err != nil {
		log.Fatalf("failed to initialize cert manager: %v", err)
	}
	if err := certMgr.Start(ctx); err != nil {
		log.Fatalf("failed to start cert manager: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB})
	grpcSvc := grpcapi.NewService(cfg, db.DB, rdb, cfg.Redis.WebhookQueueKey, cfg.Redis.BuildQueueKey, cfg.Redis.PipelineStatusQueue)

	lis, err := net.Listen("tcp", cfg.GRPC.Addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", cfg.GRPC.Addr, err)
	}

	grpcOpts := []grpc.ServerOption{}
	if cfg.TLS.Enabled {
		tlsCfg, err := loadServerTLSConfig(cfg)
		if err != nil {
			log.Fatalf("failed to load TLS config: %v", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsCfg)))
	}

	grpcOpts = append(grpcOpts, grpc.UnaryInterceptor(otelUnaryServerInterceptor("persys-forgery")))
	grpcServer := grpc.NewServer(grpcOpts...)
	forgeryv1.RegisterForgeryControlServer(grpcServer, grpcSvc)
	reflection.Register(grpcServer)

	go func() {
		log.Printf("starting forgery gRPC server on %s", cfg.GRPC.Addr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("forgery gRPC server failed: %v", err)
		}
	}()

	metricsAddr := envOr("PERSYS_FORGERY_METRICS_ADDR", "0.0.0.0")
	metricsPort := envIntOr("PERSYS_FORGERY_METRICS_PORT", 8095)
	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(metrics.RenderPrometheus()))
	})
	metricsMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	metricsServer := &http.Server{
		Addr:    net.JoinHostPort(metricsAddr, strconv.Itoa(metricsPort)),
		Handler: metricsMux,
	}
	go func() {
		log.Printf("starting forgery metrics server on %s", metricsServer.Addr)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("forgery metrics server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down forgery")

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer shutdownCancel()
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}
	_ = metricsServer.Shutdown(shutdownCtx)
}

func setupTracer() func() {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_JAEGER_ENDPOINT"))
	}
	serviceName := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	if serviceName == "" {
		serviceName = "persys-forgery"
	}
	if endpoint == "" {
		return func() {}
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithInsecure(), otlptracehttp.WithEndpoint(endpoint)}
	exporter, err := otlptracehttp.New(context.Background(), opts...)
	if err != nil {
		log.Printf("failed to create OTLP exporter: %v", err)
		return func() {}
	}
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exporter),
		tracesdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		)),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

type metadataCarrier metadata.MD

func (m metadataCarrier) Get(key string) string {
	values := metadata.MD(m).Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (m metadataCarrier) Set(key string, value string) {
	md := metadata.MD(m)
	md.Set(strings.ToLower(key), value)
}

func (m metadataCarrier) Keys() []string {
	md := metadata.MD(m)
	out := make([]string, 0, len(md))
	for k := range md {
		out = append(out, k)
	}
	return out
}

func otelUnaryServerInterceptor(service string) grpc.UnaryServerInterceptor {
	tr := otel.Tracer(service)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		ctx = otel.GetTextMapPropagator().Extract(ctx, metadataCarrier(md))
		ctx, span := tr.Start(ctx, info.FullMethod, trace.WithSpanKind(trace.SpanKindServer))
		resp, err := handler(ctx, req)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
		return resp, err
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func loadServerTLSConfig(cfg *utils.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLS.CertPath, cfg.TLS.KeyPath)
	if err != nil {
		return nil, err
	}
	caBytes, err := os.ReadFile(cfg.TLS.CAPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("failed to append CA certificate")
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
	}
	if cfg.TLS.RequireClientCert {
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return tlsCfg, nil
}
