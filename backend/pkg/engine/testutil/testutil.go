// Package testutil offers fixtures shared by the tests of several engine packages.
package testutil

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"
)

// RepoPlanPath returns the absolute path of the testdata plan.json.
func RepoPlanPath() string { return RepoFile("plan.json") }

// RepoFile returns the absolute path of a file in pkg/engine/testdata.
func RepoFile(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata", name)
}

// Road estimate constants used when no routing data is available: straight-line
// distance times a detour factor, travelled at an average urban speed.
const (
	roadFactor   = 1.35
	fallbackKmh  = 40.0
	metersPerSec = fallbackKmh * 1000 / 3600
)

// EstimateMatrix builds a complete offline travel matrix (n, durations in s,
// distances in m, row-major) from straight-line distances.
func EstimateMatrix(coords []geo.LatLon) (n int, dur, dist []float64) {
	n = len(coords)
	dur, dist = make([]float64, n*n), make([]float64, n*n)
	for i := range n {
		for j := range n {
			if i == j {
				continue
			}
			d := geo.Haversine(coords[i], coords[j]) * roadFactor
			dist[i*n+j], dur[i*n+j] = d, d/metersPerSec
		}
	}
	return n, dur, dist
}

// RepoProblem compiles the repository plan with offline (straight-line) travel
// estimates, so tests need no network access.
func RepoProblem(tb testing.TB) *model.Problem {
	tb.Helper()
	return RepoProblemFile(tb, "plan.json")
}

// RepoProblemFile is RepoProblem for another plan file in the repository root.
func RepoProblemFile(tb testing.TB, name string) *model.Problem {
	tb.Helper()
	pl, _, err := plan.Load(RepoFile(name))
	if err != nil {
		tb.Fatal(err)
	}
	p, err := model.Compile(pl, model.Options{})
	if err != nil {
		tb.Fatal(err)
	}
	n, dur, dist := EstimateMatrix(p.Locs)
	mats := make([]*model.Matrix, len(p.Profiles))
	for i := range mats {
		mats[i] = model.NewMatrix(n, dur, dist, "estimate")
	}
	if err := p.SetMatrices(mats); err != nil {
		tb.Fatal(err)
	}
	return p
}

// Synthetic builds a random but reproducible problem around Ottawa with n
// orders (a third of them pickup-and-delivery from the depot) and v vehicles
// of limited capacity and shift length, with offline travel estimates.
func Synthetic(tb testing.TB, n, v int, seed uint64) *model.Problem {
	tb.Helper()
	rng := rand.New(rand.NewPCG(seed, seed+1))
	depot := plan.Position{Lon: -75.7003, Lat: 45.4236}
	day := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)

	pl := &plan.Plan{
		TZ: "America/Toronto", Properties: map[string]any{"name": fmt.Sprintf("synthetic-%d", n)},
		Objectives: []string{"maximizeMandatoryStops", "minimizeDistance", "minimizeLargestTourDuration"},
	}
	for i := range v {
		pl.Resources = append(pl.Resources, plan.Resource{
			ID: fmt.Sprintf("v%d", i+1), VehicleProfile: plan.VehicleProfile{Type: "truck"},
			Departure: &depot, Arrival: &depot,
			WorkingTimeWindow:  &plan.TimeWindow{Begin: day, End: day.Add(14 * time.Hour)},
			Capacities:         map[string]float64{"w": 120},
			MaxWorkingDuration: plan.Duration(9 * time.Hour),
		})
	}
	for i := range n {
		pos := plan.Position{Lon: depot.Lon + rng.NormFloat64()*0.14, Lat: depot.Lat + rng.NormFloat64()*0.09}
		qty := float64(1 + rng.IntN(5))
		del := plan.Stop{Type: "single", ID: fmt.Sprintf("d%d", i), Position: pos, Kind: plan.KindDelivery,
			OperationDuration: plan.Duration(time.Duration(4+rng.IntN(7)) * time.Minute), Capacities: map[string]float64{"w": qty}}
		o := plan.Order{ID: fmt.Sprintf("o%d", i)}
		if i%3 == 0 {
			pick := plan.Stop{Type: "single", ID: fmt.Sprintf("p%d", i), Position: depot, Kind: plan.KindPickup,
				OperationDuration: plan.Duration(2 * time.Minute), Capacities: map[string]float64{"w": qty}}
			o.Stops, o.SuccessiveStops = []plan.Stop{pick, del}, true
		} else {
			o.Stops = []plan.Stop{del}
		}
		pl.Orders = append(pl.Orders, o)
	}
	p, err := model.Compile(pl, model.Options{})
	if err != nil {
		tb.Fatal(err)
	}
	n, dur, dist := EstimateMatrix(p.Locs)
	mats := make([]*model.Matrix, len(p.Profiles))
	for i := range mats {
		mats[i] = model.NewMatrix(n, dur, dist, "estimate")
	}
	if err := p.SetMatrices(mats); err != nil {
		tb.Fatal(err)
	}
	return p
}
