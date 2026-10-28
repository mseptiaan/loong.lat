package fleet

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

type rig struct {
	pool *pgxpool.Pool
	svc  *Service
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testkit.Pool(t)
	feed := changes.New(platform.NewHub(), time.Millisecond)
	return &rig{pool: pool, svc: New(pool, testkit.Publisher(pool), feed, time.Now, time.Hour)}
}

func motorInput(plate string) VehicleInput {
	return VehicleInput{
		Type: contracts.VehicleTypeMotor, Plate: plate, Model: "Honda Vario 160", MaxWeightKg: 20, MaxVolumeM3: 0.12, MaxDimensionCm: 55,
		Fare: defaultFare[contracts.VehicleTypeMotor], Status: contracts.VehicleStatusActive,
	}
}

func (r *rig) vehicle(t *testing.T, plate string) contracts.Vehicle {
	t.Helper()
	v, err := r.svc.CreateVehicle(t.Context(), motorInput(plate))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (r *rig) driver(t *testing.T, name, phone, vehicleID string) Driver {
	t.Helper()
	d, err := r.svc.CreateDriver(t.Context(), DriverInput{Name: name, Phone: phone, VehicleID: vehicleID, PIN: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func apiError(t *testing.T, err error) *platform.APIError {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok {
		t.Fatalf("error = %v, want an APIError", err)
	}
	return apiErr
}

func wantError(t *testing.T, err error, code platform.ErrorCode, field, key string) {
	t.Helper()
	apiErr := apiError(t, err)
	if apiErr.Code != code || apiErr.Field != field || apiErr.Msg.Key != key {
		t.Errorf("error = %s field %q key %q, want %s on %q with %q", apiErr.Code, apiErr.Field, apiErr.Msg.Key, code, field, key)
	}
}

// route puts a route of the vehicle in the given status into the projection.
func (r *rig) route(t *testing.T, planID, vehicleID, driverID string, status contracts.RouteStatus, startsIn time.Duration) {
	t.Helper()
	route := testkit.Route(planID, 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, time.Now().Add(startsIn))
	plan := testkit.Plan(planID, route)
	if _, err := plans.Insert(t.Context(), r.pool, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(t.Context(), `UPDATE routes SET status = $2, vehicle_id = $3, driver_id = $4 WHERE id = $1`, route.ID, string(status), vehicleID, driverID); err != nil {
		t.Fatal(err)
	}
}

type outboxRow struct {
	topic, key, eventType, payload string
	env                            contracts.Envelope
}

func outbox(t *testing.T, pool *pgxpool.Pool, types ...string) []outboxRow {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT topic, key, event_type, payload FROM outbox WHERE event_type = ANY($1) ORDER BY seq`, types)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var row outboxRow
		if err := rows.Scan(&row.topic, &row.key, &row.eventType, &row.payload); err != nil {
			t.Fatal(err)
		}
		if row.env, err = contracts.Decode([]byte(row.payload)); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

func TestFleetChangesGoToTheCompactedTopicKeyedByVehicleID(t *testing.T) {
	r := newRig(t)
	v := r.vehicle(t, "B 1001 AA")
	rename := "Yamaha NMAX"
	if _, err := r.svc.UpdateVehicle(t.Context(), v.ID, VehiclePatch{Model: &rename}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.UpdateVehicle(t.Context(), v.ID, VehiclePatch{Model: &rename}); err != nil { // changes nothing
		t.Fatal(err)
	}
	if err := r.svc.RemoveVehicle(t.Context(), v.ID); err != nil {
		t.Fatal(err)
	}

	rows := outbox(t, r.pool, contracts.TypeVehicleUpserted, contracts.TypeVehicleRemoved)
	if len(rows) != 3 {
		t.Fatalf("%d fleet events, want upserted, upserted, removed (the no-op update announces nothing)", len(rows))
	}
	spec := map[string]contracts.TopicSpec{}
	for _, s := range contracts.Topics() {
		spec[s.Name] = s
	}
	for i, row := range rows {
		if row.topic != contracts.TopicFleet || spec[row.topic].Cleanup != contracts.CleanupCompact {
			t.Errorf("event %d on %s (%s), want the compacted fleet topic", i, row.topic, spec[row.topic].Cleanup)
		}
		if row.key != v.ID || row.env.Key != v.ID || row.env.AggregateVersion != int64(i+1) {
			t.Errorf("event %d key %q version %d, want the vehicle id at version %d", i, row.key, row.env.AggregateVersion, i+1)
		}
	}
	if rows[0].eventType != contracts.TypeVehicleUpserted || rows[2].eventType != contracts.TypeVehicleRemoved {
		t.Errorf("types = %s ... %s", rows[0].eventType, rows[2].eventType)
	}
	upserted, _ := rows[1].env.Payload[contracts.VehicleUpserted]()
	if upserted.Vehicle.Model != rename || upserted.Vehicle.Fare != defaultFare[contracts.VehicleTypeMotor] {
		t.Errorf("upserted vehicle = %+v, want the full state with its fare", upserted.Vehicle)
	}
	if list, err := r.svc.Vehicles(t.Context()); err != nil || len(list) != 0 {
		t.Errorf("vehicles = %v, %v; want none after removal", list, err)
	}
}

func TestVehicleRulesAreEnforcedByTheService(t *testing.T) {
	r := newRig(t)
	r.vehicle(t, "B 1001 AA")

	bad := motorInput("B 1002 AA")
	bad.MaxWeightKg = 0
	_, err := r.svc.CreateVehicle(t.Context(), bad)
	wantError(t, err, platform.CodeInvalid, "maxWeightKg", "srv.api.mustBePositive")

	_, err = r.svc.CreateVehicle(t.Context(), motorInput("b  1001  aa"))
	wantError(t, err, platform.CodeConflict, "plate", "srv.api.plateTaken")

	other := r.vehicle(t, "B 1003 AA")
	taken := "B 1001 AA"
	_, err = r.svc.UpdateVehicle(t.Context(), other.ID, VehiclePatch{Plate: &taken})
	wantError(t, err, platform.CodeConflict, "plate", "srv.api.plateTaken")

	_, err = r.svc.UpdateVehicle(t.Context(), "veh-404", VehiclePatch{})
	wantError(t, err, platform.CodeNotFound, "", "srv.api.vehicleNotFound")
	wantError(t, r.svc.RemoveVehicle(t.Context(), "veh-404"), platform.CodeNotFound, "", "srv.api.vehicleNotFound")

	if got := len(outbox(t, r.pool, contracts.TypeVehicleUpserted)); got != 2 {
		t.Errorf("%d vehicle events, want the two successful creations only", got)
	}
}

func TestAVehicleOnAnActiveRouteCannotLeaveServiceOrBeRemoved(t *testing.T) {
	r := newRig(t)
	v := r.vehicle(t, "B 1001 AA")
	r.route(t, "pln-1", v.ID, "drv-1", contracts.RouteStatusAssigned, 30*time.Minute)

	service := contracts.VehicleStatusService
	_, err := r.svc.UpdateVehicle(t.Context(), v.ID, VehiclePatch{Status: &service})
	wantError(t, err, platform.CodeConflict, "status", "srv.api.vehicleInUseChange")
	van := contracts.VehicleTypeBlindvan
	_, err = r.svc.UpdateVehicle(t.Context(), v.ID, VehiclePatch{Type: &van})
	wantError(t, err, platform.CodeConflict, "status", "srv.api.vehicleInUseChange")
	wantError(t, r.svc.RemoveVehicle(t.Context(), v.ID), platform.CodeConflict, "", "srv.api.vehicleInUse")

	model := "Honda BeAT" // harmless changes are fine
	if _, err := r.svc.UpdateVehicle(t.Context(), v.ID, VehiclePatch{Model: &model}); err != nil {
		t.Errorf("renaming the model of a vehicle in use: %v", err)
	}
}

func TestCreatingADriverPublishesAPINHashAndNeverThePIN(t *testing.T) {
	r := newRig(t)
	v := r.vehicle(t, "B 1001 AA")
	const pin = "654321"

	d, err := r.svc.CreateDriver(t.Context(), DriverInput{Name: " Budi Santoso ", Phone: "+62 812-3456-7801", VehicleID: v.ID, PIN: pin})
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Budi Santoso" || d.Phone != "081234567801" || d.VehicleID != v.ID || d.Status != StatusOffline || d.Rating != 5 || d.CompletedTrips != 0 {
		t.Errorf("driver = %+v, want the cleaned data, offline, rating 5, no trips", d)
	}

	rows := outbox(t, r.pool, contracts.TypeDriverProvisioned)
	if len(rows) != 1 || rows[0].topic != contracts.TopicDriver || rows[0].key != d.ID || rows[0].env.AggregateVersion != 1 {
		t.Fatalf("driver.provisioned events = %+v, want one on the driver topic keyed by the driver at version 1", rows)
	}
	ev, err := rows[0].env.Payload[contracts.DriverProvisioned]()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ev.PinHash, "$argon2id$") {
		t.Fatalf("pinHash = %q, want an argon2id PHC string", ev.PinHash)
	}
	if ok, err := platform.VerifySecret(pin, ev.PinHash); err != nil || !ok {
		t.Errorf("the hash does not verify the PIN: %v, %v", ok, err)
	}
	if ev.DriverID != d.ID || ev.Name != "Budi Santoso" || ev.Phone != "081234567801" || ev.VehicleID != v.ID || ev.JoinedAt.IsZero() {
		t.Errorf("payload = %+v, want the driver's master data", ev)
	}

	// the PIN is nowhere: not in any event, not in the driver table, not in the answer
	var all string
	if err := r.pool.QueryRow(t.Context(), `SELECT string_agg(payload, ' ') FROM outbox`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := r.pool.QueryRow(t.Context(), `SELECT string_agg(d::text, ' ') FROM drivers d`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	answer, _ := json.Marshal(d)
	for where, text := range map[string]string{"an event": all, "the drivers table": stored, "the answer": string(answer)} {
		if strings.Contains(text, pin) {
			t.Errorf("the PIN is in %s", where)
		}
	}
	if strings.Contains(stored, "argon2id") || strings.Contains(string(answer), "argon2id") || strings.Contains(string(answer), "pin") {
		t.Error("the hash or a pin field is stored or returned, but only the event carries the hash")
	}

	// the vehicle is announced again, now with its driver
	cars := outbox(t, r.pool, contracts.TypeVehicleUpserted)
	last, _ := cars[len(cars)-1].env.Payload[contracts.VehicleUpserted]()
	if last.Vehicle.DriverID != d.ID || cars[len(cars)-1].env.AggregateVersion != 2 {
		t.Errorf("vehicle event = %+v at version %d, want it to carry the new driver at version 2", last.Vehicle, cars[len(cars)-1].env.AggregateVersion)
	}
}

func TestDriverRulesAreEnforcedByTheService(t *testing.T) {
	r := newRig(t)
	v := r.vehicle(t, "B 1001 AA")
	r.driver(t, "Budi", "081234567801", v.ID)
	spare := r.vehicle(t, "B 1002 AA")

	tests := []struct {
		name  string
		in    DriverInput
		code  platform.ErrorCode
		field string
		key   string
	}{
		{"no name", DriverInput{Name: " ", Phone: "081234567802", PIN: "123456"}, platform.CodeInvalid, "name", "srv.api.driverNameRequired"},
		{"a short phone", DriverInput{Name: "A", Phone: "0812", PIN: "123456"}, platform.CodeInvalid, "phone", "srv.api.phoneIncomplete"},
		{"no PIN", DriverInput{Name: "A", Phone: "081234567802"}, platform.CodeInvalid, "pin", "srv.api.pinInvalid"},
		{"a PIN of letters", DriverInput{Name: "A", Phone: "081234567802", PIN: "abcdef"}, platform.CodeInvalid, "pin", "srv.api.pinInvalid"},
		{"a PIN that is too short", DriverInput{Name: "A", Phone: "081234567802", PIN: "1234"}, platform.CodeInvalid, "pin", "srv.api.pinInvalid"},
		{"a phone already registered, written differently", DriverInput{Name: "A", Phone: "+6281234567801", PIN: "123456"}, platform.CodeConflict, "phone", "srv.api.phoneTakenDriver"},
		{"a vehicle another driver has", DriverInput{Name: "A", Phone: "081234567802", VehicleID: v.ID, PIN: "123456"}, platform.CodeConflict, "vehicleId", "srv.api.vehicleTaken"},
		{"a vehicle that does not exist", DriverInput{Name: "A", Phone: "081234567802", VehicleID: "veh-404", PIN: "123456"}, platform.CodeNotFound, "", "srv.api.vehicleNotFound"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.svc.CreateDriver(t.Context(), tt.in)
			wantError(t, err, tt.code, tt.field, tt.key)
		})
	}
	if got := len(outbox(t, r.pool, contracts.TypeDriverProvisioned)); got != 1 {
		t.Errorf("%d driver.provisioned events, want only the first driver's", got)
	}
	if _, err := r.svc.CreateDriver(t.Context(), DriverInput{Name: "No car", Phone: "081234567803", PIN: "123456"}); err != nil {
		t.Errorf("a driver without a vehicle: %v", err)
	}
	if _, err := r.svc.CreateDriver(t.Context(), DriverInput{Name: "Has spare", Phone: "081234567804", VehicleID: spare.ID, PIN: "123456"}); err != nil {
		t.Errorf("a driver with the free vehicle: %v", err)
	}
}

func TestUpdatingADriver(t *testing.T) {
	r := newRig(t)
	first, second := r.vehicle(t, "B 1001 AA"), r.vehicle(t, "B 1002 AA")
	d := r.driver(t, "Budi", "081234567801", first.ID)
	r.driver(t, "Agus", "081234567802", second.ID)
	str := func(s string) *string { return &s }

	// a rename announces the full data, with no hash
	got, err := r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{Name: str("Budi S.")})
	if err != nil || got.Name != "Budi S." {
		t.Fatalf("rename = %+v, %v", got, err)
	}
	updated := outbox(t, r.pool, contracts.TypeDriverUpdated)
	if len(updated) != 1 || updated[0].key != d.ID || updated[0].env.AggregateVersion != 2 {
		t.Fatalf("driver.updated events = %+v, want one at version 2", updated)
	}
	ev, _ := updated[0].env.Payload[contracts.DriverUpdated]()
	if ev.Name != "Budi S." || ev.Phone != "081234567801" || ev.VehicleID != first.ID || ev.PinHash != "" {
		t.Errorf("payload = %+v, want the full data and no PinHash: the PIN was not reset", ev)
	}

	// a PIN reset carries the new hash
	if _, err := r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{PIN: str("111222")}); err != nil {
		t.Fatal(err)
	}
	updated = outbox(t, r.pool, contracts.TypeDriverUpdated)
	ev, _ = updated[len(updated)-1].env.Payload[contracts.DriverUpdated]()
	if ok, err := platform.VerifySecret("111222", ev.PinHash); err != nil || !ok {
		t.Errorf("reset hash does not verify the new PIN: %v, %v", ok, err)
	}
	if strings.Contains(updated[len(updated)-1].payload, "111222") {
		t.Error("the new PIN is in the event")
	}
	_, err = r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{PIN: str("12")})
	wantError(t, err, platform.CodeInvalid, "pin", "srv.api.pinInvalid")

	// nothing changes, nothing is announced
	before := len(outbox(t, r.pool, contracts.TypeDriverUpdated))
	online := StatusOffline
	if _, err := r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{Name: str("Budi S."), Status: &online}); err != nil {
		t.Fatal(err)
	}
	if after := len(outbox(t, r.pool, contracts.TypeDriverUpdated)); after != before {
		t.Errorf("%d events after a no-op update, want %d", after, before)
	}

	// status belongs to driver-api
	on := StatusOnline
	_, err = r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{Status: &on})
	wantError(t, err, platform.CodeConflict, "status", "srv.api.statusAuto")

	// conflicts and unknowns
	_, err = r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{Phone: str("0812-3456-7802")})
	wantError(t, err, platform.CodeConflict, "phone", "srv.api.phoneUsedDriver")
	_, err = r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{VehicleID: &second.ID})
	wantError(t, err, platform.CodeConflict, "vehicleId", "srv.api.vehicleTaken")
	_, err = r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{VehicleID: str("veh-404")})
	wantError(t, err, platform.CodeNotFound, "", "srv.api.vehicleNotFound")
	_, err = r.svc.UpdateDriver(t.Context(), "drv-404", DriverPatch{})
	wantError(t, err, platform.CodeNotFound, "", "srv.api.driverNotFound")
	_, err = r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{Name: str(" ")})
	wantError(t, err, platform.CodeInvalid, "name", "srv.api.driverNameRequired")

	// taking his vehicle away (an empty id) announces both the driver and the vehicle
	vehicleEvents := len(outbox(t, r.pool, contracts.TypeVehicleUpserted))
	got, err = r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{VehicleID: str("")})
	if err != nil || got.VehicleID != "" {
		t.Fatalf("clearing the vehicle: %+v, %v", got, err)
	}
	cars := outbox(t, r.pool, contracts.TypeVehicleUpserted)
	if len(cars) != vehicleEvents+1 {
		t.Fatalf("%d vehicle events (was %d), want the freed vehicle announced once", len(cars), vehicleEvents)
	}
	freed, _ := cars[len(cars)-1].env.Payload[contracts.VehicleUpserted]()
	if freed.Vehicle.ID != first.ID || freed.Vehicle.DriverID != "" {
		t.Errorf("freed vehicle = %+v, want it without a driver", freed.Vehicle)
	}
}

func TestADriverOnDutyKeepsHisVehicle(t *testing.T) {
	r := newRig(t)
	v, spare := r.vehicle(t, "B 1001 AA"), r.vehicle(t, "B 1002 AA")
	d := r.driver(t, "Budi", "081234567801", v.ID)
	r.route(t, "pln-1", v.ID, d.ID, contracts.RouteStatusAssigned, 20*time.Minute)

	_, err := r.svc.UpdateDriver(t.Context(), d.ID, DriverPatch{VehicleID: &spare.ID})
	wantError(t, err, platform.CodeConflict, "vehicleId", "srv.api.driverOnDutyVehicle")
}

func TestDriverStatusIsDerivedFromPresenceAndRoutes(t *testing.T) {
	r := newRig(t)
	soon, later, idle := r.driver(t, "Soon", "081234567801", ""), r.driver(t, "Later", "081234567802", ""), r.driver(t, "Idle", "081234567803", "")
	for i, d := range []Driver{soon, later, idle} {
		pos := testkit.North(testkit.Kemang, float64(i))
		if err := testkit.Apply(r.pool, r.svc.Online, contracts.TypeDriverOnline, d.ID, 1, contracts.DriverOnline{DriverID: d.ID, Position: &pos}); err != nil {
			t.Fatal(err)
		}
	}
	r.route(t, "pln-1", "", soon.ID, contracts.RouteStatusAssigned, 30*time.Minute) // departs within the hour: binds him
	r.route(t, "pln-2", "", later.ID, contracts.RouteStatusAssigned, 5*time.Hour)   // far ahead: does not
	status := func() map[string]Status {
		t.Helper()
		list, err := r.svc.Drivers(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]Status{}
		for _, d := range list {
			out[d.Name] = d.Status
		}
		return out
	}
	if got := status(); got["Soon"] != StatusOnDuty || got["Later"] != StatusOnline || got["Idle"] != StatusOnline {
		t.Errorf("status = %v, want Soon on duty, the other two online", got)
	}

	idleList, err := r.svc.IdleDrivers(t.Context())
	if err != nil || len(idleList) != 2 {
		t.Fatalf("idle drivers = %+v, %v; want the two online ones", idleList, err)
	}

	// his route starts, then ends: on duty while it runs, back to online (with a trip counted) after
	if _, err := r.pool.Exec(t.Context(), `UPDATE routes SET status = 'berjalan' WHERE id = 'pln-2:r1'`); err != nil {
		t.Fatal(err)
	}
	if got := status(); got["Later"] != StatusOnDuty {
		t.Errorf("a driver on a running route is %s, want bertugas", got["Later"])
	}
	if _, err := r.pool.Exec(t.Context(), `UPDATE routes SET status = 'selesai' WHERE id = 'pln-2:r1'`); err != nil {
		t.Fatal(err)
	}
	list, _ := r.svc.Drivers(t.Context())
	for _, d := range list {
		if d.Name == "Later" && (d.Status != StatusOnline || d.CompletedTrips != 1) {
			t.Errorf("after his route: %s with %d trips, want online with 1", d.Status, d.CompletedTrips)
		}
	}

	// going offline shows even though a job is booked
	if err := testkit.Apply(r.pool, r.svc.Offline, contracts.TypeDriverOffline, idle.ID, 2, contracts.DriverOffline{DriverID: idle.ID}); err != nil {
		t.Fatal(err)
	}
	if got := status(); got["Idle"] != StatusOffline {
		t.Errorf("Idle is %s after driver.offline, want offline", got["Idle"])
	}
}

func TestADriverWhoWentSilentExpiresUnlessHeIsOnDuty(t *testing.T) {
	r := newRig(t)
	quiet, busy := r.driver(t, "Quiet", "081234567811", ""), r.driver(t, "Busy", "081234567812", "")
	for i, d := range []Driver{quiet, busy} {
		pos := testkit.North(testkit.Kemang, float64(i))
		if err := testkit.Apply(r.pool, r.svc.Online, contracts.TypeDriverOnline, d.ID, 1, contracts.DriverOnline{DriverID: d.ID, Position: &pos}); err != nil {
			t.Fatal(err)
		}
	}
	r.route(t, "pln-1", "", busy.ID, contracts.RouteStatusAssigned, 30*time.Minute) // binds him: on duty

	at := func(silence time.Duration) map[string]Status {
		t.Helper()
		r.svc.now = func() time.Time { return time.Now().Add(silence) }
		list, err := r.svc.Drivers(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]Status{}
		for _, d := range list {
			out[d.Name] = d.Status
		}
		return out
	}

	if got := at(PresenceTTL - 10*time.Second); got["Quiet"] != StatusOnline || got["Busy"] != StatusOnDuty {
		t.Errorf("just inside the TTL: %v, want Quiet online and Busy on duty", got)
	}
	if got := at(PresenceTTL + time.Minute); got["Quiet"] != StatusOffline || got["Busy"] != StatusOnDuty {
		t.Errorf("past the TTL: %v, want Quiet offline (his app is gone) and Busy still on duty (dead spot)", got)
	}
	if idle, err := r.svc.IdleDrivers(t.Context()); err != nil || len(idle) != 0 {
		t.Errorf("idle drivers past the TTL = %+v, %v; want none on the live map", idle, err)
	}
}

func TestPresenceEventsOlderOrRepeatedAreDropped(t *testing.T) {
	r := newRig(t)
	d := r.driver(t, "Budi", "081234567801", "")
	here := testkit.North(testkit.Kemang, 1)
	status := func() Status {
		t.Helper()
		list, _ := r.svc.Drivers(t.Context())
		return list[0].Status
	}
	if err := testkit.Apply(r.pool, r.svc.Online, contracts.TypeDriverOnline, d.ID, 5, contracts.DriverOnline{DriverID: d.ID, Position: &here}); err != nil {
		t.Fatal(err)
	}
	// offline at an older version arrives late: it must not switch him off
	if err := testkit.Apply(r.pool, r.svc.Offline, contracts.TypeDriverOffline, d.ID, 4, contracts.DriverOffline{DriverID: d.ID}); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != StatusOnline {
		t.Fatalf("status = %s after a late driver.offline, want online", got)
	}
	// the same version again, and a newer offline
	if err := testkit.Apply(r.pool, r.svc.Offline, contracts.TypeDriverOffline, d.ID, 5, contracts.DriverOffline{DriverID: d.ID}); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != StatusOnline {
		t.Fatalf("status = %s after a repeated version, want online", got)
	}
	if err := testkit.Apply(r.pool, r.svc.Offline, contracts.TypeDriverOffline, d.ID, 6, contracts.DriverOffline{DriverID: d.ID}); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != StatusOffline {
		t.Errorf("status = %s after a newer driver.offline, want offline", got)
	}

	// a driver we never provisioned can never become valid: dead-letter, do not retry
	err := testkit.Apply(r.pool, r.svc.Online, contracts.TypeDriverOnline, "drv-404", 1, contracts.DriverOnline{DriverID: "drv-404"})
	if err == nil || fmt.Sprintf("%T", err) != "eventbus.permanentError" {
		t.Errorf("unknown driver: %v, want a permanent error", err)
	}
}

func TestPositionsKeepTheLatestFixByRecordingTime(t *testing.T) {
	r := newRig(t)
	d := r.driver(t, "Budi", "081234567801", "")
	t0 := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	fix := func(km float64, after time.Duration) error {
		return testkit.Apply(r.pool, r.svc.Position, contracts.TypeDriverPosition, d.ID, 0, contracts.DriverPosition{
			DriverID: d.ID, Position: testkit.North(testkit.Kemang, km), RecordedAt: contracts.At(t0.Add(after)),
		})
	}
	position := func() contracts.LatLng {
		t.Helper()
		list, _ := r.svc.Drivers(t.Context())
		if list[0].Position == nil {
			t.Fatal("no position")
		}
		return *list[0].Position
	}
	if err := fix(1, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := fix(5, 30*time.Second); err != nil { // newer: wins
		t.Fatal(err)
	}
	if err := fix(9, 20*time.Second); err != nil { // recorded earlier than the one held, arrives later: dropped
		t.Fatal(err)
	}
	if got, want := position(), testkit.North(testkit.Kemang, 5); got != want {
		t.Errorf("position = %+v, want the fix recorded last %+v", got, want)
	}
	list, _ := r.svc.Drivers(t.Context())
	if want := contracts.At(t0.Add(30 * time.Second)); !list[0].LastSeen.Equal(want.Time) {
		t.Errorf("lastSeen = %v, want the time of the newest fix %v", list[0].LastSeen, want)
	}

	// a fix for a driver we do not know is ignored; an impossible one is dead-lettered
	if err := testkit.Apply(r.pool, r.svc.Position, contracts.TypeDriverPosition, "drv-404", 0, contracts.DriverPosition{DriverID: "drv-404", RecordedAt: contracts.Now()}); err != nil {
		t.Errorf("a fix of an unknown driver: %v, want it ignored", err)
	}
	err := testkit.Apply(r.pool, r.svc.Position, contracts.TypeDriverPosition, d.ID, 0, contracts.DriverPosition{DriverID: d.ID, Position: contracts.LatLng{Lat: 95}, RecordedAt: contracts.Now()})
	if err == nil || fmt.Sprintf("%T", err) != "eventbus.permanentError" {
		t.Errorf("an impossible fix: %v, want a permanent error", err)
	}
}

func TestRemovingAVehicleFreesItsDriver(t *testing.T) {
	r := newRig(t)
	v := r.vehicle(t, "B 1001 AA")
	d := r.driver(t, "Budi", "081234567801", v.ID)
	if err := r.svc.RemoveVehicle(t.Context(), v.ID); err != nil {
		t.Fatal(err)
	}
	list, err := r.svc.Drivers(t.Context())
	if err != nil || len(list) != 1 || list[0].VehicleID != "" {
		t.Fatalf("drivers = %+v, %v; want the driver kept, without a vehicle", list, err)
	}
	rows := outbox(t, r.pool, contracts.TypeDriverUpdated, contracts.TypeVehicleRemoved)
	if len(rows) != 2 || rows[0].eventType != contracts.TypeDriverUpdated || rows[0].key != d.ID || rows[1].eventType != contracts.TypeVehicleRemoved || rows[1].key != v.ID {
		t.Fatalf("events = %+v, want driver.updated then vehicle.removed", rows)
	}
	if ev, _ := rows[0].env.Payload[contracts.DriverUpdated](); ev.VehicleID != "" || ev.PinHash != "" {
		t.Errorf("driver.updated = %+v, want the driver without a vehicle and no hash", ev)
	}
}

func TestSeedDemoFillsOnlyAnEmptyFleet(t *testing.T) {
	r := newRig(t)
	if err := r.svc.SeedDemo(t.Context()); err != nil {
		t.Fatal(err)
	}
	vehicles, _ := r.svc.Vehicles(t.Context())
	drivers, _ := r.svc.Drivers(t.Context())
	if len(vehicles) != 2 || len(drivers) != 2 {
		t.Fatalf("%d vehicles, %d drivers; want a motorbike and a blind van, each with a driver", len(vehicles), len(drivers))
	}
	types := map[contracts.VehicleType]contracts.FareConfig{}
	for _, v := range vehicles {
		types[v.Type] = v.Fare
		if v.Status != contracts.VehicleStatusActive || v.DriverID == "" {
			t.Errorf("vehicle %+v, want it active with a driver", v)
		}
	}
	if types[contracts.VehicleTypeMotor] != defaultFare[contracts.VehicleTypeMotor] || types[contracts.VehicleTypeBlindvan].Base != 45000 {
		t.Errorf("fares = %+v, want the frontend's defaults", types)
	}
	prov := outbox(t, r.pool, contracts.TypeDriverProvisioned)
	if len(prov) != 2 {
		t.Fatalf("%d driver.provisioned events, want 2", len(prov))
	}
	ev, _ := prov[0].env.Payload[contracts.DriverProvisioned]()
	if ok, _ := platform.VerifySecret("123456", ev.PinHash); !ok {
		t.Error("the demo driver's hash does not verify the documented demo PIN")
	}

	if err := r.svc.SeedDemo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.svc.Drivers(t.Context()); len(again) != 2 {
		t.Errorf("%d drivers after seeding twice, want 2", len(again))
	}
}

func TestSeedDemoLeavesAFleetWithDataAlone(t *testing.T) {
	r := newRig(t)
	r.vehicle(t, "B 1001 AA")
	if err := r.svc.SeedDemo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.svc.Vehicles(t.Context()); len(list) != 1 {
		t.Errorf("%d vehicles, want only the existing one", len(list))
	}
}
