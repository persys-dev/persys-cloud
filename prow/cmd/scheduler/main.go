package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/prow/internal/api"
	"github.com/persys-dev/prow/internal/auth"
	"github.com/persys-dev/prow/internal/middleware"
	"github.com/persys-dev/prow/internal/scheduler"
	ginprometheus "github.com/zsais/go-gin-prometheus"
	gootelgin "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// Tracing setup function
func setupTracer() func() {
	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpoint("192.168.1.13:4318"),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		log.Printf("Failed to create OTLP exporter: %v", err)
		return func() {}
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("prow-scheduler"),
		)),
	)
	otel.SetTracerProvider(tp)
	return func() { _ = tp.Shutdown(context.Background()) }
}

func main() {
	// Setup tracing
	shutdown := setupTracer()
	defer shutdown()

	// Get configuration from environment
	caFile := os.Getenv("CA_FILE")
	cfsslURL := os.Getenv("CFSSL_API_URL")
	commonName := os.Getenv("CERT_COMMON_NAME")
	organization := os.Getenv("CERT_ORGANIZATION")

	// Set defaults if not provided
	if caFile == "" {
		caFile = "/etc/prow/certs/ca.pem"
	}
	if cfsslURL == "" {
		cfsslURL = "https://persys-cfssl:8888"
	}
	if commonName == "" {
		commonName = "prow-scheduler"
	}
	if organization == "" {
		organization = "persys"
	}

	// Log the paths we're using
	log.Printf("Using CA certificate path: %s", caFile)

	// Initialize certificate manager
	certManager := auth.NewCertificateManager(caFile, cfsslURL, commonName, organization)

	log.Printf("bootstrapping prow-scheduler ...")
	time.Sleep(15 * time.Second)

	// Ensure we have valid certificates
	if err := certManager.EnsureCertificate(); err != nil {
		log.Fatalf("Failed to ensure certificate: %v", err)
	}

	// Load certificates for mTLS
	cert, err := tls.LoadX509KeyPair(certManager.CertPath, certManager.KeyPath)
	if err != nil {
		log.Fatalf("Failed to load certificate pair: %v", err)
	}

	// Load CA certificate
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		log.Fatalf("Failed to read CA cert: %v , %s", caFile, err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		log.Fatalf("Failed to append CA cert to pool")
	}

	// Initialize scheduler
	sched, err := scheduler.NewScheduler()
	if err != nil {
		log.Fatalf("Failed to initialize scheduler: %v", err)
	}
	defer sched.Close()

	// Get scheduler's IP address for CoreDNS registration
	schedulerIP := os.Getenv("SCHEDULER_IP")
	if schedulerIP == "" {
		// Try to get IP from hostname if not provided
		hostname, err := os.Hostname()
		if err != nil {
			log.Printf("Warning: Failed to get hostname: %v", err)
		} else {
			// Resolve hostname to IP
			addrs, err := net.LookupHost(hostname)
			if err != nil || len(addrs) == 0 {
				log.Printf("Warning: Failed to resolve hostname %s: %v", hostname, err)
			} else {
				schedulerIP = addrs[0]
			}
		}
	}

	// Get port for CoreDNS registration
	schedulerPort := 8085 // Default port
	if portStr := os.Getenv("PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			schedulerPort = port
		}
	}

	if schedulerIP != "" {
		// Register scheduler in CoreDNS
		if err := sched.RegisterSchedulerInCoreDNS(schedulerIP, schedulerPort); err != nil {
			log.Printf("Warning: Failed to register scheduler in CoreDNS: %v", err)
		} else {
			log.Printf("Successfully registered scheduler in CoreDNS with IP: %s and port: %d", schedulerIP, schedulerPort)
		}
	} else {
		log.Printf("Warning: Could not determine scheduler IP for CoreDNS registration")
	}

	// Set up context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start node monitoring
	go sched.MonitorNodes(ctx)

	// Start Workload Monitoring
	workloadMonitor := scheduler.NewMonitor(sched)
	go workloadMonitor.StartMonitoring()

	// Start reconciliation loop
	log.Println("Starting reconciliation loop...")
	sched.StartReconciliation(ctx)

	// Set up Gin router
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// Enable OpenTelemetry tracing
	r.Use(gootelgin.Middleware("prow-scheduler"))
	r.Use(middleware.ServiceIdentityHeader("prow-scheduler"))

	// Enable Prometheus metrics
	p := ginprometheus.NewPrometheus("prow_scheduler")
	p.Use(r)

	// Create a separate router for non-mTLS endpoints
	nonMTLSRouter := gin.New()
	nonMTLSRouter.Use(gin.Logger())
	nonMTLSRouter.Use(gin.Recovery())

	// Enable OpenTelemetry tracing for non-mTLS router
	nonMTLSRouter.Use(gootelgin.Middleware("prow_scheduler-non-mtls"))
	nonMTLSRouter.Use(middleware.ServiceIdentityHeader("prow-scheduler"))

	// Enable Prometheus metrics on non-mTLS router
	p.Use(nonMTLSRouter)

	// Register API handlers for both routers
	api.RegisterMTLSHandlers(r, sched)
	api.RegisterNonMTLSHandlers(nonMTLSRouter, sched)

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}

	// Configure TLS
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}

	// Create mTLS server
	mtlsServer := &http.Server{
		Addr:      ":" + port,
		Handler:   r,
		TLSConfig: tlsConfig,
	}

	// Create non-mTLS server for metrics and nodes
	nonMTLSPort := "8084" // Use a different port for non-mTLS endpoints
	nonMTLSServer := &http.Server{
		Addr:    ":" + nonMTLSPort,
		Handler: nonMTLSRouter,
	}

	// Start mTLS server in a goroutine
	go func() {
		log.Printf("Starting mTLS server on port %s", port)
		if err := mtlsServer.ListenAndServeTLS(certManager.CertPath, certManager.KeyPath); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start mTLS server: %v", err)
		}
	}()

	// Start non-mTLS server in a goroutine
	go func() {
		log.Printf("Starting non-mTLS server on port %s", nonMTLSPort)
		if err := nonMTLSServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start non-mTLS server: %v", err)
		}
	}()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Perform graceful shutdown
	log.Println("Shutting down servers...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := mtlsServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("mTLS server shutdown error: %v", err)
	}

	if err := nonMTLSServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("non-mTLS server shutdown error: %v", err)
	}

	cancel() // Stop MonitorNodes
	log.Println("Servers stopped")
}
