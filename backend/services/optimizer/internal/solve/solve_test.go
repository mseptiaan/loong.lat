package solve

import (
	"context"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/fare"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// Places in South Jakarta, as indexes into the job's points.
var places = []job.Point{
	{Lat: -6.2608, Lng: 106.8133}, // 0 Kemang
	{Lat: -6.2275, Lng: 106.8029}, // 1 Senayan
	{Lat: -6.2441, Lng: 106.7993}, // 2 Blok M
	{Lat: -6.2653, Lng: 106.7836}, // 3 Pondok Indah
	{Lat: -6.2297, Lng: 106.8137}, // 4 Senopati
	{Lat: -6.2088, Lng: 106.8456}, // 5 Monas
}

var (
	depart    = time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC) // 10:00 WIB
	motorFare = job.Fare{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargeAboveKg: 10, SurchargePerKg: 800}
	vanFare   = job.Fare{Base: 45000, PerKm: 4500, PerMin: 500, PerExtraStop: 8000, SurchargeAboveKg: 300, SurchargePerKg: 150}
)

// testJob builds a job over places with the loong motorbike and blindvan fleet.
func testJob(shipments ...job.Shipment) *job.Job {
	n := len(places)
	dist := make([]int32, n*n)
	for a := range n {
		for b := range n {
			if a != b {
				dist[a*n+b] = int32(math.Round(geo.Haversine(geo.LatLon{Lat: places[a].Lat, Lon: places[a].Lng}, geo.LatLon{Lat: places[b].Lat, Lon: places[b].Lng}) * 1.28))
			}
		}
	}
	return &job.Job{
		Version: job.Version, ID: "test", DepartAt: depart, Points: places, DistM: dist,
		Fleet: []job.FleetType{
			{Type: "motor", Count: 6, SpeedKmh: 28, Start: 0, Capacity: job.Capacity{WeightKg: 20, VolumeM3: 0.12, MaxDimensionCm: 55}, Fare: motorFare},
			{Type: "blindvan", Count: 6, SpeedKmh: 22, Start: 0, Capacity: job.Capacity{WeightKg: 500, VolumeM3: 3.2, MaxDimensionCm: 190}, Fare: vanFare},
		},
		Shipments: shipments,
		Presets:   job.Presets,
		Dwell:     job.Dwell{Pickup: 240, Drop: 180},
	}
}

func parcel(id string, pickup, drop int, kg float64) job.Shipment {
	return job.Shipment{ID: id, Pickup: pickup, Dropoff: drop, WeightKg: kg, VolumeM3: 0.01, MaxDimensionCm: 30}
}

func run(t *testing.T, j *job.Job) *job.Result {
	t.Helper()
	res, err := Run(context.Background(), j, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// checkRoutes verifies that the candidate serves every shipment once, pickup before drop.
func checkRoutes(t *testing.T, j *job.Job, c job.Candidate) {
	t.Helper()
	picked, dropped := map[string]bool{}, map[string]bool{}
	for _, r := range c.Routes {
		for _, s := range r.Stops {
			switch s.Kind {
			case job.Pickup:
				if picked[s.Shipment] {
					t.Errorf("%s: %s is picked up twice", c.Preset, s.Shipment)
				}
				picked[s.Shipment] = true
			case job.Drop:
				if !picked[s.Shipment] {
					t.Errorf("%s: %s is dropped before it is picked up", c.Preset, s.Shipment)
				}
				dropped[s.Shipment] = true
			}
		}
	}
	for _, s := range j.Shipments {
		if !picked[s.ID] || !dropped[s.ID] {
			t.Errorf("%s: shipment %s is not served", c.Preset, s.ID)
		}
	}
}

func TestOneShipmentIsOneRouteOnTheSmallestVehicle(t *testing.T) {
	j := testJob(parcel("a", 0, 1, 3))
	res := run(t, j)
	if len(res.Candidates) != len(job.Presets) {
		t.Fatalf("%d candidates, want one per preset", len(res.Candidates))
	}
	for _, c := range res.Candidates {
		checkRoutes(t, j, c)
		if len(c.Routes) != 1 || c.Routes[0].Type != "motor" {
			t.Errorf("%s: want one motor route, got %+v", c.Preset, c.Routes)
		}
		if c.Proof != job.ProofOptimal {
			t.Errorf("%s: one shipment is solved exhaustively, proof = %q", c.Preset, c.Proof)
		}
	}
}

func TestSeveralShipmentsAreAllServedUnderEveryPreset(t *testing.T) {
	j := testJob(parcel("a", 0, 1, 3), parcel("b", 2, 3, 4), parcel("c", 4, 5, 2), parcel("d", 1, 2, 6))
	res := run(t, j)
	for _, c := range res.Candidates {
		checkRoutes(t, j, c)
		if c.Metrics.CostMilli <= 0 || c.Metrics.DistM <= 0 {
			t.Errorf("%s: empty metrics %+v", c.Preset, c.Metrics)
		}
	}
	if len(res.Unserved) != 0 {
		t.Errorf("unserved: %+v", res.Unserved)
	}
}

func TestPresetsDifferInWhatTheyOptimize(t *testing.T) {
	j := testJob(parcel("a", 0, 1, 3), parcel("b", 2, 3, 4), parcel("c", 4, 5, 2), parcel("d", 1, 2, 6))
	byPreset := map[string]job.Metrics{}
	for _, c := range run(t, j).Candidates {
		byPreset[c.Preset] = c.Metrics
	}
	cheap, short, fast := byPreset[job.Cheapest], byPreset[job.Shortest], byPreset[job.Fastest]
	for name, other := range map[string]job.Metrics{"shortest": short, "fastest": fast, "balanced": byPreset[job.Balanced]} {
		if cheap.CostMilli > other.CostMilli {
			t.Errorf("the cheapest plan costs %d, but %s costs only %d", cheap.CostMilli, name, other.CostMilli)
		}
	}
	for name, other := range map[string]job.Metrics{"cheapest": cheap, "fastest": fast, "balanced": byPreset[job.Balanced]} {
		if short.DistM > other.DistM {
			t.Errorf("the shortest plan drives %d m, but %s drives only %d m", short.DistM, name, other.DistM)
		}
	}
	for name, other := range map[string]job.Metrics{"cheapest": cheap, "shortest": short, "balanced": byPreset[job.Balanced]} {
		if fast.MakespanSec > other.MakespanSec {
			t.Errorf("the fastest plan ends after %d s, but %s ends after only %d s", fast.MakespanSec, name, other.MakespanSec)
		}
	}
}

func TestHeavyOrBulkyShipmentsGoToTheVan(t *testing.T) {
	heavy := parcel("heavy", 0, 1, 80) // above the motorbike's 20 kg
	bulky := job.Shipment{ID: "bulky", Pickup: 2, Dropoff: 3, WeightKg: 5, VolumeM3: 0.05, MaxDimensionCm: 120}
	j := testJob(heavy, bulky)
	for _, c := range run(t, j).Candidates {
		checkRoutes(t, j, c)
		for _, r := range c.Routes {
			if r.Type != "blindvan" {
				t.Errorf("%s: a %s route carries %+v, only the blindvan takes these", c.Preset, r.Type, r.Stops)
			}
		}
	}
}

func TestAShipmentNoVehicleTakesIsReportedUnserved(t *testing.T) {
	j := testJob(parcel("ok", 0, 1, 3), job.Shipment{ID: "huge", Pickup: 2, Dropoff: 3, WeightKg: 900, VolumeM3: 1, MaxDimensionCm: 30})
	res := run(t, j)
	if len(res.Unserved) != 1 || res.Unserved[0].Shipment != "huge" || res.Unserved[0].Cause == "" {
		t.Fatalf("unserved = %+v, want the huge shipment with a cause", res.Unserved)
	}
	for _, c := range res.Candidates {
		for _, r := range c.Routes {
			for _, s := range r.Stops {
				if s.Shipment == "huge" {
					t.Errorf("%s: the unservable shipment was routed", c.Preset)
				}
			}
		}
	}
}

func TestADeadlineSteersTheOrder(t *testing.T) {
	// b is far from the start and has a tight deadline; a is near. Serving b first meets it.
	a, b := parcel("a", 0, 1, 2), parcel("b", 4, 5, 2)
	due := depart.Add(30 * time.Minute)
	b.Deadline = &due
	j := testJob(a, b)
	j.Presets = []string{job.Cheapest}
	met := run(t, j).Candidates[0]
	if met.Metrics.LateSec != 0 {
		t.Fatalf("the deadline is reachable, yet the plan is %d s late: %+v", met.Metrics.LateSec, met.Routes)
	}

	// An impossible deadline does not unserve the shipment: it is served, late.
	tooSoon := depart.Add(time.Minute)
	b.Deadline = &tooSoon
	late := run(t, testJob(a, b))
	checkRoutes(t, j, late.Candidates[0])
	if late.Candidates[0].Metrics.LateSec <= 0 {
		t.Error("an impossible deadline must show up as lateness")
	}
}

func TestSameJobSameResult(t *testing.T) {
	j := testJob(parcel("a", 0, 1, 3), parcel("b", 2, 3, 4), parcel("c", 4, 5, 2))
	first, second := run(t, j), run(t, j)
	first.Stats.ElapsedMs, second.Stats.ElapsedMs = 0, 0
	if !reflect.DeepEqual(first, second) {
		t.Errorf("the same job gave different results:\n%+v\n%+v", first, second)
	}
}

// The solver minimizes the tariff itself: for a route, its cost must equal the
// fare computed from the route's legs, before rounding to Rp 500.
func TestSolverCostEqualsTheTariff(t *testing.T) {
	j := testJob(parcel("a", 0, 1, 3), parcel("b", 2, 3, 4), parcel("c", 4, 5, 12))
	j.Presets = []string{job.Cheapest}
	c := run(t, j).Candidates[0]
	var total float64
	for _, r := range c.Routes {
		f := motorFare
		speed := 28.0
		if r.Type == "blindvan" {
			f, speed = vanFare, 22
		}
		var km, sec, kg float64
		prev := -1
		for i, s := range r.Stops {
			sh := shipment(j, s.Shipment)
			at := sh.Pickup
			dwell := float64(j.Dwell.Pickup)
			if s.Kind == job.Drop {
				at, dwell = sh.Dropoff, float64(j.Dwell.Drop)
			} else {
				kg += sh.WeightKg
			}
			if i > 0 { // the way to the first stop is not billed
				leg := float64(j.DistM[prev*len(j.Points)+at])
				km += leg / 1000
				sec += leg / (speed / 3.6)
			}
			sec += dwell
			prev = at
		}
		lines, _ := fare.Compute(f, fare.Load{Km: km, Min: sec / 60, Stops: len(r.Stops), WeightKg: kg})
		for _, l := range lines {
			if l.Key != fare.KeyRounding {
				total += float64(l.AmountRp)
			}
		}
	}
	if got := float64(c.Metrics.CostMilli) / 1000; math.Abs(got-total) > 3 {
		t.Errorf("the solver's cost is Rp %.1f, the tariff of the same routes is Rp %.1f", got, total)
	}
}

func shipment(j *job.Job, id string) job.Shipment {
	i := slices.IndexFunc(j.Shipments, func(s job.Shipment) bool { return s.ID == id })
	return j.Shipments[i]
}

func TestInvalidJobsAreRejected(t *testing.T) {
	good := func() *job.Job { return testJob(parcel("a", 0, 1, 3)) }
	for name, mutate := range map[string]func(*job.Job){
		"version":      func(j *job.Job) { j.Version = 99 },
		"matrix":       func(j *job.Job) { j.DistM = j.DistM[:3] },
		"point":        func(j *job.Job) { j.Shipments[0].Dropoff = 42 },
		"no shipments": func(j *job.Job) { j.Shipments = nil },
		"preset":       func(j *job.Job) { j.Presets = []string{"nope"} },
		"engine":       func(j *job.Job) { j.Engine = "someone-else" },
		"base fare":    func(j *job.Job) { j.Fleet[0].Fare.Base = 1000 }, // below twice the extra-stop fare
	} {
		j := good()
		mutate(j)
		if _, err := Run(context.Background(), j, nil); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestCancellingEndsTheJobWithValidCandidates(t *testing.T) {
	shipments := make([]job.Shipment, 0, 8)
	for i := range 8 {
		shipments = append(shipments, parcel(string(rune('a'+i)), i%5, (i+2)%6, 2))
	}
	j := testJob(shipments...)
	j.Limits.Iterations = 50_000_000
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := Run(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("the job ignored cancellation and took %v", took)
	}
	for _, c := range res.Candidates {
		checkRoutes(t, j, c)
		if !c.Truncated {
			t.Errorf("%s: a cancelled search must be marked truncated", c.Preset)
		}
	}
}
