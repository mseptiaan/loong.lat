// Package platformtest gives tests a real Postgres schema when one is available.
package platformtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvDatabaseURL names the variable holding the DSN of a throwaway Postgres (scripts/dev-up.sh prints it).
const EnvDatabaseURL = "TEST_DATABASE_URL"

// Pool returns a pool whose connections live in a fresh, empty schema of the database in TEST_DATABASE_URL,
// so tests never see each other's tables. The test is skipped when the variable is unset. The schema is
// dropped when the test ends.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(EnvDatabaseURL)
	if dsn == "" {
		t.Skipf("%s is not set; run scripts/dev-up.sh and export it to run database tests", EnvDatabaseURL)
	}
	ctx := t.Context()

	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	schema := "t_" + hex.EncodeToString(suffix[:])

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("platformtest: connect: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("platformtest: create schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("platformtest: parse %s: %v", EnvDatabaseURL, err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		admin.Close()
		t.Fatalf("platformtest: open pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		// t.Context() is already cancelled here.
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	return pool
}
