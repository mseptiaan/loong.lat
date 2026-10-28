package plans

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

// event builds an envelope as console-api, driver-api or the tests' other neighbours would publish it.
func event(t *testing.T, eventType, key string, version int64, data any) contracts.Envelope {
	t.Helper()
	env, err := contracts.NewEnvelope("test", eventType, key, version, data)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// deliver runs a handler on the envelope and fails the test on an error.
func (r *rig) deliver(t *testing.T, h eventbus.Handler, env contracts.Envelope) {
	t.Helper()
	if err := testkit.ApplyEnvelope(r.pool, h, env); err != nil {
		t.Fatalf("%s: %v", env.Type, err)
	}
}

// notes are the admins' notifications whose title has the key, newest first.
func (r *rig) notes(t *testing.T, titleKey string) []contracts.AppNotification {
	t.Helper()
	var out []contracts.AppNotification
	for _, n := range testkit.AdminNotifications(t, r.pool) {
		if n.Title.Key == titleKey {
			out = append(out, n)
		}
	}
	return out
}

// setStatus gives the projected plan a status, as the execution events would have.
func (r *rig) setStatus(t *testing.T, planID string, status contracts.PlanStatus) {
	t.Helper()
	err := platform.WithTx(t.Context(), r.pool, func(tx pgx.Tx) error {
		a, err := Lock(t.Context(), tx, planID)
		if err != nil {
			return err
		}
		a.Plan.Status = status
		return Save(t.Context(), tx, a)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (r *rig) setDispatchAuto(t *testing.T, auto bool) {
	t.Helper()
	value := `{"auto": false}`
	if auto {
		value = `{"auto": true}`
	}
	if _, err := r.pool.Exec(t.Context(), `UPDATE settings SET value = $1 WHERE key = 'dispatch'`, value); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) customer(t *testing.T) {
	t.Helper()
	if err := testkit.Apply(r.pool, r.projection.CustomerRegistered, contracts.TypeCustomerRegistered, "cus-1", 1,
		contracts.CustomerRegistered{CustomerID: "cus-1", Name: "Dina Pramesti"}); err != nil {
		t.Fatal(err)
	}
}

func holds(a *Aggregate) []string {
	var out []string
	for _, r := range a.Routes {
		out = append(out, r.Hold)
	}
	return out
}

func conflictKey(t *testing.T, err error) string {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok || apiErr.Code != platform.CodeConflict {
		t.Fatalf("error = %v, want a conflict (bentrok)", err)
	}
	return apiErr.Msg.Key
}

func TestAdminCancelMarksThePlanPendingAndAsksConsole(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())

	got, err := r.service.Cancel(t.Context(), "pln-1", "  customer called  ", "Rani Wulandari", platform.LocaleID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PendingAction != "cancel" || got.Status != contracts.PlanStatusAwaitingDriver {
		t.Errorf("answer: pending %q, status %s; want the plan as it is, pending a cancel", got.PendingAction, got.Status)
	}
	if again, err := r.service.Get(t.Context(), "pln-1"); err != nil || again.PendingAction != "cancel" {
		t.Errorf("plans.get: pending %q, %v; want the pending action kept", again.PendingAction, err)
	}
	list, err := r.service.List(t.Context(), Filter{})
	if err != nil || len(list) != 1 || list[0].PendingAction != "cancel" {
		t.Errorf("plans.list = %+v, %v; want the row to say a cancel is pending", list, err)
	}

	asked := testkit.Outbox(t, r.pool, contracts.TypePlanCancelAsked)
	if len(asked) != 1 || asked[0].Key != "pln-1" {
		t.Fatalf("outbox = %+v, want one plan.cancel.asked keyed by the plan", asked)
	}
	ev, _ := asked[0].Payload[contracts.PlanCancelAsked]()
	if ev.PlanID != "pln-1" || ev.Reason != "customer called" || ev.AskedBy != "Rani Wulandari" || ev.Locale != "id" {
		t.Errorf("plan.cancel.asked = %+v, want the plan, the trimmed reason, the admin's name and language", ev)
	}

	if _, err := r.service.Cancel(t.Context(), "pln-1", "again", "Rani Wulandari", platform.LocaleEN); conflictKey(t, err) != MsgCancelPending {
		t.Errorf("a second cancel: %v, want the pending-cancel conflict", err)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypePlanCancelAsked)); n != 1 {
		t.Errorf("%d plan.cancel.asked after a refused second cancel, want still one", n)
	}
}

func TestAdminCancelRefusesWhatTheMockRefuses(t *testing.T) {
	tests := []struct {
		name    string
		status  contracts.PlanStatus
		wantKey string
	}{
		{"a plan under way", contracts.PlanStatusRunning, "srv.api.cancelRunning"},
		{"a finished plan", contracts.PlanStatusDone, "srv.api.planInactive"},
		{"a cancelled plan", contracts.PlanStatusCancelled, "srv.api.planInactive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.confirm(t, twoRoutePlan())
			r.setStatus(t, "pln-1", tt.status)

			_, err := r.service.Cancel(t.Context(), "pln-1", "", "Rani", platform.LocaleEN)
			if got := conflictKey(t, err); got != tt.wantKey {
				t.Errorf("message = %s, want %s", got, tt.wantKey)
			}
			if a := r.load(t, "pln-1"); a.Plan.PendingAction != "" || a.Plan.Status != tt.status {
				t.Errorf("plan %s pending %q after a refused cancel, want it untouched", a.Plan.Status, a.Plan.PendingAction)
			}
			if n := len(testkit.Outbox(t, r.pool, contracts.TypePlanCancelAsked)); n != 0 {
				t.Errorf("%d plan.cancel.asked after a refused cancel", n)
			}
		})
	}

	r := newRig(t)
	if _, err := r.service.Cancel(t.Context(), "pln-404", "", "Rani", platform.LocaleEN); !isStatus(err, platform.CodeNotFound) {
		t.Errorf("unknown plan: %v, want not found", err)
	}
}

func TestCancelRequestedFreezesEveryRouteOfThePlan(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")

	requested := event(t, contracts.TypePlanCancelRequested, "pln-1", 2, contracts.PlanCancelRequested{
		PlanID: "pln-1", RequestID: "req-1", RequestedBy: contracts.CancelByCustomer, RouteIDs: []string{"pln-1:r1", "pln-1:r2"},
	})
	r.deliver(t, r.projection.CancelRequested, requested)

	a := r.load(t, "pln-1")
	if !slices.Equal(holds(a), []string{"req-1", "req-1"}) || a.Plan.PendingAction != "cancel" || !a.Frozen() {
		t.Fatalf("holds %v, pending %q; want both routes held for the request and the plan pending", holds(a), a.Plan.PendingAction)
	}
	if a.Plan.Status != contracts.PlanStatusAwaitingDriver || a.Routes[0].DriverID != "drv-1" {
		t.Errorf("plan %s, route 1 driver %q; freezing must not change the assignment", a.Plan.Status, a.Routes[0].DriverID)
	}
	if got, err := WaitingRoutes(t.Context(), r.pool, time.Now().Add(time.Hour)); err != nil || len(got) != 0 {
		t.Errorf("waiting routes = %+v, %v; a frozen route must not be offered to dispatch", got, err)
	}

	// the same event again, and the same request in another event: nothing moves
	before := r.load(t, "pln-1")
	r.deliver(t, r.projection.CancelRequested, requested)
	r.deliver(t, r.projection.CancelRequested, event(t, contracts.TypePlanCancelRequested, "pln-1", 2, contracts.PlanCancelRequested{PlanID: "pln-1", RequestID: "req-1"}))
	if after := r.load(t, "pln-1"); after.Plan.UpdatedAt != before.Plan.UpdatedAt || !slices.Equal(holds(after), holds(before)) {
		t.Errorf("a repeated request changed the plan: %v -> %v", holds(before), holds(after))
	}

	// not a bad event but an early one: the plan is on its way
	err := testkit.Apply(r.pool, r.projection.CancelRequested, contracts.TypePlanCancelRequested, "pln-9", 2, contracts.PlanCancelRequested{PlanID: "pln-9", RequestID: "req-1"})
	if err == nil || isPermanent(err) {
		t.Errorf("unknown plan: %v, want a plain error so the consumer retries", err)
	}
	err = testkit.Apply(r.pool, r.projection.CancelRequested, contracts.TypePlanCancelRequested, "pln-1", 3, contracts.PlanCancelRequested{PlanID: "pln-1"})
	if err == nil || !isPermanent(err) {
		t.Errorf("a request without an id: %v, want a permanent error", err)
	}
}

func TestCancelRequestedForAClosedPlanIsIgnored(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	r.setStatus(t, "pln-1", contracts.PlanStatusCancelled)
	r.deliver(t, r.projection.CancelRequested, event(t, contracts.TypePlanCancelRequested, "pln-1", 2, contracts.PlanCancelRequested{PlanID: "pln-1", RequestID: "req-1"}))
	if a := r.load(t, "pln-1"); a.Frozen() {
		t.Errorf("a cancelled plan was frozen: holds %v pending %q", holds(a), a.Plan.PendingAction)
	}
}

func TestCancelAbortedLiftsOnlyTheFreezeOfItsOwnRequest(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	request := func(id string, version int64) {
		r.deliver(t, r.projection.CancelRequested, event(t, contracts.TypePlanCancelRequested, "pln-1", version, contracts.PlanCancelRequested{PlanID: "pln-1", RequestID: id}))
	}
	abort := func(id string, version int64) contracts.Envelope {
		env := event(t, contracts.TypePlanCancelAborted, "pln-1", version, contracts.PlanCancelAborted{PlanID: "pln-1", RequestID: id, Reason: contracts.Msg{Key: "x"}})
		r.deliver(t, r.projection.CancelAborted, env)
		return env
	}

	request("req-1", 2)
	abort("req-0", 3) // an attempt that ended long ago
	if a := r.load(t, "pln-1"); !a.Frozen() {
		t.Fatalf("a stale abort thawed the plan")
	}

	env := abort("req-1", 3)
	a := r.load(t, "pln-1")
	if a.Frozen() || !slices.Equal(holds(a), []string{"", ""}) || a.Plan.PendingAction != "" {
		t.Fatalf("holds %v pending %q after the abort, want the plan free", holds(a), a.Plan.PendingAction)
	}
	if got, err := WaitingRoutes(t.Context(), r.pool, time.Now().Add(time.Hour)); err != nil || len(got) != 2 {
		t.Errorf("waiting routes = %+v, %v; want both routes back for dispatch", got, err)
	}
	r.deliver(t, r.projection.CancelAborted, env) // redelivered

	// a newer attempt is not undone by the late abort of the first
	request("req-2", 4)
	abort("req-1", 3)
	if a := r.load(t, "pln-1"); !slices.Equal(holds(a), []string{"req-2", "req-2"}) {
		t.Errorf("holds = %v, want the second request still holding", holds(a))
	}
}

func TestCancelRejectedWithoutARouteIsConsolesAnswerToAnAdmin(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	if _, err := r.service.Cancel(t.Context(), "pln-1", "", "Rani", platform.LocaleEN); err != nil {
		t.Fatal(err)
	}
	reason := contracts.Msg{Key: "srv.api.cancelRunning"}

	// a driver's vote names a route: it is not ours to act on
	r.deliver(t, r.projection.CancelRejected, event(t, contracts.TypePlanCancelRejected, "pln-1", 0,
		contracts.PlanCancelRejected{PlanID: "pln-1", RequestID: "req-1", RouteID: "pln-1:r1", Reason: reason}))
	if a := r.load(t, "pln-1"); a.Plan.PendingAction != "cancel" {
		t.Fatalf("pending %q after a vote, want it kept", a.Plan.PendingAction)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypePlanCancelAsked)); n != 1 {
		t.Fatalf("%d asks", n)
	}
	if got := r.notes(t, "srv.api.notif.cancelRefused.title"); len(got) != 0 {
		t.Fatalf("%d refusal notices after a vote", len(got))
	}

	refusal := event(t, contracts.TypePlanCancelRejected, "pln-1", 0, contracts.PlanCancelRejected{PlanID: "pln-1", Reason: reason})
	r.deliver(t, r.projection.CancelRejected, refusal)
	if a := r.load(t, "pln-1"); a.Plan.PendingAction != "" {
		t.Errorf("pending %q after the refusal, want the plan back to normal", a.Plan.PendingAction)
	}
	notes := r.notes(t, "srv.api.notif.cancelRefused.title")
	if len(notes) != 1 || notes[0].PlanID != "pln-1" || notes[0].Title.Params["code"] != "PL-pln-1" {
		t.Fatalf("notices = %+v, want one that names the plan", notes)
	}
	if got, _ := notes[0].Body.Params["reason"].(map[string]any); got["key"] != "srv.api.cancelRunning" {
		t.Errorf("notice body = %+v, want the refusal's reason as a nested message", notes[0].Body)
	}
	r.deliver(t, r.projection.CancelRejected, refusal) // redelivered
	if got := r.notes(t, "srv.api.notif.cancelRefused.title"); len(got) != 1 {
		t.Errorf("%d notices after a redelivery, want one", len(got))
	}

	// a customer's own request froze the plan before the refusal arrived: that cancel is still going on
	r.deliver(t, r.projection.CancelRequested, event(t, contracts.TypePlanCancelRequested, "pln-1", 2, contracts.PlanCancelRequested{PlanID: "pln-1", RequestID: "req-9"}))
	r.deliver(t, r.projection.CancelRejected, event(t, contracts.TypePlanCancelRejected, "pln-1", 0, contracts.PlanCancelRejected{PlanID: "pln-1", Reason: reason}))
	if a := r.load(t, "pln-1"); a.Plan.PendingAction != "cancel" || a.Held() != "req-9" {
		t.Errorf("pending %q hold %q; a refusal must not thaw another request's freeze", a.Plan.PendingAction, a.Held())
	}
}

func TestCancelCommittedCancelsThePlanAndFreesItsDrivers(t *testing.T) {
	r := newRig(t)
	r.customer(t)
	r.confirm(t, twoRoutePlan())
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")
	r.assign(t, "pln-1", "pln-1:r2", "drv-2")
	r.deliver(t, r.projection.CancelRequested, event(t, contracts.TypePlanCancelRequested, "pln-1", 2, contracts.PlanCancelRequested{PlanID: "pln-1", RequestID: "req-1"}))

	committed := event(t, contracts.TypePlanCancelled, "pln-1", 3, contracts.PlanCancelled{
		PlanID: "pln-1", RequestID: "req-1", RequestedBy: contracts.CancelByCustomer, Reason: "changed my mind", RefundRp: 40000,
	})
	r.deliver(t, r.projection.CancelCommitted, committed)

	a := r.load(t, "pln-1")
	if a.Plan.Status != contracts.PlanStatusCancelled || a.Plan.CancelReason != "changed my mind" || a.Plan.PendingAction != "" || a.Frozen() {
		t.Fatalf("plan %s reason %q pending %q holds %v; want it cancelled, with the reason, nothing pending", a.Plan.Status, a.Plan.CancelReason, a.Plan.PendingAction, holds(a))
	}
	for _, route := range a.Routes {
		if route.Status != contracts.RouteStatusAwaitingDriver || route.DriverID != "" || route.VehicleID != "" || !route.AssignedAt.IsZero() || route.AssignVersion != 2 {
			t.Errorf("route %s: %s, driver %q, assigned %v, assign version %d; want it unassigned (version 2) and waiting", route.ID, route.Status, route.DriverID, route.AssignedAt, route.AssignVersion)
		}
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)); n != 0 {
		t.Errorf("%d route.unassigned events, want none: driver-api closes its tasks from plan.cancelled", n)
	}
	if busy, err := ActiveRoutes(t.Context(), r.pool); err != nil || len(busy) != 0 {
		t.Errorf("active routes = %d, %v; want the drivers free", len(busy), err)
	}
	if waiting, err := WaitingRoutes(t.Context(), r.pool, time.Now().Add(time.Hour)); err != nil || len(waiting) != 0 {
		t.Errorf("waiting routes = %+v, %v; a cancelled plan needs no driver", waiting, err)
	}
	if last := a.Plan.Events[len(a.Plan.Events)-1].Text; last.Key != "srv.api.event.cancelledReason" || last.Params["reason"] != "changed my mind" {
		t.Errorf("timeline ends with %+v, want the cancellation with its reason", last)
	}
	sum := Summarize(a.Snapshot(), "Dina", nil)
	if sum.Status != contracts.PlanStatusCancelled || sum.RoutesWithoutDriver != 0 || sum.PendingAction != "" {
		t.Errorf("summary: %s, %d routes without driver, pending %q", sum.Status, sum.RoutesWithoutDriver, sum.PendingAction)
	}

	notes := r.notes(t, "srv.api.notif.cancelled.title")
	if len(notes) != 1 || notes[0].Title.Params["code"] != "PL-pln-1" || notes[0].Body.Params["name"] != "Dina Pramesti" || notes[0].PlanID != "pln-1" {
		t.Fatalf("notices = %+v, want one: the customer cancelled", notes)
	}

	// redelivery: the same result, nothing twice
	r.deliver(t, r.projection.CancelCommitted, committed)
	again := r.load(t, "pln-1")
	if again.Routes[0].AssignVersion != 2 || len(again.Plan.Events) != len(a.Plan.Events) || len(r.notes(t, "srv.api.notif.cancelled.title")) != 1 {
		t.Errorf("a redelivered plan.cancelled changed things: version %d, %d timeline lines (was %d), %d notices",
			again.Routes[0].AssignVersion, len(again.Plan.Events), len(a.Plan.Events), len(r.notes(t, "srv.api.notif.cancelled.title")))
	}
}

func TestCancelCommittedByAnAdminDoesNotNotifyAndKeepsRoutesThatAreRunning(t *testing.T) {
	r := newRig(t)
	r.customer(t)
	r.confirm(t, twoRoutePlan())
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")
	r.deliver(t, r.projection.CancelRequested, event(t, contracts.TypePlanCancelRequested, "pln-1", 2, contracts.PlanCancelRequested{PlanID: "pln-1", RequestID: "req-1"}))

	r.deliver(t, r.projection.CancelCommitted, event(t, contracts.TypePlanCancelled, "pln-1", 3, contracts.PlanCancelled{
		PlanID: "pln-1", RequestID: "req-1", RequestedBy: contracts.CancelByAdmin, Reason: "Cancelled by admin",
	}))
	a := r.load(t, "pln-1")
	if a.Plan.Status != contracts.PlanStatusCancelled || a.Plan.CancelReason != "Cancelled by admin" {
		t.Fatalf("plan %s reason %q", a.Plan.Status, a.Plan.CancelReason)
	}
	if last := a.Plan.Events[len(a.Plan.Events)-1].Text; last.Key != "srv.api.event.cancelled" {
		t.Errorf("timeline ends with %+v, want the plain cancellation (the default reason is not repeated)", last)
	}
	if got := r.notes(t, "srv.api.notif.cancelled.title"); len(got) != 0 {
		t.Errorf("%d notices for a cancel the admin asked for himself", len(got))
	}
}

func TestCancelCommittedOfAnotherRequestIsIgnored(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	r.deliver(t, r.projection.CancelRequested, event(t, contracts.TypePlanCancelRequested, "pln-1", 3, contracts.PlanCancelRequested{PlanID: "pln-1", RequestID: "req-2"}))

	r.deliver(t, r.projection.CancelCommitted, event(t, contracts.TypePlanCancelled, "pln-1", 2, contracts.PlanCancelled{PlanID: "pln-1", RequestID: "req-1", Reason: "late"}))
	a := r.load(t, "pln-1")
	if a.Plan.Status == contracts.PlanStatusCancelled || a.Held() != "req-2" {
		t.Errorf("plan %s hold %q; the commit of an older request must change nothing", a.Plan.Status, a.Held())
	}
}

func TestACancelledPlanStaysCancelledWhateverTheRoutesDo(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	r.deliver(t, r.projection.CancelCommitted, event(t, contracts.TypePlanCancelled, "pln-1", 2, contracts.PlanCancelled{PlanID: "pln-1", Reason: "x"}))

	// a straggler from the execution topic would recompute the status from the routes
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")
	r.assign(t, "pln-1", "pln-1:r2", "drv-2")
	if a := r.load(t, "pln-1"); a.Plan.Status != contracts.PlanStatusCancelled {
		t.Errorf("plan %s, want dibatalkan to win over the status the routes would give", a.Plan.Status)
	}
}

func rescheduledTo(t *testing.T, plan contracts.Plan, from time.Time) contracts.PlanRescheduled {
	t.Helper()
	ev := contracts.PlanRescheduled{PlanID: plan.ID, Schedule: contracts.Schedule{
		Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{From: contracts.At(from), To: contracts.At(from.Add(time.Hour))},
	}}
	for _, r := range plan.Routes {
		r.StartAt = contracts.At(from)
		r.DurationMin = 33 // anything that shows the new timing was taken over
		ev.Routes = append(ev.Routes, r)
	}
	return ev
}

func TestRescheduledRetimesTheRoutesAndReturnsAssignedOnesToTheQueue(t *testing.T) {
	r := newRig(t)
	plan := twoRoutePlan()
	r.confirm(t, plan)
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")
	r.assign(t, "pln-1", "pln-1:r2", "drv-2")
	// route 2 left before the customer moved the plan: it runs on
	r.deliver(t, r.projection.RouteStarted, event(t, contracts.TypeRouteStarted, "pln-1:r2", 1,
		contracts.RouteStarted{PlanID: "pln-1", RouteID: "pln-1:r2", DriverID: "drv-2", StartedAt: contracts.Now()}))
	runningStart := r.load(t, "pln-1").Routes[1].StartAt

	newStart := time.Now().Add(3 * time.Hour).Truncate(time.Millisecond)
	moved := event(t, contracts.TypePlanRescheduled, "pln-1", 2, rescheduledTo(t, plan, newStart))
	r.deliver(t, r.projection.Rescheduled, moved)

	a := r.load(t, "pln-1")
	if a.Plan.Schedule.Mode != contracts.ScheduleModeScheduled || a.Plan.Schedule.Window == nil || !a.Plan.Schedule.Window.From.Equal(newStart) {
		t.Fatalf("schedule = %+v, want the new window", a.Plan.Schedule)
	}
	one, two := a.Routes[0], a.Routes[1]
	if one.Status != contracts.RouteStatusAwaitingDriver || one.DriverID != "" || one.VehicleID != "" || one.AssignVersion != 2 {
		t.Errorf("route 1: %s, driver %q, assign version %d; want it back in the queue at version 2", one.Status, one.DriverID, one.AssignVersion)
	}
	if !one.StartAt.Equal(newStart) || one.DurationMin != 33 {
		t.Errorf("route 1 starts %v for %v min, want the re-timed route from the event", one.StartAt, one.DurationMin)
	}
	if two.Status != contracts.RouteStatusRunning || two.DriverID != "drv-2" || !two.StartAt.Equal(runningStart.Time) || two.AssignVersion != 1 {
		t.Errorf("route 2: %s, driver %q, start %v (was %v), assign version %d; a running route is left alone", two.Status, two.DriverID, two.StartAt, runningStart, two.AssignVersion)
	}
	if a.Plan.Status != contracts.PlanStatusRunning {
		t.Errorf("plan %s, want berjalan (route 2 is running)", a.Plan.Status)
	}

	unassigned := testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)
	if len(unassigned) != 1 || unassigned[0].Key != "pln-1:r1" || unassigned[0].AggregateVersion != 2 {
		t.Fatalf("route.unassigned = %+v, want one for route 1 at its new assign version", unassigned)
	}
	if ev, _ := unassigned[0].Payload[contracts.RouteUnassigned](); ev.Reason != contracts.UnassignRescheduled || ev.DriverID != "drv-1" || ev.PlanID != "pln-1" {
		t.Errorf("route.unassigned = %+v, want reason rescheduled and the driver who lost the route", ev)
	}
	if last := a.Plan.Events[len(a.Plan.Events)-1].Text; last.Key != "srv.api.event.rescheduled" || last.Params["time"] != float64(newStart.UnixMilli()) {
		t.Errorf("timeline ends with %+v, want the new schedule", last)
	}

	// the same event again, and an older one: dropped by version
	r.deliver(t, r.projection.Rescheduled, moved)
	r.deliver(t, r.projection.Rescheduled, event(t, contracts.TypePlanRescheduled, "pln-1", 1, rescheduledTo(t, plan, newStart.Add(time.Hour))))
	after := r.load(t, "pln-1")
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)); n != 1 || !after.Routes[0].StartAt.Equal(newStart) || after.Routes[0].AssignVersion != 2 {
		t.Errorf("after a repeat and a late event: %d route.unassigned, route 1 start %v at version %d; want nothing changed", n, after.Routes[0].StartAt, after.Routes[0].AssignVersion)
	}
}

func TestRescheduledToNowAndOfARouteWaitingForADriver(t *testing.T) {
	r := newRig(t)
	plan := twoRoutePlan()
	r.confirm(t, plan)
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")

	ev := contracts.PlanRescheduled{PlanID: "pln-1", Schedule: contracts.Schedule{Mode: contracts.ScheduleModeNow}}
	start := time.Now().Add(6 * time.Minute).Truncate(time.Millisecond)
	for _, route := range plan.Routes {
		route.StartAt = contracts.At(start)
		ev.Routes = append(ev.Routes, route)
	}
	r.deliver(t, r.projection.Rescheduled, event(t, contracts.TypePlanRescheduled, "pln-1", 2, ev))

	a := r.load(t, "pln-1")
	if a.Plan.Schedule.Mode != contracts.ScheduleModeNow || !a.Routes[1].StartAt.Equal(start) || a.Routes[1].AssignVersion != 0 {
		t.Errorf("schedule %s, route 2 start %v at version %d; want the waiting route re-timed and not counted as released", a.Plan.Schedule.Mode, a.Routes[1].StartAt, a.Routes[1].AssignVersion)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)); n != 1 {
		t.Errorf("%d route.unassigned, want one: only the assigned route had a driver to lose", n)
	}
	if last := a.Plan.Events[len(a.Plan.Events)-1].Text; last.Key != "srv.api.event.rescheduledNow" {
		t.Errorf("timeline ends with %+v, want the move to now", last)
	}
}

func TestRescheduledTellsTheAdminsOnlyWhenAutomaticDispatchIsOff(t *testing.T) {
	r := newRig(t)
	r.customer(t)
	plan := twoRoutePlan()
	r.confirm(t, plan)
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")

	r.deliver(t, r.projection.Rescheduled, event(t, contracts.TypePlanRescheduled, "pln-1", 2, rescheduledTo(t, plan, time.Now().Add(2*time.Hour))))
	if got := r.notes(t, "srv.api.notif.rescheduled.title"); len(got) != 0 {
		t.Fatalf("%d notices with automatic dispatch on: the worker assigns again by itself", len(got))
	}

	r.setDispatchAuto(t, false)
	moved := event(t, contracts.TypePlanRescheduled, "pln-1", 3, rescheduledTo(t, plan, time.Now().Add(3*time.Hour)))
	r.deliver(t, r.projection.Rescheduled, moved)
	notes := r.notes(t, "srv.api.notif.rescheduled.title")
	if len(notes) != 1 || notes[0].Title.Params["code"] != "PL-pln-1" || notes[0].Body.Params["name"] != "Dina Pramesti" || notes[0].Body.Params["count"] != float64(2) {
		t.Fatalf("notices = %+v, want one naming the plan, the customer and the two routes that need a driver", notes)
	}
	r.deliver(t, r.projection.Rescheduled, moved)
	if got := r.notes(t, "srv.api.notif.rescheduled.title"); len(got) != 1 {
		t.Errorf("%d notices after a redelivery, want one", len(got))
	}
}

func TestACancelledPlanLeavesTheQueueAndShowsUnderItsStatus(t *testing.T) {
	r := newRig(t)
	other := testkit.Plan("pln-2", testkit.Route("pln-2", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, t0))
	r.confirm(t, twoRoutePlan())
	r.confirm(t, other)
	r.deliver(t, r.projection.CancelCommitted, event(t, contracts.TypePlanCancelled, "pln-1", 2, contracts.PlanCancelled{PlanID: "pln-1", Reason: "x"}))

	ids := func(f Filter) []string {
		t.Helper()
		list, err := r.service.List(t.Context(), f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range list {
			out = append(out, s.ID)
		}
		return out
	}
	if got := ids(Filter{NeedsDriver: true}); !slices.Equal(got, []string{"pln-2"}) {
		t.Errorf("queue = %v, want only the plan that still needs a driver", got)
	}
	if got := ids(Filter{Status: []contracts.PlanStatus{contracts.PlanStatusCancelled}}); !slices.Equal(got, []string{"pln-1"}) {
		t.Errorf("cancelled plans = %v, want pln-1", got)
	}
}

func TestCancelEventsThatCanNeverBeAppliedAreDeadLettered(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	if _, err := r.service.Cancel(t.Context(), "pln-1", "", "Rani", platform.LocaleEN); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		h    eventbus.Handler
		typ  string
		data any
	}{
		"an abort that names no plan":     {r.projection.CancelAborted, contracts.TypePlanCancelAborted, contracts.PlanCancelAborted{RequestID: "req-1"}},
		"an abort that names no request":  {r.projection.CancelAborted, contracts.TypePlanCancelAborted, contracts.PlanCancelAborted{PlanID: "pln-1"}},
		"a refusal that names no plan":    {r.projection.CancelRejected, contracts.TypePlanCancelRejected, contracts.PlanCancelRejected{}},
		"a commit that names no plan":     {r.projection.CancelCommitted, contracts.TypePlanCancelled, contracts.PlanCancelled{}},
		"a reschedule that names no plan": {r.projection.Rescheduled, contracts.TypePlanRescheduled, contracts.PlanRescheduled{}},
		"an undecodable payload":          {r.projection.CancelCommitted, contracts.TypePlanCancelled, struct{ PlanID int }{1}},
	} {
		t.Run(name, func(t *testing.T) {
			err := testkit.Apply(r.pool, tc.h, tc.typ, "pln-1", 9, tc.data)
			if err == nil || !isPermanent(err) {
				t.Errorf("error = %v, want a permanent one", err)
			}
		})
	}
	// the pending cancel of the admin is not touched by any of them
	if a := r.load(t, "pln-1"); a.Plan.PendingAction != "cancel" {
		t.Errorf("pending %q, want the admin's cancel still pending", a.Plan.PendingAction)
	}
}
