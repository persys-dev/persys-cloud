package store

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"
)

type PostgresLeaderElector struct {
	db           *sql.DB
	lockID       int64
	pollInterval time.Duration

	mu       sync.Mutex
	conn     *sql.Conn
	isLeader bool
}

func NewPostgresLeaderElector(db *sql.DB, lockID int64, pollInterval time.Duration) *PostgresLeaderElector {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &PostgresLeaderElector{db: db, lockID: lockID, pollInterval: pollInterval}
}

func (e *PostgresLeaderElector) Start(ctx context.Context) <-chan bool {
	updates := make(chan bool, 1)
	go func() {
		defer close(updates)
		ticker := time.NewTicker(e.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				e.revokeLeader(ctx, updates)
				return
			case <-ticker.C:
				e.step(ctx, updates)
			}
		}
	}()
	return updates
}

func (e *PostgresLeaderElector) step(ctx context.Context, updates chan<- bool) {
	e.mu.Lock()
	conn := e.conn
	isLeader := e.isLeader
	e.mu.Unlock()

	if isLeader && conn != nil {
		if _, err := conn.ExecContext(ctx, `SELECT 1`); err != nil {
			log.Printf("leader election health check failed, revoking leadership: %v", err)
			e.revokeLeader(ctx, updates)
		}
		return
	}

	conn, err := e.db.Conn(ctx)
	if err != nil {
		log.Printf("leader election failed to acquire db connection: %v", err)
		return
	}
	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, e.lockID).Scan(&acquired); err != nil {
		_ = conn.Close()
		log.Printf("leader election advisory lock query failed: %v", err)
		return
	}
	if !acquired {
		_ = conn.Close()
		return
	}

	e.mu.Lock()
	e.conn = conn
	e.isLeader = true
	e.mu.Unlock()

	select {
	case updates <- true:
	default:
	}
}

func (e *PostgresLeaderElector) revokeLeader(ctx context.Context, updates chan<- bool) {
	e.mu.Lock()
	conn := e.conn
	wasLeader := e.isLeader
	e.conn = nil
	e.isLeader = false
	e.mu.Unlock()

	if conn != nil {
		_, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, e.lockID)
		_ = conn.Close()
	}
	if wasLeader {
		select {
		case updates <- false:
		default:
		}
	}
}
