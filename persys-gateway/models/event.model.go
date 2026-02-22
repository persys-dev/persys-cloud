package models

import "time"

type WebhookEvent struct {
	DeliveryID    string    `bson:"delivery_id" json:"delivery_id"`
	EventName     string    `bson:"event_name" json:"event_name"`
	Repository    string    `bson:"repository" json:"repository"`
	ClusterID     string    `bson:"cluster_id" json:"cluster_id"`
	Verified      bool      `bson:"verified" json:"verified"`
	Status        string    `bson:"status" json:"status"`
	Attempts      int       `bson:"attempts" json:"attempts"`
	LastError     string    `bson:"last_error,omitempty" json:"last_error,omitempty"`
	ReceivedAt    time.Time `bson:"received_at" json:"received_at"`
	NextRetryAt   time.Time `bson:"next_retry_at,omitempty" json:"next_retry_at,omitempty"`
	LastUpdatedAt time.Time `bson:"last_updated_at" json:"last_updated_at"`
}
