package dispatch

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

func (r *rig) cancelRequested(t *testing.T, planID, requestID string, version int64) {
	t.Helper()
	r.apply(t, r.projection.CancelRequested, contracts.TypePlanCancelRequested, planID, version,
		contracts.PlanCancelRequested{PlanID: planID, RequestID: requestID, RequestedBy: contracts.CancelByCustomer})
}

func (r *rig) cancelAborted(t *testing.T, planID, requestID string, version int64) {
	t.Helper()
	r.apply(t, r.projection.CancelAborted, contracts.TypePlanCancelAborted, planID, version,
		contracts.PlanCancelAborted{PlanID: planID, RequestID: requestID, Reason: contracts.Msg{Key: "x"}})
}

func TestAFrozenRouteIsNeverAssignedByTheWorkerOrByHand(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5), nowRoute("pln-1", 2, contracts.VehicleTypeMotor, 5))
	d1 := r.driver(t, contracts.VehicleTypeMotor, 1)
	r.driver(t, contracts.VehicleTypeMotor, 2)
	r.cancelRequested(t, "pln-1", "req-1", 2)

	// the worker: neither a round nor a direct attempt on a frozen route assigns anything
	r.svc.pass(t.Context())
	for _, id := range []string{"pln-1:r1", "pln-1:r2"} {
		assigned, err := r.svc.AutoAssignRoute(t.Context(), plans.Waiting{PlanID: "pln-1", RouteID: id})
		if err != nil || assigned {
			t.Errorf("AutoAssignRoute(%s) = %v, %v; want it to leave the frozen route alone", id, assigned, err)
		}
	}
	// the admin: assign and autoAssign are refused with a message of their own, suggest lists nobody
	_, err := r.svc.Assign(t.Context(), "pln-1", "pln-1:r1", d1)
	if got := conflictKey(t, err); got != MsgRouteFrozen {
		t.Errorf("assign: %s, want %s", got, MsgRouteFrozen)
	}
	_, err = r.svc.AutoAssignPlan(t.Context(), "pln-1")
	if got := conflictKey(t, err); got != MsgRouteFrozen {
		t.Errorf("autoAssign: %s, want %s", got, MsgRouteFrozen)
	}
	if got, err := r.svc.Suggest(t.Context(), "pln-1", "pln-1:r1"); err != nil || len(got) != 0 {
		t.Errorf("suggest = %+v, %v; want nobody to be suggested for a frozen route", got, err)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)); n != 0 {
		t.Fatalf("%d route.assigned events while the plan was frozen", n)
	}

	// aborted: the freeze ends and the same worker picks both routes up
	r.cancelAborted(t, "pln-1", "req-1", 3)
	r.svc.pass(t.Context())
	for _, route := range r.plan(t, "pln-1").Routes {
		if route.DriverID == "" {
			t.Errorf("route %s still waits after the abort, want the worker to have assigned it", route.ID)
		}
	}
	if got, err := r.svc.Suggest(t.Context(), "pln-1", "pln-1:r1"); err != nil {
		t.Errorf("suggest after the abort: %v", err)
	} else if got == nil {
		t.Error("suggest answered null, want a list")
	}
}

func TestAPlanWithACancelPendingIsLeftAloneBeforeTheRouteIsFrozen(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	d := r.driver(t, contracts.VehicleTypeMotor, 1)
	// an admin asked to cancel; console-api has not answered with plan.cancel.requested yet
	err := platform.WithTx(t.Context(), r.pool, func(tx pgx.Tx) error {
		a, err := plans.Lock(t.Context(), tx, "pln-1")
		if err != nil {
			return err
		}
		a.Plan.PendingAction = plans.PendingCancel
		return plans.Save(t.Context(), tx, a)
	})
	if err != nil {
		t.Fatal(err)
	}

	r.svc.pass(t.Context())
	if got := r.route(t, "pln-1").DriverID; got != "" {
		t.Fatalf("worker gave a plan with a cancel pending to %q", got)
	}
	_, err = r.svc.Assign(t.Context(), "pln-1", "pln-1:r1", d)
	if got := conflictKey(t, err); got != MsgRouteFrozen {
		t.Errorf("assign: %s, want %s", got, MsgRouteFrozen)
	}
}

func TestAnAssignedRouteOfAFrozenPlanCannotBeTakenBack(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	d := r.driver(t, contracts.VehicleTypeMotor, 1)
	r.assignTo(t, "pln-1", d)
	r.cancelRequested(t, "pln-1", "req-1", 2)

	_, err := r.svc.Unassign(t.Context(), "pln-1", "pln-1:r1")
	if got := conflictKey(t, err); got != MsgRouteFrozen {
		t.Errorf("unassign: %s, want %s", got, MsgRouteFrozen)
	}
	if got := r.route(t, "pln-1"); got.DriverID != d {
		t.Errorf("driver %q after the refused unassign, want him kept", got.DriverID)
	}

	r.cancelAborted(t, "pln-1", "req-1", 3)
	if _, err := r.svc.Unassign(t.Context(), "pln-1", "pln-1:r1"); err != nil {
		t.Errorf("unassign after the abort: %v", err)
	}
	if got := unassignedEvents(t, r); len(got) != 1 || got[0].Reason != contracts.UnassignAdmin {
		t.Errorf("route.unassigned = %+v, want one with reason admin", got)
	}
}

func TestCommittingACancelFreesTheDriverForOtherWork(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	r.confirm(t, "pln-2", nowRoute("pln-2", 1, contracts.VehicleTypeMotor, 5))
	only := r.driver(t, contracts.VehicleTypeMotor, 1)
	r.assignTo(t, "pln-1", only)
	if got := r.driverStatus(t, only); got != "bertugas" {
		t.Fatalf("driver is %s, want bertugas with a route about to start", got)
	}
	if _, err := r.svc.Assign(t.Context(), "pln-2", "pln-2:r1", only); err == nil {
		t.Fatal("the busy driver was given a second overlapping route")
	}

	r.cancelRequested(t, "pln-1", "req-1", 2)
	r.apply(t, r.projection.CancelCommitted, contracts.TypePlanCancelled, "pln-1", 3,
		contracts.PlanCancelled{PlanID: "pln-1", RequestID: "req-1", RequestedBy: contracts.CancelByCustomer})

	if got := r.driverStatus(t, only); got != "online" {
		t.Errorf("driver is %s after the cancel, want online: free again", got)
	}
	r.svc.pass(t.Context())
	if got := r.route(t, "pln-2").DriverID; got != only {
		t.Errorf("driver of the other plan: %q, want the freed driver %q", got, only)
	}
	if got := r.route(t, "pln-1"); got.DriverID != "" || r.plan(t, "pln-1").Status != contracts.PlanStatusCancelled {
		t.Errorf("cancelled plan: route driver %q, status %s", got.DriverID, r.plan(t, "pln-1").Status)
	}
	if n := len(unassignedEvents(t, r)); n != 0 {
		t.Errorf("%d route.unassigned events, want none for the cancelled plan", n)
	}
}

func TestTheFirstFailedSearchTellsTheAdminsOncePerPlan(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	r.offlineDriver(t, contracts.VehicleTypeMotor)
	w := plans.Waiting{PlanID: "pln-1", RouteID: "pln-1:r1"}

	for range 3 {
		if _, err := r.svc.AutoAssignRoute(t.Context(), w); err != nil {
			t.Fatal(err)
		}
	}
	notes := notices(t, r, "srv.api.notif.newPlan.title")
	if len(notes) != 1 || notes[0].PlanID != "pln-1" || notes[0].Title.Params["code"] != "PL-pln-1" ||
		notes[0].Body.Params["name"] != "Dina Pramesti" || notes[0].Body.Params["count"] != float64(1) {
		t.Fatalf("notices = %+v, want one: the plan waits for a driver nobody could be found for", notes)
	}

	// a plan that is assigned at once is no news
	r.confirm(t, "pln-2", nowRoute("pln-2", 1, contracts.VehicleTypeMotor, 5))
	r.driver(t, contracts.VehicleTypeMotor, 1)
	if assigned, err := r.svc.AutoAssignRoute(t.Context(), plans.Waiting{PlanID: "pln-2", RouteID: "pln-2:r1"}); err != nil || !assigned {
		t.Fatalf("assigned = %v, %v", assigned, err)
	}
	if got := notices(t, r, "srv.api.notif.newPlan.title"); len(got) != 1 {
		t.Errorf("%d notices after a plan that found its driver, want still one", len(got))
	}
}

func TestADriverWhoGoesOfflineDuringACancelStillReleasesHisRoute(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	gone := r.driver(t, contracts.VehicleTypeMotor, 1)
	r.assignTo(t, "pln-1", gone)
	r.cancelRequested(t, "pln-1", "req-1", 2)

	r.offline(t, gone, 2)
	if got := r.route(t, "pln-1"); got.DriverID != "" || got.Status != contracts.RouteStatusAwaitingDriver {
		t.Fatalf("route %s with %q, want it released: its driver is gone whatever happens to the plan", got.Status, got.DriverID)
	}
	a, err := plans.Load(t.Context(), r.pool, "pln-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Held() != "req-1" {
		t.Errorf("hold %q after the release, want the route still frozen for the cancel", a.Held())
	}
	standby := r.driver(t, contracts.VehicleTypeMotor, 2)
	r.svc.pass(t.Context())
	if got := r.route(t, "pln-1").DriverID; got != "" {
		t.Fatalf("worker gave a frozen route to %q", got)
	}

	// the cancel falls through: the route is the worker's again
	r.cancelAborted(t, "pln-1", "req-1", 3)
	r.svc.pass(t.Context())
	if got := r.route(t, "pln-1").DriverID; got != standby {
		t.Errorf("driver %q after the abort, want %q", got, standby)
	}
}
