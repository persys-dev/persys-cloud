package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	"github.com/persys-dev/persys-cloud/persys-gateway/controllers"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/authn"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/catalog"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/grpcbridge"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/middleware"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/router"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/store"
	"github.com/persys-dev/persys-cloud/persys-gateway/routes"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	"github.com/sirupsen/logrus"
	ginprometheus "github.com/zsais/go-gin-prometheus"
	gootelgin "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// App holds every service/controller constructed at startup. Renamed
// fields from the old ProwService/ProwController naming, which didn't
// correspond to any real Persys service — see ClusterControlService and
// ClusterMetaController. ForgeryService is new: forgery used to share
// ProwService's pool/failover shape despite being a single fixed
// address with nothing to fail over between.
//
// db is Postgres via internal/store, replacing MongoDB entirely — see
// internal/store/schema.sql for the (much shorter) list of what's
// actually persisted now.
type App struct {
	server                *gin.Engine
	db                    *store.Store
	authService           services.AuthService
	githubService         services.GithubService
	clusterControl        *services.ClusterControlService
	forgeryService        *services.ForgeryService
	webhookService        services.WebhookService
	automationService     *services.AutomationService
	authController        controllers.AuthController
	githubController      controllers.GithubController
	clusterMetaController *controllers.ClusterMetaController
	eventsController      *controllers.EventsController
	webhookController     *controllers.WebhookController
	automationController  *controllers.AutomationController
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
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
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

	log.Printf("bootstrapping %s (deployment.mode=%s)", cnf.ServiceName, cnf.Deployment.Mode)

	certcnf := certmanager.Config{
		TLSEnabled: cnf.TLS.Enabled,

		TLSCertPath: cnf.TLS.CertPath,
		TLSKeyPath:  cnf.TLS.KeyPath,
		TLSCAPath:   cnf.TLS.CAPath,

		VaultEnabled:       cnf.Vault.Enabled,
		VaultManagerAddr:   cnf.Vault.ManagerAddr,
		VaultAddr:          cnf.Vault.Addr,
		VaultAuthMethod:    cnf.Vault.AuthMethod,
		VaultToken:         cnf.Vault.Token,
		VaultAppRoleID:     cnf.Vault.AppRoleID,
		VaultAppSecretID:   cnf.Vault.AppSecretID,
		VaultPKIMount:      cnf.Vault.PKIMount,
		VaultPKIRole:       cnf.Vault.PKIRole,
		VaultCertTTL:       cnf.Vault.CertTTL,
		VaultServiceName:   cnf.Vault.ServiceName,
		VaultServiceDomain: cnf.Vault.ServiceDomain,
		VaultRetryInterval: cnf.Vault.RetryInterval,
	}

	vaultCertManager := certmanager.NewManager(certcnf, logrus.New())
	if err != nil {
		log.Fatalf("failed to initialize vault cert manager: %v", err)
	}
	if err := vaultCertManager.Start(ctx); err != nil {
		log.Fatalf("failed to start vault cert manager: %v", err)
	}

	var db *store.Store
	if cnf.Database.Enabled() {
		db, err = store.New(ctx, cnf.Database.DSN, cnf.Database.MaxConns)
		if err != nil {
			log.Fatalf("failed to connect to Postgres: %v", err)
		}
		defer db.Close()
		if err := db.Migrate(ctx); err != nil {
			log.Fatalf("failed to migrate database: %v", err)
		}
		log.Println("Postgres connected and schema migrated")
	} else {
		// Valid and expected for self-hosted: user/session/OAuth storage
		// is only touched by /auth and /github routes, which only mount
		// in managed mode (see below); webhook.service.go's audit
		// persistence no-ops on a nil store and falls back to
		// in-memory-only replay tracking. Managed mode can't reach this
		// branch — config.LoadConfig already failed fast above if
		// database.dsn was empty there.
		log.Println("no database configured — running without persistent user/session/webhook-audit storage " +
			"(expected for self-hosted; set database.dsn to enable it, or run in managed mode where it's required)")
	}

	app := &App{
		server: gin.Default(),
		db:     db,
	}

	jwtSecret := []byte(cnf.App.JWTSecret)

	webhookTLS, err := buildMTLSClientConfig(cnf)
	if err != nil {
		log.Fatalf("failed to initialize webhook forwarding TLS: %v", err)
	}

	app.authService = services.NewAuthService(app.db, ctx, jwtSecret)
	app.githubService = services.NewGithubService(cnf, webhookTLS)
	app.clusterControl = services.NewClusterControlService(cnf)
	app.clusterControl.Start(ctx)
	app.forgeryService = services.NewForgeryService(cnf, webhookTLS)
	app.webhookService, err = services.NewWebhookService(cnf, webhookTLS, app.db)
	if err != nil {
		log.Fatalf("failed to initialize webhook service: %v", err)
	}
	app.webhookService.Start(ctx)

	app.automationService, err = services.NewAutomationService(cnf, webhookTLS)
	if err != nil {
		log.Fatalf("failed to initialize automation service: %v", err)
	}

	app.authController = controllers.NewAuthController(
		app.authService, ctx, app.githubService, app.db,
		cnf.GitHub.Auth.ClientID, cnf.GitHub.Auth.ClientSecret, jwtSecret,
	)
	app.githubController = controllers.NewGithubController(app.authService, ctx, app.githubService, cnf)
	app.clusterMetaController = controllers.NewClusterMetaController(app.clusterControl, string(cnf.Deployment.Mode), cnf.Database.Enabled())
	app.eventsController = controllers.NewEventsController(app.clusterControl)
	app.webhookController = controllers.NewWebhookController(app.webhookService)
	app.automationController = controllers.NewAutomationController(app.automationService)

	// grpcbridge classifies invocation errors from services.ClusterControlService
	// without importing the services package (avoids an import cycle,
	// since services doesn't and shouldn't import grpcbridge).
	grpcbridge.RegisterErrorClassifiers(services.IsUnknownCluster, services.IsSchedulerUnavailable)

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

	authnMW := authn.New(jwtSecret)
	gwRouter := router.New(authnMW, cnf.Deployment.Mode)

	// events/watch is registered on the mTLS router group
	app.eventsController.Register(mtlsGroup)

	// ClusterMetaController: health/list-clusters/get-cluster — the only
	// handlers left that were never RPC-shaped.
	gwRouter.RegisterControllers(mtlsGroup, app.clusterMetaController)

	// Optional service catalog for plain reverse-proxy services added
	// later with zero gateway code changes. Not having one yet (no
	// catalog.yaml on disk) is a normal out-of-the-box state, not an
	// error — RegisterCatalog no-ops in that case.
	catalogPath := os.Getenv("PERSYS_GATEWAY_CATALOG")
	if catalogPath == "" {
		catalogPath = "catalog.yaml"
	}
	if err := gwRouter.RegisterCatalog(mtlsGroup, catalogPath); err != nil {
		log.Fatalf("failed to load service catalog %q: %v", catalogPath, err)
	}

	// Dynamic RPC bridge: cluster control (pooled, HA-aware failover) and
	// forgery (single fixed address) on one Bridge, each with its own
	// Invoker/Source so neither shape leaks into the other. See
	// internal/router/bindings.go for the full list of what's callable
	// and where. Both bindings fall back to their compiled-in proto
	// descriptor (LocalFile) if the backend doesn't support gRPC
	// reflection yet — so this works against existing scheduler/forgery
	// deployments unmodified, and upgrades to live discovery
	// automatically once they add reflection.Register.
	//
	// Mounted TWICE, on purpose:
	//
	//   - v2, cluster-scoped: /clusters/:cluster_id/workloads/schedule etc.
	//     Explicit multi-cluster targeting.
	//
	//   - v1, flat: /workloads/schedule etc. — no cluster segment at all.
	//     This is the ORIGINAL route shape persysctl (and anyone else
	//     built against pre-multi-cluster persys-gateway) already calls.
	//     It keeps working unmodified: DefaultKeyResolver.ResolveClusterID
	//     returns "" when there's no :cluster_id param and no
	//     X-Persys-Cluster-ID header/query override, and
	//     ClusterControlService/ForgeryService.InvokeDynamic both already
	//     fall back to schedulerPool.DefaultClusterID() when clusterID is
	//     "". So v1 callers transparently hit the default cluster with
	//     zero gateway-side special-casing beyond mounting the routes
	//     twice — the dynamic-discovery and failover logic underneath is
	//     shared, not duplicated.
	//
	// Costs two independent reflection-discovery cycles instead of one
	// (each Bridge instance discovers/refreshes separately) — negligible
	// at a 60s refresh interval, and far simpler than trying to make one
	// Bridge serve two mount points off one discovery pass.
	clusterGroup := mtlsGroup.Group("/clusters/:cluster_id")
	bridgeV2 := grpcbridge.New()
	if err := bridgeV2.Register(clusterGroup,
		router.ClusterControlBinding(app.clusterControl, gwRouter),
		router.ForgeryBinding(app.forgeryService, gwRouter),
	); err != nil {
		log.Fatalf("failed to register grpc bridge (v2, cluster-scoped): %v", err)
	}

	bridgeV1 := grpcbridge.New()
	if err := bridgeV1.Register(mtlsGroup,
		router.ClusterControlBinding(app.clusterControl, gwRouter),
		router.ForgeryBinding(app.forgeryService, gwRouter),
	); err != nil {
		log.Fatalf("failed to register grpc bridge (v1, flat/legacy): %v", err)
	}

	// GitHub OAuth: only meaningful, and only mounted, in managed mode.
	// A self-hosted operator never sees these routes at all and never
	// needs a GitHub OAuth app.
	if cnf.Deployment.Mode == config.DeploymentManaged {
		authRouteController := routes.NewAuthRouteController(app.authController, cnf.App.OAuthRedirectURL)
		githubRouteController := routes.NewGithubRouteController(app.authController, app.githubController)
		authRouteController.AuthRoute(mtlsGroup)
		githubRouteController.GithubRoute(mtlsGroup)
	} else {
		log.Printf("deployment.mode=%s: GitHub OAuth routes are not mounted (no OAuth app needed for self-hosted installs)", cnf.Deployment.Mode)
	}

	// Automation previously had NO auth enforced at all — every group
	// mounted here except /auth and /github skipped it entirely. Now
	// explicitly resolved against deployment mode like everything else.
	automationGroup := mtlsGroup.Group("")
	automationGroup.Use(gwRouter.Resolve(catalog.AuthUser))
	automationRouteController := routes.NewAutomationRouteController(app.automationController)
	automationRouteController.AutomationRoute(automationGroup)

	// Webhook stays unauthenticated at the gateway level by design — its
	// own HMAC signature verification (X-Hub-Signature-256) IS its auth
	// mechanism, checked inside webhook.service.go. Mounted on the
	// non-mTLS listener since GitHub calls in from the public internet.
	webhookRouteController := routes.NewWebhookRouteController(app.webhookController)
	webhookRouteController.WebhookRoute(nonMTLSGroup, cnf.Webhook.PublicPath)

	// Intelligence: left as its own hand-written proxy controller rather
	// than folded into the service catalog. Its route-by-route prefix
	// handling (some paths keep "/ai", the :id action paths strip it) is
	// existing, presumably-working behavior against the real
	// persys-intelligence service that a blanket catalog entry can't
	// safely reproduce without risking a silent path mismatch.
	intelligenceRouteController := routes.NewIntelligenceRouteController(cnf)
	intelligenceRouteController.IntelligenceRoute(mtlsGroup)

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

	debugMux := http.NewServeMux()
	debugMux.HandleFunc("/debug/pprof/", pprof.Index)
	debugMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	debugMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	debugMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	debugMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	// goroutine, heap, allocs, block, mutex, threadcreate are served via pprof.Index
	// through /debug/pprof/{profile-name} automatically once Index is registered

	debugServer := &http.Server{
		Addr:    "0.0.0.0:6060",
		Handler: debugMux,
	}

	go func() {
		log.Printf("starting debug/pprof server on %s", debugServer.Addr)
		if err := debugServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("debug server failed: %v", err)
		}
	}()

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
	if err := debugServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("debug server shutdown failed: %v", err)
	}
	log.Println("servers exited gracefully")
}

func buildMTLSClientConfig(cnf *config.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cnf.TLS.CertPath, cnf.TLS.KeyPath)
	if err != nil {
		return nil, err
	}
	caCert, err := os.ReadFile(cnf.TLS.CAPath)
	if err != nil {
		return nil, err
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("invalid CA bundle")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caPool}, nil
}
