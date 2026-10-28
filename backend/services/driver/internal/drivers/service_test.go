package drivers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// These tests need Postgres (TEST_DATABASE_URL) and skip without it.

type fixture struct {
	pool  *pgxpool.Pool
	clock *drivertest.Clock
	svc   *drivers.Service
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := drivertest.Pool(t)
	clock := drivertest.NewClock(drivertest.Epoch)
	return fixture{pool, clock, drivers.NewService(pool, drivertest.Publisher(pool), clock.Now)}
}

func (f fixture) provision(t *testing.T, id, phone, vehicle string) {
	t.Helper()
	m := drivers.Master{ID: id, Name: "Budi", Phone: phone, VehicleID: vehicle, PinHash: "$argon2id$x", JoinedAt: drivertest.Epoch, Version: 1}
	if err := drivers.Provision(t.Context(), f.pool, m); err != nil {
		t.Fatal(err)
	}
}

func wantRefusal(t *testing.T, err error, code platform.ErrorCode, key string) {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok || apiErr.Code != code || apiErr.Msg.Key != key {
		t.Fatalf("error = %v, want %s %q", err, code, key)
	}
}

func TestMeShowsTheDriverAndTheirVehicle(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.provision(t, "drv-1", "081234567801", "veh-1")

	p, err := f.svc.Me(ctx, "drv-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Driver.Name != "Budi" || p.Driver.Phone != "081234567801" || p.Driver.Status != drivers.StatusOffline ||
		p.Driver.VehicleID != "veh-1" || p.Driver.Rating != drivers.Rating || p.Vehicle != nil {
		t.Errorf("profile = %+v, vehicle %v; want the offline driver and no vehicle yet", p.Driver, p.Vehicle)
	}
}

func TestMeOfAnUnknownDriverIsNotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Me(t.Context(), "drv-ghost")
	wantRefusal(t, err, platform.CodeNotFound, "srv.api.driverNotFound")
}

func TestGoingOnlinePublishesOnceWithTheLastKnownPosition(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.provision(t, "drv-1", "081234567801", "veh-1")
	here := contracts.LatLng{Lat: -6.27, Lng: 106.81}
	if err := f.svc.Ping(ctx, "drv-1", drivers.Fix{Position: here}); err != nil {
		t.Fatal(err)
	}

	f.clock.Advance(time.Minute)
	d, err := f.svc.SetOnline(ctx, "drv-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != drivers.StatusOnline || d.LastSeen.Time != f.clock.Now() {
		t.Errorf("driver = %+v", d)
	}

	events := drivertest.Outbox(t, f.pool)
	if got := drivertest.Types(events); len(got) != 2 || got[1] != contracts.TypeDriverOnline {
		t.Fatalf("events = %v, want the ping and driver.online", got)
	}
	online := drivertest.Payload[contracts.DriverOnline](t, events[1])
	if events[1].Key != "drv-1" || events[1].Version != 1 || online.Position == nil || *online.Position != here {
		t.Errorf("driver.online = %+v key %s version %d", online, events[1].Key, events[1].Version)
	}

	// a retry changes nothing and says nothing
	if _, err := f.svc.SetOnline(ctx, "drv-1", true); err != nil {
		t.Fatal(err)
	}
	if n := len(drivertest.Outbox(t, f.pool)); n != 2 {
		t.Errorf("a repeated setOnline(true) published %d events in total, want 2", n)
	}
}

func TestGoingOfflineAndOnlineAgainRaisesThePresenceVersion(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.provision(t, "drv-1", "081234567801", "veh-1")
	for _, online := range []bool{true, false, true} {
		if _, err := f.svc.SetOnline(ctx, "drv-1", online); err != nil {
			t.Fatal(err)
		}
	}
	events := drivertest.Outbox(t, f.pool)
	want := []string{contracts.TypeDriverOnline, contracts.TypeDriverOffline, contracts.TypeDriverOnline}
	if got := drivertest.Types(events); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i, e := range events {
		if e.Version != int64(i+1) {
			t.Errorf("event %d has version %d, want %d: consumers order presence by it", i, e.Version, i+1)
		}
	}
}

func TestADriverWithoutAVehicleCannotGoOnline(t *testing.T) {
	f := newFixture(t)
	f.provision(t, "drv-1", "081234567801", "")
	_, err := f.svc.SetOnline(t.Context(), "drv-1", true)
	wantRefusal(t, err, platform.CodeConflict, "srv.api.noVehicle")
	if n := len(drivertest.Outbox(t, f.pool)); n != 0 {
		t.Errorf("a refused call published %d events", n)
	}
}

func TestADriverOnARunningRouteCannotGoOffline(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.provision(t, "drv-1", "081234567801", "veh-1")
	if _, err := f.svc.SetOnline(ctx, "drv-1", true); err != nil {
		t.Fatal(err)
	}
	if err := drivers.MarkOnDuty(ctx, f.pool, "drv-1", f.clock.Now()); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.SetOnline(ctx, "drv-1", false)
	wantRefusal(t, err, platform.CodeConflict, "srv.api.finishActiveFirst")
	d, err := drivers.Get(ctx, f.pool, "drv-1")
	if err != nil || d.Status != drivers.StatusOnDuty {
		t.Errorf("driver = %+v, %v; want still on duty", d, err)
	}
	if got := drivertest.Types(drivertest.Outbox(t, f.pool)); len(got) != 1 {
		t.Errorf("events = %v, want only the earlier driver.online", got)
	}

	// going online while on duty is already true: no change, no event
	if d, err := f.svc.SetOnline(ctx, "drv-1", true); err != nil || d.Status != drivers.StatusOnDuty {
		t.Errorf("setOnline(true) on duty = %+v, %v", d, err)
	}
}

func TestAPingStoresThePositionAndPublishesItWithTheRunningRoute(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.provision(t, "drv-1", "081234567801", "veh-1")

	if err := f.svc.Ping(ctx, "drv-1", drivers.Fix{Position: contracts.LatLng{Lat: -6.2, Lng: 106.8}, SpeedKmh: 31.5}); err != nil {
		t.Fatal(err)
	}
	route := tasks.Task{PlanID: "pln-1", PlanCode: "PL-1", DriverID: "drv-1", Route: drivertest.Route("pln-1:r1", "drv-1")}
	route.Route.Status = contracts.RouteStatusRunning
	if err := tasks.Put(ctx, f.pool, route, f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(5 * time.Second)
	if err := f.svc.Ping(ctx, "drv-1", drivers.Fix{Position: contracts.LatLng{Lat: -6.21, Lng: 106.81}}); err != nil {
		t.Fatal(err)
	}

	events := drivertest.Outbox(t, f.pool)
	if len(events) != 2 {
		t.Fatalf("events = %v", drivertest.Types(events))
	}
	idle := drivertest.Payload[contracts.DriverPosition](t, events[0])
	driving := drivertest.Payload[contracts.DriverPosition](t, events[1])
	if events[0].Type != contracts.TypeDriverPosition || events[0].Key != "drv-1" || events[0].Version != 0 {
		t.Errorf("event = %+v", events[0])
	}
	if idle.RouteID != "" || idle.SpeedKmh != 31.5 || idle.RecordedAt.Time != drivertest.Epoch {
		t.Errorf("idle ping = %+v", idle)
	}
	if driving.RouteID != "pln-1:r1" || driving.RecordedAt.Time != drivertest.Epoch.Add(5*time.Second) {
		t.Errorf("ping on the route = %+v", driving)
	}
	d, _ := drivers.Get(ctx, f.pool, "drv-1")
	if d.Position == nil || d.Position.Lat != -6.21 {
		t.Errorf("stored position = %v, want the latest", d.Position)
	}
}

func TestAPingOlderThanTheStoredPositionIsDropped(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.provision(t, "drv-1", "081234567801", "veh-1")
	newer := contracts.LatLng{Lat: -6.3, Lng: 106.9}

	f.clock.Set(drivertest.Epoch.Add(time.Minute))
	if err := f.svc.Ping(ctx, "drv-1", drivers.Fix{Position: newer}); err != nil {
		t.Fatal(err)
	}
	// a slower request that was stamped earlier reaches the database later, and so does one stamped at the same instant
	for _, at := range []time.Time{drivertest.Epoch.Add(30 * time.Second), drivertest.Epoch.Add(time.Minute)} {
		f.clock.Set(at)
		if err := f.svc.Ping(ctx, "drv-1", drivers.Fix{Position: contracts.LatLng{Lat: -6.1, Lng: 106.7}}); err != nil {
			t.Fatal(err)
		}
	}

	d, _ := drivers.Get(ctx, f.pool, "drv-1")
	if d.Position == nil || *d.Position != newer {
		t.Errorf("position = %v, want the newest fix %v", d.Position, newer)
	}
	if n := len(drivertest.Outbox(t, f.pool)); n != 1 {
		t.Errorf("%d position events, want 1: a dropped fix is not published", n)
	}

	f.clock.Set(drivertest.Epoch.Add(2 * time.Minute))
	if err := f.svc.Ping(ctx, "drv-1", drivers.Fix{Position: contracts.LatLng{Lat: -6.4, Lng: 107}}); err != nil {
		t.Fatal(err)
	}
	if n := len(drivertest.Outbox(t, f.pool)); n != 2 {
		t.Errorf("%d position events after a newer fix, want 2", n)
	}
}

func TestAnImplausiblePingIsRefused(t *testing.T) {
	f := newFixture(t)
	f.provision(t, "drv-1", "081234567801", "veh-1")
	err := f.svc.Ping(t.Context(), "drv-1", drivers.Fix{Position: contracts.LatLng{Lat: 200}})
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok || apiErr.Code != platform.CodeInvalid || apiErr.Field != "position" {
		t.Fatalf("error = %v, want tidak_valid on position", err)
	}
	if n := len(drivertest.Outbox(t, f.pool)); n != 0 {
		t.Errorf("published %d events", n)
	}
}

func TestMasterDataFollowsTheVersion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provision(t, "drv-1", "081234567801", "veh-1")
	update := func(version int64, name, pin string) (bool, error) {
		return drivers.Update(ctx, f.pool, drivers.Master{ID: "drv-1", Name: name, Phone: "081234567801", VehicleID: "veh-2", PinHash: pin, Version: version})
	}

	if applied, err := update(2, "Budi S.", ""); err != nil || !applied {
		t.Fatalf("update v2 = %v, %v", applied, err)
	}
	if applied, err := update(2, "Replayed", ""); err != nil || applied {
		t.Errorf("the same version again = %v, %v; want dropped", applied, err)
	}
	if applied, err := update(1, "Old", ""); err != nil || applied {
		t.Errorf("an older version = %v, %v; want dropped", applied, err)
	}
	d, _ := drivers.Get(ctx, f.pool, "drv-1")
	if d.Name != "Budi S." || d.VehicleID != "veh-2" {
		t.Errorf("driver = %+v, want the v2 data", d)
	}

	acct, _ := drivers.FindAccount(ctx, f.pool, "081234567801")
	if acct.PinHash != "$argon2id$x" {
		t.Errorf("an update without a PIN hash must keep the PIN, got %q", acct.PinHash)
	}
	if _, err := update(3, "Budi S.", "$argon2id$new"); err != nil {
		t.Fatal(err)
	}
	if acct, _ = drivers.FindAccount(ctx, f.pool, "081234567801"); acct.PinHash != "$argon2id$new" {
		t.Errorf("PIN hash = %q, want the reset one", acct.PinHash)
	}

	if _, err := drivers.Update(ctx, f.pool, drivers.Master{ID: "drv-ghost", Name: "x", Phone: "0811", Version: 9}); !errors.Is(err, drivers.ErrNotFound) {
		t.Errorf("update of an unknown driver = %v, want ErrNotFound", err)
	}
}

func TestProvisioningAgainNeitherUndoesLaterDataNorResetsState(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provision(t, "drv-1", "081234567801", "veh-1")
	if _, err := drivers.Update(ctx, f.pool, drivers.Master{ID: "drv-1", Name: "Budi S.", Phone: "081234567801", VehicleID: "veh-1", Version: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetOnline(ctx, "drv-1", true); err != nil {
		t.Fatal(err)
	}

	f.provision(t, "drv-1", "081234567801", "veh-1") // version 1 again
	d, _ := drivers.Get(ctx, f.pool, "drv-1")
	if d.Name != "Budi S." || d.Status != drivers.StatusOnline {
		t.Errorf("driver = %+v, want the v2 name and the online status kept", d)
	}
}

func TestAPhoneNumberBelongsToOneDriver(t *testing.T) {
	f := newFixture(t)
	f.provision(t, "drv-1", "081234567801", "veh-1")
	err := drivers.Provision(t.Context(), f.pool, drivers.Master{ID: "drv-2", Name: "Sari", Phone: "081234567801", PinHash: "$argon2id$x", JoinedAt: drivertest.Epoch, Version: 1})
	if !drivers.IsPhoneTaken(err) {
		t.Errorf("error = %v, want the phone number conflict", err)
	}
}

func TestOnDutyUntilTheLastRunningRouteIsDone(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.provision(t, "drv-1", "081234567801", "veh-1")
	running := func(id string, status contracts.RouteStatus) {
		t.Helper()
		route := drivertest.Route(id, "drv-1")
		route.Status = status
		if err := tasks.Put(ctx, f.pool, tasks.Task{PlanID: "pln", DriverID: "drv-1", Route: route}, f.clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	running("pln:r1", contracts.RouteStatusDone)
	running("pln:r2", contracts.RouteStatusRunning)
	if err := drivers.FinishTrip(ctx, f.pool, "drv-1", f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if d, _ := drivers.Get(ctx, f.pool, "drv-1"); d.Status != drivers.StatusOnDuty || d.CompletedTrips != 1 {
		t.Errorf("driver = %s, %d trips; want on duty while another route runs", d.Status, d.CompletedTrips)
	}

	running("pln:r2", contracts.RouteStatusDone)
	if err := drivers.FinishTrip(ctx, f.pool, "drv-1", f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if d, _ := drivers.Get(ctx, f.pool, "drv-1"); d.Status != drivers.StatusOnline || d.CompletedTrips != 2 {
		t.Errorf("driver = %s, %d trips; want online after the last route", d.Status, d.CompletedTrips)
	}
}
