package plans

import (
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// scheduledFor is the schedule "leave at this time".
func scheduledFor(at time.Time) contracts.Schedule {
	return contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{From: contracts.At(at), To: contracts.At(at.Add(time.Hour))}}
}

func TestReschedulingRetimesTheRoutesAndTellsEveryoneElse(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	w.addRoute(p.ID)
	before := w.stored(p.ID)
	published := len(testkit.OutboxTypes(t, w.pool))

	w.clock.advance(10 * time.Minute)
	start := w.clock.at.Add(3 * time.Hour)
	got, err := w.svc.Reschedule(t.Context(), w.customer, p.ID, scheduledFor(start))
	if err != nil {
		t.Fatal(err)
	}
	if got.Schedule.Mode != contracts.ScheduleModeScheduled || got.Schedule.Window == nil || !got.Schedule.Window.From.Equal(start) {
		t.Errorf("schedule = %+v", got.Schedule)
	}
	for _, r := range got.Routes {
		if !r.StartAt.Equal(start) {
			t.Errorf("route %s starts at %v, want the window's start %v", r.ID, r.StartAt, start)
		}
		first := r.Stops[0]
		if !first.ArriveAt.Equal(start.Add(time.Duration(first.EtaMin*60_000) * time.Millisecond)) {
			t.Errorf("route %s: the first stop is due %v with eta %v min: the ETAs were not recomputed from the new start", r.ID, first.ArriveAt, first.EtaMin)
		}
		if r.CostRp != before.Plan.Routes[0].CostRp || r.Status != contracts.RouteStatusAwaitingDriver {
			t.Errorf("route %s: the price or status changed: %d %s", r.ID, r.CostRp, r.Status)
		}
	}
	if got.Status != contracts.PlanStatusAwaitingDriver || got.TotalCostRp != before.Plan.TotalCostRp {
		t.Errorf("plan = %s %d: rescheduling is not a new purchase", got.Status, got.TotalCostRp)
	}
	last := got.Events[len(got.Events)-1].Text
	if last.Key != "srv.api.event.rescheduled" || last.Params["time"] != start.UnixMilli() {
		t.Errorf("timeline ends with %+v", last)
	}

	rec := w.stored(p.ID)
	if rec.Version != before.Version+1 || !rec.Plan.Schedule.Window.From.Equal(start) {
		t.Errorf("stored version %d (was %d), schedule %+v", rec.Version, before.Version, rec.Plan.Schedule)
	}
	if after := testkit.OutboxTypes(t, w.pool); !slices.Equal(after[published:], []string{contracts.TypePlanRescheduled}) {
		t.Fatalf("published %v", after[published:])
	}
	env := testkit.Outbox(t, w.pool, contracts.TypePlanRescheduled)[0]
	ev, err := env.Payload[contracts.PlanRescheduled]()
	if err != nil {
		t.Fatal(err)
	}
	if env.Key != p.ID || env.AggregateVersion != rec.Version || ev.PlanID != p.ID || len(ev.Routes) != 2 || !ev.Schedule.Window.From.Equal(start) {
		t.Errorf("plan.rescheduled = key %s v%d %+v", env.Key, env.AggregateVersion, ev)
	}
	for i, r := range ev.Routes {
		if !r.StartAt.Equal(start) || r.ID != rec.Plan.Routes[i].ID {
			t.Errorf("the event's route %s starts at %v: it must carry the re-timed routes", r.ID, r.StartAt)
		}
	}
}

func TestReschedulingToNowLeavesInSixMinutes(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	w.clock.advance(time.Hour)

	got, err := w.svc.Reschedule(t.Context(), w.customer, p.ID, contracts.Schedule{Mode: contracts.ScheduleModeNow})
	if err != nil {
		t.Fatal(err)
	}
	if want := w.clock.at.Add(6 * time.Minute); !got.Routes[0].StartAt.Equal(want) || got.Schedule.Mode != contracts.ScheduleModeNow {
		t.Errorf("route starts %v, want now + 6 min = %v; schedule %+v", got.Routes[0].StartAt, want, got.Schedule)
	}
	if last := got.Events[len(got.Events)-1].Text; last.Key != "srv.api.event.rescheduledNow" {
		t.Errorf("timeline ends with %+v", last)
	}
}

func TestAScheduledTimeMustBeAtLeastTwentyMinutesAway(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	before := w.stored(p.ID)
	published := len(testkit.OutboxTypes(t, w.pool))

	for name, schedule := range map[string]contracts.Schedule{
		"in the past":       scheduledFor(w.clock.at.Add(-time.Hour)),
		"in ten minutes":    scheduledFor(w.clock.at.Add(10 * time.Minute)),
		"just short":        scheduledFor(w.clock.at.Add(20*time.Minute - time.Second)),
		"without a window":  {Mode: contracts.ScheduleModeScheduled},
		"with an empty one": {Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{}},
	} {
		_, err := w.svc.Reschedule(t.Context(), w.customer, p.ID, schedule)
		if got := wantError(t, err, platform.CodeInvalid, "srv.api.scheduleTooSoon"); got.Field != "schedule" {
			t.Errorf("%s: field %q, want schedule", name, got.Field)
		}
	}
	_, err := w.svc.Reschedule(t.Context(), w.customer, p.ID, contracts.Schedule{Mode: "someday"})
	if got := wantError(t, err, platform.CodeInvalid, "srv.rpc.badRequest"); got.Field != "schedule" {
		t.Errorf("unknown mode: field %q", got.Field)
	}
	if after := w.stored(p.ID); after.Version != before.Version || len(after.Plan.Events) != len(before.Plan.Events) || len(testkit.OutboxTypes(t, w.pool)) != published {
		t.Error("a refused reschedule changed the plan or published something")
	}

	// Exactly twenty minutes is allowed.
	if _, err := w.svc.Reschedule(t.Context(), w.customer, p.ID, scheduledFor(w.clock.at.Add(20*time.Minute))); err != nil {
		t.Errorf("twenty minutes ahead: %v", err)
	}
}

func TestOnlyAPlanWaitingForItsDriverCanBeRescheduled(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	far := scheduledFor(w.clock.at.Add(3 * time.Hour))

	for _, status := range []contracts.PlanStatus{contracts.PlanStatusRunning, contracts.PlanStatusDone, contracts.PlanStatusCancelled} {
		p := w.confirmed()
		w.setStatus(p.ID, status)
		_, err := w.svc.Reschedule(ctx, w.customer, p.ID, far)
		wantError(t, err, platform.CodeConflict, "srv.api.rescheduleLate")
	}
	for _, id := range []string{w.draft("s1").ID, w.ready("s1", "s2").ID} { // a plan not yet confirmed has no driver to wait for
		_, err := w.svc.Reschedule(ctx, w.customer, id, far)
		wantError(t, err, platform.CodeConflict, "srv.api.rescheduleLate")
	}

	// An assigned plan may be moved; one that is being cancelled may not.
	assigned := w.confirmed()
	w.setStatus(assigned.ID, contracts.PlanStatusAssigned)
	if _, err := w.svc.Reschedule(ctx, w.customer, assigned.ID, far); err != nil {
		t.Errorf("an assigned plan: %v", err)
	}
	pending := w.confirmed()
	if _, err := w.svc.Cancel(ctx, w.customer, pending.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.Reschedule(ctx, w.customer, pending.ID, far)
	wantError(t, err, platform.CodeConflict, "srv.api.cancelPending")

	stranger := testkit.Customer(t, w.pool, "Raka")
	_, err = w.svc.Reschedule(ctx, stranger, assigned.ID, far)
	wantError(t, err, platform.CodeDenied, "srv.api.planNoAccess")
	_, err = w.svc.Reschedule(ctx, w.customer, "pln-nope", far)
	wantError(t, err, platform.CodeNotFound, "srv.ops.err.planNotFound")
}

func TestReschedulingLeavesRoutesThatAreFinishedAlone(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	w.addRoute(p.ID)
	// The first route is done, the second still waits: the plan as a whole is waiting for its driver.
	err := platform.WithTx(t.Context(), w.pool, func(tx pgx.Tx) error {
		rec, err := Lock(t.Context(), tx, p.ID)
		if err != nil {
			return err
		}
		rec.Plan.Routes[0].Status = contracts.RouteStatusDone
		return Save(t.Context(), tx, rec)
	})
	if err != nil {
		t.Fatal(err)
	}
	doneBefore := w.stored(p.ID).Plan.Routes[0]

	start := w.clock.at.Add(5 * time.Hour)
	got, err := w.svc.Reschedule(t.Context(), w.customer, p.ID, scheduledFor(start))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Routes[0].StartAt.Equal(doneBefore.StartAt.Time) || got.Routes[0].Status != contracts.RouteStatusDone {
		t.Errorf("the finished route was re-timed: %v", got.Routes[0].StartAt)
	}
	if !got.Routes[1].StartAt.Equal(start) {
		t.Errorf("the waiting route starts %v, want %v", got.Routes[1].StartAt, start)
	}
}
