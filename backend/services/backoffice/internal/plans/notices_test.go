package plans

import (
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

func TestPlanConfirmedTellsTheAdminsOnlyWhenNobodyAssignsAutomatically(t *testing.T) {
	r := newRig(t)
	r.customer(t)

	// With automatic dispatch on, the worker tells them if it finds nobody (see dispatch); the projection stays quiet.
	r.confirm(t, twoRoutePlan())
	if got := r.notes(t, "srv.api.notif.newPlan.title"); len(got) != 0 {
		t.Fatalf("%d notices with automatic dispatch on", len(got))
	}

	r.setDispatchAuto(t, false)
	plan := testkit.Plan("pln-2", testkit.Route("pln-2", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, t0))
	env := event(t, contracts.TypePlanConfirmed, "pln-2", 1, contracts.PlanConfirmed{Plan: plan})
	r.deliver(t, r.projection.PlanConfirmed, env)
	notes := r.notes(t, "srv.api.notif.newPlan.title")
	if len(notes) != 1 || notes[0].PlanID != "pln-2" || notes[0].Title.Params["code"] != "PL-pln-2" ||
		notes[0].Body.Params["name"] != "Dina Pramesti" || notes[0].Body.Params["count"] != float64(1) {
		t.Fatalf("notices = %+v, want one that names the plan, the customer and the number of routes", notes)
	}

	// the confirmation again: the plan is known, nothing to tell
	r.deliver(t, r.projection.PlanConfirmed, env)
	r.deliver(t, r.projection.PlanConfirmed, event(t, contracts.TypePlanConfirmed, "pln-2", 1, contracts.PlanConfirmed{Plan: plan}))
	if got := r.notes(t, "srv.api.notif.newPlan.title"); len(got) != 1 {
		t.Errorf("%d notices after repeated confirmations, want one", len(got))
	}
}

func TestPlanVersionsFollowConsoleApisEvents(t *testing.T) {
	r := newRig(t)
	plan := twoRoutePlan()
	r.deliver(t, r.projection.PlanConfirmed, event(t, contracts.TypePlanConfirmed, "pln-1", 5, contracts.PlanConfirmed{Plan: plan}))
	if got := r.load(t, "pln-1").Version; got != 5 {
		t.Fatalf("plan version %d after plan.confirmed at 5", got)
	}
	r.deliver(t, r.projection.CancelRequested, event(t, contracts.TypePlanCancelRequested, "pln-1", 6, contracts.PlanCancelRequested{PlanID: "pln-1", RequestID: "req-1"}))
	r.deliver(t, r.projection.CancelAborted, event(t, contracts.TypePlanCancelAborted, "pln-1", 7, contracts.PlanCancelAborted{PlanID: "pln-1", RequestID: "req-1"}))
	if got := r.load(t, "pln-1").Version; got != 7 {
		t.Errorf("plan version %d after the cancel attempt, want 7", got)
	}
	// a reschedule that is not newer than what we have is a duplicate or a late arrival
	r.deliver(t, r.projection.Rescheduled, event(t, contracts.TypePlanRescheduled, "pln-1", 7, rescheduledTo(t, plan, time.Now().Add(time.Hour))))
	if got := r.load(t, "pln-1"); got.Plan.Schedule.Mode != contracts.ScheduleModeNow || got.Version != 7 {
		t.Errorf("schedule %s version %d, want the old schedule kept", got.Plan.Schedule.Mode, got.Version)
	}
}

func TestAStopIssueTellsTheAdmins(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	if _, err := r.pool.Exec(t.Context(), `INSERT INTO drivers (id, name, phone, joined_at) VALUES ('drv-1', 'Budi Santoso', '0811', now())`); err != nil {
		t.Fatal(err)
	}
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")
	r.deliver(t, r.projection.RouteStarted, event(t, contracts.TypeRouteStarted, "pln-1:r1", 1,
		contracts.RouteStarted{PlanID: "pln-1", RouteID: "pln-1:r1", DriverID: "drv-1", StartedAt: contracts.Now()}))

	issue := contracts.Msg{Key: "dom.issue.tidak_ada_orang"}
	problem := event(t, contracts.TypeStopIssue, "pln-1:r1", 2, contracts.StopIssue{
		PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s1", Kind: contracts.IssueKindNobodyThere, Issue: issue, ReportedAt: contracts.Now(),
	})
	r.deliver(t, r.projection.StopIssue, problem)

	notes := r.notes(t, "srv.ops.ntf.problemAdminTitle")
	if len(notes) != 1 || notes[0].PlanID != "pln-1" || notes[0].Title.Params["code"] != "PL-pln-1" {
		t.Fatalf("notices = %+v, want one that names the plan", notes)
	}
	body := notes[0].Body
	if body.Key != "srv.ops.ntf.problemAdminBody" || body.Params["driver"] != "Budi Santoso" || body.Params["place"] != "Pickup pln-1:r1" {
		t.Errorf("body = %+v, want the driver's name and the place", body)
	}
	if got, _ := body.Params["issue"].(map[string]any); got["key"] != issue.Key {
		t.Errorf("issue = %v, want the message driver-api built", body.Params["issue"])
	}

	// the same event again: dropped by version, told once
	r.deliver(t, r.projection.StopIssue, problem)
	if got := r.notes(t, "srv.ops.ntf.problemAdminTitle"); len(got) != 1 {
		t.Errorf("%d notices after a duplicate, want one", len(got))
	}
}

func TestADelayTellsTheAdminsEachTimeItGrows(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	if _, err := r.pool.Exec(t.Context(), `INSERT INTO drivers (id, name, phone, joined_at) VALUES ('drv-1', 'Budi Santoso', '0811', now())`); err != nil {
		t.Fatal(err)
	}
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")
	r.assign(t, "pln-1", "pln-1:r2", "drv-ghost") // a driver whose master data has not arrived

	delayed := func(route string, version int64, minutes float64) contracts.Envelope {
		env := event(t, contracts.TypeRouteDelayed, route, version, contracts.RouteDelayed{PlanID: "pln-1", RouteID: route, DelayMin: minutes})
		r.deliver(t, r.projection.RouteDelayed, env)
		return env
	}
	first := delayed("pln-1:r1", 1, 5.2)
	delayed("pln-1:r1", 2, 10.6)
	delayed("pln-1:r2", 1, 6)
	first2 := first
	r.deliver(t, r.projection.RouteDelayed, first2) // a duplicate

	notes := r.notes(t, "srv.api.notif.delayAdmin.title")
	if len(notes) != 3 {
		t.Fatalf("%d notices, want one per applied event (the duplicate told nothing)", len(notes))
	}
	byCount := map[float64]contracts.AppNotification{}
	for _, n := range notes {
		if n.PlanID != "pln-1" || n.Title.Params["code"] != "PL-pln-1" {
			t.Errorf("notice %+v does not name the plan", n)
		}
		if c, ok := n.Body.Params["count"].(float64); ok && n.Body.Params["name"] == "Budi Santoso" {
			byCount[c] = n
		}
	}
	if _, ok := byCount[5]; !ok {
		t.Errorf("no notice for 5 minutes (5.2 rounded) by Budi: %+v", notes)
	}
	if _, ok := byCount[11]; !ok {
		t.Errorf("no notice for 11 minutes (10.6 rounded) by Budi: %+v", notes)
	}
	var ghost map[string]any
	for _, n := range notes {
		if m, ok := n.Body.Params["name"].(map[string]any); ok {
			ghost = m
		}
	}
	if ghost["key"] != "srv.api.driverFallback" {
		t.Errorf("a driver nobody knows is named %v, want the catalog's word for driver", ghost)
	}
	if got := r.load(t, "pln-1").Routes[0].DelayMin; got != 10.6 {
		t.Errorf("route delay %v, want the latest 10.6", got)
	}
}

func TestCustomersCarryTheirContactDataAndNewOnesAreNews(t *testing.T) {
	r := newRig(t)
	created := time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC)
	registered := event(t, contracts.TypeCustomerRegistered, "cus-1", 1, contracts.CustomerRegistered{
		CustomerID: "cus-1", Name: "Dina Pramesti", Email: "dina@contoh.id", Phone: "081234567890", CreatedAt: contracts.At(created),
	})
	r.deliver(t, r.projection.CustomerRegistered, registered)
	row := func() (name, email, phone string, at time.Time) {
		t.Helper()
		err := r.pool.QueryRow(t.Context(), `SELECT name, email, phone, created_at FROM customers WHERE id = 'cus-1'`).Scan(&name, &email, &phone, &at)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if name, email, phone, at := row(); name != "Dina Pramesti" || email != "dina@contoh.id" || phone != "081234567890" || !at.Equal(created) {
		t.Fatalf("customer = %q %q %q %v", name, email, phone, at)
	}
	notes := r.notes(t, "srv.api.notif.newUser.title")
	if len(notes) != 1 || notes[0].Body.Params["name"] != "Dina Pramesti" {
		t.Fatalf("notices = %+v, want one for the sign-up", notes)
	}

	r.deliver(t, r.projection.CustomerUpdated, event(t, contracts.TypeCustomerUpdated, "cus-1", 3, contracts.CustomerUpdated{
		CustomerID: "cus-1", Name: "Dina P.", Email: "dina.p@contoh.id", Phone: "081200000000",
	}))
	r.deliver(t, r.projection.CustomerUpdated, event(t, contracts.TypeCustomerUpdated, "cus-1", 2, contracts.CustomerUpdated{
		CustomerID: "cus-1", Name: "Old", Email: "old@contoh.id", Phone: "0800",
	})) // late
	r.deliver(t, r.projection.CustomerRegistered, registered) // replayed
	if name, email, phone, at := row(); name != "Dina P." || email != "dina.p@contoh.id" || phone != "081200000000" || !at.Equal(created) {
		t.Errorf("customer = %q %q %q %v, want the newest version's contact data and the original sign-up time", name, email, phone, at)
	}
	if got := r.notes(t, "srv.api.notif.newUser.title"); len(got) != 1 {
		t.Errorf("%d sign-up notices after a replay and an update, want one", len(got))
	}
}
