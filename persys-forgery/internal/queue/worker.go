package queue

import (
	"context"
	"encoding/json"
	"log"

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
		res, err := rdb.BLPop(ctx, 0, "forge:builds").Result()
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
		go func(r models.BuildRequest) {
			ctx := context.Background()
			_ = orchestrator.BuildWithStrategy(ctx, r, r.Strategy)
		}(req)
	}
}
