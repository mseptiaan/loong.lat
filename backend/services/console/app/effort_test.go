package app

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// reportGateway is a gateway that answers the report profile with the cost of the first option, quoted from the
// facts it was sent, and records the profiles it was asked for.
func reportGateway(t *testing.T) (url string, profiles func() []aiapi.Profile) {
	t.Helper()
	var mu sync.Mutex
	var seen []aiapi.Profile
	cost := regexp.MustCompile(`"cost":"(Rp [0-9.,]+)"`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req aiapi.ChatRequest
		if r.URL.Path != aiapi.ChatPath || r.Header.Get("Authorization") != "Bearer gateway-token" || json.UnmarshalRead(r.Body, &req) != nil {
			http.Error(w, "unexpected call", http.StatusBadRequest)
			return
		}
		mu.Lock()
		seen = append(seen, req.Profile)
		mu.Unlock()
		text := "Nothing to say."
		if m := cost.FindStringSubmatch(req.Messages[len(req.Messages)-1].Content); m != nil {
			text = "Recommendation: the route costs " + m[1] + "."
		}
		body, _ := json.Marshal(aiapi.ChatResponse{Message: aiapi.Message{Role: aiapi.RoleAssistant, Content: text}, FinishReason: aiapi.FinishStop})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []aiapi.Profile {
		mu.Lock()
		defer mu.Unlock()
		return append([]aiapi.Profile(nil), seen...)
	}
}

// draftWithVehicle is a plan of the caller's with two shipments, and a vehicle for the optimizer to use.
func (s *service) draftWithVehicle(c caller) contracts.Plan {
	s.t.Helper()
	if testkit.Count(s.t, s.pool, `SELECT count(*) FROM fleet_vehicles`) == 0 {
		s.publish(contracts.TypeVehicleUpserted, "veh-aaa", 1, contracts.VehicleUpserted{Vehicle: testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor)})
		s.waitFor("the vehicle to be projected", func() bool { return testkit.Count(s.t, s.pool, `SELECT count(*) FROM fleet_vehicles`) == 1 })
	}
	var plan contracts.Plan
	c.rpc("plans.createDraft", nil).into(&plan)
	c.rpc("plans.saveDraft", map[string]any{"id": plan.ID, "patch": map[string]any{"shipments": twoShipments(), "draftStep": 2}}).into(&plan)
	return plan
}

func TestTheEffortAndTheAISwitchReachTheOptimizerThroughTheWholeStack(t *testing.T) {
	url, profiles := reportGateway(t)
	svc := startService(t, func(c *Config) { c.AIURL, c.AIToken = url, "gateway-token" })
	svc.optimizer.SetMethod(&contracts.Method{Effort: contracts.EffortReviewed, Trials: 14, Rounds: 2, Polished: true, Reviewer: contracts.ReviewerAI, Proven: false, RoadSource: "estimate"})
	svc.optimizer.SetSteps(contracts.Msg{Key: "srv.engine.search.round", Params: map[string]any{"round": 1}})
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.draftWithVehicle(dina)

	// An effort that is not one of the three is refused with its field, before the optimizer hears of it.
	r := dina.rpc("plans.computeOptions", map[string]any{"id": plan.ID, "effort": "turbo"})
	if f := r.failure(); r.status != http.StatusUnprocessableEntity || f.Code != "tidak_valid" || f.Field != "effort" || f.Msg.Key != "srv.api.badEffort" {
		t.Fatalf("an unknown effort: %d %+v", r.status, f)
	}
	if n := len(svc.optimizer.Requests()); n != 0 {
		t.Fatalf("%d quotes asked for an effort that does not exist", n)
	}

	// The body of the frontend that does not know efforts yet still works: fast.
	if lines, r := dina.stream("plans.computeOptions", map[string]any{"id": plan.ID}); r.status != http.StatusOK || len(lines) != 6 {
		t.Fatalf("without an effort: %d, %d lines", r.status, len(lines))
	}
	if req := svc.optimizer.Requests()[0]; req.Effort != contracts.EffortFast {
		t.Errorf("effort = %q, want fast", req.Effort)
	}

	// A reviewed quote, with the gateway there and the admin's switch on (its default): the AI may review. The step of the
	// search arrives with its message, and a label for clients that do not render it.
	lines, r := dina.stream("plans.computeOptions", map[string]any{"id": plan.ID, "effort": "reviewed"})
	if r.status != http.StatusOK || len(lines) != 6 {
		t.Fatalf("reviewed: %d, %d lines: %s", r.status, len(lines), r.body)
	}
	if req := svc.optimizer.Requests()[1]; req.Effort != contracts.EffortReviewed || !req.AIReview {
		t.Errorf("the optimizer was asked %q with AIReview %v", req.Effort, req.AIReview)
	}
	if lines[3] != `{"type":"progress","step":3,"total":4,"label":"Review round 1","msg":{"key":"srv.engine.search.round","params":{"round":1}}}` {
		t.Errorf("the step line = %s", lines[3])
	}
	var got contracts.Plan
	dina.rpc("plans.get", map[string]any{"id": plan.ID}).into(&got)
	if m := got.OptionSet.Method; m == nil || m.Effort != contracts.EffortReviewed || m.Trials != 14 || m.Rounds != 2 || !m.Polished || m.Reviewer != contracts.ReviewerAI || m.RoadSource != "estimate" {
		t.Errorf("plans.get returned the method %+v", m)
	}

	// The admin switches the assistant off: the next quote is reviewed by the rules, and the report is refused.
	svc.publish(contracts.TypeSettingsUpdated, contracts.SettingsKeyAI, 1, contracts.SettingsUpdated{
		Key: contracts.SettingsKeyAI, AI: &contracts.AIFeatures{Vision: true, Voice: true, Intent: true, Copilot: false},
	})
	svc.waitFor("the switch to be stored", func() bool { return testkit.Count(t, svc.pool, `SELECT count(*) FROM settings`) == 1 })
	if _, r := dina.stream("plans.computeOptions", map[string]any{"id": plan.ID, "effort": "reviewed"}); r.status != http.StatusOK {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
	if req := svc.optimizer.Requests()[2]; req.AIReview {
		t.Error("the admin switched the assistant off but the optimizer was told the AI may review")
	}
	r = dina.rpc("ai.explainOptions", map[string]any{"planId": plan.ID})
	if f := r.failure(); r.status != http.StatusForbidden || f.Msg.Key != "srv.api.aiDisabled" {
		t.Errorf("a report with the switch off: %d %+v", r.status, f)
	}
	if len(profiles()) != 0 {
		t.Errorf("the gateway was called %v with the switch off", profiles())
	}
}

func TestTheReportIsWrittenKeptAndReturnedByPlansGetThroughTheWholeStack(t *testing.T) {
	url, profiles := reportGateway(t)
	svc := startService(t, func(c *Config) { c.AIURL, c.AIToken = url, "gateway-token" })
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.draftWithVehicle(dina)

	// No options yet: there is nothing to explain.
	r := dina.rpc("ai.explainOptions", map[string]any{"planId": plan.ID})
	if f := r.failure(); r.status != http.StatusUnprocessableEntity || f.Msg.Key != "srv.ai.report.noOptions" || f.Field != "planId" {
		t.Fatalf("a plan without options: %d %+v", r.status, f)
	}
	dina.stream("plans.computeOptions", map[string]any{"id": plan.ID})

	events := dina.events()
	lines, r := dina.stream("ai.explainOptions", map[string]any{"planId": plan.ID})
	if r.status != http.StatusOK || len(lines) != 2 {
		t.Fatalf("explain: %d, %d lines: %s", r.status, len(lines), r.body)
	}
	want := "Recommendation: the route costs Rp 25,000."
	var token struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &token); err != nil || token.Type != "token" || token.Text != want {
		t.Errorf("first line = %s", lines[0])
	}
	var done struct {
		Type      string        `json:"type"`
		Actions   []interface{} `json:"actions"`
		RequestID string        `json:"requestId"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &done); err != nil || done.Type != "done" || done.Actions == nil || len(done.Actions) != 0 || !strings.HasPrefix(done.RequestID, "req-") {
		t.Errorf("second line = %s", lines[1])
	}
	await(t, events, "plans.changed", func(ev platform.Event) bool { return ev.Type == "plans.changed" })

	var got contracts.Plan
	dina.rpc("plans.get", map[string]any{"id": plan.ID}).into(&got)
	rep := got.OptionSet.Report
	if rep == nil || rep.Text != want || rep.Locale != "en" || time.Since(rep.At.Time) > time.Minute {
		t.Errorf("plans.get returned the report %+v", rep)
	}
	if got := profiles(); len(got) != 1 || got[0] != aiapi.ProfileReport {
		t.Errorf("the gateway was asked for %v", got)
	}

	// Someone else cannot explain it.
	other, _ := svc.register("eko@contoh.id")
	if r := other.rpc("ai.explainOptions", map[string]any{"planId": plan.ID}); r.status != http.StatusNotFound {
		t.Errorf("another customer's plan: %d %s", r.status, r.body)
	}
	// Computing again drops the report.
	dina.stream("plans.computeOptions", map[string]any{"id": plan.ID})
	var again contracts.Plan // a fresh value: decoding into the old one would keep what the answer leaves out
	dina.rpc("plans.get", map[string]any{"id": plan.ID}).into(&again)
	if again.OptionSet == nil || again.OptionSet.Report != nil {
		t.Errorf("a new computation kept the old report: %+v", again.OptionSet)
	}
}

func TestWithoutAGatewayTheQuoteIsNeverToldTheAIMayReview(t *testing.T) {
	svc := startService(t)
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.draftWithVehicle(dina)
	// The admin's switch is on, but there is no gateway to ask: settings.get reports the switch off, and so does the quote.
	if _, r := dina.stream("plans.computeOptions", map[string]any{"id": plan.ID, "effort": "reviewed"}); r.status != http.StatusOK {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
	if req := svc.optimizer.Requests()[0]; req.Effort != contracts.EffortReviewed || req.AIReview {
		t.Errorf("the optimizer was asked %q with AIReview %v; without a gateway the rules review", req.Effort, req.AIReview)
	}
	r := dina.rpc("ai.explainOptions", map[string]any{"planId": plan.ID})
	if f := r.failure(); r.status != http.StatusForbidden || f.Msg.Key != "srv.api.aiDisabled" {
		t.Errorf("a report without a gateway: %d %+v", r.status, f)
	}
}
