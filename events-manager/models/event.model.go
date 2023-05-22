package models

import (
	"encoding/json"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

type Event struct {
	ID                primitive.ObjectID `bson:"_id,omitempty"`
	Origin            string             `bson:"origin,omitempty"`
	Destination       string             `bson:"destination,omitempty"`
	EventType         string             `bson:"event_type,omitempty"`
	Payload           *json.RawMessage   `bson:"payload,omitempty"`
	CreatedAt         time.Time          `bson:"created_at,omitempty"`
	Username          string             `bson:"username,omitempty"`
	GithubRepoURL     string             `bson:"github_repo_url,omitempty"`
	GithubAccessToken string             `bson:"github_access_token,omitempty"`
	UserID            string             `bson:"user_id,omitempty"`
}

// TODO: implement other data models like job reports, retry , response

// Report represents the data structure of the event message.
type Report struct {
	ServiceName string             `json:"service_name"`
	JobID       primitive.ObjectID `json:"job_id"`
	JobAction   string             `json:"job_action"`
	NextAction  string             `json:"next_action"`
	Output      json.RawMessage    `json:"output"`
	Status      string             `json:"status"`
	FailCount   int                `json:"fail_count"`
}

// ProcessedEvent represents the data structure of the event proccessed
type ProcessedEvent struct {
	ProcessedID primitive.ObjectID `json:"processed_id"`
	Time        time.Time          `json:"time"`
}

type RetryEvent struct {
	// IMPLEMENT ME
}
