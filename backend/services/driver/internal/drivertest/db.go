package drivertest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/migrations"
)

// Pool is a pool on a fresh schema with all of driver-api's tables, or the test is skipped without
// TEST_DATABASE_URL.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := platformtest.Pool(t)
	own, err := migrations.All()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platform.Migrate(t.Context(), pool, slices.Concat(platform.Migrations(), eventbus.Migrations(), notices.Migrations(), own)...); err != nil {
		t.Fatal(err)
	}
	return pool
}

// Logger discards its output.
func Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Publisher is a publisher whose events stay in the outbox: Outbox reads them back. It has no Kafka client.
func Publisher(pool *pgxpool.Pool) *eventbus.Publisher {
	return eventbus.NewPublisher("driver-api", pool, nil, Logger())
}

// Published is one row of the outbox, decoded.
type Published struct {
	Type    string
	Key     string
	Version int64
	Data    jsontext.Value
}

// Payload decodes the event's data into T.
func Payload[T any](t testing.TB, p Published) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(p.Data, &v); err != nil {
		t.Fatalf("decode %s payload: %v", p.Type, err)
	}
	return v
}

// Outbox returns every event enqueued so far, in publishing order.
func Outbox(t testing.TB, pool *pgxpool.Pool) []Published {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT payload FROM outbox ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []Published
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		env, err := contracts.Decode([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, Published{Type: env.Type, Key: env.Key, Version: env.AggregateVersion, Data: env.Data})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Types lists the event types of events.
func Types(events []Published) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

// Clock is a clock a test moves by hand.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func NewClock(start time.Time) *Clock { return &Clock{now: start} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t, forward or back.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) { c.Set(c.Now().Add(d)) }
