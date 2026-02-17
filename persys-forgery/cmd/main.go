package main

import (
	"context"
	"fmt"
	"log"

	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/build"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/db"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/handlers"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/operator"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/queue"
	secretsPkg "github.com/persys-dev/persys-cloud/persys-forgery/internal/secrets"
	"github.com/persys-dev/persys-cloud/persys-forgery/utils"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	gootelgin "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

func setupObservability(ctx context.Context) (func(), error) {
	// OTLP HTTP exporter (replaces Jaeger)
	otlpExp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint("localhost:4318"), otlptracehttp.WithInsecure())
	if err != nil {
		return nil, err
	}
	// Prometheus exporter
	promExp, err := otelprom.New()
	if err != nil {
		return nil, err
	}
	res, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("persys-forge"),
		),
	)
	tracerProvider := trace.NewTracerProvider(
		trace.WithBatcher(otlpExp),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(tracerProvider)
	meterProvider := metric.NewMeterProvider(
		metric.WithReader(promExp),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)
	// Expose /metrics endpoint
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(":2112", nil)
	}()
	return func() {
		_ = tracerProvider.Shutdown(ctx)
		_ = meterProvider.Shutdown(ctx)
	}, nil
}

func main() {
	ctx := context.Background()
	cleanup, err := setupObservability(ctx)
	if err != nil {
		log.Fatalf("failed to set up observability: %v", err)
	}
	defer cleanup()

	cfg, err := utils.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if err := db.InitMySQL(cfg.MySQLDSN); err != nil {
		log.Fatalf("failed to connect to MySQL: %v", err)
	}

	secretsMgr := &secretsPkg.Manager{DB: db.DB}
	operatorClient := &operator.Client{}

	// Initialize orchestrator with proper clients
	workspaceDir := cfg.Build.Workspace
	if workspaceDir == "" {
		workspaceDir = "/tmp/forge-builds"
	}
	orchestrator := build.NewOrchestrator(workspaceDir)
	orchestrator.SetOperatorClient(operatorClient)

	// Initialize Docker client for local builds
	dockerClient := &build.Docker{}
	orchestrator.SetDockerClient(dockerClient)

	// Start Redis worker for build jobs
	go queue.StartRedisWorker(cfg, orchestrator)

	// Gin server setup
	r := gin.Default()
	r.Use(gootelgin.Middleware("persys-forge"))

	// Handlers
	buildHandler := handlers.NewBuildHandler(orchestrator)
	projectHandler := handlers.NewProjectHandler(db.DB)
	secretHandler := handlers.NewSecretHandler(secretsMgr)

	// Build endpoints
	r.POST("/build", buildHandler.Build)
	r.GET("/status/:job_id", buildHandler.Status)

	// Project endpoints
	r.POST("/projects", projectHandler.CreateProject)
	r.GET("/projects", projectHandler.ListProjects)
	r.GET("/projects/:project", projectHandler.GetProject)
	r.PUT("/projects/:project", projectHandler.UpdateProject)
	r.DELETE("/projects/:project", projectHandler.DeleteProject)

	// Secret endpoints
	r.POST("/secrets", secretHandler.SetSecret)
	// Project secrets (more specific first)
	r.GET("/secrets/:project/:key", secretHandler.GetProjectSecret)
	r.GET("/secrets/:project", secretHandler.ListProjectSecrets)
	// Global secret (more general after)
	r.GET("/secrets/:key", secretHandler.GetSecret)

	// Start server
	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Starting server on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
