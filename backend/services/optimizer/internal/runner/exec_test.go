package runner

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// buildSolver compiles cmd/solver into a temporary directory.
func buildSolver(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the solver binary")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	bin := filepath.Join(t.TempDir(), "solver")
	cmd := exec.Command("go", "build", "-o", bin, "./services/optimizer/cmd/solver")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func sampleJob() *job.Job {
	pts := []job.Point{{Lat: -6.2608, Lng: 106.8133}, {Lat: -6.2275, Lng: 106.8029}, {Lat: -6.2441, Lng: 106.7993}}
	return &job.Job{
		Version: job.Version, ID: "t", DepartAt: time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC), Points: pts,
		DistM: []int32{0, 4800, 3500, 4800, 0, 2400, 3500, 2400, 0},
		Fleet: []job.FleetType{{Type: "motor", Count: 2, SpeedKmh: 28, Start: 0,
			Capacity: job.Capacity{WeightKg: 20, VolumeM3: 0.12, MaxDimensionCm: 55},
			Fare:     job.Fare{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargeAboveKg: 10, SurchargePerKg: 800}}},
		Shipments: []job.Shipment{{ID: "a", Pickup: 1, Dropoff: 2, WeightKg: 2, VolumeM3: 0.01, MaxDimensionCm: 20}},
		Presets:   job.Presets, Dwell: job.Dwell{Pickup: 240, Drop: 180},
	}
}

func TestExecRunnerSpeaksTheSandboxContract(t *testing.T) {
	bin := buildSolver(t)
	var progress int
	res, err := Exec{Path: bin}.Run(context.Background(), sampleJob(), func(job.Progress) { progress++ })
	if err != nil {
		t.Fatal(err)
	}
	if progress != len(job.Presets) || len(res.Candidates) != len(job.Presets) {
		t.Errorf("progress events %d, candidates %d, want %d each", progress, len(res.Candidates), len(job.Presets))
	}

	// The in-process runner must give the same routes as the child process.
	local, err := Local{}.Run(context.Background(), sampleJob(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range res.Candidates {
		if res.Candidates[i].Metrics != local.Candidates[i].Metrics {
			t.Errorf("%s: exec %+v vs local %+v", res.Candidates[i].Preset, res.Candidates[i].Metrics, local.Candidates[i].Metrics)
		}
	}
}

func TestExecRunnerSurfacesJobErrorsAndMissingBinaries(t *testing.T) {
	bin := buildSolver(t)
	bad := sampleJob()
	bad.Version = 99
	if _, err := (Exec{Path: bin}).Run(context.Background(), bad, nil); err == nil {
		t.Error("a job the solver rejects must fail")
	} else if _, ok := err.(*job.Error); !ok {
		t.Errorf("want the solver's *job.Error, got %T %v", err, err)
	}
	if _, err := (Exec{Path: "/nonexistent/solver"}).Run(context.Background(), sampleJob(), nil); !isUnavailable(err) {
		t.Errorf("a missing binary is an infrastructure failure, got %v", err)
	}
}

func isUnavailable(err error) bool { return err != nil && errorsIs(err, ErrUnavailable) }

func sampleJobBytes() []byte {
	data, err := sampleJob().Encode()
	if err != nil {
		panic(err)
	}
	return data
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
