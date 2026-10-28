// Command modalprobe measures what a Modal sandbox costs this service: it runs a
// sample job through the Modal runner several times and reports how long each
// run takes, how much it costs at Modal's published sandbox rates, and whether
// it agrees with the in-process solver. Run it once after the image is published
// and whenever the sandbox settings change.
//
//	go run ./services/optimizer/cmd/modalprobe -n 10   # after scripts/publish-solver.sh
//
// Credentials come from MODAL_TOKEN_ID / MODAL_TOKEN_SECRET or ~/.modal.toml.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/app"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/roads"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

// Modal's published sandbox rates (modal.com/pricing): USD per physical core-second and per GiB-second.
const (
	usdPerCoreSec = 0.00003942
	usdPerGiBSec  = 0.00000667
)

func main() {
	n := flag.Int("n", 5, "jobs to run")
	shipments := flag.Int("shipments", 4, "shipments per job (up to 6)")
	flag.Parse()
	if err := run(*n, *shipments); err != nil {
		fmt.Fprintln(os.Stderr, "modalprobe:", err)
		os.Exit(1)
	}
}

// sample builds a job of k parcels between neighbourhoods of South Jakarta.
func sample(k int) *job.Job {
	pts := []job.Point{
		{Lat: -6.2608, Lng: 106.8133}, {Lat: -6.2275, Lng: 106.8029}, {Lat: -6.2441, Lng: 106.7993},
		{Lat: -6.2653, Lng: 106.7836}, {Lat: -6.2297, Lng: 106.8137}, {Lat: -6.2088, Lng: 106.8456},
	}
	n := len(pts)
	dist := make([]int32, n*n)
	for a := range n {
		for b := range n {
			if a != b {
				dist[a*n+b] = roads.EstimateMeters(latlng(pts[a]), latlng(pts[b]))
			}
		}
	}
	fare := job.Fare{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargeAboveKg: 10, SurchargePerKg: 800}
	j := &job.Job{
		Version: job.Version, ID: "modalprobe", DepartAt: time.Now().UTC().Add(10 * time.Minute), Points: pts, DistM: dist,
		Fleet: []job.FleetType{
			{Type: "motor", Count: k, SpeedKmh: 28, Start: 0, Capacity: job.Capacity{WeightKg: 20, VolumeM3: 0.12, MaxDimensionCm: 55}, Fare: fare},
			{Type: "blindvan", Count: k, SpeedKmh: 22, Start: 0, Capacity: job.Capacity{WeightKg: 500, VolumeM3: 3.2, MaxDimensionCm: 190},
				Fare: job.Fare{Base: 45000, PerKm: 4500, PerMin: 500, PerExtraStop: 8000, SurchargeAboveKg: 300, SurchargePerKg: 150}},
		},
		Presets: job.Presets, Dwell: job.Dwell{Pickup: 240, Drop: 180},
	}
	for i := range k {
		j.Shipments = append(j.Shipments, job.Shipment{
			ID: fmt.Sprintf("s%d", i+1), Pickup: i % n, Dropoff: (i + 2) % n, WeightKg: float64(2 + i), VolumeM3: 0.01, MaxDimensionCm: 30,
		})
	}
	return j
}

func run(n, shipments int) error {
	if err := platform.LoadDotEnv(".env"); err != nil {
		return err
	}
	env := platform.NewEnv()
	cfg := app.ModalFromEnv(env)
	if err := env.Err(); err != nil {
		return err
	}
	ctx := context.Background()

	fmt.Printf("solver %s from volume %q, pool of %d, %.0f cores, %d MiB\n", cfg.SolverVersion, cfg.Volume, cfg.PoolSize, cfg.CPU, cfg.MemoryMiB)
	start := time.Now()
	m, err := runner.NewModal(ctx, cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		return err
	}
	defer m.Close()
	fmt.Printf("connected in %v (prewarm %d runs in the background)\n\n", time.Since(start).Round(time.Millisecond), cfg.Prewarm)

	want, err := runner.Local{}.Run(ctx, sample(shipments), nil)
	if err != nil {
		return fmt.Errorf("local reference: %w", err)
	}
	same := func(got *job.Result) bool {
		if len(got.Candidates) != len(want.Candidates) {
			return false
		}
		for k := range got.Candidates {
			if got.Candidates[k].Metrics != want.Candidates[k].Metrics {
				return false
			}
		}
		return true
	}

	var times []time.Duration
	var mismatches atomic.Int32
	for i := range n {
		t := time.Now()
		got, err := m.Run(ctx, sample(shipments), nil)
		d := time.Since(t)
		if err != nil {
			return fmt.Errorf("job %d: %w", i+1, err)
		}
		times = append(times, d)
		if !same(got) {
			mismatches.Add(1)
		}
		label := "warm"
		if i == 0 {
			label = "first (may include a cold start)"
		}
		fmt.Printf("job %2d  %8v  engine %-26s  %s\n", i+1, d.Round(time.Millisecond), got.Engine, label)
	}

	// Several jobs at once: the pool serves up to its size in parallel and queues the rest.
	if cfg.PoolSize > 0 {
		burst := cfg.PoolSize * 2
		fmt.Printf("\n%d jobs at once on a pool of %d:\n", burst, cfg.PoolSize)
		var wg sync.WaitGroup
		took := make([]time.Duration, burst)
		for i := range burst {
			wg.Go(func() {
				t := time.Now()
				got, err := m.Run(ctx, sample(shipments), nil)
				took[i] = time.Since(t)
				if err != nil || !same(got) {
					mismatches.Add(1)
				}
			})
		}
		wg.Wait()
		for i, d := range took {
			fmt.Printf("  burst job %d  %v\n", i+1, d.Round(time.Millisecond))
		}
	}

	warm := append([]time.Duration(nil), times[min(1, len(times)-1):]...)
	slices.Sort(warm)
	pct := func(p float64) time.Duration { return warm[min(len(warm)-1, int(math.Ceil(p*float64(len(warm))))-1)] }
	var total time.Duration
	for _, d := range warm {
		total += d
	}
	avg := total / time.Duration(len(warm))
	perJob := avg.Seconds() * (cfg.CPU*usdPerCoreSec + float64(cfg.MemoryMiB)/1024*usdPerGiBSec)
	fmt.Printf("\nwarm jobs: p50 %v  p95 %v  max %v\n", pct(0.5).Round(time.Millisecond), pct(0.95).Round(time.Millisecond), warm[len(warm)-1].Round(time.Millisecond))
	fmt.Printf("about $%.5f of sandbox time per warm job; an idle warm sandbox costs about $%.2f an hour until its idle timeout (%v) ends it\n",
		perJob, 3600*(cfg.CPU*usdPerCoreSec+float64(cfg.MemoryMiB)/1024*usdPerGiBSec), cfg.IdleTimeout)
	if n := mismatches.Load(); n > 0 {
		fmt.Printf("WARNING: %d job(s) differ from the in-process solver. Expected only if the architectures' floating point differs; check the objective values.\n", n)
	} else {
		fmt.Println("every result matches the in-process solver")
	}
	return nil
}

func latlng(p job.Point) contracts.LatLng { return contracts.LatLng{Lat: p.Lat, Lng: p.Lng} }
