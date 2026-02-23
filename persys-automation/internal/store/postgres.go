package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	automationv1 "github.com/persys-dev/persys-cloud/persys-automation/internal/automationv1"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	s := &PostgresStore{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) DB() *sql.DB { return s.db }

func (s *PostgresStore) migrate(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS automation_policies (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL,
			target_workload TEXT NOT NULL,
			policy_type INTEGER NOT NULL,
			condition_expression TEXT NOT NULL,
			action_expression TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS automation_policies_enabled_idx ON automation_policies (enabled)`,
		`CREATE INDEX IF NOT EXISTS automation_policies_created_at_idx ON automation_policies (created_at)`,
		`CREATE TABLE IF NOT EXISTS automation_audit_log (
			id UUID PRIMARY KEY,
			policy_id UUID NOT NULL,
			policy_name TEXT NOT NULL,
			target_workload TEXT NOT NULL,
			matched BOOLEAN NOT NULL,
			dispatched BOOLEAN NOT NULL,
			reason TEXT NOT NULL,
			old_state TEXT,
			new_state TEXT,
			created_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS automation_audit_created_at_idx ON automation_audit_log (created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS automation_suggestion_queue (
			id UUID PRIMARY KEY,
			policy_id TEXT NOT NULL,
			policy_name TEXT NOT NULL,
			target_workload TEXT NOT NULL,
			action_type TEXT NOT NULL,
			desired_state TEXT,
			desired_replicas INTEGER NOT NULL DEFAULT 0,
			replica_delta INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS automation_suggestion_queue_status_idx ON automation_suggestion_queue (status, updated_at ASC)`,
	}
	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("postgres migration failed: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) CreatePolicy(ctx context.Context, req *automationv1.CreatePolicyRequest) (*automationv1.Policy, error) {
	now := time.Now().UTC()
	policy := &automationv1.Policy{
		Id:                  uuid.NewString(),
		Name:                req.GetName(),
		TargetWorkload:      req.GetTargetWorkload(),
		Type:                req.GetType(),
		ConditionExpression: req.GetConditionExpression(),
		ActionExpression:    req.GetActionExpression(),
		Enabled:             true,
		CreatedAt:           toProtoTime(now),
		UpdatedAt:           toProtoTime(now),
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO automation_policies (id, name, target_workload, policy_type, condition_expression, action_expression, enabled, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		policy.GetId(), policy.GetName(), policy.GetTargetWorkload(), int32(policy.GetType()), policy.GetConditionExpression(), policy.GetActionExpression(), policy.GetEnabled(), now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert policy: %w", err)
	}
	return policy, nil
}

func (s *PostgresStore) ListPolicies(ctx context.Context, includeDisabled bool) ([]*automationv1.Policy, error) {
	query := `SELECT id, name, target_workload, policy_type, condition_expression, action_expression, enabled, created_at, updated_at FROM automation_policies`
	args := make([]any, 0, 1)
	if !includeDisabled {
		query += ` WHERE enabled = TRUE`
	}
	query += ` ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list policies: %w", err)
	}
	defer rows.Close()

	policies := make([]*automationv1.Policy, 0)
	for rows.Next() {
		var (
			id, name, target, cond, action string
			typ                            int32
			enabled                        bool
			createdAt, updatedAt           time.Time
		)
		if err := rows.Scan(&id, &name, &target, &typ, &cond, &action, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan policy: %w", err)
		}
		policies = append(policies, &automationv1.Policy{
			Id:                  id,
			Name:                name,
			TargetWorkload:      target,
			Type:                automationv1.PolicyType(typ),
			ConditionExpression: cond,
			ActionExpression:    action,
			Enabled:             enabled,
			CreatedAt:           toProtoTime(createdAt),
			UpdatedAt:           toProtoTime(updatedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate policies: %w", err)
	}
	return policies, nil
}

func (s *PostgresStore) GetPolicy(ctx context.Context, id string) (*automationv1.Policy, error) {
	var (
		name, target, cond, action string
		typ                        int32
		enabled                    bool
		createdAt, updatedAt       time.Time
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT name, target_workload, policy_type, condition_expression, action_expression, enabled, created_at, updated_at
		 FROM automation_policies WHERE id = $1`, id,
	).Scan(&name, &target, &typ, &cond, &action, &enabled, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPolicyNotFound
		}
		return nil, fmt.Errorf("get policy: %w", err)
	}
	return &automationv1.Policy{
		Id:                  id,
		Name:                name,
		TargetWorkload:      target,
		Type:                automationv1.PolicyType(typ),
		ConditionExpression: cond,
		ActionExpression:    action,
		Enabled:             enabled,
		CreatedAt:           toProtoTime(createdAt),
		UpdatedAt:           toProtoTime(updatedAt),
	}, nil
}

func (s *PostgresStore) SetPolicyEnabled(ctx context.Context, id string, enabled bool) (*automationv1.Policy, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE automation_policies SET enabled = $1, updated_at = $2 WHERE id = $3`, enabled, time.Now().UTC(), id)
	if err != nil {
		return nil, fmt.Errorf("update policy enabled: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, ErrPolicyNotFound
	}
	return s.GetPolicy(ctx, id)
}

func (s *PostgresStore) AppendAudit(ctx context.Context, entry AuditEntry) error {
	id := uuid.NewString()
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO automation_audit_log (id, policy_id, policy_name, target_workload, matched, dispatched, reason, old_state, new_state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, entry.PolicyID, entry.PolicyName, entry.TargetWorkload, entry.Matched, entry.Dispatched, entry.Reason, entry.OldState, entry.NewState, now,
	)
	if err != nil {
		return fmt.Errorf("insert audit: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListAudit(ctx context.Context, limit uint32) ([]AuditEntry, error) {
	if limit == 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, policy_id, policy_name, target_workload, matched, dispatched, reason, old_state, new_state, created_at
		 FROM automation_audit_log ORDER BY created_at DESC LIMIT $1`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()

	entries := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.PolicyID, &e.PolicyName, &e.TargetWorkload, &e.Matched, &e.Dispatched, &e.Reason, &e.OldState, &e.NewState, &e.Timestamp); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit: %w", err)
	}
	return entries, nil
}

func (s *PostgresStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *PostgresStore) EnqueueSuggestion(ctx context.Context, item QueuedSuggestion) error {
	now := time.Now().UTC()
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx, `INSERT INTO automation_suggestion_queue
		(id, policy_id, policy_name, target_workload, action_type, desired_state, desired_replicas, replica_delta, reason, status, attempts, last_error, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending',0,'',$10,$10)`,
		id, item.PolicyID, item.PolicyName, item.TargetWorkload, item.ActionType, item.DesiredState, item.DesiredReplicas, item.ReplicaDelta, item.Reason, now,
	)
	if err != nil {
		return fmt.Errorf("enqueue suggestion: %w", err)
	}
	return nil
}

func (s *PostgresStore) ClaimQueuedSuggestions(ctx context.Context, limit int) ([]QueuedSuggestion, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, policy_id, policy_name, target_workload, action_type, desired_state, desired_replicas, replica_delta, reason, attempts, COALESCE(last_error,''), created_at, updated_at
		FROM automation_suggestion_queue
		WHERE status = 'pending'
		ORDER BY updated_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim queued suggestions: %w", err)
	}
	defer rows.Close()
	out := make([]QueuedSuggestion, 0, limit)
	for rows.Next() {
		var q QueuedSuggestion
		if err := rows.Scan(&q.ID, &q.PolicyID, &q.PolicyName, &q.TargetWorkload, &q.ActionType, &q.DesiredState, &q.DesiredReplicas, &q.ReplicaDelta, &q.Reason, &q.Attempts, &q.LastError, &q.CreatedAt, &q.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan queued suggestion: %w", err)
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate queued suggestions: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) MarkQueuedSuggestionResult(ctx context.Context, id string, success bool, reason string) error {
	if success {
		_, err := s.db.ExecContext(ctx, `DELETE FROM automation_suggestion_queue WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete queued suggestion: %w", err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE automation_suggestion_queue
		SET attempts = attempts + 1, last_error = $2, updated_at = $3
		WHERE id = $1`, id, reason, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("mark queued suggestion failure: %w", err)
	}
	return nil
}
