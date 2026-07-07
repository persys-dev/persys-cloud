package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/config"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/control"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/garbage"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/grpc"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/metrics"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/platform"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/reconcile"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/resources"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/runtime"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/state"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/storage/providers"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/task"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/telemetry"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/workload"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	"github.com/sirupsen/logrus"
)

const version = "1.0.0"

func main() {
	standaloneMode := flag.Bool("standalone", false, "run agent without scheduler registration/heartbeat")
	flag.Parse()

	// Initialize logger
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	// Load configuration (this now supports --config flag + /etc/persys + env)
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Set log level
	logLevel, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		logger.Warnf("Invalid log level '%s', using info", cfg.LogLevel)
		logLevel = logrus.InfoLevel
	}
	logger.SetLevel(logLevel)

	logger.Infof("Starting Persys Compute Agent v%s", version)
	logger.Infof("Node ID: %s", cfg.NodeID)

	otelShutdown, err := telemetry.Setup(context.Background(), logger, "compute-agent", cfg.OTELExporterEndpoint)
	if err != nil {
		logger.Fatalf("Failed to initialize OpenTelemetry: %v", err)
	}

	certCfg := certmanager.Config{
		TLSEnabled: cfg.TLSEnabled,

		TLSCertPath: cfg.TLSCertPath,
		TLSKeyPath:  cfg.TLSKeyPath,
		TLSCAPath:   cfg.TLSCAPath,

		VaultEnabled:       cfg.VaultEnabled,
		VaultManagerAddr:   cfg.VaultManagerAddr,
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
	}

	// Initialize certificate manager before starting TLS endpoints.
	var certManagerCancel context.CancelFunc
	if cfg.TLSEnabled {
		certMgr := certmanager.NewManager(certCfg, logger)
		certCtx, cancel := context.WithCancel(context.Background())
		certManagerCancel = cancel
		if err := certMgr.Start(certCtx); err != nil {
			logger.Fatalf("Failed to initialize certificate manager: %v", err)
		}
	}

	// Initialize state store
	logger.Info("Initializing state store...")
	store, err := state.NewBoltStore(cfg.StateStorePath)
	if err != nil {
		logger.Fatalf("Failed to initialize state store: %v", err)
	}
	defer store.Close()

	// Initialize runtime manager
	logger.Info("Initializing runtimes...")
	runtimeMgr := runtime.NewManager()

	// Register Docker runtime if enabled
	if cfg.DockerEnabled {
		dockerRuntime, err := runtime.NewDockerRuntime(cfg.DockerEndpoint, logger)
		if err != nil {
			logger.Warnf("Failed to initialize Docker runtime: %v", err)
		} else {
			runtimeMgr.Register(dockerRuntime)
			logger.Info("Docker runtime enabled")
		}
	}

	// Register Compose runtime if enabled
	if cfg.ComposeEnabled {
		composeRuntime, err := runtime.NewComposeRuntime(cfg.ComposeBinary, "", logger)
		if err != nil {
			logger.Warnf("Failed to initialize Compose runtime: %v", err)
		} else {
			runtimeMgr.Register(composeRuntime)
			logger.Info("Compose runtime enabled")
		}
	}

	// Register VM runtime if enabled
	if cfg.VMEnabled {
		vmRuntime, err := runtime.NewVMRuntime(cfg.LibvirtURI, logger)
		if err != nil {
			logger.Warnf("Failed to initialize VM runtime: %v", err)
		} else {
			runtimeMgr.Register(vmRuntime)
			logger.Info("VM runtime enabled")
		}
	}

	// Initialize workload manager
	logger.Info("Initializing workload manager...")
	workloadMgr := workload.NewManager(store, runtimeMgr, logger)

	// Initialize storage provider registry for managed volumes.
	providerRegistry := platform.NewProviderRegistry()
	providerRegistry.RegisterStorageProvider(providers.NewLocalProvider(cfg.StorageLocalRoot))
	providerRegistry.RegisterStorageProvider(providers.NewNFSProvider(
		cfg.StorageNFSServer,
		cfg.StorageNFSExport,
		cfg.StorageNFSStageDir,
		cfg.StorageNFSOptions,
	))
	providerRegistry.RegisterStorageProvider(providers.NewCephRBDProvider(
		cfg.StorageCephCluster,
		cfg.StorageCephPool,
		cfg.StorageCephUser,
		cfg.StorageCephKeyring,
		cfg.StorageCephStageDir,
	))
	workloadMgr.SetVolumeManager(platform.NewDefaultVolumeManager(providerRegistry))

	// Initialize metrics (Issue 7)
	logger.Info("Initializing metrics...")
	metricsInst, err := metrics.NewMetrics(logger)
	if err != nil {
		logger.Warnf("Failed to initialize metrics: %v", err)
	} else {
		workloadMgr.SetMetrics(metricsInst)
	}

	// Start metrics server
	var metricsServer *metrics.Server
	if metricsInst != nil {
		metricsServer = metrics.NewServer(fmt.Sprintf(":%d", cfg.MetricsPort), logger, metricsInst)
		if err := metricsServer.Start(); err != nil {
			logger.Warnf("Failed to start metrics server: %v", err)
			// Continue anyway, metrics is not critical
		}
	}

	// Initialize resource monitor (Issue 9)
	logger.Info("Initializing resource monitor...")
	resourceMonitor := resources.NewMonitor(
		resources.DefaultThresholds(),
		logger,
	)
	workloadMgr.SetResourceMonitor(resourceMonitor)

	// Initialize garbage collector (Issue 5)
	logger.Info("Initializing garbage collector...")
	gcConfig := garbage.DefaultConfig()
	gcCollector := garbage.NewCollector(gcConfig, store, runtimeMgr, logger)
	if metricsInst != nil {
		gcCollector.SetMetrics(metricsInst)
	}

	// Create a separate context for GC
	gcCtx := context.Background()

	// Start GC in background
	go gcCollector.Start(gcCtx)

	// Initialize reconciliation loop if enabled
	var reconciler *reconcile.Loop
	if cfg.ReconcileEnabled {
		logger.Infof("Initializing reconciliation loop (interval: %s)...", cfg.ReconcileInterval)
		reconciler = reconcile.NewLoop(store, workloadMgr, cfg.ReconcileInterval, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go reconciler.Start(ctx)
	}

	// Initialize async task queue
	logger.Info("Initializing async task queue...")
	taskQueue := task.NewQueue(10, logger) // 10 workers for async operations
	if metricsInst != nil {
		taskQueue.SetMetricsObserver(metricsInst)
	}

	// Register task handlers
	taskQueue.RegisterHandler(task.TaskTypeApplyWorkload, func(ctx context.Context, t *task.Task) error {
		w, ok := t.Result.(*models.Workload)
		if !ok || w == nil {
			return fmt.Errorf("invalid apply task payload for task %s", t.ID)
		}
		_, _, err := workloadMgr.ApplyWorkload(ctx, w)
		if err != nil {
			return fmt.Errorf("failed to apply workload %s: %w", w.ID, err)
		}
		return nil
	})

	taskQueue.RegisterHandler(task.TaskTypeDeleteWorkload, func(ctx context.Context, t *task.Task) error {
		workloadID, ok := t.Result.(string)
		if !ok || workloadID == "" {
			return fmt.Errorf("invalid delete task payload for task %s", t.ID)
		}
		err := workloadMgr.DeleteWorkload(ctx, workloadID)
		if err != nil {
			return fmt.Errorf("failed to delete workload %s: %w", workloadID, err)
		}
		return nil
	})

	// Start task queue in background
	taskQueueCtx := context.Background()
	go taskQueue.Start(taskQueueCtx)

	// Initialize gRPC server
	logger.Info("Initializing gRPC server...")
	grpcServer, err := grpc.NewServer(cfg, workloadMgr, runtimeMgr, logger)
	if err != nil {
		logger.Fatalf("Failed to initialize gRPC server: %v", err)
	}

	// Inject dependencies into gRPC server
	grpcServer.SetResourceMonitor(resourceMonitor)
	if metricsInst != nil {
		grpcServer.SetMetrics(metricsInst)
	}
	grpcServer.SetTaskQueue(taskQueue)

	// Start gRPC server in goroutine
	errChan := make(chan error, 1)
	go func() {
		if err := grpcServer.Start(); err != nil {
			errChan <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	// Start scheduler control client loop in background unless standalone mode is enabled
	var cancelControl context.CancelFunc
	if *standaloneMode {
		logger.Info("Standalone mode enabled: skipping scheduler registration and heartbeat loop")
	} else {
		controlClient := control.NewClient(cfg, workloadMgr, runtimeMgr, resourceMonitor, logger)
		controlCtx, cancel := context.WithCancel(context.Background())
		cancelControl = cancel
		defer cancelControl()
		go controlClient.Run(controlCtx)
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	logger.Info("Persys Compute Agent is ready")

	select {
	case sig := <-sigChan:
		logger.Infof("Received signal %v, shutting down...", sig)
	case err := <-errChan:
		logger.Errorf("Server error: %v", err)
	}

	// Graceful shutdown
	logger.Info("Shutting down gracefully...")

	// Stop garbage collector
	gcCollector.Stop()

	if reconciler != nil {
		reconciler.Stop()
	}
	taskQueue.Stop()

	if cancelControl != nil {
		cancelControl()
	}
	if certManagerCancel != nil {
		certManagerCancel()
	}

	grpcServer.Stop()

	// Stop metrics server
	if metricsServer != nil {
		if err := metricsServer.Stop(); err != nil {
			logger.Warnf("Error stopping metrics server: %v", err)
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := otelShutdown(shutdownCtx); err != nil {
		logger.Warnf("Error stopping OpenTelemetry: %v", err)
	}

	logger.Info("Shutdown complete")
}
