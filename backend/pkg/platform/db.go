package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is what pgxpool.Pool and pgx.Tx have in common: stores take it so they run in the caller's
// transaction (the outbox/inbox pattern needs that) or on the pool.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TxBeginner starts a transaction; *pgxpool.Pool implements it.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PoolConfig configures NewPool.
type PoolConfig struct {
	DSN string
	// MaxConns defaults to pgx's own default (the larger of 4 and the CPU count) when zero.
	MaxConns int32
	// AppName shows up in pg_stat_activity.
	AppName string
}

// NewPool opens a pgx pool and checks it can reach the database.
func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("platform: parse database url: %w", err)
	}
	if cfg.MaxConns > 0 {
		pc.MaxConns = cfg.MaxConns
	}
	if cfg.AppName != "" {
		pc.ConnConfig.RuntimeParams["application_name"] = cfg.AppName
	}
	pc.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("platform: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("platform: ping database: %w", err)
	}
	return pool, nil
}

// WithTx runs fn in a transaction: committed if fn returns nil, rolled back otherwise (also on panic).
func WithTx(ctx context.Context, db TxBeginner, fn func(tx pgx.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("platform: begin: %w", err)
	}
	// After a successful Commit this is a no-op; detached from ctx so a cancelled request still rolls back.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("platform: commit: %w", err)
	}
	return nil
}
