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
	Payload           json.RawMessage    `bson:"payload,omitempty"`
	CreatedAt         time.Time          `bson:"created_at,omitempty"`
	Username          string             `bson:"username,omitempty"`
	GithubRepoURL     string             `bson:"github_repo_url,omitempty"`
	GithubAccessToken string             `bson:"github_access_token,omitempty"`
	UserID            string             `bson:"user_id,omitempty"`
}
