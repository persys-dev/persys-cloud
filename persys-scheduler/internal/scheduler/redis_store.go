package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

var redisLogger = logging.C("scheduler.redis")

func (s *Scheduler) initRedisStore() {
	if s.cfg == nil || s.cfg.RedisAddr == "" {
		return
	}
	client := redis.NewClient(&redis.Options{
		Addr:     s.cfg.RedisAddr,
		Password: s.cfg.RedisPassword,
		DB:       s.cfg.RedisDB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		redisLogger.WithError(err).Warn("redis configured but unavailable; falling back to etcd for reconciliation telemetry")
		_ = client.Close()
		return
	}
	s.redisClient = client
	redisLogger.WithField("addr", s.cfg.RedisAddr).Info("redis telemetry store enabled")
}

func (s *Scheduler) writeReconciliationTelemetry(workloadID, action string, success bool, reason string, attemptedAt time.Time) {
	rec := map[string]interface{}{
		"workloadId":  workloadID,
		"action":      action,
		"success":     success,
		"reason":      reason,
		"attemptedAt": attemptedAt.UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if s.redisClient != nil {
		ttl := 24 * time.Hour
		if s.cfg != nil && s.cfg.RedisReconcileTTL > 0 {
			ttl = s.cfg.RedisReconcileTTL
		}
		key := fmt.Sprintf("reconciliation:%s", workloadID)
		historyKey := "reconciliation:history"
		if err := s.redisClient.Set(context.Background(), key, payload, ttl).Err(); err == nil {
			maxEntries := int64(2000)
			if s.cfg != nil && s.cfg.RedisEventMaxEntries > 0 {
				maxEntries = s.cfg.RedisEventMaxEntries
			}
			pipe := s.redisClient.TxPipeline()
			pipe.LPush(context.Background(), historyKey, payload)
			pipe.LTrim(context.Background(), historyKey, 0, maxEntries-1)
			pipe.Expire(context.Background(), historyKey, ttl)
			_, _ = pipe.Exec(context.Background())
			return
		}
		redisLogger.WithError(err).WithFields(logrus.Fields{"key": key}).Warn("failed writing reconciliation telemetry to redis")
	}
	_ = s.RetryableEtcdPut(reconciliationKey(workloadID), string(payload))
}

func (s *Scheduler) writeEventTelemetry(payload []byte) bool {
	if s.redisClient == nil {
		return false
	}
	ttl := 24 * time.Hour
	maxEntries := int64(2000)
	if s.cfg != nil {
		if s.cfg.RedisEventTTL > 0 {
			ttl = s.cfg.RedisEventTTL
		}
		if s.cfg.RedisEventMaxEntries > 0 {
			maxEntries = s.cfg.RedisEventMaxEntries
		}
	}
	pipe := s.redisClient.TxPipeline()
	pipe.LPush(context.Background(), "events:history", payload)
	pipe.LTrim(context.Background(), "events:history", 0, maxEntries-1)
	pipe.Expire(context.Background(), "events:history", ttl)
	_, err := pipe.Exec(context.Background())
	return err == nil
}
