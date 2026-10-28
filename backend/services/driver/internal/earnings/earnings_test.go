package earnings

import (
	"reflect"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// The expected values are what frontend/packages/domain/src/earnings.ts returns (checked with node), including
// the floating-point result for 22,500.
func TestRouteRpMatchesTheFrontend(t *testing.T) {
	for cost, want := range map[int64]int64{
		0: 0, 2400: 1500, 2500: 2000, 7500: 5500, 21500: 15000, 22500: 15500, 42500: 29500, 100000: 70000,
	} {
		if got := RouteRp(cost); got != want {
			t.Errorf("RouteRp(%d) = %d, want %d", cost, got, want)
		}
	}
}

func at(day, hour, min int) time.Time {
	return time.Date(2026, time.October, day, hour, min, 0, 0, wib).UTC()
}

func finished(when time.Time, costRp int64) tasks.Finished {
	return tasks.Finished{PlanID: "pln-1", PlanCode: "PL-2001", RouteID: "pln-1:r1", FinishedAt: when, Stops: 3, DistanceKm: 6, DurationMin: 30, CostRp: costRp}
}

func TestSummarizeCountsTheLastSevenWIBDays(t *testing.T) {
	now := at(21, 9, 0) // 21 October 09:00 WIB, which is 02:00 UTC
	got := summarize([]tasks.Finished{
		finished(at(21, 0, 5), 21500),  // today, just after midnight WIB (still 20 October in UTC)
		finished(at(21, 8, 0), 10000),  // today
		finished(at(20, 23, 59), 5000), // yesterday, a minute before midnight WIB
		finished(at(15, 12, 0), 40000), // six days ago: the first day of the week
		finished(at(14, 23, 0), 99999), // seven days ago: outside
	}, now)

	wantDays := []Day{
		{"2026-10-15", RouteRp(40000), 1}, {"2026-10-16", 0, 0}, {"2026-10-17", 0, 0}, {"2026-10-18", 0, 0}, {"2026-10-19", 0, 0},
		{"2026-10-20", RouteRp(5000), 1}, {"2026-10-21", RouteRp(21500) + RouteRp(10000), 2},
	}
	if !reflect.DeepEqual(got.ByDay, wantDays) {
		t.Errorf("by day = %+v\nwant     %+v", got.ByDay, wantDays)
	}
	if got.TodayRp != RouteRp(21500)+RouteRp(10000) || got.TripsToday != 2 {
		t.Errorf("today = %d Rp over %d trips", got.TodayRp, got.TripsToday)
	}
	if want := RouteRp(40000) + RouteRp(5000) + got.TodayRp; got.WeekRp != want || got.TripsWeek != 4 {
		t.Errorf("week = %d Rp over %d trips, want %d over 4", got.WeekRp, got.TripsWeek, want)
	}
}

func TestSummarizeOfNothingIsSevenEmptyDays(t *testing.T) {
	got := summarize(nil, at(21, 9, 0))
	if len(got.ByDay) != 7 || got.WeekRp != 0 || got.TripsWeek != 0 || got.ByDay[6].Date != "2026-10-21" {
		t.Errorf("summary = %+v", got)
	}
}

func TestItemOfPaysTheDriversShare(t *testing.T) {
	item := itemOf(finished(at(21, 8, 0), 21500))
	if item.EarningsRp != 15000 || item.Stops != 3 || item.RouteID != "pln-1:r1" || item.FinishedAt.Time != at(21, 8, 0) {
		t.Errorf("item = %+v", item)
	}
}
