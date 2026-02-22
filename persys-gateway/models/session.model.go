package models

import "time"

type OAuthSession struct {
	State     string    `bson:"state" json:"state"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	ExpiresAt time.Time `bson:"expires_at" json:"expires_at"`
	Consumed  bool      `bson:"consumed" json:"consumed"`
}
