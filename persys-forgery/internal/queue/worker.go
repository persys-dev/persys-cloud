package queue

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/persys-dev/persys-cloud/persys-forgery/internal/build"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
	"github.com/persys-dev/persys-cloud/persys-forgery/utils"
	"github.com/redis/go-redis/v9"
)

func StartRedisWorker(cfg *utils.Config, orchestrator *build.Orchestrator) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	ctx := context.Background()
	for {
		res, err := rdb.BLPop(ctx, 0, cfg.Redis.BuildQueueKey).Result()
		if err != nil {
			log.Println("Redis error:", err)
			continue
		}
		if len(res) < 2 {
			continue
		}
		var req models.BuildRequest
		if err := json.Unmarshal([]byte(res[1]), &req); err != nil {
			log.Println("Failed to unmarshal build request:", err)
			continue
		}
		log.Printf("Dequeued build job: %+v", req)
		publishPipelineStatus(ctx, rdb, cfg.Redis.PipelineStatusQueue, PipelineStatusEvent{
			DeliveryID: req.ID,
			Repository: req.ProjectName,
			Status:     "build_started",
			Message:    "Build dequeued and started",
			Timestamp:  time.Now().UTC(),
		})
		go func(r models.BuildRequest) {
			ctx := context.Background()
			err := orchestrator.BuildWithStrategy(ctx, r, r.Strategy)
			status := "build_succeeded"
			msg := "Build completed successfully"
			if err != nil {
				status = "build_failed"
				msg = err.Error()
			}
			publishPipelineStatus(ctx, rdb, cfg.Redis.PipelineStatusQueue, PipelineStatusEvent{
				DeliveryID: r.ID,
				Repository: r.ProjectName,
				Status:     status,
				Message:    msg,
				Timestamp:  time.Now().UTC(),
			})
			if err == nil && autoDeployEnabled(r) {
				imageTag := imageTagFor(r)
				publishPipelineStatus(ctx, rdb, cfg.Redis.PipelineStatusQueue, PipelineStatusEvent{
					DeliveryID: r.ID,
					Repository: r.ProjectName,
					Status:     "redeploy_required",
					Message:    "image ready for secure scheduler apply: " + imageTag,
					Timestamp:  time.Now().UTC(),
				})
			}
		}(req)
	}
}

func autoDeployEnabled(req models.BuildRequest) bool {
	if req.Metadata == nil {
		return false
	}
	enabled, ok := req.Metadata["auto_deploy"].(bool)
	return ok && enabled
}

func imageTagFor(req models.BuildRequest) string {
	tag := "latest"
	if req.CommitHash != "" {
		tag = req.CommitHash
	}
	if req.NexusRepo != "" {
		return req.NexusRepo + "/" + req.ProjectName + ":" + tag
	}
	return req.ProjectName + ":" + tag
}
