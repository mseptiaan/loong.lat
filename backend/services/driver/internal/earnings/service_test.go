package earnings

import (
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// These tests need Postgres (TEST_DATABASE_URL) and skip without it.

func TestHistoryAndEarningsComeFromTheFinishedRoutes(t *testing.T) {
	pool := drivertest.Pool(t)
	ctx := t.Context()
	for _, id := range []string{"drv-1", "drv-2"} {
		m := drivers.Master{ID: id, Name: id, Phone: "08" + id, PinHash: "$argon2id$x", JoinedAt: drivertest.Epoch, Version: 1}
		if err := drivers.Provision(ctx, pool, m); err != nil {
			t.Fatal(err)
		}
	}
	now := drivertest.Epoch.Add(8 * time.Hour) // 11:00 WIB on 21 October
	put := func(planID, driver string, status contracts.RouteStatus, finished time.Time, cost int64) {
		t.Helper()
		route := drivertest.Route(planID+":r1", driver)
		route.Status, route.CostRp = status, cost
		if status == contracts.RouteStatusDone {
			route.FinishedAt = contracts.At(finished)
		}
		if err := tasks.Put(ctx, pool, tasks.Task{PlanID: planID, PlanCode: "PL-" + planID, DriverID: driver, Route: route}, now); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "drv-1", contracts.RouteStatusDone, now.Add(-time.Hour), 21500)
	put("b", "drv-1", contracts.RouteStatusDone, now.AddDate(0, 0, -2), 10000)
	put("c", "drv-1", contracts.RouteStatusDone, now.AddDate(0, 0, -9), 30000) // too old for the week, still history
	put("d", "drv-1", contracts.RouteStatusRunning, time.Time{}, 99000)        // not finished: no pay yet
	put("e", "drv-2", contracts.RouteStatusDone, now.Add(-time.Hour), 77000)   // someone else's

	svc := NewService(pool, func() time.Time { return now })

	history, err := svc.History(ctx, "drv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[0].PlanCode != "PL-a" || history[1].PlanCode != "PL-b" || history[2].PlanCode != "PL-c" {
		t.Fatalf("history = %+v, want a, b, c newest first", history)
	}
	if h := history[0]; h.EarningsRp != 15000 || h.Stops != 3 || h.RouteID != "a:r1" || h.PlanID != "a" || h.FinishedAt.Time != now.Add(-time.Hour) {
		t.Errorf("first item = %+v", h)
	}

	got, err := svc.Earnings(ctx, "drv-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.TodayRp != 15000 || got.TripsToday != 1 || got.TripsWeek != 2 || got.WeekRp != 15000+RouteRp(10000) {
		t.Errorf("earnings = %+v", got)
	}

	if none, err := svc.History(ctx, "drv-9"); err != nil || none == nil || len(none) != 0 {
		t.Errorf("history of nobody = %v, %v; want an empty list", none, err)
	}
}
