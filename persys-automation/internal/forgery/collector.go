package forgery

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type PipelineStatusEvent struct {
	DeliveryID string    `json:"delivery_id"`
	Repository string    `json:"repository"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	Timestamp  time.Time `json:"timestamp"`
}

type Collector struct {
	client      *redis.Client
	key         string
	pollEvery   time.Duration
	mu          sync.RWMutex
	latestEvent PipelineStatusEvent
}

func NewCollector(addr, password string, db int, key string) *Collector {
	if strings.TrimSpace(key) == "" {
		key = "pipeline_status"
	}
	return &Collector{
		client:    redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db}),
		key:       key,
		pollEvery: 2 * time.Second,
	}
}

func (c *Collector) Start(ctx context.Context) {
	ticker := time.NewTicker(c.pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.consume(ctx)
		}
	}
}

func (c *Collector) consume(ctx context.Context) {
	for {
		payload, err := c.client.RPop(ctx, c.key).Result()
		if err == redis.Nil {
			return
		}
		if err != nil {
			log.Printf("forgery pipeline collector redis error: %v", err)
			return
		}
		var evt PipelineStatusEvent
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			log.Printf("forgery pipeline collector decode error: %v", err)
			continue
		}
		c.mu.Lock()
		c.latestEvent = evt
		c.mu.Unlock()
	}
}

func (c *Collector) LastEvent() PipelineStatusEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latestEvent
}

func (c *Collector) LastPipelineStatus() (repository, status, message string) {
	evt := c.LastEvent()
	return evt.Repository, evt.Status, evt.Message
}
