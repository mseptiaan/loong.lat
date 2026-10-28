package projections

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/notify"
)

func TestANewTaskTellsTheDriverWhoGetsItOnceWhatEverTheRepeats(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.provision("drv-2", "081234567802")

	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 2, drivertest.Assigned(route, "drv-1"))
	n, ok := f.notice("drv-1", "srv.ops.ntf.newJobTitle")
	if !ok || n.Body.Key != "srv.ops.ntf.newJobBody" || n.Body.Params["code"] != "PL-2001" || n.Body.Params["count"] != float64(3) ||
		n.Body.Params["start"] != contracts.At(drivertest.Epoch).Format("2006-01-02T15:04:05.000Z") || n.PlanID != "pln-1" || n.Audience != "driver:drv-1" || n.Read {
		t.Errorf("notification = %+v (found %v)", n, ok)
	}

	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 3, drivertest.Assigned(route, "drv-1")) // a new snapshot, same driver
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 1, drivertest.Assigned(route, "drv-2")) // stale: dropped
	if f.count("drv-1", "srv.ops.ntf.newJobTitle") != 1 || f.count("drv-2", "srv.ops.ntf.newJobTitle") != 0 {
		t.Errorf("drv-1 has %d new-job notifications and drv-2 %d, want 1 and 0", f.count("drv-1", "srv.ops.ntf.newJobTitle"), f.count("drv-2", "srv.ops.ntf.newJobTitle"))
	}

	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 4, drivertest.Assigned(route, "drv-2")) // handed over
	if f.count("drv-2", "srv.ops.ntf.newJobTitle") != 1 || f.count("drv-1", "srv.ops.ntf.newJobTitle") != 1 {
		t.Error("the new driver must be told, and only once")
	}
}

func TestAnUnassignmentRemovesTheTaskAndTellsTheDriverWhyAccordingToTheReason(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	for i, tc := range []struct {
		reason    string
		wantTitle string // "" = the driver is told nothing
		wantBody  string
	}{
		{"", "srv.api.notif.taskRemoved.title", "srv.api.notif.taskRemoved.body"},
		{contracts.UnassignAdmin, "srv.api.notif.taskRemoved.title", "srv.api.notif.taskRemoved.body"},
		{contracts.UnassignDriverSilent, "srv.api.notif.taskSilent.title", "srv.api.notif.taskSilent.body"},
		{contracts.UnassignRescheduled, "srv.api.notif.taskRescheduled.title", "srv.api.notif.taskRescheduled.body"},
		{contracts.UnassignCancelled, "srv.api.notif.taskCancelled.title", "srv.api.notif.taskCancelled.body"},
		{contracts.UnassignDriverOffline, "", ""},
		{"some_future_reason", "srv.api.notif.taskRemoved.title", "srv.api.notif.taskRemoved.body"},
	} {
		routeID := fmt.Sprintf("pln-%d:r1", 10+i)
		f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, routeID, 1, drivertest.Assigned(routeID, "drv-1"))
		before := len(f.bell("drv-1"))
		f.mustApply(f.p.routeUnassigned, contracts.TypeRouteUnassigned, routeID, 2,
			contracts.RouteUnassigned{PlanID: fmt.Sprintf("pln-%d", 10+i), RouteID: routeID, DriverID: "drv-1", Reason: tc.reason})

		if active, _ := f.exec().Routes(t.Context(), "drv-1"); len(active) != 0 {
			t.Errorf("reason %q: %d tasks are still listed, want none", tc.reason, len(active))
		}
		bell := f.bell("drv-1")
		if tc.wantTitle == "" {
			if len(bell) != before {
				t.Errorf("reason %q: the driver was told something: %+v", tc.reason, bell[0])
			}
			continue
		}
		if len(bell) != before+1 {
			t.Errorf("reason %q: %d notifications, want %d", tc.reason, len(bell), before+1)
			continue
		}
		var got *contracts.AppNotification
		for _, n := range bell {
			if n.PlanID == fmt.Sprintf("pln-%d", 10+i) && n.Title.Key != "srv.ops.ntf.newJobTitle" {
				got = &n
			}
		}
		if got == nil || got.Title.Key != tc.wantTitle || got.Body.Key != tc.wantBody || got.Body.Params["code"] != "PL-2001" {
			t.Errorf("reason %q: notification = %+v, want %s / %s with the plan code", tc.reason, got, tc.wantTitle, tc.wantBody)
		}
	}
}

func TestUnassignmentOfARouteThatRunsTellsTheDriverNothing(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 1, drivertest.Assigned(route, "drv-1"))
	f.run(contracts.RouteStatusRunning)
	before := len(f.bell("drv-1"))
	if err := f.apply(f.p.routeUnassigned, contracts.TypeRouteUnassigned, route, 2,
		contracts.RouteUnassigned{PlanID: "pln-1", RouteID: route, DriverID: "drv-1", Reason: contracts.UnassignAdmin}); err != nil {
		t.Fatal(err)
	}
	if after := len(f.bell("drv-1")); after != before {
		t.Errorf("a refused unassignment notified the driver (%d entries, had %d)", after, before)
	}
}

func TestACancellationIsAnnouncedOnceWhicheverWayTheDriverLearnsOfIt(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.assigned(route, 1)
	job := notify.Job{RouteID: route, PlanID: "pln-1", PlanCode: "PL-2001", DriverID: "drv-1"}

	// the unassignment of a cancelled plan, then the commit of the cancel, then the unassignment again
	f.mustApply(f.p.routeUnassigned, contracts.TypeRouteUnassigned, route, 2,
		contracts.RouteUnassigned{PlanID: "pln-1", RouteID: route, DriverID: "drv-1", Reason: contracts.UnassignCancelled})
	for range 2 {
		err := platform.WithTx(t.Context(), f.pool, func(tx pgx.Tx) error {
			return notify.Cancelled(t.Context(), tx, job, "", drivertest.Epoch)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count("drv-1", "srv.api.notif.taskCancelled.title"); n != 1 {
		t.Errorf("%d cancellation notifications for one route, want 1", n)
	}
}
