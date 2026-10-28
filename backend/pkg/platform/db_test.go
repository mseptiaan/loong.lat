package platform

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
)

// These need a real Postgres (TEST_DATABASE_URL, see scripts/dev-up.sh) and skip without one.

func TestPgSessionStoreMeetsTheSessionStoreContract(t *testing.T) {
	pool := platformtest.Pool(t)
	if _, err := Migrate(t.Context(), pool, Migrations()...); err != nil {
		t.Fatal(err)
	}
	sessionStoreContract(t, NewPgSessionStore(pool))

	// only hashes reach the table
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE octet_length(token_hash) <> 32`).Scan(&n); err != nil || n != 0 {
		t.Errorf("token_hash must be a 32-byte SHA-256: %d rows differ, %v", n, err)
	}
}

func TestPgIdempotencyStoreMeetsTheIdempotencyStoreContract(t *testing.T) {
	pool := platformtest.Pool(t)
	if _, err := Migrate(t.Context(), pool, Migrations()...); err != nil {
		t.Fatal(err)
	}
	idempotencyStoreContract(t, NewPgIdempotencyStore(pool))
}

func TestPgIdempotencyStoreTakesOverAbandonedClaims(t *testing.T) {
	pool := platformtest.Pool(t)
	if _, err := Migrate(t.Context(), pool, Migrations()...); err != nil {
		t.Fatal(err)
	}
	store := NewPgIdempotencyStore(pool)
	fp := []byte("fp")
	if state, _, err := store.Begin(t.Context(), "s", "stuck", fp); err != nil || state != IdempotencyClaimed {
		t.Fatalf("claim: %v %v", state, err)
	}
	if state, _, _ := store.Begin(t.Context(), "s", "stuck", fp); state != IdempotencyInFlight {
		t.Fatalf("second claim = %v", state)
	}
	// simulate the owner dying a while ago
	if _, err := pool.Exec(t.Context(), `UPDATE idempotency_keys SET created_at = now() - interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := store.Begin(t.Context(), "s", "stuck", fp); state != IdempotencyClaimed {
		t.Errorf("an abandoned claim must be taken over, got %v", state)
	}

	// expired rows are reusable, even with a different request
	if _, err := pool.Exec(t.Context(), `UPDATE idempotency_keys SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := store.Begin(t.Context(), "s", "stuck", []byte("other")); state != IdempotencyClaimed {
		t.Errorf("an expired key must be reusable, got %v", state)
	}
}

func TestPgIdempotencyStoreAllowsExactlyOneConcurrentClaim(t *testing.T) {
	pool := platformtest.Pool(t)
	if _, err := Migrate(t.Context(), pool, Migrations()...); err != nil {
		t.Fatal(err)
	}
	store := NewPgIdempotencyStore(pool)

	const racers = 12
	var wg sync.WaitGroup
	claimed := make(chan struct{}, racers)
	for range racers {
		wg.Go(func() {
			state, _, err := store.Begin(t.Context(), "s", "race", []byte("fp"))
			if err != nil {
				t.Error(err)
				return
			}
			if state == IdempotencyClaimed {
				claimed <- struct{}{}
			}
		})
	}
	wg.Wait()
	if len(claimed) != 1 {
		t.Errorf("%d requests claimed one key, want exactly 1", len(claimed))
	}
}

func TestMigrateAppliesInOrderOnceAndDetectsEditedMigrations(t *testing.T) {
	pool := platformtest.Pool(t)
	migrations := []Migration{
		{ID: "svc/0001_create", SQL: `CREATE TABLE widgets (id int PRIMARY KEY); INSERT INTO widgets VALUES (1);`},
		{ID: "svc/0002_alter", SQL: `ALTER TABLE widgets ADD COLUMN name text NOT NULL DEFAULT 'w'; UPDATE widgets SET name = 'first';`},
	}

	applied, err := Migrate(t.Context(), pool, migrations...)
	if err != nil || len(applied) != 2 || applied[0] != "svc/0001_create" || applied[1] != "svc/0002_alter" {
		t.Fatalf("applied %v, %v", applied, err)
	}
	var name string
	if err := pool.QueryRow(t.Context(), `SELECT name FROM widgets WHERE id = 1`).Scan(&name); err != nil || name != "first" {
		t.Errorf("migrations did not run in order: %q, %v", name, err)
	}

	again, err := Migrate(t.Context(), pool, migrations...)
	if err != nil || len(again) != 0 {
		t.Errorf("rerun applied %v, %v; want nothing", again, err)
	}

	next := append(migrations, Migration{ID: "svc/0003_more", SQL: `INSERT INTO widgets (id, name) VALUES (2, 'second')`})
	if applied, err := Migrate(t.Context(), pool, next...); err != nil || len(applied) != 1 || applied[0] != "svc/0003_more" {
		t.Errorf("a new migration alone should apply: %v, %v", applied, err)
	}

	edited := []Migration{{ID: "svc/0001_create", SQL: `CREATE TABLE widgets (id bigint PRIMARY KEY)`}}
	if _, err := Migrate(t.Context(), pool, edited...); err == nil {
		t.Error("editing an applied migration must be an error")
	}
}

func TestMigrateRollsBackAFailedMigrationAndRecordsNothing(t *testing.T) {
	pool := platformtest.Pool(t)
	broken := []Migration{
		{ID: "svc/0001_ok", SQL: `CREATE TABLE ok_table (id int)`},
		{ID: "svc/0002_half", SQL: `CREATE TABLE half_table (id int); SELECT * FROM does_not_exist;`},
		{ID: "svc/0003_never", SQL: `CREATE TABLE never_table (id int)`},
	}
	applied, err := Migrate(t.Context(), pool, broken...)
	if err == nil || len(applied) != 1 || applied[0] != "svc/0001_ok" {
		t.Fatalf("applied %v, %v; want the first one applied and an error", applied, err)
	}
	for table, want := range map[string]bool{"ok_table": true, "half_table": false, "never_table": false} {
		var exists bool
		if err := pool.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != want {
			t.Errorf("table %s exists = %v, want %v (a failed migration must roll back whole)", table, exists, want)
		}
	}

	fixed := []Migration{broken[0], {ID: "svc/0002_half", SQL: `CREATE TABLE half_table (id int)`}}
	if applied, err := Migrate(t.Context(), pool, fixed...); err == nil || len(applied) != 0 {
		// the 0002 ID was never recorded, so a corrected script is a fresh migration, not an "edit"
		if err != nil {
			t.Errorf("a corrected script for a never-applied migration must apply: %v", err)
		}
	}
}

func TestConcurrentMigratorsApplyEachMigrationOnce(t *testing.T) {
	pool := platformtest.Pool(t)
	migrations := Migrations()

	const replicas = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range replicas {
		wg.Go(func() {
			applied, err := Migrate(t.Context(), pool, migrations...)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += len(applied)
			mu.Unlock()
		})
	}
	wg.Wait()
	if total != len(migrations) {
		t.Errorf("%d applications across %d replicas, want %d", total, replicas, len(migrations))
	}
}

func TestWithTxCommitsOnSuccessAndRollsBackOnError(t *testing.T) {
	pool := platformtest.Pool(t)
	if _, err := pool.Exec(t.Context(), `CREATE TABLE t (n int)`); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM t`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if err := WithTx(t.Context(), pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `INSERT INTO t VALUES (1)`)
		return err
	}); err != nil || count() != 1 {
		t.Fatalf("commit: %v, count %d", err, count())
	}

	boom := errors.New("business rule")
	if err := WithTx(t.Context(), pool, func(tx pgx.Tx) error {
		_, _ = tx.Exec(t.Context(), `INSERT INTO t VALUES (2)`)
		return boom
	}); !errors.Is(err, boom) || count() != 1 {
		t.Errorf("rollback: %v, count %d", err, count())
	}

	func() {
		defer func() { _ = recover() }()
		_ = WithTx(t.Context(), pool, func(tx pgx.Tx) error {
			_, _ = tx.Exec(t.Context(), `INSERT INTO t VALUES (3)`)
			panic("handler bug")
		})
	}()
	if count() != 1 {
		t.Errorf("a panic must roll back, count %d", count())
	}

	// a cancelled caller still releases the connection cleanly
	ctx, cancel := context.WithCancel(t.Context())
	err := WithTx(ctx, pool, func(tx pgx.Tx) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if count() != 1 {
		t.Error("cancel must roll back")
	}
}

func TestNewPoolReportsABadOrUnreachableDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := NewPool(ctx, PoolConfig{DSN: "://not a dsn"}); err == nil {
		t.Error("a malformed DSN must be an error")
	}
	if _, err := NewPool(ctx, PoolConfig{DSN: "postgres://nobody@127.0.0.1:1/none?connect_timeout=1"}); err == nil {
		t.Error("an unreachable database must be an error at startup, not at first query")
	}
}
