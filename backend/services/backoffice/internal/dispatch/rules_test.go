package dispatch

import (
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// 10:00 in Jakarta, outside the rush hours, so the ETAs below are stable.
var now = time.Date(2026, time.October, 21, 3, 0, 0, 0, time.UTC)

var pickup = testkit.Kemang

// newMember is an online driver of a motorbike who is `km` kilometres north of the pickup.
func newMember(id string, km float64) fleet.Member {
	pos := testkit.North(pickup, km)
	return fleet.Member{
		Driver: fleet.Driver{ID: id, Name: "Driver " + id, Rating: 4.8, VehicleID: "veh-" + id, Status: fleet.StatusOnline, Position: &pos},
		Vehicle: &contracts.Vehicle{
			ID: "veh-" + id, Type: contracts.VehicleTypeMotor, Plate: "B 1 " + id, Model: "Vario", DriverID: id,
			MaxWeightKg: 20, MaxVolumeM3: 0.12, MaxDimensionCm: 55, Status: contracts.VehicleStatusActive,
		},
	}
}

func newRoute(weightKg float64) (contracts.Plan, contracts.Route) {
	route := testkit.Route("pln-1", 1, contracts.VehicleTypeMotor, pickup, weightKg, now.Add(2*time.Minute))
	return testkit.Plan("pln-1", route), route
}

func verdictFor(m fleet.Member, busy []contracts.Route, plan contracts.Plan, route contracts.Route) verdict {
	return assess(m, busy, plan, route, LoadOf(plan, route), now)
}

func TestAssessWhoMayTakeARoute(t *testing.T) {
	plan, route := newRoute(5)
	scheduledPlan := plan
	scheduledPlan.Schedule.Mode = contracts.ScheduleModeScheduled

	tests := []struct {
		name  string
		tweak func(*fleet.Member)
		plan  contracts.Plan
		want  refusal
	}{
		{"an online driver nearby", func(*fleet.Member) {}, plan, assignable},
		{"offline", func(m *fleet.Member) { m.Driver.Status = fleet.StatusOffline }, plan, unavailable},
		{"on duty with another job", func(m *fleet.Member) { m.Driver.Status = fleet.StatusOnDuty }, plan, unavailable},
		{"no vehicle", func(m *fleet.Member) { m.Vehicle = nil }, plan, noVehicle},
		{"vehicle in service", func(m *fleet.Member) { m.Vehicle.Status = contracts.VehicleStatusService }, plan, unavailable},
		{"a blind van for a motorbike route", func(m *fleet.Member) { m.Vehicle.Type = contracts.VehicleTypeBlindvan }, plan, wrongType},
		{"over the weight the vehicle carries", func(m *fleet.Member) { m.Vehicle.MaxWeightKg = 4 }, plan, overCapacity},
		{"over the volume", func(m *fleet.Member) { m.Vehicle.MaxVolumeM3 = 0.001 }, plan, overCapacity},
		{"an item longer than the vehicle takes", func(m *fleet.Member) { m.Vehicle.MaxDimensionCm = 10 }, plan, overCapacity},
		{"within the radius", func(m *fleet.Member) { *m.Driver.Position = testkit.North(pickup, Radius-0.5) }, plan, assignable},
		{"beyond the radius of a now route", func(m *fleet.Member) { *m.Driver.Position = testkit.North(pickup, Radius+0.5) }, plan, unavailable},
		{"the radius does not apply to a scheduled route", func(m *fleet.Member) { *m.Driver.Position = testkit.North(pickup, Radius+0.5) }, scheduledPlan, assignable},
		{"position unknown counts as near", func(m *fleet.Member) { m.Driver.Position = nil }, plan, assignable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMember("a", 1)
			tt.tweak(&m)
			got := verdictFor(m, nil, tt.plan, route)
			if got.refusal != tt.want {
				t.Errorf("refusal = %v, want %v", got.refusal, tt.want)
			}
		})
	}
}

func TestAssessKeepsTheNumbersOfADriverWhoIsTooBig(t *testing.T) {
	plan, route := newRoute(5)
	m := newMember("a", 2)
	m.Vehicle.MaxWeightKg = 4
	got := verdictFor(m, nil, plan, route)
	if got.refusal != overCapacity || got.fits || got.roadKm <= 0 || got.etaMin < 1 {
		t.Errorf("verdict = %+v: an over-capacity driver is shown with his distance, eta and fits=false", got)
	}
}

func TestLoadOfCountsTheLargerWeight(t *testing.T) {
	plan, route := newRoute(5)
	if l := LoadOf(plan, route); l.WeightKg != 5 || l.MaxDimensionCm != 20 || l.VolumeM3 != 0.008 {
		t.Errorf("load = %+v, want 5 kg, 20 cm, 0.008 m3 from the item", l)
	}
	route.TotalWeightKg = 30
	if l := LoadOf(plan, route); l.WeightKg != 30 {
		t.Errorf("weight = %v, want the route's larger total of 30", l.WeightKg)
	}
	// a shipment the route does not serve does not weigh on it
	plan.Shipments = append(plan.Shipments, contracts.Shipment{ID: "other", Items: []contracts.Item{{Qty: 10, WeightKg: 100}}})
	route.TotalWeightKg = 0
	if l := LoadOf(plan, route); l.WeightKg != 5 {
		t.Errorf("weight = %v, want only the served shipment", l.WeightKg)
	}
}

func TestAssessBusyDrivers(t *testing.T) {
	plan, route := newRoute(5)
	// the new route occupies a driver from now until about 20 minutes plus the drive to the pickup
	job := func(startsIn time.Duration, status contracts.RouteStatus) contracts.Route {
		r := testkit.Route("pln-0", 1, contracts.VehicleTypeMotor, pickup, 1, now.Add(startsIn))
		r.Status, r.DriverID = status, "a"
		return r
	}
	tests := []struct {
		name string
		busy []contracts.Route
		want refusal
	}{
		{"free", nil, assignable},
		{"a job starting right now", []contracts.Route{job(0, contracts.RouteStatusAssigned)}, unavailable},
		{"a job that ends just before, within the rest between jobs", []contracts.Route{job(-30*time.Minute, contracts.RouteStatusAssigned)}, unavailable},
		{"a job hours away", []contracts.Route{job(5*time.Hour, contracts.RouteStatusAssigned)}, assignable},
		{"a job long finished by the clock", []contracts.Route{job(-3*time.Hour, contracts.RouteStatusAssigned)}, assignable},
		{"a running job that is late still holds the driver", []contracts.Route{job(-3*time.Hour, contracts.RouteStatusRunning)}, unavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verdictFor(newMember("a", 1), tt.busy, plan, route); got.refusal != tt.want {
				t.Errorf("refusal = %v, want %v", got.refusal, tt.want)
			}
		})
	}
}

func TestScheduledRouteBusyAroundItsOwnTime(t *testing.T) {
	plan, route := newRoute(5)
	plan.Schedule.Mode = contracts.ScheduleModeScheduled
	route.StartAt = contracts.At(now.Add(3 * time.Hour))
	clash := testkit.Route("pln-0", 1, contracts.VehicleTypeMotor, pickup, 1, now.Add(3*time.Hour))
	clash.Status = contracts.RouteStatusAssigned
	if got := verdictFor(newMember("a", 1), []contracts.Route{clash}, plan, route); got.refusal != unavailable {
		t.Errorf("refusal = %v, want unavailable: a job at the same time", got.refusal)
	}
	elsewhere := clash
	elsewhere.StartAt = contracts.At(now.Add(10 * time.Hour))
	if got := verdictFor(newMember("a", 1), []contracts.Route{elsewhere}, plan, route); got.refusal != assignable {
		t.Errorf("refusal = %v, want assignable: the other job is hours later", got.refusal)
	}
}

func TestNearestFirstBreaksTiesByRatingThenID(t *testing.T) {
	plan, route := newRoute(5)
	mk := func(id string, km, rating float64) candidate {
		m := newMember(id, km)
		m.Driver.Rating = rating
		return verdictFor(m, nil, plan, route).candidate
	}
	cands := []candidate{mk("far", 6, 5), mk("b", 2, 4.5), mk("a", 2, 4.5), mk("best", 2, 4.9), mk("near", 0.5, 3)}
	nearestFirst(cands)
	var got []string
	for _, c := range cands {
		got = append(got, c.member.Driver.ID)
	}
	want := []string{"near", "best", "a", "b", "far"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v: nearest first, then rating, then id", got, want)
		}
	}
}

func TestScore(t *testing.T) {
	plan, route := newRoute(5)
	score := func(km, rating float64, fits bool) int {
		m := newMember("a", km)
		m.Driver.Rating = rating
		c := verdictFor(m, nil, plan, route).candidate
		c.fits = fits
		return c.score()
	}
	near, far := score(0.1, 4.95, true), score(10, 4.95, true)
	if near < 99 || far >= near {
		t.Errorf("score near = %d, far = %d: a driver right at the pickup with a top rating is about 100 and distance costs", near, far)
	}
	if score(1, 4.95, true) <= score(1, 4.2, true) {
		t.Error("a better rating must score higher")
	}
	if got, tooBig := score(1, 4.95, true), score(1, 4.95, false); got-tooBig != 60 {
		t.Errorf("a vehicle that is too small costs 60 points, got %d", got-tooBig)
	}
	if got := score(50, 3, false); got != 0 {
		t.Errorf("score = %d, want it clamped at 0", got)
	}
}

func TestDeparture(t *testing.T) {
	plan, route := newRoute(5)
	if got := departure(plan, route, pickup, now); !got.Equal(now.Add(2 * time.Minute)) {
		t.Errorf("a now route leaves in two minutes, got %v", got.Sub(now))
	}

	scheduled := plan
	scheduled.Schedule = contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{From: contracts.At(now.Add(3 * time.Hour))}}
	got := departure(scheduled, route, pickup, now)
	if !got.Before(now.Add(3*time.Hour)) || got.Before(now.Add(2*time.Hour+30*time.Minute)) {
		t.Errorf("a scheduled route leaves a little before its window opens (the drive to the pickup), got %v after now", got.Sub(now))
	}

	soon := scheduled
	soon.Schedule.Window = &contracts.TimeWindow{From: contracts.At(now.Add(time.Minute))}
	if got := departure(soon, route, pickup, now); !got.Equal(now.Add(2 * time.Minute)) {
		t.Errorf("a window that already opened leaves in two minutes, got %v", got.Sub(now))
	}
}
