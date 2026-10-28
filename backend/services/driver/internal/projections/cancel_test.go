package projections

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/execution"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// The two-phase plan cancel as driver-api takes part in it: votes, holds, tombstones, commit and abort.

const planID = "pln-1"

func (f fixture) request(requestID string, routeIDs ...string) error {
	f.t.Helper()
	return f.apply(f.p.planCancelRequested, contracts.TypePlanCancelRequested, planID, 0, contracts.PlanCancelRequested{
		PlanID: planID, RequestID: requestID, RequestedBy: contracts.CancelByCustomer, RouteIDs: routeIDs,
	})
}

func (f fixture) commit(requestID, reason string) {
	f.t.Helper()
	f.mustApply(f.p.planCancelled, contracts.TypePlanCancelled, planID, 0, contracts.PlanCancelled{
		PlanID: planID, RequestID: requestID, RequestedBy: contracts.CancelByCustomer, Reason: reason,
	})
}

func (f fixture) abort(requestID string) {
	f.t.Helper()
	f.mustApply(f.p.planCancelAborted, contracts.TypePlanCancelAborted, planID, 0, contracts.PlanCancelAborted{
		PlanID: planID, RequestID: requestID, Reason: contracts.Msg{Key: "srv.api.cancelAborted"},
	})
}

// vote is one vote read back from the outbox.
type vote struct {
	Type      string
	Key       string
	Version   int64
	RequestID string
	RouteID   string
	Reason    string
}

func (f fixture) votes() []vote {
	f.t.Helper()
	var out []vote
	for _, e := range drivertest.Outbox(f.t, f.pool) {
		switch e.Type {
		case contracts.TypePlanCancelAccepted:
			p := drivertest.Payload[contracts.PlanCancelAccepted](f.t, e)
			out = append(out, vote{e.Type, e.Key, e.Version, p.RequestID, p.RouteID, ""})
		case contracts.TypePlanCancelRejected:
			p := drivertest.Payload[contracts.PlanCancelRejected](f.t, e)
			out = append(out, vote{e.Type, e.Key, e.Version, p.RequestID, p.RouteID, p.Reason.Key})
		}
	}
	return out
}

// assigned stores route id as assigned to drv-1 (a task of the plan) through the projection.
func (f fixture) assigned(routeID string, version int64) {
	f.t.Helper()
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, routeID, version, drivertest.Assigned(routeID, "drv-1"))
}

// put sets the status of a stored route directly, as the execution service would.
func (f fixture) put(routeID string, status contracts.RouteStatus) {
	f.t.Helper()
	task := f.task(routeID)
	task.Route.Status = status
	if err := tasks.Put(f.t.Context(), f.pool, task, drivertest.Epoch); err != nil {
		f.t.Fatal(err)
	}
}

func (f fixture) tombstones(requestID string) []string {
	f.t.Helper()
	rows, err := f.pool.Query(f.t.Context(), `SELECT route_id FROM route_tombstones WHERE request_id = $1 ORDER BY route_id`, requestID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			f.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func (f fixture) bell(driverID string) []contracts.AppNotification {
	f.t.Helper()
	list, err := notices.List(f.t.Context(), f.pool, notices.Driver(driverID))
	if err != nil {
		f.t.Fatal(err)
	}
	return list
}

// notice is the newest entry of the driver's bell with this title key.
func (f fixture) notice(driverID, titleKey string) (contracts.AppNotification, bool) {
	f.t.Helper()
	for _, n := range f.bell(driverID) {
		if n.Title.Key == titleKey {
			return n, true
		}
	}
	return contracts.AppNotification{}, false
}

func (f fixture) count(driverID, titleKey string) int {
	f.t.Helper()
	n := 0
	for _, e := range f.bell(driverID) {
		if e.Title.Key == titleKey {
			n++
		}
	}
	return n
}

func (f fixture) exec() *execution.Service {
	return execution.NewService(f.pool, drivertest.Publisher(f.pool), func() time.Time { return drivertest.Epoch })
}

func wantConflict(t *testing.T, err error, key string) {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok || apiErr.Code != platform.CodeConflict || apiErr.Msg.Key != key {
		t.Fatalf("error = %v, want a conflict with %q", err, key)
	}
}

func TestEveryRouteOfARequestGetsOneVoteAccordingToWhatTheDriverApiKnowsOfIt(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	for _, r := range []string{"pln-1:r1", "pln-1:r2", "pln-1:r3", "pln-1:r4"} {
		f.assigned(r, 1)
	}
	f.put("pln-1:r2", contracts.RouteStatusRunning)
	f.put("pln-1:r3", contracts.RouteStatusDone)
	f.mustApply(f.p.routeUnassigned, contracts.TypeRouteUnassigned, "pln-1:r4", 2, contracts.RouteUnassigned{PlanID: planID, RouteID: "pln-1:r4", DriverID: "drv-1"})

	// r1: assigned, not started. r2: running. r3: done. r4: dispatch took it back. r5: never heard of.
	if err := f.request("req-1", "pln-1:r5", "pln-1:r3", "pln-1:r1", "pln-1:r4", "pln-1:r2", "pln-1:r1"); err != nil {
		t.Fatal(err)
	}

	want := []vote{
		{contracts.TypePlanCancelAccepted, planID, 0, "req-1", "pln-1:r1", ""},
		{contracts.TypePlanCancelRejected, planID, 0, "req-1", "pln-1:r2", "srv.api.cancelRunning"},
		{contracts.TypePlanCancelRejected, planID, 0, "req-1", "pln-1:r3", "srv.api.cancelRunning"},
		{contracts.TypePlanCancelAccepted, planID, 0, "req-1", "pln-1:r4", ""},
		{contracts.TypePlanCancelAccepted, planID, 0, "req-1", "pln-1:r5", ""},
	}
	if got := f.votes(); !slices.Equal(got, want) {
		t.Errorf("votes =\n%+v\nwant (one per route, keyed by plan, unversioned)\n%+v", got, want)
	}

	holds := map[string]string{}
	for _, r := range []string{"pln-1:r1", "pln-1:r2", "pln-1:r3", "pln-1:r4"} {
		holds[r] = f.task(r).Hold
	}
	if holds["pln-1:r1"] != "req-1" || holds["pln-1:r4"] != "req-1" || holds["pln-1:r2"] != "" || holds["pln-1:r3"] != "" {
		t.Errorf("holds = %v: only the routes that voted yes, and are known, are held", holds)
	}
	if got := f.tombstones("req-1"); !slices.Equal(got, []string{"pln-1:r5"}) {
		t.Errorf("tombstones = %v, want only the unknown route", got)
	}
	if _, err := tasks.Lock(t.Context(), f.pool, "pln-1:r5"); err == nil {
		t.Error("a tombstone created a task")
	}
}

func TestTheSameRequestVotesTheSameWhenItIsDeliveredAgain(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.assigned("pln-1:r1", 1)
	f.assigned("pln-1:r2", 1)
	f.put("pln-1:r2", contracts.RouteStatusRunning)

	for range 3 { // another delivery of the request under another event id
		if err := f.request("req-1", "pln-1:r1", "pln-1:r2", "pln-1:r3"); err != nil {
			t.Fatal(err)
		}
	}

	byRoute := map[string][]string{}
	for _, v := range f.votes() {
		byRoute[v.RouteID] = append(byRoute[v.RouteID], v.Type)
	}
	for route, types := range byRoute {
		want := contracts.TypePlanCancelAccepted
		if route == "pln-1:r2" {
			want = contracts.TypePlanCancelRejected
		}
		if len(types) != 3 || slices.ContainsFunc(types, func(ty string) bool { return ty != want }) {
			t.Errorf("votes for %s = %v, want three of %s", route, types, want)
		}
	}
	if f.task("pln-1:r1").Hold != "req-1" || len(f.tombstones("req-1")) != 1 {
		t.Error("repeating the request changed the state it left")
	}
}

func TestAnAssignmentThatArrivesAfterItsTombstoneIsIgnoredAndTellsNobody(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	if err := f.request("req-1", "pln-1:r1"); err != nil {
		t.Fatal(err)
	}

	f.assigned("pln-1:r1", 1)
	f.assigned("pln-1:r1", 5) // nor does a newer version get through

	if _, err := tasks.Lock(t.Context(), f.pool, "pln-1:r1"); !errors.Is(err, tasks.ErrNotFound) {
		t.Errorf("the assignment created a task (%v)", err)
	}
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-1"); len(active) != 0 {
		t.Errorf("the driver lists %d tasks", len(active))
	}
	if bell := f.bell("drv-1"); len(bell) != 0 {
		t.Errorf("the driver was told of a job that never existed: %+v", bell)
	}

	// once the cancel commits the tombstone stays: the plan is gone, nothing of it may come back
	f.commit("req-1", "")
	f.assigned("pln-1:r1", 9)
	if _, err := tasks.Lock(t.Context(), f.pool, "pln-1:r1"); !errors.Is(err, tasks.ErrNotFound) {
		t.Errorf("an assignment after the commit created a task (%v)", err)
	}
	if got := f.tombstones("req-1"); len(got) != 1 {
		t.Errorf("tombstones after the commit = %v, want them kept", got)
	}
}

func TestACommitClosesTheHeldRoutesTakesThemOffTheListAndTellsTheDriver(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.provision("drv-2", "081234567802")
	f.assigned("pln-1:r1", 1)
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, "pln-1:r2", 1, drivertest.Assigned("pln-1:r2", "drv-2"))
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, "pln-9:r1", 1, drivertest.Assigned("pln-9:r1", "drv-1"))
	if err := f.request("req-1", "pln-1:r1", "pln-1:r2", "pln-1:r3"); err != nil {
		t.Fatal(err)
	}
	const cancelledTitle = "srv.api.notif.taskCancelled.title"

	f.commit("req-1", "salah alamat")

	for _, r := range []string{"pln-1:r1", "pln-1:r2"} {
		if task := f.task(r); !task.Cancelled() || !task.CancelledAt.Equal(drivertest.Epoch) {
			t.Errorf("route %s = %+v, want closed as cancelled", r, task)
		}
	}
	active, _ := tasks.Active(t.Context(), f.pool, "drv-1")
	if len(active) != 1 || active[0].Route.ID != "pln-9:r1" {
		t.Errorf("drv-1 lists %d tasks, want only the one of the other plan", len(active))
	}
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-2"); len(active) != 0 {
		t.Errorf("drv-2 still lists %d tasks", len(active))
	}
	if routes, err := f.exec().Routes(t.Context(), "drv-2"); err != nil || len(routes) != 0 {
		t.Errorf("driver.routes of drv-2 = %d, %v: the cancelled task must be gone", len(routes), err)
	}
	if f.task("pln-9:r1").Cancelled() {
		t.Error("another plan's route was cancelled")
	}

	n, ok := f.notice("drv-1", cancelledTitle)
	if !ok || n.Body.Key != "srv.api.notif.taskCancelled.bodyReason" ||
		n.Body.Params["code"] != "PL-2001" || n.Body.Params["reason"] != "salah alamat" || n.PlanID != planID || n.Read {
		t.Errorf("notification = %+v (found %v)", n, ok)
	}
	if f.count("drv-1", cancelledTitle) != 1 || f.count("drv-2", cancelledTitle) != 1 {
		t.Error("each driver of the plan must be told once")
	}

	// the same commit again closes and tells nothing more
	f.commit("req-1", "salah alamat")
	if f.count("drv-1", cancelledTitle) != 1 || f.count("drv-2", cancelledTitle) != 1 {
		t.Error("a repeated commit notified again")
	}
	// and a newer assignment of the closed route does not bring it back
	f.assigned("pln-1:r1", 7)
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-1"); len(active) != 1 {
		t.Error("an assignment brought the cancelled route back")
	}
}

func TestACommitWithoutAReasonUsesTheShortText(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.assigned("pln-1:r1", 1)
	if err := f.request("req-1", "pln-1:r1"); err != nil {
		t.Fatal(err)
	}
	f.commit("req-1", "")
	n, ok := f.notice("drv-1", "srv.api.notif.taskCancelled.title")
	if !ok || n.Body.Key != "srv.api.notif.taskCancelled.body" || n.Body.Params["code"] != "PL-2001" || n.Body.Params["reason"] != nil {
		t.Errorf("body = %+v", n.Body)
	}
}

func TestACommitOnlyClosesWhatItsOwnRequestHolds(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.assigned("pln-1:r1", 1)
	f.assigned("pln-1:r2", 1)
	if err := f.request("req-1", "pln-1:r1"); err != nil {
		t.Fatal(err)
	}
	f.commit("req-other", "")
	if f.task("pln-1:r1").Cancelled() {
		t.Error("another request's commit closed the route")
	}
	f.commit("req-1", "")
	if !f.task("pln-1:r1").Cancelled() || f.task("pln-1:r2").Cancelled() {
		t.Error("the commit must close the held route and only that one")
	}
}

func TestAnAbortReleasesTheHoldsSoTheRouteCanBeStartedAgainAndForgetsTheTombstones(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.assigned("pln-1:r1", 1)
	if err := f.request("req-1", "pln-1:r1", "pln-1:r2"); err != nil {
		t.Fatal(err)
	}
	if err := f.request("req-2", "pln-1:r3"); err != nil {
		t.Fatal(err)
	}

	_, err := f.exec().Start(t.Context(), "drv-1", planID, "pln-1:r1")
	wantConflict(t, err, "srv.api.routeCancelling")

	f.abort("req-1")

	if hold := f.task("pln-1:r1").Hold; hold != "" {
		t.Errorf("hold = %q after the abort", hold)
	}
	if got := f.tombstones("req-1"); len(got) != 0 {
		t.Errorf("tombstones of the aborted request = %v", got)
	}
	if got := f.tombstones("req-2"); len(got) != 1 {
		t.Errorf("an abort of one request removed the tombstones of another: %v", got)
	}
	started, err := f.exec().Start(t.Context(), "drv-1", planID, "pln-1:r1")
	if err != nil || started.Route.Status != contracts.RouteStatusRunning {
		t.Fatalf("start after the abort = %+v, %v", started.Route.Status, err)
	}
	// the assignment that was ignored while its tombstone stood is accepted now
	f.assigned("pln-1:r2", 1)
	if _, err := tasks.Lock(t.Context(), f.pool, "pln-1:r2"); err != nil {
		t.Errorf("after the abort an assignment is still ignored: %v", err)
	}
	// an abort never un-cancels a route that a commit closed
	f.assigned("pln-1:r4", 1)
	if err := f.request("req-3", "pln-1:r4"); err != nil {
		t.Fatal(err)
	}
	f.commit("req-3", "")
	f.abort("req-3")
	if !f.task("pln-1:r4").Cancelled() {
		t.Error("an abort reopened a cancelled route")
	}
}

func TestAHeldRouteCannotBeActedOnUntilTheCancelEnds(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.assigned("pln-1:r1", 1)
	if err := f.request("req-1", "pln-1:r1"); err != nil {
		t.Fatal(err)
	}
	svc := f.exec()
	stop := "pln-1:r1:s1"

	_, err := svc.Start(t.Context(), "drv-1", planID, "pln-1:r1")
	wantConflict(t, err, "srv.api.routeCancelling")
	_, err = svc.Arrive(t.Context(), "drv-1", planID, "pln-1:r1", stop)
	wantConflict(t, err, "srv.api.routeCancelling")
	_, err = svc.Complete(t.Context(), "drv-1", planID, "pln-1:r1", stop, nil)
	wantConflict(t, err, "srv.api.routeCancelling")
	_, err = svc.ReportIssue(t.Context(), "drv-1", planID, "pln-1:r1", stop, contracts.IssueKindOther, "x")
	wantConflict(t, err, "srv.api.routeCancelling")
	if got := f.task("pln-1:r1"); got.Route.Status != contracts.RouteStatusAssigned || got.ExecVersion != 0 {
		t.Errorf("a refused action changed the route: %+v", got)
	}
	if n := len(drivertest.Types(drivertest.Outbox(t, f.pool))); n != 1 {
		t.Errorf("%d events in the outbox, want only the vote", n)
	}

	// the route is still listed while the vote stands; a commit removes it, and then it is not found at all
	if routes, _ := svc.Routes(t.Context(), "drv-1"); len(routes) != 1 {
		t.Errorf("a held route is listed: %d routes", len(routes))
	}
	f.commit("req-1", "")
	_, err = svc.Start(t.Context(), "drv-1", planID, "pln-1:r1")
	if apiErr, ok := errors.AsType[*platform.APIError](err); !ok || apiErr.Code != platform.CodeNotFound {
		t.Errorf("start of a cancelled route = %v, want not found", err)
	}
}

// pending is a delivery that has not finished: it either completes or is still waiting.
func pending(t *testing.T, what string, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s did not wait for the transaction that holds the route (it returned %v)", what, err)
	case <-time.After(300 * time.Millisecond):
	}
}

// The race, one order at a time: whoever has the route's lock decides, the other waits for it and then sees the
// result. These are the two sides of TestAStartAndACancelRequestRace, made deterministic.
func TestAVoteInProgressMakesTheStartWaitAndRefuseButAStartInProgressMakesTheVoteRefuse(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.assigned("pln-1:r1", 1)
	f.assigned("pln-1:r2", 1)

	// the vote is under way (not committed) for r1: a start of r1 must wait for it
	voteTx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = voteTx.Rollback(context.WithoutCancel(t.Context())) }()
	env, err := contracts.NewEnvelope("console-api", contracts.TypePlanCancelRequested, planID, 0, contracts.PlanCancelRequested{
		PlanID: planID, RequestID: "req-1", RouteIDs: []string{"pln-1:r1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.p.planCancelRequested(t.Context(), voteTx, env); err != nil {
		t.Fatal(err)
	}
	started := make(chan error, 1)
	go func() {
		_, err := f.exec().Start(t.Context(), "drv-1", planID, "pln-1:r1")
		started <- err
	}()
	pending(t, "the start", started)
	if err := voteTx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	wantConflict(t, <-started, "srv.api.routeCancelling")

	// the start of r2 is under way: the vote for r2 must wait for it, then refuse
	start, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = start.Rollback(context.WithoutCancel(t.Context())) }()
	task, err := tasks.Lock(t.Context(), start, "pln-1:r2")
	if err != nil {
		t.Fatal(err)
	}
	task.Route.Status = contracts.RouteStatusRunning
	if err := tasks.Put(t.Context(), start, task, drivertest.Epoch); err != nil {
		t.Fatal(err)
	}
	voted := make(chan error, 1)
	go func() { voted <- f.request("req-2", "pln-1:r2") }()
	pending(t, "the vote", voted)
	if err := start.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-voted; err != nil {
		t.Fatal(err)
	}
	var got vote
	for _, v := range f.votes() {
		if v.RouteID == "pln-1:r2" {
			got = v
		}
	}
	if got.Type != contracts.TypePlanCancelRejected || f.task("pln-1:r2").Hold != "" {
		t.Errorf("vote after a start = %s, hold %q; want a rejection and no hold", got.Type, f.task("pln-1:r2").Hold)
	}
}

func TestAnAssignmentWaitsForAVoteInProgressAndThenFindsItsTombstone(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	voteTx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = voteTx.Rollback(context.WithoutCancel(t.Context())) }()
	env, err := contracts.NewEnvelope("console-api", contracts.TypePlanCancelRequested, planID, 0, contracts.PlanCancelRequested{
		PlanID: planID, RequestID: "req-1", RouteIDs: []string{"pln-1:r1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.p.planCancelRequested(t.Context(), voteTx, env); err != nil { // the route is unknown: a tombstone, not yet committed
		t.Fatal(err)
	}

	assigned := make(chan error, 1)
	go func() {
		assigned <- f.apply(f.p.routeAssigned, contracts.TypeRouteAssigned, "pln-1:r1", 1, drivertest.Assigned("pln-1:r1", "drv-1"))
	}()
	pending(t, "the assignment", assigned)
	if err := voteTx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-assigned; err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Lock(t.Context(), f.pool, "pln-1:r1"); !errors.Is(err, tasks.ErrNotFound) {
		t.Errorf("the assignment that waited created a task (%v)", err)
	}
}

func TestAStartAndACancelRequestRaceToExactlyOneOutcome(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	const rounds = 25
	var startWon, holdWon int
	for i := range rounds {
		routeID := fmt.Sprintf("pln-%d:r1", 100+i)
		plan := fmt.Sprintf("pln-%d", 100+i)
		f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, routeID, 1, drivertest.Assigned(routeID, "drv-1"))

		var (
			wg       sync.WaitGroup
			begin    = make(chan struct{})
			startErr error
			voteErr  error
		)
		wg.Go(func() {
			<-begin
			_, startErr = f.exec().Start(t.Context(), "drv-1", plan, routeID)
		})
		wg.Go(func() {
			<-begin
			voteErr = f.apply(f.p.planCancelRequested, contracts.TypePlanCancelRequested, plan, 0, contracts.PlanCancelRequested{
				PlanID: plan, RequestID: "req-" + plan, RouteIDs: []string{routeID},
			})
		})
		close(begin)
		wg.Wait()
		if voteErr != nil {
			t.Fatalf("round %d: the vote failed: %v", i, voteErr)
		}

		var voted vote
		for _, v := range f.votes() {
			if v.RouteID == routeID {
				voted = v
			}
		}
		task := f.task(routeID)
		switch {
		case startErr == nil:
			// the start won: the route runs and the vote refuses
			startWon++
			if voted.Type != contracts.TypePlanCancelRejected || task.Route.Status != contracts.RouteStatusRunning || task.Hold != "" {
				t.Fatalf("round %d: the start succeeded but vote = %s, route %s, hold %q", i, voted.Type, task.Route.Status, task.Hold)
			}
		default:
			// the hold won: the vote accepted, the route is held, and the start was refused for it
			holdWon++
			wantConflict(t, startErr, "srv.api.routeCancelling")
			if voted.Type != contracts.TypePlanCancelAccepted || task.Route.Status != contracts.RouteStatusAssigned || task.Hold != "req-"+plan {
				t.Fatalf("round %d: the start was refused but vote = %s, route %s, hold %q", i, voted.Type, task.Route.Status, task.Hold)
			}
		}
	}
	t.Logf("start won %d, hold won %d of %d", startWon, holdWon, rounds)
}

func TestAnAssignmentAndACancelRequestRaceToOneOutcomeToo(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	const rounds = 25
	for i := range rounds {
		routeID := fmt.Sprintf("pln-%d:r1", 200+i)
		plan := fmt.Sprintf("pln-%d", 200+i)

		var (
			wg      sync.WaitGroup
			begin   = make(chan struct{})
			errs    [2]error
			request = func() error {
				return f.apply(f.p.planCancelRequested, contracts.TypePlanCancelRequested, plan, 0, contracts.PlanCancelRequested{
					PlanID: plan, RequestID: "req-" + plan, RouteIDs: []string{routeID},
				})
			}
		)
		wg.Go(func() {
			<-begin
			errs[0] = f.apply(f.p.routeAssigned, contracts.TypeRouteAssigned, routeID, 1, drivertest.Assigned(routeID, "drv-1"))
		})
		wg.Go(func() {
			<-begin
			errs[1] = request()
		})
		close(begin)
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("round %d: %v, %v", i, errs[0], errs[1])
		}

		// Whichever came first, the route the vote said yes to must not be startable: either held, or never made.
		task, err := tasks.Lock(t.Context(), f.pool, routeID)
		switch {
		case errors.Is(err, tasks.ErrNotFound):
			if got := f.tombstones("req-" + plan); len(got) != 1 {
				t.Fatalf("round %d: no task and no tombstone", i)
			}
		case err != nil:
			t.Fatal(err)
		case task.Hold != "req-"+plan:
			t.Fatalf("round %d: the route exists unheld (%s) although its cancel voted yes", i, task.Route.Status)
		}
	}
}

func (f fixture) parked() int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.t.Context(), `SELECT count(*) FROM parked_assignments`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// While the tombstone stands the assignment is ignored; if the cancel is aborted it is not lost, because
// dispatch does not send it again.
func TestAnAssignmentIgnoredBehindATombstoneIsAppliedIfTheCancelIsAborted(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	if err := f.request("req-1", "pln-1:r1", "pln-1:r2", "pln-1:r3"); err != nil {
		t.Fatal(err)
	}

	// r1: assigned twice (v1, then a newer v3), r2: assigned then taken back, r3: nothing said
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, "pln-1:r1", 1, withCost(drivertest.Assigned("pln-1:r1", "drv-1"), 11111))
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, "pln-1:r1", 3, withCost(drivertest.Assigned("pln-1:r1", "drv-1"), 33333))
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, "pln-1:r1", 2, withCost(drivertest.Assigned("pln-1:r1", "drv-1"), 22222)) // older than what is parked
	f.assigned("pln-1:r2", 1)
	f.mustApply(f.p.routeUnassigned, contracts.TypeRouteUnassigned, "pln-1:r2", 2, contracts.RouteUnassigned{PlanID: planID, RouteID: "pln-1:r2", DriverID: "drv-1"})
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-1"); len(active) != 0 || len(f.bell("drv-1")) != 0 {
		t.Fatalf("while the cancel is open the driver has %d tasks and %d notifications, want none", len(active), len(f.bell("drv-1")))
	}
	if f.parked() != 2 {
		t.Fatalf("%d assignments parked, want the newest word for r1 and for r2", f.parked())
	}

	f.abort("req-1")

	task := f.task("pln-1:r1")
	if task.DriverID != "drv-1" || task.Route.CostRp != 33333 || task.AssignVersion != 3 || task.Hold != "" || task.Route.Status != contracts.RouteStatusAssigned {
		t.Errorf("r1 after the abort = driver %q, cost %d, version %d, hold %q: want the newest assignment applied", task.DriverID, task.Route.CostRp, task.AssignVersion, task.Hold)
	}
	if _, err := tasks.Lock(t.Context(), f.pool, "pln-1:r2"); err == nil {
		t.Error("r2 was given to the driver although dispatch took it back last")
	}
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-1"); len(active) != 1 {
		t.Errorf("the driver lists %d tasks, want the one that was replayed", len(active))
	}
	if f.count("drv-1", "srv.ops.ntf.newJobTitle") != 1 || f.parked() != 0 {
		t.Errorf("new-job notifications %d, parked %d; want one and none", f.count("drv-1", "srv.ops.ntf.newJobTitle"), f.parked())
	}
}

func TestParkedAssignmentsWaitForEveryTombstoneAndAreDroppedByACommit(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	if err := f.request("req-1", "pln-1:r1"); err != nil {
		t.Fatal(err)
	}
	if err := f.request("req-2", "pln-1:r1"); err != nil { // the second attempt, while the first has not ended
		t.Fatal(err)
	}
	f.assigned("pln-1:r1", 1)

	f.abort("req-1")
	if _, err := tasks.Lock(t.Context(), f.pool, "pln-1:r1"); err == nil || f.parked() != 1 {
		t.Fatalf("the assignment was applied while another request's tombstone stands (parked %d, %v)", f.parked(), err)
	}
	f.abort("req-2")
	if _, err := tasks.Lock(t.Context(), f.pool, "pln-1:r1"); err != nil || f.parked() != 0 {
		t.Fatalf("after the last tombstone the assignment was not applied (parked %d, %v)", f.parked(), err)
	}

	// a commit throws the parked assignments away: the plan is gone
	if err := f.request("req-3", "pln-1:r7"); err != nil {
		t.Fatal(err)
	}
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, "pln-1:r7", 1, drivertest.Assigned("pln-1:r7", "drv-1"))
	if f.parked() != 1 {
		t.Fatalf("parked = %d", f.parked())
	}
	f.commit("req-3", "")
	if f.parked() != 0 {
		t.Error("a commit left the delivery codes of a cancelled plan parked")
	}
	f.abort("req-3") // a late abort of a committed request must not revive anything
	if _, err := tasks.Lock(t.Context(), f.pool, "pln-1:r7"); err == nil {
		t.Error("the route of a committed cancel came back")
	}
}
