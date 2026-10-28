package main

import (
	"bytes"
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

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

func TestRunStreamsProgressThenResult(t *testing.T) {
	data, err := sampleJob().Encode()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run(bytes.NewReader(data), &out); code != 0 {
		t.Fatalf("exit code %d, output:\n%s", code, out.String())
	}
	var progress []job.Progress
	res, err := job.ReadResult(&out, func(p job.Progress) { progress = append(progress, p) })
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != len(job.Presets) || progress[len(progress)-1].Done != len(job.Presets) {
		t.Errorf("progress = %+v, want one event per preset ending at %d", progress, len(job.Presets))
	}
	if len(res.Candidates) != len(job.Presets) {
		t.Errorf("%d candidates, want %d", len(res.Candidates), len(job.Presets))
	}
}

func TestRunReportsABadJobAsAnErrorEvent(t *testing.T) {
	for name, in := range map[string]string{"not json": "{", "wrong version": `{"version":99}`} {
		var out bytes.Buffer
		if code := run(strings.NewReader(in), &out); code != 1 {
			t.Errorf("%s: exit code %d, want 1", name, code)
		}
		if _, err := job.ReadResult(&out, nil); err == nil {
			t.Errorf("%s: want an error from the stream", name)
		} else if _, ok := err.(*job.Error); !ok {
			t.Errorf("%s: want a *job.Error, got %T %v", name, err, err)
		}
	}
}

// searchJob is a job of six shipments between three places, with a Search in it: the default experiments and a polish.
func searchJob() *job.Job {
	j := sampleJob()
	j.Limits.Iterations = 100
	j.Shipments = nil
	for i, id := range []string{"a", "b", "c", "d", "e", "f"} {
		pickup, drop := 1, 2
		if i%2 == 1 {
			pickup, drop = 2, 1
		}
		j.Shipments = append(j.Shipments, job.Shipment{ID: id, Pickup: pickup, Dropoff: drop, WeightKg: 2, VolumeM3: 0.01, MaxDimensionCm: 20})
	}
	j.Search = &job.Search{
		Experiments: []job.Experiment{
			{Algorithm: job.AlgKMedoids}, {Algorithm: job.AlgKMeans, K: 2, Effort: job.EffortQuick}, {Algorithm: job.AlgNone, Effort: job.EffortNormal},
		},
		Polish: job.EffortQuick,
	}
	return j
}

func solveOverStdio(t *testing.T, j *job.Job) (*job.Result, []job.Progress) {
	t.Helper()
	data, err := j.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run(bytes.NewReader(data), &out); code != 0 {
		t.Fatalf("exit code %d, output:\n%s", code, out.String())
	}
	var progress []job.Progress
	res, err := job.ReadResult(&out, func(p job.Progress) { progress = append(progress, p) })
	if err != nil {
		t.Fatal(err)
	}
	return res, progress
}

func TestRunRoundTripsAJobWithASearch(t *testing.T) {
	j := searchJob()
	res, progress := solveOverStdio(t, j)
	if len(progress) != len(job.Presets) {
		t.Errorf("progress = %+v, want one event per preset", progress)
	}
	if want := len(job.Presets) * 4; res.Stats.Trials != want || !res.Stats.Polished {
		t.Errorf("stats = %+v, want %d trials (three experiments and a polish per preset) and a polish", res.Stats, want)
	}
	for _, c := range res.Candidates {
		if len(c.Score) == 0 || c.Facts == nil || !c.Facts.Valid || len(c.Trials) != 4 {
			t.Errorf("%s: the search record did not come through the stream: %+v", c.Preset, c)
			continue
		}
		wins := 0
		for _, tr := range c.Trials {
			if tr.Won {
				wins++
			}
		}
		if wins != 1 || c.Trials[3].Kind != job.TrialPolish {
			t.Errorf("%s: trials %+v, want one winner and the polish last", c.Preset, c.Trials)
		}
	}

	// What came out can go back in as the incumbent of the next job, over the same pipe.
	from := map[string][]job.Route{}
	for _, c := range res.Candidates {
		from[c.Preset] = c.Routes
	}
	next := searchJob()
	next.Search = &job.Search{From: from}
	again, _ := solveOverStdio(t, next)
	for i, c := range again.Candidates {
		if slices.Compare(c.Score, res.Candidates[i].Score) != 0 || len(c.Trials) != 1 || c.Trials[0].Kind != job.TrialIncumbent || !c.Trials[0].Valid {
			t.Errorf("%s: an incumbent alone must come back as it went in: %+v", c.Preset, c)
		}
	}
}

func TestRunRefusesASearchItCannotRun(t *testing.T) {
	j := searchJob()
	j.Search.Experiments = append(j.Search.Experiments, job.Experiment{Algorithm: "simulated-annealing"})
	data, err := j.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run(bytes.NewReader(data), &out); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	var je *job.Error
	if _, err := job.ReadResult(&out, nil); err == nil || !strings.Contains(err.Error(), "simulated-annealing") {
		t.Errorf("want the invalid_job error to name the algorithm, got %v", err)
	} else if je, _ = err.(*job.Error); je == nil || je.Code != job.CodeInvalid {
		t.Errorf("want code %s, got %v", job.CodeInvalid, err)
	}
}

// An older solver must not choke on a job from a newer caller that sends search members it does not know.
func TestRunToleratesUnknownMembersInTheSearch(t *testing.T) {
	j := searchJob()
	j.Search = &job.Search{Experiments: []job.Experiment{{Algorithm: job.AlgNone, Effort: job.EffortQuick}}}
	data, err := j.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	generic["search"].(map[string]any)["somethingNew"] = true
	data, _ = json.Marshal(generic)
	var out bytes.Buffer
	if code := run(bytes.NewReader(data), &out); code != 0 {
		t.Fatalf("exit code %d, output:\n%s", code, out.String())
	}
}
