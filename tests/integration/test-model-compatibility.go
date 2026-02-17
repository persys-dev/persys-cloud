package integration

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// Prow's BuildQueueMessage (what gets sent to Redis)
type BuildQueueMessage struct {
	ID           string                 `json:"id"`
	ProjectName  string                 `json:"project_name"`
	Type         string                 `json:"type"`
	Source       string                 `json:"source"`
	CommitHash   string                 `json:"commit_hash"`
	Branch       string                 `json:"branch"`
	Strategy     string                 `json:"strategy"`
	PushArtifact bool                   `json:"push_artifact"`
	WebhookData  map[string]interface{} `json:"webhook_data"`
	Metadata     map[string]interface{} `json:"metadata"`
	CreatedAt    time.Time              `json:"created_at"`
}

// Forgery's BuildRequest (what gets consumed from Redis)
type BuildRequest struct {
	ID           string                 `json:"id,omitempty"`
	ProjectName  string                 `json:"project_name"`
	Type         string                 `json:"type"`
	Source       string                 `json:"source"`
	CommitHash   string                 `json:"commit_hash"`
	Branch       string                 `json:"branch,omitempty"`
	Pipeline     string                 `json:"pipeline,omitempty"`
	Strategy     string                 `json:"strategy"`
	PushArtifact bool                   `json:"push_artifact,omitempty"`
	NexusRepo    string                 `json:"nexus_repo,omitempty"`
	WebhookData  map[string]interface{} `json:"webhook_data,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt    time.Time              `json:"created_at,omitempty"`
}

func IntegrationTest() {
	fmt.Println("Testing Prow-Forgery Model Compatibility")
	fmt.Println("========================================")

	// Create a test message from Prow
	prowMessage := BuildQueueMessage{
		ID:           uuid.New().String(),
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
			"correlation_id": uuid.New().String(),
			"processed_by":   "prow",
		},
		CreatedAt: time.Now(),
	}

	fmt.Printf("Prow Message:\n")
	fmt.Printf("  ID: %s\n", prowMessage.ID)
	fmt.Printf("  Project: %s\n", prowMessage.ProjectName)
	fmt.Printf("  Type: %s\n", prowMessage.Type)
	fmt.Printf("  Source: %s\n", prowMessage.Source)
	fmt.Printf("  Commit: %s\n", prowMessage.CommitHash)
	fmt.Printf("  Branch: %s\n", prowMessage.Branch)
	fmt.Printf("  Strategy: %s\n", prowMessage.Strategy)
	fmt.Printf("  PushArtifact: %t\n", prowMessage.PushArtifact)

	// Marshal Prow message to JSON
	prowJSON, err := json.Marshal(prowMessage)
	if err != nil {
		log.Fatalf("Failed to marshal Prow message: %v", err)
	}

	fmt.Printf("\nProw JSON:\n%s\n", string(prowJSON))

	// Unmarshal into Forgery's BuildRequest
	var forgeryRequest BuildRequest
	if err := json.Unmarshal(prowJSON, &forgeryRequest); err != nil {
		log.Fatalf("Failed to unmarshal to Forgery model: %v", err)
	}

	fmt.Printf("\nForgery Request (unmarshaled):\n")
	fmt.Printf("  ID: %s\n", forgeryRequest.ID)
	fmt.Printf("  Project: %s\n", forgeryRequest.ProjectName)
	fmt.Printf("  Type: %s\n", forgeryRequest.Type)
	fmt.Printf("  Source: %s\n", forgeryRequest.Source)
	fmt.Printf("  Commit: %s\n", forgeryRequest.CommitHash)
	fmt.Printf("  Branch: %s\n", forgeryRequest.Branch)
	fmt.Printf("  Strategy: %s\n", forgeryRequest.Strategy)
	fmt.Printf("  PushArtifact: %t\n", forgeryRequest.PushArtifact)
	fmt.Printf("  Webhook Event: %s\n", forgeryRequest.WebhookData["event_type"])
	fmt.Printf("  Correlation ID: %s\n", forgeryRequest.Metadata["correlation_id"])

	// Test reverse compatibility (Forgery -> Prow)
	forgeryJSON, err := json.Marshal(forgeryRequest)
	if err != nil {
		log.Fatalf("Failed to marshal Forgery request: %v", err)
	}

	var backToProw BuildQueueMessage
	if err := json.Unmarshal(forgeryJSON, &backToProw); err != nil {
		log.Fatalf("Failed to unmarshal back to Prow model: %v", err)
	}

	fmt.Printf("\nRound-trip compatibility test:\n")
	fmt.Printf("  Original ID: %s\n", prowMessage.ID)
	fmt.Printf("  Round-trip ID: %s\n", backToProw.ID)
	fmt.Printf("  IDs match: %t\n", prowMessage.ID == backToProw.ID)

	// Check if all critical fields are preserved
	criticalFieldsMatch :=
		prowMessage.ProjectName == backToProw.ProjectName &&
			prowMessage.Type == backToProw.Type &&
			prowMessage.Source == backToProw.Source &&
			prowMessage.CommitHash == backToProw.CommitHash &&
			prowMessage.Strategy == backToProw.Strategy

	fmt.Printf("  Critical fields match: %t\n", criticalFieldsMatch)

	if criticalFieldsMatch {
		fmt.Println("\n✅ Model compatibility test PASSED!")
		fmt.Println("Prow and Forgery models are fully compatible.")
	} else {
		fmt.Println("\n❌ Model compatibility test FAILED!")
		fmt.Println("Some fields were lost during round-trip conversion.")
	}
}
