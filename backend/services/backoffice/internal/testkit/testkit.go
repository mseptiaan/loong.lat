// Package testkit is what the service's database tests share: a migrated schema, a publisher whose outbox the
// test reads instead of Kafka, fixtures for confirmed plans, and a way to feed an event handler.
package testkit

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/migrations"
)

func init() {
	// Hashing at production cost takes most of a test run, and a test only needs hashes that verify. Binaries
	// never import this package.
	platform.DefaultArgon2 = platform.Argon2Params{Memory: 64, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}
}

// Discard is a logger that drops everything.
var Discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// Pool returns a pool on a fresh schema with every migration of the service applied. The test is skipped when
// TEST_DATABASE_URL is not set.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := platformtest.Pool(t)
	own, err := migrations.All()
	if err != nil {
		t.Fatal(err)
	}
	all := slices.Concat(platform.Migrations(), eventbus.Migrations(), notices.Migrations(), own)
	if _, err := platform.Migrate(t.Context(), pool, all...); err != nil {
		t.Fatal(err)
	}
	return pool
}

// Publisher returns a publisher whose relay is never run: what it enqueues stays in the outbox for Outbox to read.
func Publisher(pool *pgxpool.Pool) *eventbus.Publisher {
	return eventbus.NewPublisher("backoffice-api", pool, nil, Discard)
}

// Outbox returns the envelopes enqueued so far for an event type, oldest first.
func Outbox(t testing.TB, pool *pgxpool.Pool, eventType string) []contracts.Envelope {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT payload FROM outbox WHERE event_type = $1 ORDER BY seq`, eventType)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []contracts.Envelope
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		env, err := contracts.Decode([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, env)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// AdminNotifications returns the admins' bell menu, newest first.
func AdminNotifications(t testing.TB, pool *pgxpool.Pool) []contracts.AppNotification {
	t.Helper()
	list, err := notices.List(t.Context(), pool, notices.AudienceAdmin)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// Apply runs an event handler on a new event inside a transaction, as the consumer would, and returns the
// handler's error (the transaction is rolled back then).
func Apply(pool *pgxpool.Pool, handler eventbus.Handler, eventType, key string, version int64, data any) error {
	env, err := contracts.NewEnvelope("test", eventType, key, version, data)
	if err != nil {
		return err
	}
	return ApplyEnvelope(pool, handler, env)
}

// ApplyEnvelope is Apply for an envelope that already exists, to replay one.
func ApplyEnvelope(pool *pgxpool.Pool, handler eventbus.Handler, env contracts.Envelope) error {
	ctx := context.Background()
	return platform.WithTx(ctx, pool, func(tx pgx.Tx) error { return handler(ctx, tx, env) })
}

// Kemang is a point in south Jakarta that fixtures are placed around.
var Kemang = contracts.LatLng{Lat: -6.2694, Lng: 106.8143}

// North moves a point km kilometres north (one degree of latitude is about 111 km).
func North(p contracts.LatLng, km float64) contracts.LatLng {
	p.Lat += km / 111
	return p
}

// Route is a route waiting for a driver: a pickup at pickup and a drop-off 3 km north of it, for a shipment of
// weightKg in one item. n numbers it within the plan (ids "<plan>:r<n>").
func Route(planID string, n int, vehicle contracts.VehicleType, pickup contracts.LatLng, weightKg float64, start time.Time) contracts.Route {
	id := fmt.Sprintf("%s:r%d", planID, n)
	shipment := fmt.Sprintf("%s:shp%d", planID, n)
	stop := func(i int, kind contracts.StopKind, at contracts.LatLng, name string) contracts.RouteStop {
		return contracts.RouteStop{
			ID: fmt.Sprintf("%s:s%d", id, i), Seq: i, Kind: kind, ShipmentID: shipment, Status: contracts.StopStatusWaiting,
			Waypoint: contracts.Waypoint{PlaceRef: contracts.PlaceRef{Name: name, Position: at}},
			LegKm:    3, LegMin: 10, EtaMin: float64(10 * i), ArriveAt: contracts.At(start.Add(time.Duration(10*i) * time.Minute)),
		}
	}
	return contracts.Route{
		ID: id, PlanID: planID, VehicleType: vehicle, Status: contracts.RouteStatusAwaitingDriver,
		StartPosition: pickup, StartAt: contracts.At(start),
		Stops:      []contracts.RouteStop{stop(1, contracts.StopKindPickup, pickup, "Pickup "+id), stop(2, contracts.StopKindDropoff, North(pickup, 3), "Dropoff "+id)},
		DistanceKm: 3, DurationMin: 20, CostRp: 20000, TotalWeightKg: weightKg,
	}
}

// Plan is a confirmed plan of the customer "cus-1" made of the given routes, waiting for drivers; every route
// serves one shipment of its own weight. Its schedule is "now".
func Plan(id string, routes ...contracts.Route) contracts.Plan {
	created := time.Now().UTC().Add(-time.Hour)
	plan := contracts.Plan{
		ID: id, Code: "PL-" + id, UserID: "cus-1", Status: contracts.PlanStatusAwaitingDriver,
		Schedule: contracts.Schedule{Mode: contracts.ScheduleModeNow}, Priority: contracts.Priority{Preset: contracts.PresetBalanced},
		Routes: routes, CreatedAt: contracts.At(created), UpdatedAt: contracts.At(created), ConfirmedAt: contracts.At(created),
		TotalCostRp: 20000 * int64(len(routes)),
	}
	for _, r := range routes {
		plan.Shipments = append(plan.Shipments, contracts.Shipment{
			ID:      r.Stops[0].ShipmentID,
			Pickup:  r.Stops[0].Waypoint,
			Dropoff: r.Stops[1].Waypoint,
			Items:   []contracts.Item{{ID: "itm", Name: "box", Qty: 1, WeightKg: r.TotalWeightKg, LengthCm: 20, WidthCm: 20, HeightCm: 20}},
		})
	}
	return plan
}
