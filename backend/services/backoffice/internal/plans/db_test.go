package plans

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

type rig struct {
	pool       *pgxpool.Pool
	projection *Projection
	service    *Service
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testkit.Pool(t)
	feed, pub := changes.New(platform.NewHub(), time.Millisecond), testkit.Publisher(pool)
	return &rig{pool: pool, projection: NewProjection(feed, pub), service: NewService(pool, pub, feed, time.Now)}
}

func (r *rig) confirm(t *testing.T, plan contracts.Plan) {
	t.Helper()
	if err := testkit.Apply(r.pool, r.projection.PlanConfirmed, contracts.TypePlanConfirmed, plan.ID, 1, contracts.PlanConfirmed{Plan: plan}); err != nil {
		t.Fatal(err)
	}
}

// assign gives the plan's route a driver the way dispatch does, without dispatch.
func (r *rig) assign(t *testing.T, planID, routeID, driverID string) {
	t.Helper()
	err := platform.WithTx(t.Context(), r.pool, func(tx pgx.Tx) error {
		a, err := Lock(t.Context(), tx, planID)
		if err != nil {
			return err
		}
		route, err := a.Route(routeID)
		if err != nil {
			return err
		}
		route.Route = routeplan.Assign(route.Route, driverID, "veh-"+driverID, time.Now())
		route.AssignVersion++
		a.Sync(time.Now())
		return Save(t.Context(), tx, a)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (r *rig) load(t *testing.T, planID string) *Aggregate {
	t.Helper()
	a, err := Load(t.Context(), r.pool, planID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func isPermanent(err error) bool { return fmt.Sprintf("%T", err) == "eventbus.permanentError" }

func TestPlanConfirmedProjectsThePlanAndItsRoutes(t *testing.T) {
	r := newRig(t)
	plan := twoRoutePlan()
	r.confirm(t, plan)

	got, err := r.service.Get(t.Context(), "pln-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != plan.Code || got.Status != contracts.PlanStatusAwaitingDriver || len(got.Routes) != 2 || len(got.Shipments) != 2 {
		t.Fatalf("plan = %s %s, %d routes, %d shipments", got.Code, got.Status, len(got.Routes), len(got.Shipments))
	}
	if got.Routes[0].ID != "pln-1:r1" || got.Routes[1].ID != "pln-1:r2" || got.Routes[0].Stops[1].Waypoint.Name != "Dropoff pln-1:r1" {
		t.Errorf("routes = %+v, want them in order with their stops", got.Routes)
	}
	if got.Events == nil {
		t.Error("events is null, want an array")
	}

	if _, err := r.service.Get(t.Context(), "pln-404"); !isStatus(err, platform.CodeNotFound) {
		t.Errorf("unknown plan: %v, want not found", err)
	}
}

func isStatus(err error, code platform.ErrorCode) bool {
	apiErr, ok := errors.AsType[*platform.APIError](err)
	return ok && apiErr.Code == code
}

func TestARepeatedPlanConfirmedDoesNotUndoAnAssignment(t *testing.T) {
	r := newRig(t)
	plan := twoRoutePlan()
	r.confirm(t, plan)
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")

	// the same confirmation again, in a new event: the plan is already known
	r.confirm(t, plan)

	a := r.load(t, "pln-1")
	if a.Routes[0].DriverID != "drv-1" || a.Routes[0].AssignVersion != 1 {
		t.Errorf("route 1 driver %q at version %d after a repeated plan.confirmed, want the assignment kept", a.Routes[0].DriverID, a.Routes[0].AssignVersion)
	}
	var plans int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM plans`).Scan(&plans); err != nil || plans != 1 {
		t.Errorf("%d plans, %v; want one", plans, err)
	}
}

func TestPlanConfirmedRefusesSnapshotsDispatchCannotUse(t *testing.T) {
	mutate := map[string]func(*contracts.Plan){
		"a draft":                 func(p *contracts.Plan) { p.Status = contracts.PlanStatusDraft },
		"options only":            func(p *contracts.Plan) { p.Status = contracts.PlanStatusOptionsReady },
		"no routes":               func(p *contracts.Plan) { p.Routes = nil },
		"a route of another plan": func(p *contracts.Plan) { p.Routes[0].PlanID = "pln-other" },
		"a route with a driver":   func(p *contracts.Plan) { p.Routes[0].DriverID = "drv-1" },
		"a route without stops":   func(p *contracts.Plan) { p.Routes[0].Stops = nil },
		"no user":                 func(p *contracts.Plan) { p.UserID = "" },
	}
	r := newRig(t)
	for name, tweak := range mutate {
		t.Run(name, func(t *testing.T) {
			plan := twoRoutePlan()
			tweak(&plan)
			err := testkit.Apply(r.pool, r.projection.PlanConfirmed, contracts.TypePlanConfirmed, "pln-1", 1, contracts.PlanConfirmed{Plan: plan})
			if err == nil || !isPermanent(err) {
				t.Errorf("error = %v, want a permanent one (dead-letter, no retries)", err)
			}
		})
	}
	if err := testkit.Apply(r.pool, r.projection.PlanConfirmed, contracts.TypePlanConfirmed, "pln-1", 1, struct{ Plan string }{"nope"}); err == nil || !isPermanent(err) {
		t.Errorf("an undecodable payload: %v, want a permanent error", err)
	}
	if _, err := r.service.Get(t.Context(), "pln-1"); !isStatus(err, platform.CodeNotFound) {
		t.Errorf("a refused snapshot must not be projected: %v", err)
	}
}

// run drives a route through the execution events with versions 1.. in order.
type execution struct {
	r       *rig
	t       *testing.T
	version int64
}

func (e *execution) apply(h func(context.Context, pgx.Tx, contracts.Envelope) error, eventType string, data any) error {
	e.version++
	return testkit.Apply(e.r.pool, h, eventType, "pln-1:r1", e.version, data)
}

func TestExecutionEventsMoveTheRouteAndThePlanToDone(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")
	r.assign(t, "pln-1", "pln-1:r2", "drv-2")
	e := &execution{r: r, t: t}
	at := func(min int) contracts.Time { return contracts.At(time.Now().Add(time.Duration(min) * time.Minute)) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	must(e.apply(r.projection.RouteStarted, contracts.TypeRouteStarted, contracts.RouteStarted{PlanID: "pln-1", RouteID: "pln-1:r1", DriverID: "drv-1", StartedAt: at(0)}))
	if got := r.load(t, "pln-1"); got.Plan.Status != contracts.PlanStatusRunning || got.Routes[0].Status != contracts.RouteStatusRunning || got.Routes[0].Stops[0].Status != contracts.StopStatusEnRoute {
		t.Fatalf("after route.started: plan %s, route %s, first stop %s", got.Plan.Status, got.Routes[0].Status, got.Routes[0].Stops[0].Status)
	}

	must(e.apply(r.projection.StopArrived, contracts.TypeStopArrived, contracts.StopArrived{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s1", ArrivedAt: at(5)}))
	must(e.apply(r.projection.StopCompleted, contracts.TypeStopCompleted, contracts.StopCompleted{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s1", CompletedAt: at(9)}))
	must(e.apply(r.projection.StopArrived, contracts.TypeStopArrived, contracts.StopArrived{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s2", ArrivedAt: at(20)}))
	issue := contracts.Msg{Key: "dom.issue.tidak_ada_orang"}
	must(e.apply(r.projection.StopIssue, contracts.TypeStopIssue, contracts.StopIssue{
		PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s2", Kind: contracts.IssueKindNobodyThere, Issue: issue, ReportedAt: at(25),
	}))
	got := r.load(t, "pln-1")
	route := got.Routes[0]
	if route.Status != contracts.RouteStatusDone || route.Stops[1].Status != contracts.StopStatusProblem || route.Stops[1].Issue == nil || route.Stops[1].Issue.Key != issue.Key {
		t.Fatalf("route %s, last stop %s (issue %v): closing the last stop with a problem finishes the route", route.Status, route.Stops[1].Status, route.Stops[1].Issue)
	}
	if got.Plan.Status != contracts.PlanStatusAssigned {
		t.Errorf("plan %s with one route done and one still assigned, want ditugaskan", got.Plan.Status)
	}
	must(e.apply(r.projection.RouteCompleted, contracts.TypeRouteCompleted, contracts.RouteCompleted{PlanID: "pln-1", RouteID: "pln-1:r1", DriverID: "drv-1", FinishedAt: at(25)}))
	must(e.apply(r.projection.RouteDelayed, contracts.TypeRouteDelayed, contracts.RouteDelayed{PlanID: "pln-1", RouteID: "pln-1:r1", DelayMin: 7}))

	// the second route, to the end
	e2 := &execution{r: r, t: t}
	apply2 := func(h func(context.Context, pgx.Tx, contracts.Envelope) error, eventType string, data any) {
		t.Helper()
		e2.version++
		must(testkit.Apply(r.pool, h, eventType, "pln-1:r2", e2.version, data))
	}
	apply2(r.projection.RouteStarted, contracts.TypeRouteStarted, contracts.RouteStarted{PlanID: "pln-1", RouteID: "pln-1:r2", DriverID: "drv-2", StartedAt: at(0)})
	for _, stop := range []string{"pln-1:r2:s1", "pln-1:r2:s2"} {
		apply2(r.projection.StopArrived, contracts.TypeStopArrived, contracts.StopArrived{PlanID: "pln-1", RouteID: "pln-1:r2", StopID: stop, ArrivedAt: at(10)})
		apply2(r.projection.StopCompleted, contracts.TypeStopCompleted, contracts.StopCompleted{PlanID: "pln-1", RouteID: "pln-1:r2", StopID: stop, CompletedAt: at(12)})
	}
	apply2(r.projection.RouteCompleted, contracts.TypeRouteCompleted, contracts.RouteCompleted{PlanID: "pln-1", RouteID: "pln-1:r2", DriverID: "drv-2", FinishedAt: at(40)})

	done := r.load(t, "pln-1")
	if done.Plan.Status != contracts.PlanStatusDone || done.Plan.FinishedAt.IsZero() {
		t.Errorf("plan %s finished %v, want selesai with a finish time", done.Plan.Status, done.Plan.FinishedAt)
	}
	if done.Routes[0].ExecVersion != e.version || done.Routes[1].ExecVersion != e2.version || done.Routes[0].DelayMin != 7 {
		t.Errorf("exec versions %d/%d, delay %v; want the last applied versions %d/%d and 7 min", done.Routes[0].ExecVersion, done.Routes[1].ExecVersion, done.Routes[0].DelayMin, e.version, e2.version)
	}
	last := done.Plan.Events[len(done.Plan.Events)-1].Text.Key
	if last != "srv.ops.event.allDone" {
		t.Errorf("timeline ends with %s, want the plan's completion", last)
	}
}

func TestExecutionEventsOlderOrRepeatedAreDropped(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")
	started := contracts.RouteStarted{PlanID: "pln-1", RouteID: "pln-1:r1", DriverID: "drv-1", StartedAt: contracts.Now()}
	arrived := contracts.StopArrived{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s1", ArrivedAt: contracts.Now()}

	apply := func(h func(context.Context, pgx.Tx, contracts.Envelope) error, eventType string, version int64, data any) error {
		return testkit.Apply(r.pool, h, eventType, "pln-1:r1", version, data)
	}
	if err := apply(r.projection.RouteStarted, contracts.TypeRouteStarted, 1, started); err != nil {
		t.Fatal(err)
	}
	if err := apply(r.projection.StopArrived, contracts.TypeStopArrived, 2, arrived); err != nil {
		t.Fatal(err)
	}
	before := r.load(t, "pln-1")

	// Re-applying either would fail the route's rules (already started, already arrived) if it were applied
	// again; both are dropped by version instead.
	if err := apply(r.projection.StopArrived, contracts.TypeStopArrived, 2, arrived); err != nil {
		t.Errorf("a duplicate stop.arrived: %v, want it dropped quietly", err)
	}
	if err := apply(r.projection.RouteStarted, contracts.TypeRouteStarted, 1, started); err != nil {
		t.Errorf("a late route.started: %v, want it dropped quietly", err)
	}
	after := r.load(t, "pln-1")
	if after.Routes[0].ExecVersion != 2 || len(after.Plan.Events) != len(before.Plan.Events) || after.Plan.UpdatedAt != before.Plan.UpdatedAt {
		t.Errorf("version %d, %d timeline entries (was %d); want nothing to change", after.Routes[0].ExecVersion, len(after.Plan.Events), len(before.Plan.Events))
	}
}

func TestExecutionEventsAheadOfTheirPredecessorsAreRetried(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	r.assign(t, "pln-1", "pln-1:r1", "drv-1")

	// stop.arrived reaches us before route.started: not a bad event, only early
	err := testkit.Apply(r.pool, r.projection.StopArrived, contracts.TypeStopArrived, "pln-1:r1", 2,
		contracts.StopArrived{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s1", ArrivedAt: contracts.Now()})
	if err == nil || isPermanent(err) {
		t.Errorf("error = %v, want a plain error so the consumer retries", err)
	}
	if got := r.load(t, "pln-1").Routes[0]; got.ExecVersion != 0 || got.Status != contracts.RouteStatusAssigned {
		t.Errorf("route %s at version %d, want it untouched", got.Status, got.ExecVersion)
	}

	// and so are events about a plan or a route we have not heard of yet
	err = testkit.Apply(r.pool, r.projection.RouteStarted, contracts.TypeRouteStarted, "pln-9:r1", 1,
		contracts.RouteStarted{PlanID: "pln-9", RouteID: "pln-9:r1", StartedAt: contracts.Now()})
	if err == nil || isPermanent(err) {
		t.Errorf("unknown plan: %v, want a plain error", err)
	}
	err = testkit.Apply(r.pool, r.projection.RouteStarted, contracts.TypeRouteStarted, "pln-1:r9", 1,
		contracts.RouteStarted{PlanID: "pln-1", RouteID: "pln-1:r9", StartedAt: contracts.Now()})
	if err == nil || isPermanent(err) {
		t.Errorf("unknown route: %v, want a plain error", err)
	}
	// route.completed before the stops that complete the route
	err = testkit.Apply(r.pool, r.projection.RouteCompleted, contracts.TypeRouteCompleted, "pln-1:r1", 3,
		contracts.RouteCompleted{PlanID: "pln-1", RouteID: "pln-1:r1", FinishedAt: contracts.Now()})
	if err == nil || isPermanent(err) {
		t.Errorf("route.completed of an unfinished route: %v, want a plain error", err)
	}
}

func TestCustomerNamesFollowTheirVersions(t *testing.T) {
	r := newRig(t)
	apply := func(h func(context.Context, pgx.Tx, contracts.Envelope) error, eventType string, version int64, data any) {
		t.Helper()
		if err := testkit.Apply(r.pool, h, eventType, "cus-1", version, data); err != nil {
			t.Fatal(err)
		}
	}
	name := func() string {
		n, err := CustomerName(t.Context(), r.pool, "cus-1")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := name(); got != "cus-1" {
		t.Errorf("name before any event = %q, want the id as a stand-in", got)
	}
	apply(r.projection.CustomerRegistered, contracts.TypeCustomerRegistered, 1, contracts.CustomerRegistered{CustomerID: "cus-1", Name: "Dina"})
	apply(r.projection.CustomerUpdated, contracts.TypeCustomerUpdated, 3, contracts.CustomerUpdated{CustomerID: "cus-1", Name: "Dina Pramesti"})
	apply(r.projection.CustomerUpdated, contracts.TypeCustomerUpdated, 2, contracts.CustomerUpdated{CustomerID: "cus-1", Name: "Dina P."}) // late
	apply(r.projection.CustomerUpdated, contracts.TypeCustomerUpdated, 3, contracts.CustomerUpdated{CustomerID: "cus-1", Name: "Dina X."}) // repeated version
	if got := name(); got != "Dina Pramesti" {
		t.Errorf("name = %q, want the one of the newest version", got)
	}

	r.confirm(t, twoRoutePlan())
	list, err := r.service.List(t.Context(), Filter{})
	if err != nil || len(list) != 1 || list[0].CustomerName != "Dina Pramesti" {
		t.Errorf("list = %+v, %v; want the customer's name on the row", list, err)
	}
}

func TestListFiltersAndOrder(t *testing.T) {
	r := newRig(t)
	mk := func(id, user string, status contracts.PlanStatus, vt contracts.VehicleType, age time.Duration) contracts.Plan {
		plan := testkit.Plan(id, testkit.Route(id, 1, vt, testkit.Kemang, 5, t0))
		plan.UserID = user
		plan.CreatedAt = contracts.At(time.Now().Add(-age))
		plan.UpdatedAt = plan.CreatedAt
		return plan
	}
	r.confirm(t, mk("pln-a", "cus-1", contracts.PlanStatusAwaitingDriver, contracts.VehicleTypeMotor, 3*time.Hour))
	r.confirm(t, mk("pln-b", "cus-2", contracts.PlanStatusAwaitingDriver, contracts.VehicleTypeBlindvan, 2*time.Hour))
	r.confirm(t, mk("pln-c", "cus-1", contracts.PlanStatusAwaitingDriver, contracts.VehicleTypeMotor, time.Hour))
	if _, err := r.pool.Exec(t.Context(), `INSERT INTO drivers (id, name, phone, joined_at) VALUES ('drv-1', 'Budi Santoso', '0811', now())`); err != nil {
		t.Fatal(err)
	}
	r.assign(t, "pln-a", "pln-a:r1", "drv-1") // plan a: ditugaskan, with a driver

	ids := func(f Filter) []string {
		t.Helper()
		list, err := r.service.List(t.Context(), f)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, s := range list {
			out = append(out, s.ID)
		}
		return out
	}
	tests := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"everything, most recently updated first", Filter{}, []string{"pln-a", "pln-c", "pln-b"}},
		{"by status", Filter{Status: []contracts.PlanStatus{contracts.PlanStatusAssigned}}, []string{"pln-a"}},
		{"by several statuses", Filter{Status: []contracts.PlanStatus{contracts.PlanStatusAssigned, contracts.PlanStatusAwaitingDriver}}, []string{"pln-a", "pln-c", "pln-b"}},
		{"by customer", Filter{UserID: "cus-1"}, []string{"pln-a", "pln-c"}},
		{"plans that need a driver", Filter{NeedsDriver: true}, []string{"pln-c", "pln-b"}},
		{"by vehicle type", Filter{VehicleType: contracts.VehicleTypeBlindvan}, []string{"pln-b"}},
		{"by driver name", Filter{Q: "budi"}, []string{"pln-a"}},
		{"by code", Filter{Q: "PL-pln-c"}, []string{"pln-c"}},
		{"created from", Filter{From: contracts.At(time.Now().Add(-150 * time.Minute))}, []string{"pln-c", "pln-b"}},
		{"created until", Filter{To: contracts.At(time.Now().Add(-150 * time.Minute))}, []string{"pln-a"}},
		{"no match", Filter{Q: "nothing like it"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ids(tt.filter); !slices.Equal(got, tt.want) {
				t.Errorf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWaitingRoutesInServiceOrder(t *testing.T) {
	r := newRig(t)
	now := time.Now()
	mk := func(id string, scheduled bool, age time.Duration, start time.Duration) {
		plan := testkit.Plan(id, testkit.Route(id, 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, now.Add(start)))
		plan.CreatedAt = contracts.At(now.Add(-age))
		if scheduled {
			plan.Schedule.Mode = contracts.ScheduleModeScheduled
		}
		r.confirm(t, plan)
	}
	mk("now-new", false, time.Hour, 0)
	mk("sched-late", true, 5*time.Hour, 50*time.Minute)
	mk("now-old", false, 3*time.Hour, 0)
	mk("sched-soon", true, time.Hour, 20*time.Minute)
	mk("sched-far", true, 6*time.Hour, 3*time.Hour)

	got, err := WaitingRoutes(t.Context(), r.pool, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, w := range got {
		ids = append(ids, w.PlanID)
	}
	want := []string{"now-old", "now-new", "sched-soon", "sched-late"}
	if !slices.Equal(ids, want) {
		t.Errorf("waiting = %v, want %v: now plans first, the longest waiting first, then scheduled ones by departure, none beyond the lead", ids, want)
	}

	r.assign(t, "now-old", "now-old:r1", "drv-1")
	got, err = WaitingRoutes(t.Context(), r.pool, now.Add(time.Hour))
	if err != nil || len(got) != 3 || got[0].PlanID != "now-new" {
		t.Errorf("after assigning one: %+v, %v; want it gone from the list", got, err)
	}
}

func TestLiveRoutesAreTheAssignedAndRunningOnes(t *testing.T) {
	r := newRig(t)
	r.confirm(t, twoRoutePlan())
	if err := testkit.Apply(r.pool, r.projection.CustomerRegistered, contracts.TypeCustomerRegistered, "cus-1", 1, contracts.CustomerRegistered{CustomerID: "cus-1", Name: "Dina"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(t.Context(), `INSERT INTO drivers (id, name, phone, joined_at) VALUES ('drv-1', 'Budi', '0811', now())`); err != nil {
		t.Fatal(err)
	}
	live, err := r.service.LiveRoutes(t.Context())
	if err != nil || len(live) != 0 {
		t.Fatalf("live = %+v, %v; want none while every route waits", live, err)
	}
	r.assign(t, "pln-1", "pln-1:r2", "drv-1")
	live, err = r.service.LiveRoutes(t.Context())
	if err != nil || len(live) != 1 {
		t.Fatalf("live = %+v, %v; want the assigned route", live, err)
	}
	if got := live[0]; got.PlanCode != "PL-pln-1" || got.CustomerName != "Dina" || got.DriverName != "Budi" || got.Route.ID != "pln-1:r2" {
		t.Errorf("live route = %+v, want plan code, customer and driver names", got)
	}
}
