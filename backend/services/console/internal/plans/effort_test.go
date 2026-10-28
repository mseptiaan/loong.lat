package plans

import (
	"context"
	"encoding/json/v2"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

func TestComputeForwardsTheEffortAndDefaultsToFast(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1", "s2")
	for _, tc := range []struct{ asked, sent contracts.Effort }{
		{"", contracts.EffortFast},
		{contracts.EffortFast, contracts.EffortFast},
		{contracts.EffortThorough, contracts.EffortThorough},
		{contracts.EffortReviewed, contracts.EffortReviewed},
	} {
		if _, err := w.computeWith(p.ID, tc.asked); err != nil {
			t.Fatalf("effort %q: %v", tc.asked, err)
		}
	}
	var sent []contracts.Effort
	for _, req := range w.optimizer.Requests() {
		sent = append(sent, req.Effort)
	}
	if want := []contracts.Effort{"fast", "fast", "thorough", "reviewed"}; !slices.Equal(sent, want) {
		t.Errorf("the optimizer was asked for %v, want %v", sent, want)
	}
}

func TestComputeRejectsAnEffortThatIsNotOneOfTheLevelsBeforeAskingTheOptimizer(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1")
	for _, bad := range []contracts.Effort{"turbo", "Fast", "THOROUGH", "reviewed ", "deep"} {
		events, err := w.computeWith(p.ID, bad)
		got := wantError(t, err, platform.CodeInvalid, "srv.api.badEffort")
		if got.Field != "effort" || got.Status != 422 {
			t.Errorf("%q: field %q status %d", bad, got.Field, got.Status)
		}
		if len(events) != 0 {
			t.Errorf("%q: %d events were streamed for a request that cannot be served", bad, len(events))
		}
	}
	if n := len(w.optimizer.Requests()); n != 0 {
		t.Errorf("%d quotes were asked for efforts that do not exist", n)
	}
	if rec := w.stored(p.ID); rec.Plan.OptionSet != nil || rec.Plan.Status != contracts.PlanStatusDraft {
		t.Errorf("a refused request changed the plan: %s", rec.Plan.Status)
	}
}

func TestEveryEffortOfTheContractIsAccepted(t *testing.T) {
	for _, e := range contracts.Efforts {
		if got, err := parseEffort(e); err != nil || got != e {
			t.Errorf("%q: %q, %v", e, got, err)
		}
	}
}

// AIReview tells the optimizer that its reviewer may be the model: the admin's switch for the assistant is on and a
// gateway exists. The service is told that as one answer, asked afresh for every quote so that a switch the admin
// flips takes effect at once.
func TestComputeTellsTheOptimizerWhetherTheAIMayReview(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1")
	ask := func() bool {
		t.Helper()
		before := len(w.optimizer.Requests())
		if _, err := w.computeWith(p.ID, contracts.EffortReviewed); err != nil {
			t.Fatal(err)
		}
		reqs := w.optimizer.Requests()
		if len(reqs) != before+1 {
			t.Fatalf("%d quotes asked, want %d", len(reqs), before+1)
		}
		return reqs[len(reqs)-1].AIReview
	}

	if ask() {
		t.Error("with no AI wiring at all (no gateway) the optimizer was told the AI may review")
	}

	var on atomic.Bool
	var asked atomic.Int32
	WithAIReview(func(context.Context) bool { asked.Add(1); return on.Load() })(w.svc)
	if ask() {
		t.Error("the AI switch is off but the optimizer was told the AI may review")
	}
	on.Store(true)
	if !ask() {
		t.Error("the AI switch is on and a gateway exists, but the optimizer was told the AI may not review")
	}
	on.Store(false)
	if ask() {
		t.Error("the admin switched the AI off again; the next quote must not use it")
	}
	if asked.Load() != 3 {
		t.Errorf("the switch was read %d times for 3 quotes", asked.Load())
	}
}

func TestComputeRelaysSearchStepsWithTheirMessageAndAnEnglishLabel(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1", "s2")
	steps := []contracts.Msg{
		{Key: "srv.engine.search.experiments", Params: map[string]any{"n": 4}},
		{Key: "srv.engine.search.experiments", Params: map[string]any{"n": 1}},
		{Key: "srv.engine.search.round", Params: map[string]any{"round": 2}},
		{Key: "srv.engine.search.reviewAI"},
		{Key: "srv.engine.search.reviewRules"},
		{Key: "srv.engine.search.more", Params: map[string]any{"n": 3}},
		{Key: "srv.engine.search.more", Params: map[string]any{"n": 1}},
		{Key: "srv.engine.search.satisfied"},
		{Key: "srv.engine.search.polish"},
		{Key: "srv.engine.search.somethingNew", Params: map[string]any{"n": 9}}, // a message this console has never heard of
		{Key: "srv.engine.search.round"},                                        // a known one without its parameter
	}
	w.optimizer.SetSteps(steps...)
	events, err := w.computeWith(p.ID, contracts.EffortReviewed)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4+len(steps)+1 {
		t.Fatalf("%d events, want the 4 stages, %d steps and done", len(events), len(steps))
	}

	stage3 := "Building vehicle combinations for 2 shipments"
	wantLabels := []string{
		"Trying 4 ways of grouping the stops",
		"Trying 1 way of grouping the stops",
		"Review round 2",
		"The AI reviewer is checking the best routes",
		"The built-in reviewer is checking the best routes",
		"Trying 3 more experiments",
		"Trying 1 more experiment",
		"The reviewer is satisfied with the result",
		"Polishing the best routes",
		stage3, // unknown message: the label of its stage
		stage3, // a round without its number
	}
	for i, step := range steps {
		ev := events[3+i] // after stages 1 to 3
		if ev.Type != "progress" || ev.Step != 3 || ev.Total != 4 || ev.Label != wantLabels[i] || ev.OptionSet != nil {
			t.Errorf("step %d = %+v, want a progress line of stage 3 labelled %q", i, ev, wantLabels[i])
		}
		if ev.Msg == nil || ev.Msg.Key != step.Key || len(ev.Msg.Params) != len(step.Params) {
			t.Errorf("step %d: message %+v, want %+v", i, ev.Msg, step)
		}
	}
	if events[3].Msg.Params["n"] != float64(4) {
		t.Errorf("params = %#v", events[3].Msg.Params)
	}
	// A stage line has no message, so a client that does not know them shows the label as before.
	if ev := events[0]; ev.Msg != nil || ev.Label != "Reading 4 locations" {
		t.Errorf("stage 1 = %+v", ev)
	}
	if ev := events[len(events)-2]; ev.Step != 4 || ev.Msg != nil || ev.Label != "Comparing cost, distance, and time" {
		t.Errorf("stage 4 = %+v", ev)
	}

	// The message survives the trip to the client.
	raw, err := json.Marshal(events[5])
	if err != nil || !strings.Contains(string(raw), `"msg":{"key":"srv.engine.search.round","params":{"round":2}}`) || !strings.Contains(string(raw), `"label":"Review round 2"`) {
		t.Errorf("wire form = %s, %v", raw, err)
	}
	if raw, _ := json.Marshal(events[0]); strings.Contains(string(raw), `"msg"`) {
		t.Errorf("a stage line carries no msg: %s", raw)
	}
}

func TestMethodSurvivesTheStoreAndIsReturnedByGet(t *testing.T) {
	w := newWorld(t)
	method := &contracts.Method{
		Effort: contracts.EffortReviewed, Trials: 14, Rounds: 2, Polished: true, Reviewer: contracts.ReviewerAI, Proven: false, RoadSource: "mapbox",
	}
	w.optimizer.SetMethod(method)
	p := w.draft("s1", "s2")
	events, err := w.computeWith(p.ID, contracts.EffortReviewed)
	if err != nil {
		t.Fatal(err)
	}
	if done := events[len(events)-1]; done.OptionSet == nil || done.OptionSet.Method == nil || *done.OptionSet.Method != *method {
		t.Fatalf("done carries method %+v", done.OptionSet)
	}
	if rec := w.stored(p.ID); rec.Plan.OptionSet.Method == nil || *rec.Plan.OptionSet.Method != *method {
		t.Errorf("stored method = %+v, want %+v", rec.Plan.OptionSet.Method, method)
	}
	got, err := w.svc.Get(t.Context(), w.customer, p.ID)
	if err != nil || got.OptionSet == nil || got.OptionSet.Method == nil || *got.OptionSet.Method != *method {
		t.Fatalf("plans.get = %+v, %v", got.OptionSet, err)
	}
	raw, err := json.Marshal(got)
	var wire struct {
		OptionSet struct {
			Method map[string]any `json:"method"`
		} `json:"optionSet"`
	}
	if err != nil || json.Unmarshal(raw, &wire) != nil {
		t.Fatalf("encoding the plan: %v", err)
	}
	wantWire := map[string]any{"effort": "reviewed", "trials": 14.0, "rounds": 2.0, "polished": true, "reviewer": "ai", "roadSource": "mapbox"}
	for key, want := range wantWire {
		if wire.OptionSet.Method[key] != want {
			t.Errorf("wire form of the method: %s = %v, want %v (%v)", key, wire.OptionSet.Method[key], want, wire.OptionSet.Method)
		}
	}

	// An optimizer that says nothing about how it searched leaves no method rather than a made-up one.
	w.optimizer.SetMethod(nil)
	if _, err := w.compute(p.ID); err != nil {
		t.Fatal(err)
	}
	if rec := w.stored(p.ID); rec.Plan.OptionSet.Method != nil {
		t.Errorf("method = %+v", rec.Plan.OptionSet.Method)
	}
	if raw, _ := json.Marshal(w.stored(p.ID).Plan); strings.Contains(string(raw), `"method"`) {
		t.Error("an absent method must be left out of the plan")
	}
}

// Computing again replaces the options of the earlier computation, with what the optimizer found this time; the
// choice among the old options is forgotten, since the new ones need not contain it, and so is the old report.
func TestComputingAgainReplacesTheOptionsTheirMethodAndTheReport(t *testing.T) {
	w := newWorld(t)
	w.optimizer.SetMethod(&contracts.Method{Effort: contracts.EffortFast, Trials: 4, Proven: true, RoadSource: "estimate"})
	p := w.ready("s1", "s2")
	first := w.stored(p.ID).Plan.OptionSet
	if p.SelectedOptionID == "" || first.Method == nil {
		t.Fatalf("setup: selected %q, method %+v", p.SelectedOptionID, first.Method)
	}
	w.clock.advance(time.Minute)
	stored, err := StoreReport(t.Context(), w.pool, w.customer, p.ID, RefOf(first), contracts.Report{Text: "old words", Locale: "en", At: contracts.At(w.clock.at)})
	if err != nil || !stored {
		t.Fatalf("StoreReport = %v, %v", stored, err)
	}

	w.clock.advance(time.Minute)
	w.optimizer.SetMethod(&contracts.Method{Effort: contracts.EffortThorough, Trials: 12, Polished: true, RoadSource: "mapbox"})
	// An optimizer that sent a report would not be believed: it is written by the console, on request.
	w.optimizer.SetReport(&contracts.Report{Text: "smuggled", Locale: "en"})
	if _, err := w.computeWith(p.ID, contracts.EffortThorough); err != nil {
		t.Fatal(err)
	}
	rec := w.stored(p.ID)
	set := rec.Plan.OptionSet
	if rec.Plan.Status != contracts.PlanStatusOptionsReady || rec.Plan.SelectedOptionID != "" {
		t.Errorf("status %s, selected %q: a recompute leaves the options ready and nothing chosen", rec.Plan.Status, rec.Plan.SelectedOptionID)
	}
	if set.Method == nil || set.Method.Effort != contracts.EffortThorough || set.Method.Trials != 12 || !set.Method.Polished {
		t.Errorf("method = %+v, want what the second computation found", set.Method)
	}
	if set.Report != nil {
		t.Errorf("the report of the earlier options (or one the optimizer sent) is still there: %+v", set.Report)
	}
	if !set.ComputedAt.After(first.ComputedAt.Time) {
		t.Errorf("computed at %v, not after the first computation %v", set.ComputedAt, first.ComputedAt)
	}
	if rec.Quote == nil || rec.Quote.Engine != "fake-1" {
		t.Errorf("quote = %+v", rec.Quote)
	}
}
