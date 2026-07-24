package models

import "time"

// OAuthSession mirrors the oauth_sessions table (internal/store/schema.sql).
// Not required by store.Store's own methods, which scan directly into
// primitives, but kept as the documented shape of what that table holds.
type OAuthSession struct {
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Consumed  bool      `json:"consumed"`
}
