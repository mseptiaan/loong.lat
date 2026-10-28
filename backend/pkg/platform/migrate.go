package platform

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration is one SQL script applied once. ID must be unique across everything a database receives, so
// shared scripts and a service's own are prefixed differently (see MigrationsFromFS).
type Migration struct {
	ID  string
	SQL string
}

var (
	//go:embed sql/sessions.sql
	sessionsSQL string
	//go:embed sql/idempotency.sql
	idempotencySQL string
)

// Migrations returns the platform's own tables (sessions, idempotency keys). Apply them before the
// service's migrations: slices.Concat(platform.Migrations(), eventbus.Migrations(), own).
func Migrations() []Migration {
	return []Migration{
		{ID: "platform/0001_sessions", SQL: sessionsSQL},
		{ID: "platform/0002_idempotency", SQL: idempotencySQL},
	}
}

// MigrationsFromFS reads the *.sql files in the root of fsys in file-name order (use fs.Sub for a directory
// of an embed.FS). Each ID is prefix + "/" + the file name without extension.
func MigrationsFromFS(prefix string, fsys fs.FS) ([]Migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	migrations := make([]Migration, 0, len(names))
	for _, name := range names {
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		id := prefix + "/" + strings.TrimSuffix(path.Base(name), ".sql")
		migrations = append(migrations, Migration{ID: id, SQL: string(body)})
	}
	return migrations, nil
}

// migrationLockID is the advisory-lock key that serializes concurrent migrators (several replicas starting).
const migrationLockID int64 = 0x6c6f6f6e675f6d67 // "loong_mg"

// Migrate applies the migrations that the database has not seen, in the order given, each in its own
// transaction together with its schema_migrations row. It returns the IDs it applied. A migration that was
// already applied but whose SQL has changed since is an error: edit by adding a new migration.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrations ...Migration) (applied []string, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("platform: migrate: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return nil, fmt.Errorf("platform: migrate: lock: %w", err)
	}
	defer func() {
		// Detached: the lock must be released even when ctx was cancelled.
		if _, unlockErr := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID); unlockErr != nil && err == nil {
			err = fmt.Errorf("platform: migrate: unlock: %w", unlockErr)
		}
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		id         text        PRIMARY KEY,
		checksum   text        NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, fmt.Errorf("platform: migrate: %w", err)
	}

	done := map[string]string{}
	rows, err := conn.Query(ctx, `SELECT id, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("platform: migrate: %w", err)
	}
	for rows.Next() {
		var id, sum string
		if err := rows.Scan(&id, &sum); err != nil {
			rows.Close()
			return nil, fmt.Errorf("platform: migrate: %w", err)
		}
		done[id] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("platform: migrate: %w", err)
	}

	for _, m := range migrations {
		sum := checksum(m.SQL)
		if prev, ok := done[m.ID]; ok {
			if prev != sum {
				return applied, fmt.Errorf("platform: migrate: %s was applied with different SQL; add a new migration instead of editing it", m.ID)
			}
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return applied, fmt.Errorf("platform: migrate %s: %w", m.ID, err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return applied, fmt.Errorf("platform: migrate %s: %w", m.ID, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (id, checksum) VALUES ($1, $2)`, m.ID, sum); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return applied, fmt.Errorf("platform: migrate %s: %w", m.ID, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, fmt.Errorf("platform: migrate %s: %w", m.ID, err)
		}
		applied = append(applied, m.ID)
	}
	return applied, nil
}

func checksum(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}
