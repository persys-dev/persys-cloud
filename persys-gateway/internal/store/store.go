// Package store is the gateway's only database dependency now — Postgres
// via pgx, replacing MongoDB entirely. Three tables (see schema.sql):
// users, oauth_sessions, webhook_events. That's the complete list of
// things the gateway's code actually persists; see schema.sql for what
// was deliberately NOT carried over from Mongo and why.
package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/persys-dev/persys-cloud/persys-gateway/models"
)

//go:embed schema.sql
var schemaSQL string

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

// New connects to Postgres and applies the schema. The schema is plain
// CREATE TABLE/INDEX IF NOT EXISTS — safe to run on every startup rather
// than needing a separate migration-runner step, which matters for the
// self-hosted "works out of the box" story: a fresh Postgres just works,
// no manual migration command required first.
func New(ctx context.Context, dsn string, maxConns int32) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("run schema migration: %w", err)
	}
	return nil
}

func (s *Store) Close() { s.pool.Close() }

// ---- Users ----

// UpsertUser creates or updates a user record via Postgres' native
// ON CONFLICT — atomic by construction. Replaces the old Mongo
// find-then-branch logic, which had a bug where InsertOne ran
// unconditionally regardless of which branch was taken, relying on a
// unique index that was never actually created to reject the resulting
// duplicate. Concurrent logins for the same user_id cannot create two
// rows here no matter how they interleave; Postgres' own conflict
// resolution guarantees it, not application-level branching.
func (s *Store) UpsertUser(ctx context.Context, u *models.UserInput) (*models.DBResponse, error) {
	const q = `
		INSERT INTO users (user_id, login, name, email, company, url, github_token, persys_token, state, status, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
		ON CONFLICT (user_id) DO UPDATE SET
			login        = EXCLUDED.login,
			name         = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE users.name END,
			email        = CASE WHEN EXCLUDED.email <> '' THEN EXCLUDED.email ELSE users.email END,
			company      = CASE WHEN EXCLUDED.company <> '' THEN EXCLUDED.company ELSE users.company END,
			url          = CASE WHEN EXCLUDED.url <> '' THEN EXCLUDED.url ELSE users.url END,
			github_token = EXCLUDED.github_token,
			persys_token = EXCLUDED.persys_token,
			state        = EXCLUDED.state,
			updated_at   = now()
		RETURNING user_id, login, name, email, company, url, github_token, persys_token, state, status,
		          created_at::text, updated_at::text
	`
	row := s.pool.QueryRow(ctx, q, u.UserID, u.Login, u.Name, u.Email, u.Company, u.URL, u.GithubToken, u.PersysToken, u.State, u.Status)
	return scanUser(row)
}

func (s *Store) FindUserByID(ctx context.Context, userID int64) (*models.DBResponse, error) {
	const q = `
		SELECT user_id, login, name, email, company, url, github_token, persys_token, state, status,
		       created_at::text, updated_at::text
		FROM users WHERE user_id = $1
	`
	return scanUser(s.pool.QueryRow(ctx, q, userID))
}

// FindUserByState supports the CLI login flow, which polls by the OAuth
// state token issued at the start of the flow rather than a user ID it
// doesn't have yet.
func (s *Store) FindUserByState(ctx context.Context, state string) (*models.DBResponse, error) {
	const q = `
		SELECT user_id, login, name, email, company, url, github_token, persys_token, state, status,
		       created_at::text, updated_at::text
		FROM users WHERE state = $1
		ORDER BY updated_at DESC LIMIT 1
	`
	return scanUser(s.pool.QueryRow(ctx, q, state))
}

func scanUser(row pgx.Row) (*models.DBResponse, error) {
	var u models.DBResponse
	err := row.Scan(
		&u.UserID, &u.Login, &u.Name, &u.Email, &u.Company, &u.URL,
		&u.GithubToken, &u.PersysToken, &u.State, &u.Status, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// ---- OAuth CSRF state ----
//
// Replaces the package-level `state` Go variable the original
// AuthController held, overwritten on every login attempt — a real race
// under concurrent logins. A row per in-flight attempt, keyed by the
// state token itself, closes that outright: two logins never share a
// mutable slot.

func (s *Store) StoreOAuthState(ctx context.Context, state string, ttl time.Duration) error {
	const q = `
		INSERT INTO oauth_sessions (state, created_at, expires_at, consumed)
		VALUES ($1, now(), now() + $2, false)
		ON CONFLICT (state) DO UPDATE SET created_at = now(), expires_at = now() + $2, consumed = false
	`
	_, err := s.pool.Exec(ctx, q, state, ttl)
	return err
}

// ValidateAndConsumeState atomically checks and marks a CSRF state token
// used in one round trip, so a token can be redeemed exactly once even
// under concurrent requests racing the same state value.
func (s *Store) ValidateAndConsumeState(ctx context.Context, state string) error {
	const q = `
		UPDATE oauth_sessions SET consumed = true
		WHERE state = $1 AND consumed = false AND expires_at > now()
	`
	tag, err := s.pool.Exec(ctx, q, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("oauth state invalid, expired, or already used")
	}
	return nil
}

// ---- Webhook delivery audit trail ----

func (s *Store) UpsertWebhookEvent(ctx context.Context, e *models.WebhookEvent) error {
	const q = `
		INSERT INTO webhook_events
			(delivery_id, event_name, repository, cluster_id, verified, status, attempts, last_error, received_at, next_retry_at, last_updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (delivery_id) DO UPDATE SET
			event_name      = EXCLUDED.event_name,
			repository      = EXCLUDED.repository,
			cluster_id      = EXCLUDED.cluster_id,
			verified        = EXCLUDED.verified,
			status          = EXCLUDED.status,
			attempts        = EXCLUDED.attempts,
			last_error      = EXCLUDED.last_error,
			next_retry_at   = EXCLUDED.next_retry_at,
			last_updated_at = EXCLUDED.last_updated_at
	`
	_, err := s.pool.Exec(ctx, q,
		e.DeliveryID, e.EventName, e.Repository, e.ClusterID, e.Verified, e.Status, e.Attempts, e.LastError,
		nilIfZero(e.ReceivedAt), nilIfZero(e.NextRetryAt), e.LastUpdatedAt,
	)
	return err
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
