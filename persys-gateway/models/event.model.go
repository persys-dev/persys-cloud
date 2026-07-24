package models

import "time"

type WebhookEvent struct {
	DeliveryID    string    `json:"delivery_id"`
	EventName     string    `json:"event_name"`
	Repository    string    `json:"repository"`
	ClusterID     string    `json:"cluster_id"`
	Verified      bool      `json:"verified"`
	Status        string    `json:"status"`
	Attempts      int       `json:"attempts"`
	LastError     string    `json:"last_error,omitempty"`
	ReceivedAt    time.Time `json:"received_at"`
	NextRetryAt   time.Time `json:"next_retry_at,omitempty"`
	LastUpdatedAt time.Time `json:"last_updated_at"`
}
