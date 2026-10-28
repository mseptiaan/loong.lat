package job

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"
)

func valid() *Job {
	return &Job{
		Version: Version, ID: "j", DepartAt: time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC),
		Points: []Point{{Lat: -6.2, Lng: 106.8}, {Lat: -6.3, Lng: 106.9}}, DistM: []int32{0, 1000, 1000, 0},
		Fleet:     []FleetType{{Type: "motor", Count: 1, SpeedKmh: 30, Start: 0}},
		Shipments: []Shipment{{ID: "a", Pickup: 0, Dropoff: 1}},
		Presets:   Presets,
	}
}

func TestValidateAcceptsAWellFormedJobAndNamesWhatIsWrongOtherwise(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("a good job was refused: %v", err)
	}
	for name, mutate := range map[string]func(*Job){
		"version":            func(j *Job) { j.Version = 2 },
		"matrix size":        func(j *Job) { j.DistM = j.DistM[:3] },
		"no points":          func(j *Job) { j.Points, j.DistM = nil, nil },
		"no shipments":       func(j *Job) { j.Shipments = nil },
		"no fleet":           func(j *Job) { j.Fleet = nil },
		"no presets":         func(j *Job) { j.Presets = nil },
		"unknown preset":     func(j *Job) { j.Presets = []string{"x"} },
		"no departure":       func(j *Job) { j.DepartAt = time.Time{} },
		"repeated id":        func(j *Job) { j.Shipments = append(j.Shipments, j.Shipments[0]) },
		"point out of range": func(j *Job) { j.Shipments[0].Dropoff = 9 },
		"negative size":      func(j *Job) { j.Shipments[0].WeightKg = -1 },
		"zero speed":         func(j *Job) { j.Fleet[0].SpeedKmh = 0 },
		"repeated type":      func(j *Job) { j.Fleet = append(j.Fleet, j.Fleet[0]) },
	} {
		j := valid()
		mutate(j)
		var je *Error
		if err := j.Validate(); !errors.As(err, &je) {
			t.Errorf("%s: want a *job.Error, got %v", name, err)
		}
	}
}

func TestVersionMismatchHasItsOwnCode(t *testing.T) {
	j := valid()
	j.Version = 9
	var je *Error
	if err := j.Validate(); !errors.As(err, &je) || je.Code != CodeVersion {
		t.Errorf("got %v, want the version_mismatch code", err)
	}
}

func TestJobsSurviveTheWire(t *testing.T) {
	deadline := time.Date(2026, 10, 21, 5, 0, 0, 0, time.UTC)
	j := valid()
	j.Shipments[0].Deadline = &deadline
	data, err := j.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Shipments[0].Deadline == nil || !back.Shipments[0].Deadline.Equal(deadline) || len(back.DistM) != 4 || !back.DepartAt.Equal(j.DepartAt) {
		t.Errorf("round trip lost data: %+v", back)
	}
	// Unknown members are tolerated, so a newer caller does not break an older solver on additive changes.
	extra := strings.Replace(string(data), `"version":1`, `"version":1,"somethingNew":true`, 1)
	if _, err := Decode([]byte(extra)); err != nil {
		t.Errorf("an additive field must not break decoding: %v", err)
	}
}

func TestReadResultFollowsTheStream(t *testing.T) {
	var buf bytes.Buffer
	WriteEvent(&buf, Event{Type: EventProgress, Progress: &Progress{Done: 1, Total: 2}})
	WriteEvent(&buf, Event{Type: EventProgress, Progress: &Progress{Done: 2, Total: 2}})
	WriteEvent(&buf, Event{Type: EventResult, Result: &Result{Version: Version, Engine: "e", Candidates: []Candidate{{Preset: Cheapest}}}})

	var seen []Progress
	res, err := ReadResult(bytes.NewReader(buf.Bytes()), func(p Progress) { seen = append(seen, p) })
	if err != nil || res.Engine != "e" || len(res.Candidates) != 1 {
		t.Fatalf("result %+v err %v", res, err)
	}
	if len(seen) != 2 || seen[1].Done != 2 {
		t.Errorf("progress = %+v", seen)
	}

	var failed bytes.Buffer
	WriteEvent(&failed, Event{Type: EventError, Error: Fail(CodeInvalid, "no good")})
	var je *Error
	if _, err := ReadResult(&failed, nil); !errors.As(err, &je) || je.Code != CodeInvalid {
		t.Errorf("want the solver's error, got %v", err)
	}

	if _, err := ReadResult(strings.NewReader(""), nil); !errors.Is(err, ErrNoResult) {
		t.Errorf("an empty stream is ErrNoResult, got %v", err)
	}
	if _, err := ReadResult(strings.NewReader("not json\n"), nil); err == nil {
		t.Error("garbage must be an error")
	}
}

func TestValidateChecksTheSearch(t *testing.T) {
	good := func() *Search {
		return &Search{
			Experiments: []Experiment{{Algorithm: AlgKMedoids}, {Algorithm: AlgKMeans, K: 99, Effort: EffortQuick}, {Algorithm: AlgDBSCAN, EpsMinutes: 5}, {Algorithm: AlgNone, Effort: EffortNormal}},
			Polish:      EffortThorough,
			From:        map[string][]Route{Cheapest: {{Type: "motor", Stops: []Stop{{Shipment: "a", Kind: Pickup}}}}},
		}
	}
	j := valid()
	j.Search = good()
	if err := j.Validate(); err != nil {
		t.Fatalf("a good search was refused (a k beyond the shipments is the solver's to skip, not a wire error): %v", err)
	}
	for name, mutate := range map[string]func(*Search){
		"unknown algorithm": func(s *Search) { s.Experiments[1].Algorithm = "tabu" },
		"no algorithm":      func(s *Search) { s.Experiments[0].Algorithm = "" },
		"unknown effort":    func(s *Search) { s.Experiments[0].Effort = "extreme" },
		"negative k":        func(s *Search) { s.Experiments[1].K = -1 },
		"negative eps":      func(s *Search) { s.Experiments[2].EpsMinutes = -3 },
		"unknown polish":    func(s *Search) { s.Polish = "fast" },
	} {
		s := good()
		mutate(s)
		j := valid()
		j.Search = s
		var je *Error
		if err := j.Validate(); !errors.As(err, &je) || je.Code != CodeInvalid {
			t.Errorf("%s: want an invalid_job error, got %v", name, err)
		}
	}
}

func TestSearchAndItsRecordSurviveTheWire(t *testing.T) {
	j := valid()
	j.Search = &Search{
		Experiments: []Experiment{{Algorithm: AlgDBSCAN, EpsMinutes: 7.5, Effort: EffortQuick}, {Algorithm: AlgKMeans, K: 3}},
		From:        map[string][]Route{Balanced: {{Type: "motor", Stops: []Stop{{Shipment: "a", Kind: Pickup}, {Shipment: "a", Kind: Drop}}}}},
		Polish:      EffortNormal,
	}
	data, err := j.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, back.Search); !bytes.Equal(got, mustJSON(t, j.Search)) {
		t.Errorf("the search changed on the wire: %s", got)
	}

	want := &Result{Version: Version, Engine: "e", Stats: Stats{Trials: 5, Polished: true}, Candidates: []Candidate{{
		Preset: Cheapest, Score: []int64{0, 0, 45000, 9100, 1800},
		Trials: []Trial{
			{Kind: TrialExperiment, Algorithm: AlgKMedoids, K: 3, Effort: EffortNormal, Silhouette: 0.41, Valid: true, Unserved: 1, Iterations: 1500, Score: []int64{2, 0, 1}, Won: true},
			{Kind: TrialIncumbent}, {Kind: TrialPolish, Effort: EffortThorough, Valid: true},
		},
		Facts: &Facts{Valid: true, MaxOverlap: 0.2, ClusterPurity: 0.9, Hints: []string{"h"}, Unserved: []Unserved{{Shipment: "a", Cause: "too_heavy"}}},
	}}}
	var buf bytes.Buffer
	if err := WriteEvent(&buf, Event{Type: EventResult, Result: want}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadResult(&buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustJSON(t, got), mustJSON(t, want)) {
		t.Errorf("the result changed on the wire:\n%s\n%s", mustJSON(t, got), mustJSON(t, want))
	}
	c := got.Candidates[0]
	if c.Facts == nil || c.Facts.Unserved[0].Cause != "too_heavy" || len(c.Trials) != 3 || !c.Trials[0].Won || c.Score[2] != 45000 || !got.Stats.Polished {
		t.Errorf("a field was lost: %+v", c)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
