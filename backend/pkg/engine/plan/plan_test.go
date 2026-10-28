package plan

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"PT5M", 5 * time.Minute, false},
		{"PT30M", 30 * time.Minute, false},
		{"PT6H", 6 * time.Hour, false},
		{"PT1H30M", 90 * time.Minute, false},
		{"PT45S", 45 * time.Second, false},
		{"PT0.5H", 30 * time.Minute, false},
		{"P1DT2H", 26 * time.Hour, false},
		{"", 0, true},
		{"P", 0, true},
		{"PT", 0, true},
		{"5M", 0, true},
		{"PT5X", 0, true},
		{"P1M", 0, true}, // months are ambiguous and unsupported
		{"PT5", 0, true},
	}
	for _, tc := range tests {
		got, err := ParseDuration(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseDuration(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if err == nil && got != tc.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFormatDurationRoundTrip(t *testing.T) {
	for _, d := range []time.Duration{0, 5 * time.Minute, 90 * time.Minute, 9 * time.Hour, 37 * time.Second} {
		got, err := ParseDuration(FormatDuration(d))
		if err != nil || got != d {
			t.Errorf("round trip of %v = %v, %v", d, got, err)
		}
	}
}

func TestLoadRepositoryPlan(t *testing.T) {
	p, warnings, err := Load("../testdata/plan.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if got, want := len(p.Resources), 3; got != want {
		t.Errorf("resources = %d, want %d", got, want)
	}
	if got, want := len(p.Orders), 22; got != want {
		t.Errorf("orders = %d, want %d", got, want)
	}
	truck := p.Resources[0]
	if truck.MaxWorkingDuration.Std() != 9*time.Hour {
		t.Errorf("maxWorkingDuration = %v", truck.MaxWorkingDuration.Std())
	}
	if len(truck.Breaks) != 1 || truck.Breaks[0].MaxInterBreakDuration.Std() != 6*time.Hour {
		t.Errorf("breaks not decoded: %+v", truck.Breaks)
	}
	if truck.Cost == nil || truck.Cost.WorkedHours == nil || truck.Cost.WorkedHours.CostFloor != 8 {
		t.Errorf("cost not decoded: %+v", truck.Cost)
	}
	if len(p.GlobalConstraints) != 1 || p.GlobalConstraints[0].Maximum != 1 {
		t.Errorf("global constraints not decoded: %+v", p.GlobalConstraints)
	}
	if p.Resources[2].Capacities != nil {
		t.Errorf("driver-1 must have no capacities, got %v", p.Resources[2].Capacities)
	}
}

func TestParseRejectsInvalidPlans(t *testing.T) {
	const noOrders = `{"resources":[{"id":"a","departure":{"lon":1,"lat":1}}],"orders":[]}`
	if _, _, err := Parse([]byte(noOrders)); err == nil {
		t.Error("expected an error for a plan without orders")
	}
	const badKind = `{"resources":[{"id":"a","departure":{"lon":1,"lat":1}}],
		"orders":[{"id":"o","stops":[{"id":"s","kind":"teleport","position":{"lon":1,"lat":1}}]}]}`
	if _, _, err := Parse([]byte(badKind)); err == nil {
		t.Error("expected an error for an unknown stop kind")
	}
}

func TestParseWarnsOnUnknownFields(t *testing.T) {
	const src = `{"resources":[{"id":"a","departure":{"lon":1,"lat":1},"mystery":true}],
		"orders":[{"id":"o","stops":[{"id":"s","kind":"delivery","position":{"lon":1,"lat":1}}]}]}`
	_, warnings, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning about the unknown field")
	}
}
