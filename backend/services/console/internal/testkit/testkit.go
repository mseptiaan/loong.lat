// Package testkit is what the console's tests share: a migrated database, customers and vehicles to put in it,
// a fake optimizer, and readers for the outbox. It is not used by the service itself.
package testkit

import (
	"encoding/json/v2"
	"log/slog"
	"slices"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/services/console/migrations"
)

// Log discards everything; tests that need to see logs build their own.
var Log = slog.New(slog.DiscardHandler)

// DB returns a pool on a fresh schema holding all of the console's tables, or skips the test when no test
// database is configured (see platformtest.Pool).
func DB(t testing.TB) *pgxpool.Pool {
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

// Publisher returns a publisher that can only enqueue: it has no Kafka client, so nothing is relayed. The
// outbox rows it writes are for the test to read (see OutboxTypes).
func Publisher(pool *pgxpool.Pool) *eventbus.Publisher {
	return eventbus.NewPublisher("console-api", pool, nil, Log)
}

// Customer inserts a customer with an empty wallet, bypassing registration (and its password hashing), and
// returns the id.
func Customer(t testing.TB, pool *pgxpool.Pool, name string) string {
	t.Helper()
	id := "cus-" + uuid.NewV7().String()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO customers (id, name, email, phone, password_hash, created_at) VALUES ($1, $2, $3, '081234567890', 'x', now())`,
		id, name, id+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO wallets (customer_id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Fund puts money in a customer's wallet through the ledger, like a paid top-up, so the wallet stays
// consistent with its entries.
func Fund(t testing.TB, pool *pgxpool.Pool, customerID string, amountRp int64) {
	t.Helper()
	ctx := t.Context()
	var balance int64
	if err := pool.QueryRow(ctx, `UPDATE wallets SET balance_rp = balance_rp + $2 WHERE customer_id = $1 RETURNING balance_rp`, customerID, amountRp).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO wallet_entries (id, customer_id, at, kind, delta_rp, balance_after_rp, reference, title)
		 VALUES ($1, $2, now(), 'topup', $3, $4, $5, '{"key":"srv.ops.wallet.topUp"}')`,
		"wtx-"+uuid.NewV7().String(), customerID, amountRp, balance, "seed-"+uuid.NewV7().String())
	if err != nil {
		t.Fatal(err)
	}
}

// Vehicle is an active vehicle with the default tariff.
func Vehicle(id string, typ contracts.VehicleType) contracts.Vehicle {
	return contracts.Vehicle{
		ID: id, Type: typ, Plate: "B 1234 " + id[len(id)-3:], Model: "Test", MaxWeightKg: 50, MaxVolumeM3: 0.5, MaxDimensionCm: 80,
		Fare:   contracts.FareConfig{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargePerKg: 800, SurchargeAboveKg: 10},
		Status: contracts.VehicleStatusActive,
	}
}

// ProjectVehicle puts a vehicle in the fleet projection, as the fleet consumer would.
func ProjectVehicle(t testing.TB, pool *pgxpool.Pool, v contracts.Vehicle) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(),
		`INSERT INTO fleet_vehicles (id, vehicle, status, removed, version, updated_at) VALUES ($1, $2, $3, false, 1, now())`,
		v.ID, raw, string(v.Status))
	if err != nil {
		t.Fatal(err)
	}
}

// OutboxTypes lists the types of the events enqueued so far, oldest first.
func OutboxTypes(t testing.TB, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT event_type FROM outbox ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types := []string{}
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			t.Fatal(err)
		}
		types = append(types, typ)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return types
}

// Outbox decodes the envelopes of the enqueued events of one type, oldest first.
func Outbox(t testing.TB, pool *pgxpool.Pool, eventType string) []contracts.Envelope {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT payload FROM outbox WHERE event_type = $1 ORDER BY seq`, eventType)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var envs []contracts.Envelope
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		env, err := contracts.Decode([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		envs = append(envs, env)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return envs
}

// Count runs a query that returns one integer.
func Count(t testing.TB, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
