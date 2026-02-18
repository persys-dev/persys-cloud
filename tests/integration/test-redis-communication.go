package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	// Connect to Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	ctx := context.Background()

	// Test message from Prow
	testMessage := BuildQueueMessage{
		ID:           "test-build-123",
		ProjectName:  "test-org/test-repo",
		Type:         "dockerfile",
		Source:       "https://github.com/test-org/test-repo.git",
		CommitHash:   "abc123def456",
		Branch:       "main",
		Strategy:     "local",
		PushArtifact: true,
		WebhookData: map[string]interface{}{
			"event_type": "push",
			"repository": "test-org/test-repo",
			"sender":     "testuser",
			"ref":        "refs/heads/main",
			"before":     "old-commit",
			"after":      "abc123def456",
		},
		Metadata: map[string]interface{}{
			"triggered_by":   "webhook",
			"correlation_id": "correlation-123",
			"processed_by":   "prow",
		},
		CreatedAt: time.Now(),
	}

	// Marshal to JSON
	messageJSON, err := json.Marshal(testMessage)
	if err != nil {
		log.Fatalf("Failed to marshal message: %v", err)
	}

	fmt.Printf("Sending message to Redis:\n%s\n", string(messageJSON))

	// Push to Redis queue
	err = rdb.LPush(ctx, "forge:builds", messageJSON).Err()
	if err != nil {
		log.Fatalf("Failed to push to Redis: %v", err)
	}

	fmt.Println("Message sent to Redis queue 'forge:builds'")

	// Test consuming the message (simulating Forgery)
	fmt.Println("\nTesting message consumption...")
	res, err := rdb.BLPop(ctx, 5*time.Second, "forge:builds").Result()
	if err != nil {
		log.Fatalf("Failed to consume from Redis: %v", err)
	}

	if len(res) < 2 {
		log.Fatal("Unexpected response format")
	}

	fmt.Printf("Received message: %s\n", res[1])

	// Test unmarshaling into BuildRequest (Forgery's model)
	var receivedMessage BuildQueueMessage
	if err := json.Unmarshal([]byte(res[1]), &receivedMessage); err != nil {
		log.Fatalf("Failed to unmarshal received message: %v", err)
	}

	fmt.Printf("Successfully unmarshaled message:\n")
	fmt.Printf("  ID: %s\n", receivedMessage.ID)
	fmt.Printf("  Project: %s\n", receivedMessage.ProjectName)
	fmt.Printf("  Type: %s\n", receivedMessage.Type)
	fmt.Printf("  Source: %s\n", receivedMessage.Source)
	fmt.Printf("  Commit: %s\n", receivedMessage.CommitHash)
	fmt.Printf("  Branch: %s\n", receivedMessage.Branch)
	fmt.Printf("  Strategy: %s\n", receivedMessage.Strategy)
	fmt.Printf("  Webhook Event: %s\n", receivedMessage.WebhookData["event_type"])

	fmt.Println("\n✅ Redis communication test successful!")
}
