package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus/eventbustest"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func envelope(t *testing.T, eventType, key string, version int64, data any) contracts.Envelope {
	t.Helper()
	env, err := contracts.NewEnvelope("backoffice-api", eventType, key, version, data)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func apply(t *testing.T, pool *pgxpool.Pool, h eventbus.Handler, env contracts.Envelope) {
	t.Helper()
	err := platform.WithTx(t.Context(), pool, func(tx pgx.Tx) error { return h(t.Context(), tx, env) })
	if err != nil {
		t.Fatal(err)
	}
}

func upserted(t *testing.T, v contracts.Vehicle, version int64) contracts.Envelope {
	return envelope(t, contracts.TypeVehicleUpserted, v.ID, version, contracts.VehicleUpserted{Vehicle: v})
}

func removed(t *testing.T, id string, version int64) contracts.Envelope {
	return envelope(t, contracts.TypeVehicleRemoved, id, version, contracts.VehicleRemoved{VehicleID: id})
}

func ids(vs []contracts.Vehicle) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

func TestOnlyActiveVehiclesAreOffered(t *testing.T) {
	pool := testkit.DB(t)
	p := New(pool)
	active := testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor)
	inService := testkit.Vehicle("veh-bbb", contracts.VehicleTypeBlindvan)
	inService.Status = contracts.VehicleStatusService
	apply(t, pool, upsert, upserted(t, active, 1))
	apply(t, pool, upsert, upserted(t, inService, 1))

	got, err := p.Active(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "veh-aaa" || got[0].Fare.Base != 6000 {
		t.Errorf("active = %+v, want only the motorbike with its tariff", got)
	}

	// Back in service later.
	inService.Status = contracts.VehicleStatusActive
	apply(t, pool, upsert, upserted(t, inService, 2))
	if got, _ := p.Active(t.Context()); len(got) != 2 {
		t.Errorf("active = %v after the van returned to service", ids(got))
	}
}

func TestTheLatestEventOfAVehicleWins(t *testing.T) {
	pool := testkit.DB(t)
	p := New(pool)
	v := testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor)
	apply(t, pool, upsert, upserted(t, v, 1))
	v.Plate = "B 9999 NEW"
	apply(t, pool, upsert, upserted(t, v, 2))

	old := v
	old.Plate = "B 0000 OLD"
	apply(t, pool, upsert, upserted(t, old, 1)) // a redelivery of something older

	got, _ := p.Active(t.Context())
	if len(got) != 1 || got[0].Plate != "B 9999 NEW" {
		t.Errorf("active = %+v, want the version 2 state", got)
	}
}

func TestARemovedVehicleStaysRemovedUntilANewerStateArrives(t *testing.T) {
	pool := testkit.DB(t)
	p := New(pool)
	v := testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor)
	apply(t, pool, upsert, upserted(t, v, 1))
	apply(t, pool, remove, removed(t, v.ID, 3))
	if got, _ := p.Active(t.Context()); len(got) != 0 {
		t.Fatalf("active = %v after removal", ids(got))
	}
	if _, known, _ := Plate(t.Context(), pool, v.ID); known {
		t.Error("a removed vehicle's plate is still looked up")
	}

	apply(t, pool, upsert, upserted(t, v, 2)) // older than the removal: must not bring it back
	if got, _ := p.Active(t.Context()); len(got) != 0 {
		t.Errorf("an older upsert resurrected the vehicle: %v", ids(got))
	}
	apply(t, pool, upsert, upserted(t, v, 4))
	if got, _ := p.Active(t.Context()); len(got) != 1 {
		t.Errorf("active = %v after a newer upsert", ids(got))
	}
}

func TestARemovalSeenFirstIsRemembered(t *testing.T) {
	pool := testkit.DB(t)
	p := New(pool)
	v := testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor)
	apply(t, pool, remove, removed(t, v.ID, 5))
	apply(t, pool, upsert, upserted(t, v, 4))
	if got, _ := p.Active(t.Context()); len(got) != 0 {
		t.Errorf("active = %v: the removal came first but is newer", ids(got))
	}
}

func TestPlateOfAKnownVehicle(t *testing.T) {
	pool := testkit.DB(t)
	v := testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor)
	apply(t, pool, upsert, upserted(t, v, 1))
	if plate, known, err := Plate(t.Context(), pool, v.ID); err != nil || !known || plate != v.Plate {
		t.Errorf("Plate = %q, %v, %v", plate, known, err)
	}
	if _, known, err := Plate(t.Context(), pool, "veh-nope"); err != nil || known {
		t.Errorf("an unknown vehicle: known = %v, err = %v", known, err)
	}
}

// An event that can never be valid (a vehicle without an id) is dead-lettered at once, without blocking the topic.
func TestTheConsumerFollowsTheFleetTopic(t *testing.T) {
	pool := testkit.DB(t)
	p := New(pool)
	brokers := eventbustest.NewCluster(t, contracts.TopicFleet)
	v := testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor)
	renamed := v
	renamed.Model = "Latest"
	eventbustest.Produce(t, brokers,
		eventbustest.Record(t, "backoffice-api", contracts.TypeVehicleUpserted, v.ID, 1, contracts.VehicleUpserted{Vehicle: v}),
		eventbustest.Record(t, "backoffice-api", contracts.TypeVehicleUpserted, v.ID, 2, contracts.VehicleUpserted{Vehicle: renamed}),
		eventbustest.Record(t, "backoffice-api", contracts.TypeVehicleUpserted, "veh-bad", 1, contracts.VehicleUpserted{}),
	)

	c, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
		Brokers: brokers, Group: "console.fleet", Topics: []string{contracts.TopicFleet}, DB: pool, Log: testkit.Log,
		MaxAttempts: 3, BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Register(c)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "the latest state of the vehicle", func() bool {
		got, err := p.Active(t.Context())
		return err == nil && len(got) == 1 && got[0].Model == "Latest"
	})
	if dead := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicFleet), 1); len(dead) != 1 || string(dead[0].Key) != "veh-bad" {
		t.Errorf("dead letters = %d, want the unreadable event alone", len(dead))
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
