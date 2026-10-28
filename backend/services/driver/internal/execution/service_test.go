package execution_test

import (
	"encoding/json/v2"
	"errors"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/execution"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// These tests need Postgres (TEST_DATABASE_URL) and skip without it.

const (
	plan  = "pln-1"
	route = "pln-1:r1"
	stop1 = route + ":s1" // pickup
	stop2 = route + ":s2" // drop-off, drivertest.CodeA
	stop3 = route + ":s3" // drop-off, drivertest.CodeB
)

type fixture struct {
	t     *testing.T
	pool  *pgxpool.Pool
	clock *drivertest.Clock
	svc   *execution.Service
}

// newFixture has two drivers and, assigned to drv-1, the route of drivertest.Route with its start at the epoch.
func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := drivertest.Pool(t)
	clock := drivertest.NewClock(drivertest.Epoch)
	f := fixture{t, pool, clock, execution.NewService(pool, drivertest.Publisher(pool), clock.Now)}
	for id, phone := range map[string]string{"drv-1": "081234567801", "drv-2": "081234567802"} {
		m := drivers.Master{ID: id, Name: id, Phone: phone, VehicleID: "veh-" + id, PinHash: "$argon2id$x", JoinedAt: drivertest.Epoch, Version: 1}
		if err := drivers.Provision(t.Context(), pool, m); err != nil {
			t.Fatal(err)
		}
	}
	f.assign(route, "drv-1")
	return f
}

func (f fixture) assign(routeID, driverID string) {
	f.t.Helper()
	task := tasks.Task{PlanID: plan, PlanCode: "PL-2001", CustomerName: "Dina", DriverID: driverID, Route: drivertest.Route(routeID, driverID), AssignVersion: 1, UpdatedAt: drivertest.Epoch}
	if err := tasks.Put(f.t.Context(), f.pool, task, drivertest.Epoch); err != nil {
		f.t.Fatal(err)
	}
}

func (f fixture) task() tasks.Task {
	f.t.Helper()
	task, err := tasks.Lock(f.t.Context(), f.pool, route)
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f fixture) driver(id string) drivers.Driver {
	f.t.Helper()
	d, err := drivers.Get(f.t.Context(), f.pool, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f fixture) outbox() []drivertest.Published { return drivertest.Outbox(f.t, f.pool) }

// step moves the clock a minute, runs the action as drv-1 and fails the test if it is refused.
func (f fixture) step(act func(driver string) (tasks.Task, error)) tasks.Task {
	f.t.Helper()
	f.clock.Advance(time.Minute)
	task, err := act("drv-1")
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f fixture) start() tasks.Task {
	return f.step(func(d string) (tasks.Task, error) { return f.svc.Start(f.t.Context(), d, plan, route) })
}

func (f fixture) arrive(stop string) tasks.Task {
	return f.step(func(d string) (tasks.Task, error) { return f.svc.Arrive(f.t.Context(), d, plan, route, stop) })
}

func (f fixture) complete(stop string, proof *execution.StopProof) tasks.Task {
	return f.step(func(d string) (tasks.Task, error) { return f.svc.Complete(f.t.Context(), d, plan, route, stop, proof) })
}

func otp(code string) *execution.StopProof { return &execution.StopProof{Kind: "otp", Value: code} }

func wantRefusal(t *testing.T, err error, code platform.ErrorCode, key, field string) {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok || apiErr.Code != code || apiErr.Msg.Key != key || apiErr.Field != field {
		t.Fatalf("error = %v, want %s %q field %q", err, code, key, field)
	}
}

func TestARouteRunsFromStartToFinishWithOneRisingVersionPerEvent(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	biggestPhoto := "data:image/jpeg;base64," + strings.Repeat("A", 900_000-len("data:image/jpeg;base64,"))

	var seen []string
	check := func(action string, wantNew ...string) {
		t.Helper()
		events := f.outbox()
		var got []string
		for _, e := range events[len(seen):] {
			got = append(got, e.Type)
		}
		if len(got) != len(wantNew) {
			t.Fatalf("%s published %v, want %v", action, got, wantNew)
		}
		for i, typ := range wantNew {
			if got[i] != typ {
				t.Fatalf("%s published %v, want %v", action, got, wantNew)
			}
		}
		seen = drivertest.Types(events)
		for i, e := range events {
			if e.Key != route || e.Version != int64(i+1) {
				t.Fatalf("event %d is %s key %s version %d; versions must rise by one per event on the route", i, e.Type, e.Key, e.Version)
			}
		}
	}

	f.start()
	check("start", contracts.TypeRouteStarted)
	if d := f.driver("drv-1"); d.Status != drivers.StatusOnDuty {
		t.Errorf("status after start = %s, want bertugas", d.Status)
	}

	f.arrive(stop1)
	check("arrive 1", contracts.TypeStopArrived)
	f.complete(stop1, nil)
	check("complete 1", contracts.TypeStopCompleted)
	f.arrive(stop2)
	check("arrive 2", contracts.TypeStopArrived)
	f.complete(stop2, otp(drivertest.CodeA))
	check("complete 2", contracts.TypeStopCompleted)
	f.arrive(stop3)
	check("arrive 3", contracts.TypeStopArrived)
	done := f.complete(stop3, &execution.StopProof{Kind: "foto", Value: "bukti.jpg", Photo: biggestPhoto})
	check("complete 3", contracts.TypeStopCompleted, contracts.TypeRouteCompleted)

	if done.Route.Status != contracts.RouteStatusDone || done.ExecVersion != 8 {
		t.Errorf("returned task = %s at version %d", done.Route.Status, done.ExecVersion)
	}
	stored := f.task()
	if stored.Route.Status != contracts.RouteStatusDone || stored.ExecVersion != 8 || stored.AssignVersion != 1 || stored.Route.FinishedAt.IsZero() {
		t.Errorf("stored task = %s, exec %d, assign %d", stored.Route.Status, stored.ExecVersion, stored.AssignVersion)
	}
	events := f.outbox()
	completed := drivertest.Payload[contracts.StopCompleted](t, events[6])
	if completed.Proof == nil || completed.Proof.Kind != contracts.ProofKindPhoto || len(completed.Proof.Photo) != 900_000 {
		t.Errorf("the largest accepted photo must travel in stop.completed: %+v", completed.Proof)
	}
	if otpProof := drivertest.Payload[contracts.StopCompleted](t, events[4]).Proof; otpProof == nil || otpProof.Kind != contracts.ProofKindOTP {
		t.Errorf("otp proof = %+v", otpProof)
	}
	for _, e := range events {
		if strings.Contains(string(e.Data), drivertest.CodeA) || strings.Contains(string(e.Data), "deliveryCode") {
			t.Errorf("%s carries a delivery code", e.Type)
		}
	}

	d := f.driver("drv-1")
	last := stored.Route.Stops[2].Waypoint.Position
	if d.Status != drivers.StatusOnline || d.CompletedTrips != 1 || d.Position == nil || *d.Position != last {
		t.Errorf("driver = %s, %d trips, at %v; want online, 1 trip, at the last stop %v", d.Status, d.CompletedTrips, d.Position, last)
	}
	if _, err := f.svc.Routes(ctx, "drv-1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := f.svc.Routes(ctx, "drv-1"); len(list) != 0 {
		t.Errorf("a finished route is still listed as a task")
	}
}

func TestARepeatedActionReturnsTheSameStateAndPublishesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	first := f.start()
	f.arrive(stop1)
	f.complete(stop1, nil)
	f.arrive(stop2)
	f.complete(stop2, otp(drivertest.CodeA))
	before, eventsBefore := f.task(), len(f.outbox())

	for name, act := range map[string]func() (tasks.Task, error){
		"start again":    func() (tasks.Task, error) { return f.svc.Start(ctx, "drv-1", plan, route) },
		"arrive again":   func() (tasks.Task, error) { return f.svc.Arrive(ctx, "drv-1", plan, route, stop2) },
		"arrive earlier": func() (tasks.Task, error) { return f.svc.Arrive(ctx, "drv-1", plan, route, stop1) },
		"complete again": func() (tasks.Task, error) {
			return f.svc.Complete(ctx, "drv-1", plan, route, stop2, otp(drivertest.CodeA))
		},
		"complete again with another proof": func() (tasks.Task, error) {
			return f.svc.Complete(ctx, "drv-1", plan, route, stop2, otp("0000"))
		},
		"complete the pickup again": func() (tasks.Task, error) { return f.svc.Complete(ctx, "drv-1", plan, route, stop1, nil) },
	} {
		f.clock.Advance(time.Minute)
		got, err := act()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got.ExecVersion != before.ExecVersion || got.Route.Status != before.Route.Status {
			t.Errorf("%s: answered version %d, status %s", name, got.ExecVersion, got.Route.Status)
		}
	}
	after := f.task()
	if after.ExecVersion != before.ExecVersion || after.UpdatedAt != before.UpdatedAt {
		t.Errorf("a repeat changed the stored task: version %d -> %d, updated %v -> %v", before.ExecVersion, after.ExecVersion, before.UpdatedAt, after.UpdatedAt)
	}
	if n := len(f.outbox()); n != eventsBefore {
		t.Errorf("repeats published %d more events", n-eventsBefore)
	}
	if first.Route.StartedAt != after.Route.StartedAt {
		t.Error("a repeated start moved the start time")
	}
}

func TestAWrongOTPRefusesAndChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.start()
	f.arrive(stop1)
	f.complete(stop1, nil)
	f.arrive(stop2)
	before, events, driver := f.task(), len(f.outbox()), f.driver("drv-1")

	for _, proof := range []*execution.StopProof{otp("0000"), otp(""), otp(drivertest.CodeB), {Kind: "tanpa"}, nil} {
		f.clock.Advance(time.Minute)
		_, err := f.svc.Complete(ctx, "drv-1", plan, route, stop2, proof)
		apiErr, ok := errors.AsType[*platform.APIError](err)
		if !ok || apiErr.Code != platform.CodeInvalid || apiErr.Field != "proof" {
			t.Fatalf("proof %+v: error = %v, want tidak_valid on proof", proof, err)
		}
	}
	wantRefusal(t, func() error {
		_, err := f.svc.Complete(ctx, "drv-1", plan, route, stop2, otp("0000"))
		return err
	}(), platform.CodeInvalid, "srv.api.otpMismatch", "proof")

	after := f.task()
	if after.ExecVersion != before.ExecVersion || after.UpdatedAt != before.UpdatedAt || after.Route.Stops[1].Status != contracts.StopStatusArrived || after.Route.Stops[1].Proof != nil {
		t.Errorf("refused attempts changed the task: %+v", after.Route.Stops[1])
	}
	if n := len(f.outbox()); n != events {
		t.Errorf("refused attempts published %d events", n-events)
	}
	if got := f.driver("drv-1"); got.Status != driver.Status || got.CompletedTrips != driver.CompletedTrips || got.LastSeen != driver.LastSeen {
		t.Errorf("refused attempts changed the driver: %+v", got)
	}

	f.complete(stop2, otp(drivertest.CodeA)) // the right code still works afterwards
	if f.task().Route.Stops[1].Status != contracts.StopStatusDone {
		t.Error("the correct code was refused after wrong ones")
	}
}

func TestStopsCannotBeClosedOutOfOrderOrBeforeArriving(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	proof := otp(drivertest.CodeA)

	_, err := f.svc.Arrive(ctx, "drv-1", plan, route, stop1)
	wantRefusal(t, err, platform.CodeConflict, "srv.api.startRouteFirst", "")
	_, err = f.svc.Complete(ctx, "drv-1", plan, route, stop1, nil)
	wantRefusal(t, err, platform.CodeConflict, "srv.api.markArrivedFirst", "")

	f.start()
	_, err = f.svc.Complete(ctx, "drv-1", plan, route, stop1, nil) // en route, not arrived
	wantRefusal(t, err, platform.CodeConflict, "srv.api.markArrivedFirst", "")
	_, err = f.svc.Arrive(ctx, "drv-1", plan, route, stop2)
	wantRefusal(t, err, platform.CodeConflict, "srv.api.finishPreviousStop", "")
	_, err = f.svc.Complete(ctx, "drv-1", plan, route, stop2, proof)
	wantRefusal(t, err, platform.CodeConflict, "srv.api.markArrivedFirst", "")
	_, err = f.svc.Arrive(ctx, "drv-1", plan, route, "nope")
	wantRefusal(t, err, platform.CodeNotFound, "srv.ops.err.stopNotFound", "")

	if got := drivertest.Types(f.outbox()); len(got) != 1 || got[0] != contracts.TypeRouteStarted {
		t.Errorf("events = %v, want only route.started", got)
	}
	if f.task().Route.Stops[1].Status != contracts.StopStatusWaiting {
		t.Error("a refused action changed a stop")
	}
}

func TestOnlyTheDriverOfARouteMayActOnIt(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	_, err := f.svc.Start(ctx, "drv-2", plan, route)
	wantRefusal(t, err, platform.CodeDenied, "srv.api.notYourRoute", "")
	_, err = f.svc.Start(ctx, "drv-1", plan, "pln-9:r1")
	wantRefusal(t, err, platform.CodeNotFound, "srv.ops.err.routeNotFound", "")
	_, err = f.svc.Start(ctx, "drv-1", "pln-9", route)
	wantRefusal(t, err, platform.CodeNotFound, "srv.ops.err.routeNotFound", "")

	f.start()
	for name, act := range map[string]func() error{
		"arrive":   func() error { _, err := f.svc.Arrive(ctx, "drv-2", plan, route, stop1); return err },
		"complete": func() error { _, err := f.svc.Complete(ctx, "drv-2", plan, route, stop1, nil); return err },
		"issue": func() error {
			_, err := f.svc.ReportIssue(ctx, "drv-2", plan, route, stop1, contracts.IssueKindNobodyThere, "")
			return err
		},
	} {
		wantRefusal(t, act(), platform.CodeDenied, "srv.api.notYourRoute", "")
		_ = name
	}

	// a route dispatch took back belongs to nobody
	task := f.task()
	task.DriverID, task.Route.Status = "", contracts.RouteStatusAwaitingDriver
	task.Route.DriverID = ""
	if err := tasks.Put(ctx, f.pool, task, drivertest.Epoch); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Arrive(ctx, "drv-1", plan, route, stop1)
	wantRefusal(t, err, platform.CodeDenied, "srv.api.notYourRoute", "")

	if got := drivertest.Types(f.outbox()); len(got) != 1 {
		t.Errorf("events = %v, want only the route.started of the owner", got)
	}
	if d := f.driver("drv-2"); d.Status != drivers.StatusOffline || d.CompletedTrips != 0 || d.Position != nil {
		t.Errorf("the other driver was touched: %+v", d)
	}
}

func TestStartIsRefusedTooEarlyForAScheduledRoute(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	task := f.task()
	task.Route.StartAt = contracts.At(drivertest.Epoch.Add(3 * time.Hour))
	if err := tasks.Put(ctx, f.pool, task, drivertest.Epoch); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Start(ctx, "drv-1", plan, route)
	wantRefusal(t, err, platform.CodeConflict, "srv.api.tooEarly", "")
	if len(f.outbox()) != 0 || f.task().Route.Status != contracts.RouteStatusAssigned {
		t.Error("a refused start changed something")
	}

	f.clock.Set(drivertest.Epoch.Add(2*time.Hour + 15*time.Minute))
	if _, err := f.svc.Start(ctx, "drv-1", plan, route); err != nil {
		t.Errorf("45 minutes before the departure: %v", err)
	}
}

func TestAnIssueClosesTheStopAndMovesOn(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.start()
	f.arrive(stop1)
	f.complete(stop1, nil)

	_, err := f.svc.ReportIssue(ctx, "drv-1", plan, route, stop2, contracts.IssueKindOther, " ")
	wantRefusal(t, err, platform.CodeInvalid, "srv.api.issueNoteRequired", "note")
	_, err = f.svc.ReportIssue(ctx, "drv-1", plan, route, stop3, contracts.IssueKindNobodyThere, "")
	wantRefusal(t, err, platform.CodeConflict, "srv.api.issueOnlyActive", "")

	f.clock.Advance(time.Minute)
	task, err := f.svc.ReportIssue(ctx, "drv-1", plan, route, stop2, contracts.IssueKindRecipientRefused, "menolak")
	if err != nil {
		t.Fatal(err)
	}
	if task.Route.Stops[1].Status != contracts.StopStatusProblem || task.Route.Stops[2].Status != contracts.StopStatusEnRoute {
		t.Errorf("stops = %s, %s", task.Route.Stops[1].Status, task.Route.Stops[2].Status)
	}
	events := f.outbox()
	last := events[len(events)-1]
	issue := drivertest.Payload[contracts.StopIssue](t, last)
	if last.Type != contracts.TypeStopIssue || last.Version != 4 || issue.StopID != stop2 || issue.Kind != contracts.IssueKindRecipientRefused || issue.Note != "menolak" {
		t.Errorf("event = %s v%d %+v", last.Type, last.Version, issue)
	}
	stored := f.task().Route.Stops[1].Issue
	if stored == nil || stored.Key != issue.Issue.Key || stored.Key != "srv.ops.issue.withNote" {
		t.Errorf("stored issue %+v and event issue %+v must be the one message", stored, issue.Issue)
	}

	n := len(events)
	if _, err := f.svc.ReportIssue(ctx, "drv-1", plan, route, stop2, contracts.IssueKindRecipientRefused, "menolak"); err != nil || len(f.outbox()) != n {
		t.Errorf("a repeated report: %v, %d new events", err, len(f.outbox())-n)
	}

	// closing the last stop with a problem still finishes the route
	f.arrive(stop3)
	f.clock.Advance(time.Minute)
	if _, err := f.svc.ReportIssue(ctx, "drv-1", plan, route, stop3, contracts.IssueKindWrongAddress, ""); err != nil {
		t.Fatal(err)
	}
	events = f.outbox()
	if got := events[len(events)-1]; got.Type != contracts.TypeRouteCompleted || got.Version != 7 {
		t.Errorf("last event = %s v%d, want route.completed v7", got.Type, got.Version)
	}
	if d := f.driver("drv-1"); d.CompletedTrips != 1 || d.Status != drivers.StatusOnline {
		t.Errorf("driver = %+v", d)
	}
}

func TestConcurrentRepeatsOfOneActionPublishOneEvent(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.start()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := f.svc.Arrive(ctx, "drv-1", plan, route, stop1)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a concurrent retry failed: %v", err)
		}
	}
	got := drivertest.Types(f.outbox())
	if len(got) != 2 || got[1] != contracts.TypeStopArrived {
		t.Errorf("events = %v, want route.started and exactly one stop.arrived", got)
	}
}

func TestRoutesListsTheDriversOwnOpenRoutesWithoutCodes(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.assign("pln-2:r1", "drv-2")
	f.assign("pln-3:r1", "drv-1")
	done := tasks.Task{PlanID: "pln-4", DriverID: "drv-1", Route: drivertest.Route("pln-4:r1", "drv-1")}
	done.Route.Status = contracts.RouteStatusDone
	if err := tasks.Put(ctx, f.pool, done, drivertest.Epoch); err != nil {
		t.Fatal(err)
	}
	f.start()

	list, err := f.svc.Routes(ctx, "drv-1")
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]contracts.RouteStatus{}
	for _, r := range list {
		status[r.Route.ID] = r.Route.Status
		if r.PlanCode != "PL-2001" || r.CustomerName != "Dina" {
			t.Errorf("route %s lost its plan code or customer: %+v", r.Route.ID, r)
		}
	}
	if want := map[string]contracts.RouteStatus{route: contracts.RouteStatusRunning, "pln-3:r1": contracts.RouteStatusAssigned}; !maps.Equal(status, want) {
		t.Fatalf("routes = %v, want %v: only this driver's assigned and running ones", status, want)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), drivertest.CodeA) || strings.Contains(string(raw), drivertest.CodeB) || strings.Contains(string(raw), "deliveryCode") {
		t.Errorf("driver.routes leaks a delivery code: %s", raw)
	}
	if f.task().Route.Stops[1].DeliveryCode != drivertest.CodeA {
		t.Error("the stored route lost its code")
	}

	if none, err := f.svc.Routes(ctx, "drv-9"); err != nil || none == nil || len(none) != 0 {
		t.Errorf("a driver without tasks gets %v, %v; want an empty list", none, err)
	}
}
