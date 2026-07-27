// Command persys-meter consumes per-workload usage events published by
// persys-scheduler onto a Redis Stream and durably stores them in
// ClickHouse, so downstream billing/analytics can query historical
// per-workload resource usage - something neither compute-agent nor
// persys-scheduler retain on their own (they only ever expose the latest
// sample).
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/persys-dev/persys-cloud/persys-meter/internal/api"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/cache"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/config"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/consumer"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/health"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/store"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// compositeChecker reports readiness across every dependency this service
// actually needs: Redis (to consume) and ClickHouse (to write). Either one
// being unreachable means the service can't do useful work right now.
type compositeChecker struct {
	redisClient *redis.Client
	chStore     *store.ClickHouseStore
}

func (c *compositeChecker) Ready(ctx context.Context) error {
	if err := c.redisClient.Ping(ctx).Err(); err != nil {
		return err
	}
	return c.chStore.Ping(ctx)
}

func main() {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})
	log := logger.WithField("component", "cmd.meter")

	cfg, err := config.Load()
	if err != nil {
		log.WithError(err).Fatal("failed to load configuration")
	}

	metrics.Register()

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer redisClient.Close()

	{
		pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := redisClient.Ping(pingCtx).Err(); err != nil {
			log.WithError(err).Fatal("failed to connect to redis")
		}
	}
	log.WithField("addr", cfg.RedisAddr).Info("connected to redis")

	chStore, err := store.NewClickHouseStore(store.ClickHouseConfig{
		Addr:          cfg.ClickHouseAddr,
		Database:      cfg.ClickHouseDatabase,
		Username:      cfg.ClickHouseUsername,
		Password:      cfg.ClickHousePassword,
		TLS:           cfg.ClickHouseTLS,
		RetentionDays: cfg.RetentionDays,
	}, log)
	if err != nil {
		log.WithError(err).Fatal("failed to connect to clickhouse")
	}
	defer chStore.Close()
	log.WithField("addr", cfg.ClickHouseAddr).Info("connected to clickhouse")

	initCtx, initCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := chStore.Init(initCtx); err != nil {
		initCancel()
		log.WithError(err).Fatal("failed to initialize clickhouse schema")
	}
	initCancel()

	// latestCache backs the per-workload Prometheus metrics AND the API's
	// live endpoints - one shared, in-memory view of "what is every
	// workload doing right now", kept current by the consumer.
	latestCache := cache.New()

	workloadCollector := metrics.NewWorkloadCollector(latestCache, cfg.CacheMaxAge)
	metrics.RegisterWorkloadCollector(workloadCollector)

	c := consumer.New(cfg, redisClient, chStore, latestCache, log)

	healthSrv := health.Serve(cfg.HealthAddr, &compositeChecker{redisClient: redisClient, chStore: chStore}, log)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := healthSrv.Shutdown(shutdownCtx); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Warn("health server shutdown error")
		}
	}()

	// chStore satisfies store.QueryStore (History/Summary) as well as
	// store.Store - the API only needs the read side.
	queryAPI := api.New(latestCache, chStore, log, cfg.APIToken, cfg.CacheMaxAge)
	apiSrv := &http.Server{Addr: cfg.APIAddr, Handler: queryAPI.Handler()}
	go func() {
		if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Error("API server stopped unexpectedly")
		}
	}()
	if cfg.APIToken == "" {
		log.WithField("addr", cfg.APIAddr).Warn("query API listening with no auth token configured (METER_API_TOKEN unset) - fine for local dev, not for anything reachable outside a trusted network")
	} else {
		log.WithField("addr", cfg.APIAddr).Info("query API listening")
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := apiSrv.Shutdown(shutdownCtx); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Warn("API server shutdown error")
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Periodically evict cache entries for workloads that have stopped
	// reporting (deleted workloads, decommissioned nodes, etc.) so the
	// in-memory cache doesn't grow forever. Independent of CacheMaxAge,
	// which only controls what All()/ByNode() *return*, not what's *kept*.
	go func() {
		ticker := time.NewTicker(cfg.CachePruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if removed := latestCache.Prune(cfg.CacheMaxAge); removed > 0 {
					log.WithField("removed", removed).Debug("pruned stale entries from usage cache")
				}
			}
		}
	}()

	log.WithFields(logrus.Fields{
		"stream":         cfg.StreamName,
		"consumer_group": cfg.ConsumerGroup,
		"consumer_name":  cfg.ConsumerName,
		"workers":        cfg.Workers,
	}).Info("persys-meter starting")

	if err := c.Run(ctx); err != nil {
		log.WithError(err).Fatal("consumer exited with error")
	}

	log.Info("persys-meter stopped cleanly")
}
