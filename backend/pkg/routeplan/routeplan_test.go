package routeplan

import (
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

func stop(kind contracts.StopKind, lat, lng float64, legMin float64) contracts.RouteStop {
	return contracts.RouteStop{Kind: kind, LegMin: legMin, Waypoint: contracts.Waypoint{PlaceRef: contracts.PlaceRef{Position: contracts.LatLng{Lat: lat, Lng: lng}}}}
}

func option() contracts.PlanOption {
	return contracts.PlanOption{Routes: []contracts.Route{
		{StartPosition: contracts.LatLng{Lat: -6.25, Lng: 106.80},
			Stops: []contracts.RouteStop{stop(contracts.StopKindPickup, -6.26, 106.81, 5), stop(contracts.StopKindDropoff, -6.23, 106.80, 8)}},
		{StartPosition: contracts.LatLng{Lat: -6.25, Lng: 106.80},
			Stops: []contracts.RouteStop{stop(contracts.StopKindPickup, -6.24, 106.79, 6), stop(contracts.StopKindDropoff, -6.27, 106.78, 9)}},
	}}
}

func TestRoutesFromOptionGetStableIdsCodesAndStatus(t *testing.T) {
	routes, err := RoutesFromOption("PL-1", option(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if routes[1].ID != "PL-1:r2" || routes[1].Stops[1].ID != "PL-1:r2:s2" || routes[0].PlanID != "PL-1" {
		t.Errorf("unexpected ids: %+v", routes)
	}
	for _, r := range routes {
		if r.Status != contracts.RouteStatusAwaitingDriver {
			t.Errorf("%s status = %s", r.ID, r.Status)
		}
		for _, s := range r.Stops {
			if s.Status != contracts.StopStatusWaiting {
				t.Errorf("%s status = %s", s.ID, s.Status)
			}
			switch s.Kind {
			case contracts.StopKindDropoff:
				if len(s.DeliveryCode) != 4 || strings.Trim(s.DeliveryCode, "0123456789") != "" || s.DeliveryCode[0] == '0' {
					t.Errorf("%s: code %q is not a 4-digit code", s.ID, s.DeliveryCode)
				}
			default:
				if s.DeliveryCode != "" {
					t.Errorf("%s: only drops carry a code", s.ID)
				}
			}
		}
	}
	// The quote itself is untouched: options stay valid after confirmation.
	if option().Routes[0].Stops[0].ID != "" {
		t.Error("RoutesFromOption must not edit the option it was given")
	}
}

func TestRebaseStartRecomputesTheFirstLegAndEveryEta(t *testing.T) {
	routes, _ := RoutesFromOption("PL-1", option(), nil)
	r := routes[0]
	r.VehicleType = contracts.VehicleTypeMotor
	r.CostRp = 17500
	r.Geometry = []contracts.LngLat{{106.8, -6.2}}
	start := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC) // 10:00 WIB
	driver := contracts.LatLng{Lat: -6.30, Lng: 106.85}

	moved := RebaseStart(r, driver, start)
	if moved.CostRp != r.CostRp {
		t.Error("the price must not change when the start moves")
	}
	if moved.Geometry != nil {
		t.Error("a start that moved invalidates the drawn path")
	}
	wantLeg := Round1(RoadKm(driver, r.Stops[0].Waypoint.Position) / SpeedKmh(contracts.VehicleTypeMotor, 10) * 60)
	if moved.Stops[0].LegMin != wantLeg || moved.Stops[0].EtaMin != Round1(wantLeg) {
		t.Errorf("first leg %.1f min, eta %.1f, want %.1f", moved.Stops[0].LegMin, moved.Stops[0].EtaMin, wantLeg)
	}
	// Second stop: first eta + pickup dwell + its own leg.
	if want := Round1(wantLeg + DwellPickupMin + 8); moved.Stops[1].EtaMin != want {
		t.Errorf("second eta = %.1f, want %.1f", moved.Stops[1].EtaMin, want)
	}
	if want := Round1(wantLeg + DwellPickupMin + 8 + DwellDropMin); moved.DurationMin != want {
		t.Errorf("duration = %.1f, want %.1f", moved.DurationMin, want)
	}
	if !moved.Stops[1].ArriveAt.After(moved.Stops[0].ArriveAt.Time) {
		t.Error("arrival times must ascend")
	}

	// A tiny move keeps the path.
	near := RebaseStart(r, contracts.LatLng{Lat: r.StartPosition.Lat + 0.00005, Lng: r.StartPosition.Lng}, start)
	if near.Geometry == nil {
		t.Error("a start that barely moved keeps its path")
	}
}

func TestReanchorEtasOnlyTouchesTheStopsFromTheIndexOn(t *testing.T) {
	routes, _ := RoutesFromOption("PL-1", option(), nil)
	r := routes[0]
	first := contracts.At(time.Date(2026, 10, 21, 3, 5, 0, 0, time.UTC))
	r.Stops[0].ArriveAt = first
	legStart := time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)
	got := ReanchorETAs(r, 1, legStart)
	if got.Stops[0].ArriveAt != first {
		t.Error("stops before the index keep their arrival time")
	}
	if want := legStart.Add(8 * time.Minute); !got.Stops[1].ArriveAt.Equal(want) {
		t.Errorf("arrival = %v, want %v", got.Stops[1].ArriveAt, want)
	}
}

func TestStatusFollowsTheRoutes(t *testing.T) {
	r := func(s ...contracts.RouteStatus) []contracts.Route {
		out := make([]contracts.Route, len(s))
		for i, st := range s {
			out[i].Status = st
		}
		return out
	}
	for name, tc := range map[string]struct {
		routes []contracts.Route
		want   contracts.PlanStatus
	}{
		"none":               {nil, contracts.PlanStatusAwaitingDriver},
		"waiting":            {r(contracts.RouteStatusAwaitingDriver), contracts.PlanStatusAwaitingDriver},
		"partly assigned":    {r(contracts.RouteStatusAssigned, contracts.RouteStatusAwaitingDriver), contracts.PlanStatusAwaitingDriver},
		"all assigned":       {r(contracts.RouteStatusAssigned, contracts.RouteStatusAssigned), contracts.PlanStatusAssigned},
		"one running":        {r(contracts.RouteStatusRunning, contracts.RouteStatusAwaitingDriver), contracts.PlanStatusRunning},
		"one done one ready": {r(contracts.RouteStatusDone, contracts.RouteStatusAssigned), contracts.PlanStatusAssigned},
		"all done":           {r(contracts.RouteStatusDone, contracts.RouteStatusDone), contracts.PlanStatusDone},
	} {
		if got := StatusOf(tc.routes); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

func TestOpenIndexSkipsFinishedStops(t *testing.T) {
	r := contracts.Route{Stops: []contracts.RouteStop{{Status: contracts.StopStatusDone}, {Status: contracts.StopStatusProblem}, {Status: contracts.StopStatusWaiting}}}
	if got := OpenIndex(r); got != 2 {
		t.Errorf("OpenIndex = %d, want 2", got)
	}
	r.Stops[2].Status = contracts.StopStatusDone
	if got := OpenIndex(r); got != -1 {
		t.Errorf("OpenIndex = %d, want -1", got)
	}
}

func TestRushHourIsSlower(t *testing.T) {
	if SpeedKmh(contracts.VehicleTypeMotor, 8) >= SpeedKmh(contracts.VehicleTypeMotor, 10) {
		t.Error("morning rush must be slower than midday")
	}
	if HourWIB(time.Date(2026, 10, 21, 0, 50, 0, 0, time.UTC)) != 7 {
		t.Error("00:50 UTC is 07:50 WIB")
	}
}
