package app_test

import (
	"bufio"
	"bytes"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/app"
)

// These tests start the whole service with an AI gateway address (a stub that speaks the gateway's protocol) or
// without one, and ask the admin's AI methods what an admin's screen would.

// chatStub is a gateway that answers chat calls with the given replies, one per call, as NDJSON streams, and
// records what it was asked and with which credentials.
type chatStub struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []aiapi.ChatRequest
	auth     []string
}

func newChatStub(t *testing.T, replies ...aiapi.ChatResponse) *chatStub {
	t.Helper()
	s := &chatStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req aiapi.ChatRequest
		if r.URL.Path != aiapi.ChatPath || json.UnmarshalRead(r.Body, &req) != nil {
			http.Error(w, "unexpected call", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		n := len(s.requests)
		s.requests = append(s.requests, req)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		if n >= len(replies) {
			http.Error(w, "no more replies", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		reply := replies[n]
		if reply.Message.Content != "" {
			line, _ := json.Marshal(aiapi.Event{Type: aiapi.EventDelta, Content: reply.Message.Content})
			_, _ = w.Write(append(line, '\n'))
		}
		line, _ := json.Marshal(aiapi.Event{Type: aiapi.EventDone, Response: &reply})
		_, _ = w.Write(append(line, '\n'))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *chatStub) calls() ([]aiapi.ChatRequest, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]aiapi.ChatRequest(nil), s.requests...), append([]string(nil), s.auth...)
}

// stream calls a streaming method as the admin and returns its events.
func (s *stack) stream(method string, in any) (int, []map[string]any, []byte) {
	s.t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		s.t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(s.t.Context(), http.MethodPost, s.base+"/v1/rpc/"+method, bytes.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-ndjson") {
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, nil, data
	}
	var events []map[string]any
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			s.t.Fatalf("bad stream line %q: %v", sc.Text(), err)
		}
		events = append(events, ev)
	}
	return resp.StatusCode, events, nil
}

func TestWithoutAGatewayTheAIMethodsAreOffButTheSettingsAreTheAdmins(t *testing.T) {
	s := start(t, nil) // no AIURL

	// settings.get is the admin's stored truth, whatever the gateway: the settings page must show what the admin chose.
	var got struct {
		AI       contracts.AIFeatures       `json:"ai"`
		Dispatch contracts.DispatchSettings `json:"dispatch"`
	}
	s.admin("settings.get", map[string]any{}, &got)
	if !got.AI.Copilot || !got.AI.Vision || !got.AI.Voice || !got.AI.Intent {
		t.Fatalf("settings.get = %+v, want the stored flags (all on) unmodified", got.AI)
	}

	status, _, body := s.stream("ai.copilot", map[string]any{"scope": map[string]any{"kind": "ops"}, "question": "what first?"})
	if status != http.StatusInternalServerError || !strings.Contains(string(body), `"srv.api.aiError"`) || !strings.Contains(string(body), `"srv.api.feature.copilot"`) {
		t.Errorf("ai.copilot: %d %s, want 500 aiError for the Copilot", status, body)
	}
	status, _, body = s.stream("ai.insights", map[string]any{})
	if status != http.StatusInternalServerError || !strings.Contains(string(body), `"srv.api.aiError"`) || !strings.Contains(string(body), `"srv.api.feature.insights"`) {
		t.Errorf("ai.insights: %d %s, want 500 aiError for the summary", status, body)
	}

	// An admin who switches the Copilot off is told so, and settings.get then shows his choice.
	s.admin("settings.update", map[string]any{"patch": map[string]any{"ai": map[string]any{"copilot": false}}}, nil)
	status, _, body = s.stream("ai.copilot", map[string]any{"scope": map[string]any{"kind": "ops"}, "question": "what first?"})
	if status != http.StatusForbidden || !strings.Contains(string(body), `"srv.api.aiDisabled"`) {
		t.Errorf("switched off: %d %s, want 403 aiDisabled", status, body)
	}
	s.admin("settings.get", map[string]any{}, &got)
	if got.AI.Copilot || !got.AI.Vision {
		t.Errorf("settings.get = %+v, want copilot off and the rest as they were", got.AI)
	}
}

func TestAnUnreachableGatewayIsAiErrorNotTheGenericOne(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	s := start(t, func(c *app.Config) { c.AIURL, c.AIToken = url, "test-token" })

	status, events, body := s.stream("ai.copilot", map[string]any{"scope": map[string]any{"kind": "ops"}, "question": "what first?"})
	if status != http.StatusInternalServerError || len(events) != 0 || !strings.Contains(string(body), `"srv.api.aiError"`) || strings.Contains(string(body), "srv.engine.unavailable") {
		t.Errorf("ai.copilot: %d %s", status, body)
	}
}

func TestTheCopilotAndTheSummaryReadTheBackOfficesOwnData(t *testing.T) {
	stub := newChatStub(t,
		aiapi.ChatResponse{Message: aiapi.Message{Role: aiapi.RoleAssistant, ToolCalls: []aiapi.ToolCall{{ID: "c1", Name: "get_queue", Arguments: "{}"}}}, FinishReason: aiapi.FinishToolCalls},
		aiapi.ChatResponse{Message: aiapi.Message{Role: aiapi.RoleAssistant, Content: "PL-2001 is waiting for a driver."}, FinishReason: aiapi.FinishStop},
		aiapi.ChatResponse{Message: aiapi.Message{Role: aiapi.RoleAssistant, Content: "- A quiet week."}, FinishReason: aiapi.FinishStop},
	)
	s := start(t, func(c *app.Config) { c.AIURL, c.AIToken = stub.srv.URL, "test-token" })

	// A plan waits for a driver (no driver is online).
	s.produce("console-api", contracts.TypeCustomerRegistered, "cus-1", 1, contracts.CustomerRegistered{CustomerID: "cus-1", Name: "Dina Pramesti"})
	s.confirm("pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor))
	s.waitPlan("pln-1", "the plan to reach the queue", func(contracts.Plan) bool { return true })

	status, events, body := s.stream("ai.copilot", map[string]any{"scope": map[string]any{"kind": "ops"}, "question": "What needs me first?"})
	if status != http.StatusOK || len(events) < 4 {
		t.Fatalf("ai.copilot: %d %s", status, body)
	}
	last := events[len(events)-1]
	if last["type"] != "done" || last["requestId"] == "" {
		t.Errorf("the answer ends with %v", last)
	}
	var text strings.Builder
	for _, ev := range events {
		if ev["type"] == "token" {
			text.WriteString(ev["text"].(string))
		}
	}
	if text.String() != "PL-2001 is waiting for a driver." {
		t.Errorf("answer = %q", text.String())
	}

	status, events, body = s.stream("ai.insights", map[string]any{})
	if status != http.StatusOK || events[len(events)-1]["type"] != "done" {
		t.Fatalf("ai.insights: %d %s", status, body)
	}

	reqs, auth := stub.calls()
	if len(reqs) != 3 {
		t.Fatalf("the gateway was called %d times, want 3", len(reqs))
	}
	for i, a := range auth {
		if a != "Bearer test-token" {
			t.Errorf("call %d authorization = %q, want the configured token", i+1, a)
		}
	}
	if reqs[0].Profile != aiapi.ProfileCopilot || len(reqs[0].Tools) != 7 {
		t.Errorf("copilot request = profile %q, %d tools", reqs[0].Profile, len(reqs[0].Tools))
	}
	// The queue the model read is the projection's: it names the plan that was confirmed.
	var queue string
	for _, m := range reqs[1].Messages {
		if m.Role == aiapi.RoleTool {
			queue = m.Content
		}
	}
	if !strings.Contains(queue, `"plan":"PL-pln-1"`) && !strings.Contains(queue, `"id":"pln-1"`) {
		t.Errorf("the model's view of the queue = %s", queue)
	}
	if reqs[2].Profile != aiapi.ProfileInsights || len(reqs[2].Tools) != 0 || !strings.Contains(reqs[2].Messages[1].Content, `"plansWaitingForADriver":1`) {
		t.Errorf("insights request = profile %q, %d tools, figures %q", reqs[2].Profile, len(reqs[2].Tools), reqs[2].Messages[1].Content)
	}
}
