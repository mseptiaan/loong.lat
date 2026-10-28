package ai

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/agent"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi/aiapitest"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/settings"
)

var ops = map[string]any{"kind": "ops"}

func askBody(question string) map[string]any {
	return map[string]any{"scope": ops, "question": question}
}

// askCopilot signs in an admin and asks one question over HTTP.
func askCopilot(h *harness, question string, header map[string]string) reply {
	h.t.Helper()
	return h.call(h.token(contracts.RoleAdmin, "adm-1"), "ai.copilot", askBody(question), header)
}

func eventTypes(rp reply) []string {
	var out []string
	for _, ev := range rp.events {
		out = append(out, ev["type"].(string))
	}
	return out
}

func toolMessages(req aiapi.ChatRequest) []aiapi.Message {
	var out []aiapi.Message
	for _, m := range req.Messages {
		if m.Role == aiapi.RoleTool {
			out = append(out, m)
		}
	}
	return out
}

func TestCopilotStreamsTracesTokensAndDone(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(
		aiapitest.UseTools(aiapitest.Call("c1", "get_queue", "{}"), aiapitest.Call("c2", "list_drivers", `{"status":"idle"}`)),
		aiapitest.Say("PL-2001 has waited longest.", "PL-2001 has ", "waited longest."),
	)
	h := serve(t, r.service(model))
	rp := askCopilot(h, "What do I do first?", nil)

	if rp.status != http.StatusOK {
		t.Fatalf("status %d: %s", rp.status, rp.body)
	}
	want := []string{"tool", "tool", "tool", "tool", "token", "token", "done"}
	if got := eventTypes(rp); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v\n%s", got, want, rp.body)
	}

	tr := rp.traces()
	if tr[0]["id"] != "t0" || tr[0]["status"] != "running" || tr[0]["title"] != "Reading the queue" || tr[0]["ms"] != nil || tr[0]["detail"] != nil {
		t.Errorf("first line while running = %v", tr[0])
	}
	if tr[1]["id"] != "t0" || tr[1]["status"] != "done" || tr[1]["target"] != "6 active plans" || tr[1]["ms"] == nil || !strings.Contains(tr[1]["detail"].(string), "waiting for a driver: 3") {
		t.Errorf("first line when done = %v", tr[1])
	}
	if tr[2]["id"] != "t1" || tr[2]["status"] != "running" || tr[3]["id"] != "t1" || tr[3]["status"] != "done" || tr[3]["title"] != "Checking driver availability" {
		t.Errorf("second line = %v then %v", tr[2], tr[3])
	}
	if got := rp.text(); got != "PL-2001 has waited longest." {
		t.Errorf("answer = %q", got)
	}
	done := rp.done()
	if id, _ := done["requestId"].(string); !regexp.MustCompile(`^req-[0-9a-f]{12}$`).MatchString(id) {
		t.Errorf("requestId = %v", done["requestId"])
	}
	if actions, ok := done["actions"].([]any); !ok || len(actions) != 0 {
		t.Errorf("actions = %#v, want an empty list (the screen reads its length)", done["actions"])
	}

	// What the model was given: the copilot profile, the role and the rules, the question, exactly the tools of the
	// admin's Copilot, and the tool results as data.
	reqs := model.Requests()
	if len(reqs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(reqs))
	}
	first := reqs[0]
	if first.Profile != aiapi.ProfileCopilot || len(first.Messages) != 2 || first.Messages[1].Content != "What do I do first?" {
		t.Errorf("first request = profile %q, %d messages", first.Profile, len(first.Messages))
	}
	system := first.Messages[0].Content
	for _, want := range []string{
		"Answer in English", agent.UntrustedDataNotice, "Every number you write", "You cannot change anything", "2026-10-21 19:00",
		// style: plain sentences, no quotation marks, digits, sparing bold, all reading tools in the first turn
		"Write plain sentences", "Never put quotation marks", "Write every number as digits", "Use **bold** sparingly", "at most three",
		"Ask for every read tool you may need in your first turn", "must contain no text at all", "in your own words",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
	var tools []string
	for _, tool := range first.Tools {
		tools = append(tools, tool.Name)
	}
	if want := []string{"get_queue", "get_plan", "list_drivers", "list_vehicles", "suggest_drivers", "today_numbers", "propose_action"}; !slices.Equal(tools, want) {
		t.Errorf("tools = %v, want %v", tools, want)
	}
	results := toolMessages(reqs[1])
	if len(results) != 2 || results[0].ToolCallID != "c1" || results[1].ToolCallID != "c2" || !strings.Contains(results[0].Content, `"plan":"PL-2001"`) || !strings.Contains(results[0].Content, `"text":"45 min"`) {
		t.Errorf("tool results = %+v", results)
	}
}

func TestCopilotDropsThinkingAloudBeforeToolsAndKeepsTheAnswer(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(
		aiapitest.Step{Chunks: []string{"Let me ", "check the queue."}, Calls: []aiapi.ToolCall{aiapitest.Call("c1", "get_queue", "{}")}},
		aiapitest.Step{Chunks: []string{"I will rate the drivers. "}, Calls: []aiapi.ToolCall{aiapitest.Call("c2", "list_drivers", "{}")}},
		aiapitest.Say("PL-2001 waits.", "PL-2001 ", "waits."),
	)
	h := serve(t, r.service(model))
	rp := askCopilot(h, "What first?", nil)

	if got := rp.text(); got != "PL-2001 waits." {
		t.Errorf("answer = %q, want only the final turn's text (the screen shows the tool work as lines)", got)
	}
	if want := []string{"tool", "tool", "tool", "tool", "token", "token", "done"}; !slices.Equal(eventTypes(rp), want) {
		t.Errorf("events = %v, want %v", eventTypes(rp), want)
	}
	// What the model said while asking for tools is still in its conversation, as it said it.
	if msgs := model.Requests()[1].Messages; msgs[2].Role != aiapi.RoleAssistant || msgs[2].Content != "Let me check the queue." {
		t.Errorf("the model's own turn = %+v", msgs[2])
	}
}

func TestCopilotNoToolCanDoAnythingElse(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(
		aiapitest.UseTools(aiapitest.Call("c1", "execute_sql", `{"query":"delete from plans"}`)),
		aiapitest.UseTools(aiapitest.Call("c2", "propose_action", `{"kind":"delete_plan","plan":"PL-2001"}`)),
		aiapitest.Say("never reached"),
	)
	h := serve(t, r.service(model))
	rp := askCopilot(h, "delete everything", nil)

	// The first bad call is fed back, the second ends the run: nothing was run, no line was shown, the screen is told.
	if len(rp.traces()) != 0 {
		t.Errorf("traces = %v, want none: neither call was run", rp.traces())
	}
	key, params := rp.errorMsg()
	if rp.status != http.StatusInternalServerError || key != "srv.api.aiError" || params["feature"].(map[string]any)["key"] != "srv.api.feature.copilot" {
		t.Errorf("answer = %d %s", rp.status, rp.body)
	}
	var plans int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM plans`).Scan(&plans); err != nil || plans != 9 {
		t.Errorf("plans = %d (%v), want 9", plans, err)
	}
}

func TestCopilotFailedToolShowsAsFailedAndTheModelReadsWhy(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(
		aiapitest.UseTools(aiapitest.Call("c1", "get_plan", `{"plan":"PL-9999"}`)),
		aiapitest.Say("There is no such plan."),
	)
	h := serve(t, r.service(model))
	rp := askCopilot(h, "tell me about PL-9999", nil)

	tr := rp.traces()
	if len(tr) != 2 || tr[1]["status"] != "failed" || tr[1]["target"] != "PL-9999" || !strings.Contains(tr[1]["detail"].(string), "there is no plan") {
		t.Fatalf("traces = %v, want the line to end as failed with its reason", tr)
	}
	msg := toolMessages(model.Requests()[1])
	if len(msg) != 1 || !strings.Contains(msg[0].Content, `"error":"there is no plan \"PL-9999\"`) {
		t.Errorf("the model was told %+v", msg)
	}
	if rp.done() == nil {
		t.Errorf("the answer still ends with done: %v", eventTypes(rp))
	}
}

func TestCopilotOffersOnlyActionsThatAreTrue(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(
		aiapitest.UseTools(
			aiapitest.Call("a", "propose_action", `{"kind":"buka_plan","plan":"PL-2004"}`),
			aiapitest.Call("b", "propose_action", `{"kind":"tugaskan_terbaik","plan":"PL-2001"}`),
			aiapitest.Call("c", "propose_action", `{"kind":"tugaskan_terbaik","plan":"PL-9999"}`),
			aiapitest.Call("d", "propose_action", `{"kind":"tugaskan_terbaik","plan":"PL-2004"}`),
		),
		aiapitest.Say("Assign Budi to PL-2001; look at PL-2004."),
	)
	h := serve(t, r.service(model))
	rp := askCopilot(h, "who is best for PL-2001?", map[string]string{"Accept-Language": "id"})

	actions := rp.actions()
	if len(actions) != 2 {
		t.Fatalf("actions = %v, want the assignment of PL-2001 and the opening of PL-2004 only", actions)
	}
	if a := actions[0]; a["kind"] != "tugaskan_terbaik" || a["planId"] != "pln-1" || a["label"] != "Terapkan saran driver terbaik" || a["primary"] != true || a["id"] != "tugaskan:pln-1" {
		t.Errorf("first action = %v", a)
	}
	if a := actions[1]; a["kind"] != "buka_plan" || a["planId"] != "pln-4" || a["label"] != "Buka PL-2004" || a["primary"] != nil {
		t.Errorf("second action = %v", a)
	}
	var failed []string
	for _, tr := range rp.traces() {
		if tr["status"] == "failed" {
			failed = append(failed, tr["target"].(string))
		}
	}
	if !slices.Equal(failed, []string{"PL-9999", "PL-2004"}) {
		t.Errorf("failed proposals = %v, want PL-9999 (invented) and PL-2004 (its route has a driver)", failed)
	}
}

func TestCopilotPlanScope(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(
		aiapitest.UseTools(aiapitest.Call("c1", "get_plan", `{}`), aiapitest.Call("c2", "suggest_drivers", `{}`)),
		aiapitest.Say("Budi is the best."),
	)
	h := serve(t, r.service(model))
	admin := h.token(contracts.RoleAdmin, "adm-1")

	// A plan that does not exist is "not found" before the model is called.
	rp := h.call(admin, "ai.copilot", map[string]any{"scope": map[string]any{"kind": "plan", "planId": "pln-invented"}, "question": "why late?"}, nil)
	if key, _ := rp.errorMsg(); rp.status != http.StatusNotFound || key != "srv.ops.err.planNotFound" || model.Calls() != 0 {
		t.Fatalf("unknown plan: %d %s, model calls %d", rp.status, rp.body, model.Calls())
	}

	rp = h.call(admin, "ai.copilot", map[string]any{"scope": map[string]any{"kind": "plan", "planId": "pln-1"}, "question": "who is the best driver?"}, nil)
	if rp.status != http.StatusOK || rp.done() == nil {
		t.Fatalf("answer = %d %s", rp.status, rp.body)
	}
	reqs := model.Requests()
	if system := reqs[0].Messages[0].Content; !strings.Contains(system, "looking at plan PL-2001 (id pln-1)") {
		t.Errorf("the system prompt does not name the plan: %s", system)
	}
	results := toolMessages(reqs[1])
	if len(results) != 2 || !strings.Contains(results[0].Content, `"plan":"PL-2001"`) || !strings.Contains(results[1].Content, `"bestDriver":"Budi"`) {
		t.Errorf("tools without a plan argument should read the plan of the question: %+v", results)
	}
	if tr := rp.traces(); tr[0]["target"] != "PL-2001" || tr[2]["target"] != "PL-2001" {
		t.Errorf("the running lines name the plan: %v", tr)
	}
}

func TestCopilotRefusesWhatItShould(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New()
	h := serve(t, r.service(model))
	body := askBody("hello")

	tests := []struct {
		name   string
		token  string
		body   any
		status int
		code   string
		key    string
	}{
		{"nobody signed in", "", body, 401, "ditolak", "srv.api.signInFirst"},
		{"a customer", h.token(contracts.RoleCustomer, "cus-1"), body, 403, "ditolak", "srv.api.roleDenied"},
		{"a driver", h.token(contracts.RoleDriver, "drv-1"), body, 403, "ditolak", "srv.api.roleDenied"},
		{"an empty question", h.token(contracts.RoleAdmin, "adm-1"), askBody("   "), 422, "tidak_valid", "srv.api.questionEmpty"},
		{"a scope that is not one", h.token(contracts.RoleAdmin, "adm-1"), map[string]any{"scope": map[string]any{"kind": "plans"}, "question": "hi"}, 422, "tidak_valid", "srv.rpc.badRequest"},
		{"a plan scope without a plan", h.token(contracts.RoleAdmin, "adm-1"), map[string]any{"scope": map[string]any{"kind": "plan"}, "question": "hi"}, 404, "tidak_ditemukan", "srv.ops.err.planNotFound"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := h.call(tt.token, "ai.copilot", tt.body, nil)
			key, _ := rp.errorMsg()
			if rp.status != tt.status || rp.code() != tt.code || key != tt.key {
				t.Errorf("answer = %d %s, want %d %s %s", rp.status, rp.body, tt.status, tt.code, tt.key)
			}
		})
	}
	if model.Calls() != 0 {
		t.Errorf("the model was called %d times for refused requests", model.Calls())
	}

	// The same doors for insights.
	for _, token := range []string{"", h.token(contracts.RoleCustomer, "cus-1"), h.token(contracts.RoleDriver, "drv-1")} {
		if rp := h.call(token, "ai.insights", struct{}{}, nil); rp.status != 401 && rp.status != 403 {
			t.Errorf("insights for %q: %d %s", token, rp.status, rp.body)
		}
	}
	if model.Calls() != 0 {
		t.Errorf("the model was called %d times for refused insights", model.Calls())
	}
}

func switchCopilot(t *testing.T, r *rig, on bool) {
	t.Helper()
	if _, err := r.settings.Update(t.Context(), settings.Patch{AI: &settings.AIPatch{Copilot: &on}}); err != nil {
		t.Fatal(err)
	}
}

func TestFeatureSwitchedOffByTheAdminIsRefused(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(aiapitest.Say("hi"), aiapitest.Say("hi"))
	h := serve(t, r.service(model, func(o *Options) { o.Burst, o.RatePerMinute = 1, 1 }))
	admin := h.token(contracts.RoleAdmin, "adm-1")
	switchCopilot(t, r, false)

	for _, c := range []struct {
		method, feature string
		in              any
	}{
		{"ai.copilot", "srv.api.feature.copilot", askBody("hello")},
		{"ai.insights", "srv.api.feature.insights", struct{}{}},
	} {
		rp := h.call(admin, c.method, c.in, nil)
		key, params := rp.errorMsg()
		if rp.status != http.StatusForbidden || rp.code() != "ditolak" || key != "srv.api.aiDisabled" || params["feature"].(map[string]any)["key"] != c.feature {
			t.Errorf("%s: %d %s, want 403 ditolak aiDisabled for %s", c.method, rp.status, rp.body, c.feature)
		}
	}
	if model.Calls() != 0 {
		t.Errorf("the model was called %d times while the feature was off", model.Calls())
	}

	// Refused calls cost nothing of the admin's rate (burst 1): switched back on, the next call goes through.
	switchCopilot(t, r, true)
	if rp := h.call(admin, "ai.copilot", askBody("hello"), nil); rp.status != http.StatusOK || rp.done() == nil {
		t.Errorf("after switching back on: %d %s", rp.status, rp.body)
	}

	// The admin's stored truth is untouched by all this: that is what settings.get shows.
	if cur, err := settings.Read(t.Context(), r.pool); err != nil || !cur.AI.Copilot || !cur.AI.Vision {
		t.Errorf("settings = %+v (%v)", cur, err)
	}
}

func TestWithoutAGatewayTheFeaturesAnswerAiError(t *testing.T) {
	r := newRig(t)
	r.seed()
	h := serve(t, r.service(nil))
	admin := h.token(contracts.RoleAdmin, "adm-1")
	for _, c := range []struct {
		method, feature string
		in              any
	}{
		{"ai.copilot", "srv.api.feature.copilot", askBody("hello")},
		{"ai.insights", "srv.api.feature.insights", struct{}{}},
	} {
		rp := h.call(admin, c.method, c.in, nil)
		key, params := rp.errorMsg()
		if rp.status != http.StatusInternalServerError || rp.code() != "layanan" || key != "srv.api.aiError" || params["feature"].(map[string]any)["key"] != c.feature {
			t.Errorf("%s: %d %s, want 500 layanan aiError for %s (not srv.engine.unavailable)", c.method, rp.status, rp.body, c.feature)
		}
	}
	// The switch still decides first: an admin who turned it off is told so, not that it has problems.
	switchCopilot(t, r, false)
	if rp := h.call(admin, "ai.copilot", askBody("hello"), nil); rp.status != http.StatusForbidden {
		t.Errorf("switched off without a gateway: %d %s, want 403", rp.status, rp.body)
	}
}

// gatewayStub answers every call of the gateway's chat path with a fixed failure.
func gatewayStub(t *testing.T, status int, body string) *aiapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &aiapi.Client{BaseURL: srv.URL, Token: "test-token"}
}

func TestGatewayFailuresAreAiErrorNotTheGenericOne(t *testing.T) {
	r := newRig(t)
	r.seed()
	kinds := []struct {
		name   string
		status int
		body   string
	}{
		{"down", 503, `{"error":{"kind":"unavailable","msg":{"key":"srv.engine.unavailable"}}}`},
		{"no key at the gateway", 503, `{"error":{"kind":"not_configured","msg":{"key":"srv.engine.unavailable"}}}`},
		{"rate limited", 429, `{"error":{"kind":"rate_limited","msg":{"key":"srv.rpc.tooManyRequests"},"retryAfter":3}}`},
		{"truncated", 502, `{"error":{"kind":"truncated","msg":{"key":"srv.engine.unavailable"}}}`},
		{"timed out", 504, `{"error":{"kind":"timeout","msg":{"key":"srv.engine.unavailable"}}}`},
		{"wrong token", 401, `{"error":{"kind":"unauthorized","msg":{"key":"srv.engine.unavailable"}}}`},
		{"refused our request", 422, `{"error":{"kind":"invalid","field":"messages","msg":{"key":"srv.rpc.badRequest"}}}`},
		{"a proxy page", 502, `<html>bad gateway</html>`},
	}
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			h := serve(t, r.service(gatewayStub(t, k.status, k.body)))
			admin := h.token(contracts.RoleAdmin, "adm-1")
			requireAiError(t, h.call(admin, "ai.copilot", askBody("hello"), nil), "srv.api.feature.copilot")
			requireAiError(t, h.call(admin, "ai.insights", struct{}{}, nil), "srv.api.feature.insights")
		})
	}

	t.Run("nothing listens", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		h := serve(t, r.service(&aiapi.Client{BaseURL: url}))
		requireAiError(t, h.call(h.token(contracts.RoleAdmin, "adm-1"), "ai.copilot", askBody("hello"), nil), "srv.api.feature.copilot")
	})
}

// requireAiError checks the answer is "this feature is having problems" for the feature: a 500 layanan before the
// stream began (the Copilot asks the model before it has anything to show), or the last line of a stream that had
// started (the analytics are read first, then the model is asked). Never the generic engine error.
func requireAiError(t *testing.T, rp reply, feature string) {
	t.Helper()
	var code, key string
	var params map[string]any
	switch {
	case len(rp.events) > 0:
		last := rp.events[len(rp.events)-1]
		e, _ := last["error"].(map[string]any)
		if last["type"] != "error" || e == nil {
			t.Errorf("the stream does not end with an error line: %s", rp.body)
			return
		}
		msg, _ := e["msg"].(map[string]any)
		code, key, params = fmt.Sprint(e["code"]), fmt.Sprint(msg["key"]), msg["params"].(map[string]any)
	default:
		code = rp.code()
		key, params = rp.errorMsg()
		if rp.status != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500: %s", rp.status, rp.body)
		}
	}
	if code != "layanan" || key != "srv.api.aiError" || params["feature"].(map[string]any)["key"] != feature {
		t.Errorf("answer = %s %s %v, want layanan srv.api.aiError for %s\n%s", code, key, params, feature, rp.body)
	}
}

func TestGatewayFailingMidStreamEndsTheStreamWithAnErrorLine(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(
		aiapitest.UseTools(aiapitest.Call("c1", "get_queue", "{}")),
		aiapitest.Fail(&aiapi.Error{Kind: aiapi.KindTruncated}),
	)
	h := serve(t, r.service(model))
	rp := askCopilot(h, "What do I do first?", nil)

	if rp.status != http.StatusOK || len(rp.traces()) != 2 {
		t.Fatalf("answer = %d, traces %v", rp.status, rp.traces())
	}
	last := rp.events[len(rp.events)-1]
	if last["type"] != "error" {
		t.Fatalf("last event = %v, want an error line", last)
	}
	e := last["error"].(map[string]any)
	if e["code"] != "layanan" || e["msg"].(map[string]any)["key"] != "srv.api.aiError" {
		t.Errorf("error line = %v", e)
	}
}

func TestCopilotAnswersInTheLanguageOfTheRequest(t *testing.T) {
	r := newRig(t)
	r.seed()
	script := func() *aiapitest.Fake {
		return aiapitest.New(
			aiapitest.UseTools(aiapitest.Call("c1", "get_queue", "{}"), aiapitest.Call("c2", "propose_action", `{"kind":"buka_plan","plan":"PL-2004"}`)),
			aiapitest.Say("ok"),
		)
	}
	for _, tt := range []struct {
		header, language, queue, target, action, detail string
	}{
		{"", "Answer in English", "Reading the queue", "6 active plans", "Open PL-2004", "waiting for a driver: 3"},
		{"en-US,en;q=0.9", "Answer in English", "Reading the queue", "6 active plans", "Open PL-2004", "waiting for a driver: 3"},
		{"id-ID,id;q=0.9,en;q=0.8", "Answer in Indonesian (Bahasa Indonesia)", "Membaca antrean", "6 plan aktif", "Buka PL-2004", "menunggu driver: 3"},
	} {
		t.Run(tt.header, func(t *testing.T) {
			model := script()
			h := serve(t, r.service(model))
			var header map[string]string
			if tt.header != "" {
				header = map[string]string{"Accept-Language": tt.header}
			}
			rp := askCopilot(h, "apa yang harus dikerjakan?", header)
			if system := model.Requests()[0].Messages[0].Content; !strings.Contains(system, tt.language) {
				t.Errorf("the system prompt does not say %q", tt.language)
			}
			tr := rp.traces()
			if tr[1]["title"] != tt.queue || tr[1]["target"] != tt.target || !strings.Contains(tr[1]["detail"].(string), tt.detail) {
				t.Errorf("queue line = %v, want %q / %q / %q", tr[1], tt.queue, tt.target, tt.detail)
			}
			if a := rp.actions(); len(a) != 1 || a[0]["label"] != tt.action {
				t.Errorf("actions = %v, want %q", a, tt.action)
			}
		})
	}
}

func TestCopilotIsRateLimitedPerAdmin(t *testing.T) {
	r := newRig(t)
	r.seed()
	steps := make([]aiapitest.Step, 0, 8)
	for range 8 {
		steps = append(steps, aiapitest.Say("ok"))
	}
	model := aiapitest.New(steps...)
	h := serve(t, r.service(model, func(o *Options) { o.Burst, o.RatePerMinute = 2, 1 }))
	one, two := h.token(contracts.RoleAdmin, "adm-1"), h.token(contracts.RoleAdmin, "adm-2")

	for i := range 2 {
		if rp := h.call(one, "ai.copilot", askBody("hello"), nil); rp.status != http.StatusOK {
			t.Fatalf("request %d: %d %s", i+1, rp.status, rp.body)
		}
	}
	// An invalid question costs nothing; the third real request is over the burst, for copilot and insights alike.
	if rp := h.call(one, "ai.copilot", askBody(" "), nil); rp.status != http.StatusUnprocessableEntity {
		t.Errorf("empty question: %d", rp.status)
	}
	for _, method := range []string{"ai.copilot", "ai.insights"} {
		var in any = askBody("hello")
		if method == "ai.insights" {
			in = struct{}{}
		}
		rp := h.call(one, method, in, nil)
		key, _ := rp.errorMsg()
		if rp.status != http.StatusTooManyRequests || key != "srv.rpc.tooManyRequests" {
			t.Errorf("%s over the burst: %d %s", method, rp.status, rp.body)
		}
	}
	if calls := model.Calls(); calls != 2 {
		t.Errorf("the model was called %d times, want 2: the limited requests must not reach it", calls)
	}
	// Another admin has a rate of his own.
	if rp := h.call(two, "ai.copilot", askBody("hello"), nil); rp.status != http.StatusOK {
		t.Errorf("another admin: %d %s", rp.status, rp.body)
	}
}

func TestCopilotStopsWhenTheClientCancels(t *testing.T) {
	r := newRig(t)
	r.seed()
	held, released := make(chan struct{}), make(chan error, 1)
	model := aiapitest.New(
		aiapitest.UseTools(aiapitest.Call("c1", "get_queue", "{}")),
		aiapitest.Step{Chunks: []string{"PL-2001 ", "waits."}, Hold: func(ctx context.Context) {
			close(held)
			<-ctx.Done() // the model call goes on until the request's context ends
			released <- ctx.Err()
		}},
		aiapitest.Say("a step that must never be used"),
	)
	h := serve(t, r.service(model))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	finished := make(chan reply, 1)
	go func() {
		finished <- h.callCtx(ctx, h.token(contracts.RoleAdmin, "adm-1"), "ai.copilot", askBody("What first?"), nil, nil)
	}()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the model call never started")
	}
	cancel() // the admin closes the panel while the model is answering
	select {
	case err := <-released:
		if err == nil {
			t.Error("the model call ended without its context being cancelled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not stop when the client went away")
	}
	<-finished
	// Give the handler a moment to finish; nothing may come after the cancellation.
	time.Sleep(100 * time.Millisecond)
	if model.Remaining() != 1 || model.Calls() != 2 {
		t.Errorf("model calls = %d, unused steps = %d: the run went on after the cancel", model.Calls(), model.Remaining())
	}
}

func TestCopilotStopsWhenTheStreamCannotBeWrittenTo(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(
		aiapitest.UseTools(aiapitest.Call("c1", "get_queue", "{}"), aiapitest.Call("c2", "list_drivers", "{}")),
		aiapitest.Say("never"),
	)
	svc := r.service(model)
	writes := 0
	err := svc.copilot(platformAdmin(t), copilotBody{Scope: CopilotScope{Kind: "ops"}, Question: "hello"}, func(any) error {
		writes++
		if writes == 2 { // the first trace line is out, the second write fails: the client is gone
			return fmt.Errorf("broken pipe")
		}
		return nil
	})
	if err != nil {
		t.Errorf("copilot returned %v: with nobody to answer it just stops", err)
	}
	if writes != 2 {
		t.Errorf("events written = %d, want 2: nothing may be sent after a failed write (the second tool must not even run)", writes)
	}
	if model.Calls() != 1 {
		t.Errorf("model calls = %d, want 1", model.Calls())
	}
}

func platformAdmin(t *testing.T) context.Context {
	t.Helper()
	ctx := platform.WithPrincipal(t.Context(), platform.Principal{Role: contracts.RoleAdmin, UserID: "adm-1", Name: "Rani"})
	return platform.WithLocale(ctx, platform.LocaleEN)
}

// snapshot is a digest of every table a Copilot run could conceivably touch.
func snapshot(t *testing.T, r *rig) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range []string{"plans", "routes", "drivers", "vehicles", "settings", "customers", "outbox", "inbox", "notifications", "wallet_entries", "wallet_balances", "topups", "sessions"} {
		var digest string
		err := r.pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT count(*) || ':' || md5(coalesce(string_agg(x::text, '|' ORDER BY x::text), '')) FROM %s x`, table)).Scan(&digest)
		if err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		out[table] = digest
	}
	return out
}

func TestCopilotAndInsightsChangeNothing(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(
		aiapitest.UseTools(
			aiapitest.Call("1", "get_queue", "{}"), aiapitest.Call("2", "get_plan", `{"plan":"PL-2005"}`),
			aiapitest.Call("3", "list_drivers", "{}"), aiapitest.Call("4", "list_vehicles", "{}"),
			aiapitest.Call("5", "suggest_drivers", `{"plan":"PL-2001"}`), aiapitest.Call("6", "today_numbers", "{}"),
			aiapitest.Call("7", "propose_action", `{"kind":"tugaskan_terbaik","plan":"PL-2001"}`),
			aiapitest.Call("8", "propose_action", `{"kind":"buka_plan","plan":"PL-2002"}`),
		),
		aiapitest.Say("Done reading."),
		aiapitest.Say("- A week of figures."),
	)
	h := serve(t, r.service(model))
	before := snapshot(t, r)

	rp := askCopilot(h, "check everything and assign the best driver", nil)
	if rp.status != http.StatusOK || len(rp.traces()) != 16 || len(rp.actions()) != 2 {
		t.Fatalf("copilot: %d, %d trace events, %d actions\n%s", rp.status, len(rp.traces()), len(rp.actions()), rp.body)
	}
	rp = h.call(h.token(contracts.RoleAdmin, "adm-1"), "ai.insights", struct{}{}, nil)
	if rp.status != http.StatusOK || rp.done() == nil {
		t.Fatalf("insights: %d %s", rp.status, rp.body)
	}

	after := snapshot(t, r)
	for table, was := range before {
		if after[table] != was {
			t.Errorf("table %s changed: %s -> %s", table, was, after[table])
		}
	}
	// The proposal was only a proposal: PL-2001 still has no driver.
	var driver *string
	if err := r.pool.QueryRow(t.Context(), `SELECT driver_id FROM routes WHERE id = 'pln-1:r1'`).Scan(&driver); err != nil || driver != nil {
		t.Errorf("route of PL-2001 has driver %v (%v), want none", driver, err)
	}
}

func TestEveryToolOfTheCopilotIsDeclaredReadOnly(t *testing.T) {
	// The tool list is the whole of what the model can reach; this pins it, so that a tool added later has to be
	// added here on purpose, after asking whether it reads.
	rn := &copilotRun{s: &Service{}, l: newLang(platform.LocaleEN)}
	var names []string
	for _, tool := range rn.tools() {
		names = append(names, tool.Name)
		if len(tool.Schema) > 0 {
			var schema struct {
				AdditionalProperties *bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(tool.Schema, &schema); err != nil || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
				t.Errorf("%s: its schema must refuse unknown arguments", tool.Name)
			}
		}
	}
	want := []string{"get_queue", "get_plan", "list_drivers", "list_vehicles", "suggest_drivers", "today_numbers", "propose_action"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}
