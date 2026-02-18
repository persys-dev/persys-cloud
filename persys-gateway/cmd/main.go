package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	"github.com/persys-dev/persys-cloud/persys-gateway/controllers"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/middleware"
	"github.com/persys-dev/persys-cloud/persys-gateway/routes"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
	ginprometheus "github.com/zsais/go-gin-prometheus"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	gootelgin "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

type App struct {
	server           *gin.Engine
	authCollection   *mongo.Collection
	githubCollection *mongo.Collection
	prowCollection   *mongo.Collection
	authService      services.AuthService
	githubService    services.GithubService
	prowService      *services.ProwService
	authController   controllers.AuthController
	githubController controllers.GithubController
	prowController   *controllers.ProwController
}

// Tracing setup function (call this at the start of main)
func setupTracer() func() {
	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpoint("jaeger:4318"),
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
			semconv.ServiceNameKey.String("persys-api-gateway"),
		)),
	)
	otel.SetTracerProvider(tp)
	return func() { _ = tp.Shutdown(context.Background()) }
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup tracing
	shutdown := setupTracer()
	defer shutdown()

	// Read config
	cnf, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}

	log.Printf("bootstrapping api-gateway ...")

	time.Sleep(10 * time.Second)

	// --- Certificate Bootstrapping ---
	certManager := services.NewCertificateManager(
		cnf.TLS.CAPath,
		cnf.TLS.CertPath,
		cnf.TLS.KeyPath,
		cnf.CFSSL.APIURL,
		"api-gateway", // Common name
		"Persys",      // Organization
	)
	if err := certManager.EnsureCertificate(); err != nil {
		log.Fatalf("Failed to ensure certificate: %v", err)
	}

	// Setup MongoDB
	mongoclient, err := setupMongoDB(ctx, cnf.Database.MongoURI)
	if err != nil {
		log.Fatalf("Failed to setup MongoDB: %v", err)
	}
	defer mongoclient.Disconnect(ctx)

	// Initialize application
	app := &App{
		server:           gin.Default(),
		authCollection:   mongoclient.Database(cnf.Database.Name).Collection("users"),
		githubCollection: mongoclient.Database(cnf.Database.Name).Collection("repos"),
		prowCollection:   mongoclient.Database(cnf.Database.Name).Collection("prow"),
	}
	// app.server.Use(gin.LoggerWithWriter(logFile))
	app.authService = services.NewAuthService(app.authCollection, ctx)
	app.githubService = services.NewGithubService(app.githubCollection, ctx)
	app.prowService = services.NewProwService(cnf)

	// Discover Prow schedulers in a go routine with a 90 second delay (wait for prows to come up and register)
	go func() {
		time.Sleep(90 * time.Second)
		if err := app.prowService.DiscoverSchedulers(cnf.CoreDNS.Addr); err != nil {
			log.Printf("Warning: Failed to discover Prow schedulers: %v", err)
		}
	}()
	
	app.authController = controllers.NewAuthController(app.authService, ctx, app.githubService, app.authCollection)
	app.githubController = controllers.NewGithubController(app.authService, ctx, app.githubService, app.githubCollection, cnf)
	app.prowController = controllers.NewProwController(app.prowService, app.authService, ctx)

	// Setup CORS
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"*"}
	corsConfig.AllowCredentials = true
	app.server.Use(cors.New(corsConfig))
	app.server.Use(middleware.ServiceIdentityHeader("api-gateway"))

	// Create separate routers for mTLS and non-mTLS
	mtlsRouter := gin.New()
	nonMTLSRouter := gin.New()

	// Apply middleware to both routers
	mtlsRouter.Use(gin.Logger())
	mtlsRouter.Use(cors.New(corsConfig))
	mtlsRouter.Use(gootelgin.Middleware("api-gateway-mtls"))
	mtlsRouter.Use(middleware.ServiceIdentityHeader("api-gateway"))

	nonMTLSRouter.Use(gin.Logger())
	nonMTLSRouter.Use(cors.New(corsConfig))
	nonMTLSRouter.Use(gootelgin.Middleware("api-gateway-non-mtls"))
	nonMTLSRouter.Use(middleware.ServiceIdentityHeader("api-gateway"))

	// Add Prometheus instrumentation to both routers
	p := ginprometheus.NewPrometheus("api_gateway")
	p.Use(mtlsRouter)
	p.Use(nonMTLSRouter)

	// Setup routes
	mtlsGroup := mtlsRouter.Group("")
	nonMTLSGroup := nonMTLSRouter.Group("")

	// Non-mTLS routes
	nonMTLSGroup.GET("/", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "success", "message": "Persys API Gateway running", "version": "1.0.0"})
	})

	// Setup route controllers
	authRouteController := routes.NewAuthRouteController(app.authController)
	githubRouteController := routes.NewGithubRouteController(app.githubController)
	prowRouteController := routes.NewProwRouteController(app.prowController)

	// Apply routes to appropriate routers
	authRouteController.AuthRoute(mtlsGroup)
	githubRouteController.GithubRoute(mtlsGroup)
	prowRouteController.ProwRoute(mtlsGroup)

	// --- mTLS Server Setup ---
	caCert, err := os.ReadFile(cnf.TLS.CAPath)
	if err != nil {
		log.Fatalf("Failed to read CA cert: %v", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		log.Fatalf("Failed to append CA cert")
	}
	cert, err := tls.LoadX509KeyPair(cnf.TLS.CertPath, cnf.TLS.KeyPath)
	if err != nil {
		log.Fatalf("Failed to load server cert/key: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}

	// Create mTLS server
	mtlsServer := &http.Server{
		Addr:      cnf.App.HTTPAddr,
		Handler:   mtlsRouter,
		TLSConfig: tlsConfig,
	}

	// Create non-mTLS server
	nonMTLSPort := cnf.App.HTTPAddrNonMTLS
	nonMTLSServer := &http.Server{
		Addr:    ":" + nonMTLSPort,
		Handler: nonMTLSRouter,
	}

	// Start mTLS server
	go func() {
		log.Printf("Starting mTLS server on %s", cnf.App.HTTPAddr)
		if err := mtlsServer.ListenAndServeTLS(cnf.TLS.CertPath, cnf.TLS.KeyPath); err != nil && err != http.ErrServerClosed {
			log.Fatalf("mTLS server failed: %v", err)
		}
	}()

	// Start non-mTLS server
	go func() {
		log.Printf("Starting non-mTLS server on port %s", nonMTLSPort)
		if err := nonMTLSServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("non-mTLS server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down servers...")

	// Perform graceful shutdown
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mtlsServer.Shutdown(ctx); err != nil {
		log.Printf("mTLS server shutdown failed: %v", err)
	}
	if err := nonMTLSServer.Shutdown(ctx); err != nil {
		log.Printf("non-mTLS server shutdown failed: %v", err)
	}
	log.Println("Servers exited gracefully")
}

func setupMongoDB(ctx context.Context, uri string) (*mongo.Client, error) {
	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetMaxPoolSize(50).
		SetMinPoolSize(5))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}
	fmt.Println("MongoDB successfully connected...")
	return client, nil
}
