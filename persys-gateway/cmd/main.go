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
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/certmanager"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/middleware"
	"github.com/persys-dev/persys-cloud/persys-gateway/routes"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
	"github.com/sirupsen/logrus"
	ginprometheus "github.com/zsais/go-gin-prometheus"
	"go.mongodb.org/mongo-driver/bson"
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
	server            *gin.Engine
	authCollection    *mongo.Collection
	sessionCollection *mongo.Collection
	clusterCollection *mongo.Collection
	githubCollection  *mongo.Collection
	prowCollection    *mongo.Collection
	webhookCollection *mongo.Collection
	authService       services.AuthService
	githubService     services.GithubService
	prowService       *services.ProwService
	webhookService    services.WebhookService
	authController    controllers.AuthController
	githubController  controllers.GithubController
	prowController    *controllers.ProwController
	webhookController *controllers.WebhookController
}

func setupTracer(endpoint string, serviceName string) func() {
	opts := []otlptracehttp.Option{otlptracehttp.WithInsecure()}
	if endpoint != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(endpoint))
	}
	exporter, err := otlptracehttp.New(context.Background(), opts...)
	if err != nil {
		log.Printf("failed to create OTLP exporter: %v", err)
		return func() {}
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		)),
	)
	otel.SetTracerProvider(tp)
	return func() { _ = tp.Shutdown(context.Background()) }
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cnf, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("failed to read config: %v", err)
	}

	shutdown := setupTracer(cnf.Telemetry.OTLPEndpoint, cnf.ServiceName)
	defer shutdown()

	log.Printf("bootstrapping %s", cnf.ServiceName)

	vaultCertManager, err := certmanager.NewFromConfig(cnf, logrus.New())
	if err != nil {
		log.Fatalf("failed to initialize vault cert manager: %v", err)
	}
	if err := vaultCertManager.Start(ctx); err != nil {
		log.Fatalf("failed to start vault cert manager: %v", err)
	}

	mongoclient, err := setupMongoDB(ctx, cnf.Database.MongoURI)
	if err != nil {
		log.Fatalf("failed to setup MongoDB: %v", err)
	}
	defer mongoclient.Disconnect(ctx)

	app := &App{
		server:            gin.Default(),
		authCollection:    mongoclient.Database(cnf.Database.Name).Collection("users"),
		sessionCollection: mongoclient.Database(cnf.Database.Name).Collection("sessions"),
		clusterCollection: mongoclient.Database(cnf.Database.Name).Collection("cluster_state"),
		githubCollection:  mongoclient.Database(cnf.Database.Name).Collection("repos"),
		prowCollection:    mongoclient.Database(cnf.Database.Name).Collection("prow"),
		webhookCollection: mongoclient.Database(cnf.Database.Name).Collection("webhooks"),
	}

	webhookTLS, err := buildMTLSClientConfig(cnf)
	if err != nil {
		log.Fatalf("failed to initialize webhook forwarding TLS: %v", err)
	}

	app.authService = services.NewAuthService(app.authCollection, ctx)
	app.githubService = services.NewGithubService(app.githubCollection, ctx, cnf, webhookTLS)
	app.prowService = services.NewProwService(cnf)
	app.prowService.Start(ctx)
	go persistClusterSnapshots(ctx, app.clusterCollection, app.prowService)
	app.webhookService, err = services.NewWebhookService(cnf, webhookTLS, app.webhookCollection)
	if err != nil {
		log.Fatalf("failed to initialize webhook service: %v", err)
	}
	app.webhookService.Start(ctx)

	app.authController = controllers.NewAuthController(app.authService, ctx, app.githubService, app.authCollection, app.sessionCollection)
	app.githubController = controllers.NewGithubController(app.authService, ctx, app.githubService, app.githubCollection, cnf)
	app.prowController = controllers.NewProwController(app.prowService, app.authService, ctx)
	app.webhookController = controllers.NewWebhookController(app.webhookService)

	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"*"}
	corsConfig.AllowCredentials = true

	mtlsRouter := gin.New()
	nonMTLSRouter := gin.New()

	mtlsRouter.Use(gin.Logger())
	mtlsRouter.Use(cors.New(corsConfig))
	mtlsRouter.Use(gootelgin.Middleware("persys-gateway-mtls"))
	mtlsRouter.Use(middleware.ServiceIdentityHeader("persys-gateway"))

	nonMTLSRouter.Use(gin.Logger())
	nonMTLSRouter.Use(cors.New(corsConfig))
	nonMTLSRouter.Use(gootelgin.Middleware("persys-gateway-public"))
	nonMTLSRouter.Use(middleware.ServiceIdentityHeader("persys-gateway"))

	p := ginprometheus.NewPrometheus("persys_gateway")
	p.Use(mtlsRouter)
	p.Use(nonMTLSRouter)

	mtlsGroup := mtlsRouter.Group("")
	nonMTLSGroup := nonMTLSRouter.Group("")

	nonMTLSGroup.GET("/", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "success", "message": "Persys Gateway running", "version": "1.0.0"})
	})

	authRouteController := routes.NewAuthRouteController(app.authController, cnf.App.OAuthRedirectURL)
	githubRouteController := routes.NewGithubRouteController(app.githubController)
	prowRouteController := routes.NewProwRouteController(app.prowController)
	webhookRouteController := routes.NewWebhookRouteController(app.webhookController)

	authRouteController.AuthRoute(mtlsGroup)
	githubRouteController.GithubRoute(mtlsGroup)
	prowRouteController.ProwRoute(mtlsGroup)
	webhookRouteController.WebhookRoute(nonMTLSGroup, cnf.Webhook.PublicPath)

	caCert, err := os.ReadFile(cnf.TLS.CAPath)
	if err != nil {
		log.Fatalf("failed to read CA cert: %v", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		log.Fatalf("failed to append CA cert")
	}
	cert, err := tls.LoadX509KeyPair(cnf.TLS.CertPath, cnf.TLS.KeyPath)
	if err != nil {
		log.Fatalf("failed to load server cert/key: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caCertPool,
	}
	if cnf.TLS.RequireClientCert {
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	mtlsServer := &http.Server{Addr: cnf.App.HTTPAddr, Handler: mtlsRouter, TLSConfig: tlsConfig}
	nonMTLSServer := &http.Server{Addr: cnf.App.HTTPAddrPublic, Handler: nonMTLSRouter}

	go func() {
		log.Printf("starting mTLS server on %s", cnf.App.HTTPAddr)
		if err := mtlsServer.ListenAndServeTLS(cnf.TLS.CertPath, cnf.TLS.KeyPath); err != nil && err != http.ErrServerClosed {
			log.Fatalf("mTLS server failed: %v", err)
		}
	}()

	go func() {
		log.Printf("starting public server on %s", cnf.App.HTTPAddrPublic)
		if err := nonMTLSServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("public server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("shutting down servers")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := mtlsServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("mTLS server shutdown failed: %v", err)
	}
	if err := nonMTLSServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("public server shutdown failed: %v", err)
	}
	log.Println("servers exited gracefully")
}

func setupMongoDB(ctx context.Context, uri string) (*mongo.Client, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri).SetMaxPoolSize(50).SetMinPoolSize(5))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}
	fmt.Println("MongoDB successfully connected")
	return client, nil
}

func buildMTLSClientConfig(cfg *config.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLS.CertPath, cfg.TLS.KeyPath)
	if err != nil {
		return nil, err
	}
	caCert, err := os.ReadFile(cfg.TLS.CAPath)
	if err != nil {
		return nil, err
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("invalid CA bundle")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caPool}, nil
}

func persistClusterSnapshots(ctx context.Context, collection *mongo.Collection, prowService *services.ProwService) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	persist := func() {
		for _, cluster := range prowService.SnapshotClusters() {
			schedulers := make([]bson.M, 0, len(cluster.Schedulers))
			for _, sch := range cluster.Schedulers {
				schedulers = append(schedulers, bson.M{
					"id":        sch.ID,
					"address":   sch.Address,
					"is_leader": sch.IsLeader,
					"healthy":   sch.Healthy,
					"last_seen": sch.LastSeen,
				})
			}
			_, err := collection.UpdateOne(ctx,
				bson.M{"cluster_id": cluster.ID},
				bson.M{"$set": bson.M{
					"cluster_id":       cluster.ID,
					"name":             cluster.Name,
					"routing_strategy": string(cluster.RoutingStrategy),
					"schedulers":       schedulers,
					"updated_at":       time.Now().UTC(),
				}},
				options.Update().SetUpsert(true),
			)
			if err != nil {
				log.Printf("failed to persist cluster snapshot cluster=%s err=%v", cluster.ID, err)
			}
		}
	}

	persist()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			persist()
		}
	}
}
