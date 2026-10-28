package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/meta"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/ai/internal/profile"
)

const (
	metaKey   = "meta-key-0123456789-DO-NOT-LEAK"
	callerKey = "caller-token-s3cret-DO-NOT-LEAK"
)

// syncBuffer is a log sink that handler goroutines and the test can share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeMeta is a fake Model API that records what the gateway sends it.
type fakeMeta struct {
	mu       sync.Mutex
	requests []metaRequest
	handler  func(n int, w http.ResponseWriter, r *http.Request)
}

type metaRequest struct {
	Path   string
	Header http.Header
	Body   []byte
}

func (f *fakeMeta) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body) // read first: the server notices a vanished client only after the body is consumed
	r.Body = io.NopCloser(bytes.NewReader(body))
	f.mu.Lock()
	f.requests = append(f.requests, metaRequest{Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	n := len(f.requests)
	f.mu.Unlock()
	f.handler(n, w, r)
}

func (f *fakeMeta) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeMeta) request(i int) metaRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

func (f *fakeMeta) body(t *testing.T, i int) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(f.request(i).Body, &m); err != nil {
		t.Fatalf("request %d is not JSON: %v", i, err)
	}
	return m
}

type rig struct {
	client  *aiapi.Client
	url     string
	meta    *fakeMeta
	logs    *syncBuffer
	handler *Handler
}

type rigOptions struct {
	key       string
	tokens    []meta.Secret
	profiles  profile.Set
	limiter   *platform.LoginLimiter
	inFlight  int
	upstream  func(n int, w http.ResponseWriter, r *http.Request)
	noRetries bool
}

const replyOK = `{"id":"r1","model":"served-model","choices":[{"index":0,"message":{"role":"assistant","content":"the answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":21,"completion_tokens":9,"total_tokens":30,"completion_tokens_details":{"reasoning_tokens":4}}}`

func newRig(t *testing.T, mod ...func(*rigOptions)) *rig {
	t.Helper()
	o := rigOptions{
		key: metaKey, tokens: []meta.Secret{callerKey}, profiles: profile.Defaults("m-chat", "m-voice"),
		upstream: func(_ int, w http.ResponseWriter, _ *http.Request) { writeRaw(w, 200, replyOK) },
	}
	for _, m := range mod {
		m(&o)
	}
	fm := &fakeMeta{handler: o.upstream}
	metaSrv := httptest.NewServer(fm)
	t.Cleanup(metaSrv.Close)

	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	retries := 0
	if o.noRetries {
		retries = -1
	}
	h := New(Options{
		Upstream: meta.New(meta.Config{BaseURL: metaSrv.URL + "/v1", APIKey: meta.Secret(o.key), MaxRetries: retries, Backoff: time.Millisecond}),
		Profiles: o.profiles, Tokens: o.tokens, Limiter: o.limiter, MaxInFlight: o.inFlight, Log: log,
	})
	gw := httptest.NewServer(platform.Chain(h.Routes(), platform.TraceMiddleware, platform.AccessLog(log), platform.Recover(log)))
	t.Cleanup(gw.Close)
	return &rig{client: &aiapi.Client{BaseURL: gw.URL, Token: callerKey}, url: gw.URL, meta: fm, logs: logs, handler: h}
}

func writeRaw(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

func metaError(w http.ResponseWriter, status int, typ, code, message string) {
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"message": message, "type": typ, "param": nil, "code": code}})
	writeRaw(w, status, string(body))
}

// post sends a raw request to the gateway and returns the status and body.
func (r *rig) post(t *testing.T, path, token, contentType string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, r.url+path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, out
}

func (r *rig) get(t *testing.T, path, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, r.url+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func asking(profile aiapi.Profile, text string) aiapi.ChatRequest {
	return aiapi.ChatRequest{Profile: profile, Messages: []aiapi.Message{aiapi.SystemText("be useful"), aiapi.UserText(text)}}
}

func wantKind(t *testing.T, err error, want *aiapi.Error) *aiapi.Error {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want kind %s", err, want.Kind)
	}
	e, _ := errors.AsType[*aiapi.Error](err)
	return e
}

func png(extra string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), extra...)
}

func TestEachProfileAppliesItsOwnSettingsAndCallersCannotChangeThem(t *testing.T) {
	profiles := profile.Defaults("m-chat", "m-voice")
	for name, model := range map[aiapi.Profile]string{
		aiapi.ProfileCopilot: "m-copilot", aiapi.ProfileVision: "m-vision", aiapi.ProfileIntent: "m-intent", aiapi.ProfileInsights: "m-insights",
		aiapi.ProfileReview: "m-review", aiapi.ProfileReport: "m-report",
	} {
		p := profiles[name]
		p.Model = model
		profiles[name] = p
	}
	r := newRig(t, func(o *rigOptions) { o.profiles = profiles })

	for _, name := range aiapi.ChatProfiles {
		want := profiles[name]
		resp, err := r.client.Chat(t.Context(), asking(name, "hello"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if resp.Message.Content != "the answer" || resp.Model != "served-model" || resp.Usage != (aiapi.Usage{Prompt: 21, Completion: 9, Reasoning: 4, Total: 30}) || resp.FinishReason != "stop" {
			t.Errorf("%s: response = %+v", name, resp)
		}
		body := r.meta.body(t, r.meta.count()-1)
		if body["model"] != want.Model || body["reasoning_effort"] != want.ReasoningEffort || body["max_completion_tokens"] != float64(want.MaxTokens) {
			t.Errorf("%s: Meta received model %v effort %v max tokens %v, want %s %s %d", name, body["model"], body["reasoning_effort"], body["max_completion_tokens"], want.Model, want.ReasoningEffort, want.MaxTokens)
		}
		if want.MaxTokens < 1500 || want.ReasoningEffort == "" || want.ReasoningEffort == "none" {
			t.Errorf("%s: unsafe defaults %+v", name, want)
		}
		if _, streaming := body["stream"]; streaming {
			t.Errorf("%s: a plain call must not stream", name)
		}
		rq := r.meta.request(r.meta.count() - 1)
		if rq.Path != "/v1/chat/completions" || rq.Header.Get("Authorization") != "Bearer "+metaKey {
			t.Errorf("%s: upstream request %s auth %q", name, rq.Path, rq.Header.Get("Authorization"))
		}
	}

	// A caller's own model, effort or budget fields are ignored: they are not part of the wire format and a
	// hand-written request carrying them changes nothing.
	raw := `{"profile":"intent","model":"expensive-model","reasoning_effort":"high","max_completion_tokens":99999,"messages":[{"role":"user","content":"hi"}]}`
	if status, _, body := r.post(t, aiapi.ChatPath, callerKey, "application/json", []byte(raw)); status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	last := r.meta.body(t, r.meta.count()-1)
	if last["model"] != "m-intent" || last["reasoning_effort"] != "minimal" || last["max_completion_tokens"] != float64(profiles[aiapi.ProfileIntent].MaxTokens) {
		t.Errorf("a caller overrode the profile: %v", last)
	}
}

func TestToolsAndImagesReachMetaInItsFormat(t *testing.T) {
	r := newRig(t, func(o *rigOptions) {
		o.upstream = func(_ int, w http.ResponseWriter, _ *http.Request) {
			writeRaw(w, 200, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"get_plan","arguments":"{\"id\":\"PL-1\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`)
		}
	})
	resp, err := r.client.Chat(t.Context(), aiapi.ChatRequest{
		Profile: aiapi.ProfileCopilot,
		Messages: []aiapi.Message{
			aiapi.UserText("status of PL-1?"),
			{Role: aiapi.RoleAssistant, ToolCalls: []aiapi.ToolCall{{ID: "c0", Name: "list_plans", Arguments: "{}"}}},
			{Role: aiapi.RoleTool, ToolCallID: "c0", Content: `[{"id":"PL-1"}]`},
		},
		Tools: []aiapi.Tool{{Name: "get_plan", Description: "Read a plan.", Schema: []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`)}, {Name: "list_plans"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != "tool_calls" || len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0] != (aiapi.ToolCall{ID: "c1", Name: "get_plan", Arguments: `{"id":"PL-1"}`}) {
		t.Errorf("response = %+v", resp)
	}
	body := r.meta.body(t, 0)
	tools := body["tools"].([]any)
	msgs := body["messages"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["function"].(map[string]any)["name"] != "get_plan" {
		t.Errorf("tools = %v", tools)
	}
	if msgs[1].(map[string]any)["tool_calls"] == nil || msgs[2].(map[string]any)["tool_call_id"] != "c0" || msgs[2].(map[string]any)["role"] != "tool" {
		t.Errorf("messages = %v", msgs)
	}

	data := png("pixels")
	if _, err := r.client.Chat(t.Context(), aiapi.ChatRequest{
		Profile:    aiapi.ProfileVision,
		Messages:   []aiapi.Message{{Role: aiapi.RoleUser, Content: "list the items", Images: []aiapi.Image{{MIME: "image/jpeg", Data: data}}}},
		JSONSchema: &aiapi.JSONSchema{Name: "items", Schema: []byte(`{"type":"object"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	parts := r.meta.body(t, 1)["messages"].([]any)[0].(map[string]any)["content"].([]any)
	img := parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
	if parts[0].(map[string]any)["text"] != "list the items" || img != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data) {
		t.Errorf("parts = %v (the type sent is the sniffed one, not the declared image/jpeg)", parts)
	}
	if rf := r.meta.body(t, 1)["response_format"].(map[string]any); rf["type"] != "json_schema" {
		t.Errorf("response_format = %v", rf)
	}
}

func TestCallersMustAuthenticate(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct{ name, path, method, token string }{
		{"chat without a token", aiapi.ChatPath, "POST", ""},
		{"chat with a wrong token", aiapi.ChatPath, "POST", "nope"},
		{"transcribe without a token", aiapi.TranscribePath, "POST", ""},
		{"transcribe with a wrong token", aiapi.TranscribePath, "POST", callerKey + "x"},
		{"info without a token", aiapi.InfoPath, "GET", ""},
	} {
		var status int
		var body []byte
		if tc.method == "GET" {
			status, body = r.get(t, tc.path, tc.token)
		} else {
			status, _, body = r.post(t, tc.path, tc.token, "application/json", []byte(`{"profile":"intent","messages":[{"role":"user","content":"hi"}]}`))
		}
		var env aiapi.ErrorEnvelope
		if status != 401 || json.Unmarshal(body, &env) != nil || env.Error.Kind != aiapi.KindUnauthorized {
			t.Errorf("%s: status %d body %s", tc.name, status, body)
		}
	}
	if r.meta.count() != 0 {
		t.Errorf("%d unauthenticated requests reached Meta", r.meta.count())
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		if status, _ := r.get(t, path, ""); status != 200 {
			t.Errorf("%s needs no token, got %d", path, status)
		}
	}
	bad := &aiapi.Client{BaseURL: r.url, Token: "wrong"}
	_, err := bad.Chat(t.Context(), asking(aiapi.ProfileIntent, "hi"))
	wantKind(t, err, aiapi.ErrUnauthorized)
}

func TestSeveralTokensAreSeveralCallersWithTheirOwnLimits(t *testing.T) {
	r := newRig(t, func(o *rigOptions) {
		o.tokens = []meta.Secret{"console-token", "backoffice-token", "optimizer-token"}
		o.limiter = platform.NewLoginLimiter(time.Hour, 2) // two calls, then one an hour
	})
	console := &aiapi.Client{BaseURL: r.url, Token: "console-token"}
	backoffice := &aiapi.Client{BaseURL: r.url, Token: "backoffice-token"}
	optimizer := &aiapi.Client{BaseURL: r.url, Token: "optimizer-token"}

	for i := range 2 {
		if _, err := console.Chat(t.Context(), asking(aiapi.ProfileIntent, "hi")); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	_, err := console.Chat(t.Context(), asking(aiapi.ProfileIntent, "hi"))
	e := wantKind(t, err, aiapi.ErrRateLimited)
	if e.Status != 429 || e.RetryAfter < time.Second {
		t.Errorf("error = %+v", e)
	}
	if api, _ := errors.AsType[*platform.APIError](err); api == nil || api.Status != 429 || api.RetryAfter == 0 {
		t.Errorf("platform error = %+v", api)
	}
	status, header, _ := r.post(t, aiapi.ChatPath, "console-token", "application/json", []byte(`{}`))
	if status != 429 || header.Get("Retry-After") == "" {
		t.Errorf("raw status %d, Retry-After %q", status, header.Get("Retry-After"))
	}
	if _, err := backoffice.Chat(t.Context(), asking(aiapi.ProfileIntent, "hi")); err != nil {
		t.Errorf("another caller must not be throttled by the first: %v", err)
	}

	// The optimizer is a third caller with its own token and its own bucket: it reviews quotes on the review profile,
	// and neither the console's spent burst nor the back office's call counts against it.
	for i := range 2 {
		if _, err := optimizer.Chat(t.Context(), asking(aiapi.ProfileReview, "facts")); err != nil {
			t.Fatalf("optimizer call %d: %v", i, err)
		}
	}
	_, err = optimizer.Chat(t.Context(), asking(aiapi.ProfileReview, "facts"))
	wantKind(t, err, aiapi.ErrRateLimited)
	if _, err := backoffice.Chat(t.Context(), asking(aiapi.ProfileIntent, "hi")); err != nil {
		t.Errorf("the optimizer's burst must not throttle the back office: %v", err)
	}
	if r.meta.count() != 6 {
		t.Errorf("%d calls reached Meta, want 6 (the throttled ones must not)", r.meta.count())
	}
	callers := map[string]int{}
	for _, rec := range logLines(t, r.logs) {
		if rec["msg"] == "chat" {
			callers[rec["caller"].(string)+"/"+rec["profile"].(string)]++
		}
	}
	if want := map[string]int{"token0/intent": 2, "token1/intent": 2, "token2/review": 2}; !maps.Equal(callers, want) {
		t.Errorf("callers in the log = %v, want %v", callers, want)
	}
}

// review and report are chat profiles of their own: a model and budget each, no tools and no photos, and a body
// limit of their own (the reviewer's is a small fact sheet, the report's a few options).
func TestReviewAndReportProfilesApplyTheirOwnLimits(t *testing.T) {
	r := newRig(t)
	schema := &aiapi.JSONSchema{Name: "next_experiments", Schema: []byte(`{"type":"object","properties":{"satisfied":{"type":"boolean"}}}`)}

	review := asking(aiapi.ProfileReview, "facts about the candidates")
	review.JSONSchema = schema
	if _, err := r.client.Chat(t.Context(), review); err != nil {
		t.Fatalf("review: %v", err)
	}
	body := r.meta.body(t, 0)
	if rf, _ := body["response_format"].(map[string]any); rf["type"] != "json_schema" {
		t.Errorf("the review schema did not reach Meta: %v", body["response_format"])
	}
	if body["model"] != "m-chat" || body["reasoning_effort"] != "minimal" || body["max_completion_tokens"] != 2000.0 {
		t.Errorf("review reached Meta as %v", body)
	}
	if _, err := r.client.Chat(t.Context(), asking(aiapi.ProfileReport, "facts")); err != nil {
		t.Fatalf("report: %v", err)
	}
	if body := r.meta.body(t, 1); body["reasoning_effort"] != "minimal" || body["max_completion_tokens"] != 3000.0 {
		t.Errorf("report reached Meta as %v", body)
	}

	// Neither takes tools or photos.
	for _, name := range []aiapi.Profile{aiapi.ProfileReview, aiapi.ProfileReport} {
		withTools := asking(name, "hi")
		withTools.Tools = []aiapi.Tool{{Name: "get_plan"}}
		wantKind(t, mustFail(t, r, withTools), aiapi.ErrInvalid)
		withImage := asking(name, "hi")
		withImage.Messages[1].Images = []aiapi.Image{{MIME: "image/png", Data: png("p")}}
		wantKind(t, mustFail(t, r, withImage), aiapi.ErrInvalid)
		asToolTurn := asking(name, "hi")
		asToolTurn.Messages = append(asToolTurn.Messages, aiapi.Message{Role: aiapi.RoleTool, ToolCallID: "c1", Content: "{}"})
		wantKind(t, mustFail(t, r, asToolTurn), aiapi.ErrInvalid)
	}

	// 100 KiB fits the report (256 KiB) and not the review (64 KiB); a report over 256 KiB is refused too.
	sized := func(name aiapi.Profile, n int) error {
		q := asking(name, "x")
		q.Messages[1].Content = strings.Repeat("a", n)
		_, err := r.client.Chat(t.Context(), q)
		return err
	}
	if err := sized(aiapi.ProfileReport, 100<<10); err != nil {
		t.Errorf("a 100 KiB report request: %v", err)
	}
	wantKind(t, sized(aiapi.ProfileReview, 100<<10), aiapi.ErrInvalid)
	wantKind(t, sized(aiapi.ProfileReport, 257<<10), aiapi.ErrInvalid)
	if r.meta.count() != 3 {
		t.Errorf("%d calls reached Meta, want 3 (the refused ones must not)", r.meta.count())
	}
}

func mustFail(t *testing.T, r *rig, req aiapi.ChatRequest) error {
	t.Helper()
	_, err := r.client.Chat(t.Context(), req)
	if err == nil {
		t.Fatalf("a %s request that should be refused was accepted", req.Profile)
	}
	return err
}

func TestRequestsOverTheirLimitsOrOutsideTheirProfileAreRefusedBeforeMeta(t *testing.T) {
	r := newRig(t)
	valid := func() aiapi.ChatRequest { return asking(aiapi.ProfileIntent, "hi") }
	bigImage := aiapi.Image{MIME: "image/png", Data: png(strings.Repeat("x", profile.MaxImageBytes))}
	fiveImages := make([]aiapi.Image, profile.MaxImages+1)
	for i := range fiveImages {
		fiveImages[i] = aiapi.Image{MIME: "image/png", Data: png("p")}
	}
	cases := []struct {
		name      string
		req       func() aiapi.ChatRequest
		wantField string
		wantKey   string
	}{
		{"intent body over its limit", func() aiapi.ChatRequest {
			q := valid()
			q.Messages[1].Content = strings.Repeat("a", 65<<10)
			return q
		}, "", "srv.rpc.badRequest"},
		{"image over 4 MiB", func() aiapi.ChatRequest {
			return aiapi.ChatRequest{Profile: aiapi.ProfileVision, Messages: []aiapi.Message{{Role: aiapi.RoleUser, Images: []aiapi.Image{bigImage}}}}
		}, "messages.0.images.0", "srv.api.imageTooLarge"},
		{"not an image", func() aiapi.ChatRequest {
			return aiapi.ChatRequest{Profile: aiapi.ProfileVision, Messages: []aiapi.Message{{Role: aiapi.RoleUser, Images: []aiapi.Image{{MIME: "image/png", Data: []byte("<html>definitely not a png</html>")}}}}}
		}, "messages.0.images.0", "srv.api.fileNotImage"},
		{"a bitmap is not accepted", func() aiapi.ChatRequest {
			return aiapi.ChatRequest{Profile: aiapi.ProfileVision, Messages: []aiapi.Message{{Role: aiapi.RoleUser, Images: []aiapi.Image{{MIME: "image/bmp", Data: []byte("BM......................")}}}}}
		}, "messages.0.images.0", "srv.api.fileNotImage"},
		{"empty image", func() aiapi.ChatRequest {
			return aiapi.ChatRequest{Profile: aiapi.ProfileVision, Messages: []aiapi.Message{{Role: aiapi.RoleUser, Images: []aiapi.Image{{MIME: "image/png"}}}}}
		}, "messages.0.images.0", "srv.api.fileEmpty"},
		{"too many images", func() aiapi.ChatRequest {
			return aiapi.ChatRequest{Profile: aiapi.ProfileVision, Messages: []aiapi.Message{{Role: aiapi.RoleUser, Images: fiveImages}}}
		}, "messages.0.images.4", "srv.rpc.badRequest"},
		{"images on a profile that takes none", func() aiapi.ChatRequest {
			q := valid()
			q.Messages[1].Images = []aiapi.Image{{MIME: "image/png", Data: png("p")}}
			return q
		}, "messages.1.images", "srv.rpc.badRequest"},
		{"tools on a profile that takes none", func() aiapi.ChatRequest {
			q := valid()
			q.Tools = []aiapi.Tool{{Name: "get_plan"}}
			return q
		}, "tools", "srv.rpc.badRequest"},
		{"unknown profile", func() aiapi.ChatRequest { q := valid(); q.Profile = "admin"; return q }, "profile", "srv.rpc.badRequest"},
		{"the transcribe profile on the chat endpoint", func() aiapi.ChatRequest { q := valid(); q.Profile = aiapi.ProfileTranscribe; return q }, "profile", "srv.rpc.badRequest"},
		{"no messages", func() aiapi.ChatRequest { q := valid(); q.Messages = nil; return q }, "messages", "srv.rpc.badRequest"},
		{"unknown role", func() aiapi.ChatRequest { q := valid(); q.Messages[0].Role = "root"; return q }, "messages.0.role", "srv.rpc.badRequest"},
		{"a tool message without a call id", func() aiapi.ChatRequest {
			q := asking(aiapi.ProfileCopilot, "hi")
			q.Messages = append(q.Messages, aiapi.Message{Role: aiapi.RoleTool, Content: "{}"})
			return q
		}, "messages.2.toolCallId", "srv.rpc.badRequest"},
		{"a tool with a bad name", func() aiapi.ChatRequest {
			q := asking(aiapi.ProfileCopilot, "hi")
			q.Tools = []aiapi.Tool{{Name: "drop table;"}}
			return q
		}, "tools.0.name", "srv.rpc.badRequest"},
		{"duplicate tools", func() aiapi.ChatRequest {
			q := asking(aiapi.ProfileCopilot, "hi")
			q.Tools = []aiapi.Tool{{Name: "a"}, {Name: "a"}}
			return q
		}, "tools.1.name", "srv.rpc.badRequest"},
		{"a tool schema that is not an object", func() aiapi.ChatRequest {
			q := asking(aiapi.ProfileCopilot, "hi")
			q.Tools = []aiapi.Tool{{Name: "a", Schema: []byte(`[1,2]`)}}
			return q
		}, "tools.0.schema", "srv.rpc.badRequest"},
		{"a json schema that is not an object", func() aiapi.ChatRequest {
			q := valid()
			q.JSONSchema = &aiapi.JSONSchema{Name: "s", Schema: []byte(`"string"`)}
			return q
		}, "jsonSchema.schema", "srv.rpc.badRequest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.client.Chat(t.Context(), tc.req())
			e := wantKind(t, err, aiapi.ErrInvalid)
			if e.Field != tc.wantField || e.Msg.Key != tc.wantKey {
				t.Errorf("field %q key %q, want %q %q", e.Field, e.Msg.Key, tc.wantField, tc.wantKey)
			}
			if api, ok := errors.AsType[*platform.APIError](err); !ok || api.Status != 422 || api.Code != platform.CodeInvalid {
				t.Errorf("platform error = %+v", api)
			}
		})
	}
	if r.meta.count() != 0 {
		t.Errorf("%d refused requests reached Meta", r.meta.count())
	}

	// Bodies over every profile's limit are cut off at the door, and a body that is not JSON is a bad request.
	huge := bytes.Repeat([]byte("a"), 6<<20+1024)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, r.url+aiapi.ChatPath, bytes.NewReader(huge))
	req.Header.Set("Authorization", "Bearer "+callerKey)
	// The gateway answers without reading the body and closes the connection, so the client may see either the
	// 413 or the connection being cut while it is still sending; both mean the body was not accepted.
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("a %d byte body got status %d", len(huge), resp.StatusCode)
		}
	}
	if status, _, _ := r.post(t, aiapi.ChatPath, callerKey, "application/json", []byte("{not json")); status != http.StatusBadRequest {
		t.Errorf("malformed JSON got status %d", status)
	}
}

func TestMissingKeyAnswers503ButTheServiceIsUp(t *testing.T) {
	r := newRig(t, func(o *rigOptions) { o.key = "" })

	_, err := r.client.Chat(t.Context(), asking(aiapi.ProfileCopilot, "hi"))
	wantKind(t, err, aiapi.ErrNotConfigured)
	if api, ok := errors.AsType[*platform.APIError](err); !ok || api.Msg.Key != "srv.engine.unavailable" || api.Code != platform.CodeService {
		t.Errorf("platform error = %+v", api)
	}
	status, _, body := r.post(t, aiapi.ChatPath, callerKey, "application/json", []byte(`{"profile":"intent","messages":[{"role":"user","content":"hi"}]}`))
	var env aiapi.ErrorEnvelope
	if status != http.StatusServiceUnavailable || json.Unmarshal(body, &env) != nil || env.Error.Kind != aiapi.KindNotConfigured || env.Error.Msg.Key != "srv.engine.unavailable" {
		t.Errorf("status %d body %s", status, body)
	}
	_, err = r.client.ChatStream(t.Context(), asking(aiapi.ProfileCopilot, "hi"), nil)
	wantKind(t, err, aiapi.ErrNotConfigured)
	_, err = r.client.Transcribe(t.Context(), wavBytes(), "a.wav", "audio/wav")
	wantKind(t, err, aiapi.ErrNotConfigured)

	status, body = r.get(t, "/readyz", "")
	if status != 200 || !strings.Contains(string(body), `"configured":false`) || !strings.Contains(string(body), `"ready"`) {
		t.Errorf("readyz = %d %s: ready without a key, saying so", status, body)
	}
	if status, _ := r.get(t, "/healthz", ""); status != 200 {
		t.Errorf("healthz = %d", status)
	}
	info, err := r.client.Info(t.Context())
	if err != nil || info.Configured || len(info.Profiles) != 7 {
		t.Errorf("info = %+v, %v", info, err)
	}
	if r.meta.count() != 0 {
		t.Errorf("%d requests reached Meta without a key", r.meta.count())
	}
}

func TestReadyzReportsAKeyAndDrains(t *testing.T) {
	r := newRig(t)
	status, body := r.get(t, "/readyz", "")
	if status != 200 || !strings.Contains(string(body), `"configured":true`) {
		t.Errorf("readyz = %d %s", status, body)
	}
	if strings.Contains(string(body), metaKey) {
		t.Error("readyz leaks the key")
	}
	if r.meta.count() != 0 {
		t.Error("readyz must not call Meta")
	}
	info, err := r.client.Info(t.Context())
	if err != nil || !info.Configured {
		t.Fatalf("info = %+v, %v", info, err)
	}
	for _, p := range info.Profiles {
		if p.Name != aiapi.ProfileTranscribe && (p.ReasoningEffort == "" || p.MaxTokens < 1500 || p.TimeoutMs <= 0 || p.Model == "") {
			t.Errorf("profile info = %+v", p)
		}
	}
	r.handler.SetDraining()
	if status, _ := r.get(t, "/readyz", ""); status != http.StatusServiceUnavailable {
		t.Errorf("a draining gateway is not ready, got %d", status)
	}
}

func sse(w http.ResponseWriter, events ...string) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	f, _ := w.(http.Flusher)
	for _, e := range events {
		io.WriteString(w, "data: "+e+"\n\n")
		f.Flush()
	}
	return f
}

func TestStreamIsRelayedAsItArrives(t *testing.T) {
	release := make(chan struct{})
	r := newRig(t, func(o *rigOptions) {
		o.upstream = func(_ int, w http.ResponseWriter, _ *http.Request) {
			f := sse(w,
				`{"id":"s1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
				`{"id":"s1","choices":[{"index":0,"delta":{"reasoning_content":"thinking about it"}}]}`,
				`{"id":"s1","choices":[{"index":0,"delta":{"content":"Hel"}}]}`)
			<-release // the rest of the answer is held back until the test has seen the first part
			io.WriteString(w, "data: "+`{"id":"s1","choices":[{"index":0,"delta":{"content":"lo"}}]}`+"\n\n")
			io.WriteString(w, "data: "+`{"id":"s1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"get_plan","arguments":"{\"id\":"}}]}}]}`+"\n\n")
			io.WriteString(w, "data: "+`{"id":"s1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"PL-1\"}"}}]}}]}`+"\n\n")
			io.WriteString(w, "data: "+`{"id":"s1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
			io.WriteString(w, "data: "+`{"id":"s1","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":6,"total_tokens":18,"completion_tokens_details":{"reasoning_tokens":2}}}`+"\n\n")
			io.WriteString(w, "data: [DONE]\n\n")
			f.Flush()
		}
	})
	var got []string
	var fragments []aiapi.ToolCallDelta
	resp, err := r.client.ChatStream(t.Context(), aiapi.ChatRequest{
		Profile: aiapi.ProfileCopilot, Messages: []aiapi.Message{aiapi.UserText("hi")}, Tools: []aiapi.Tool{{Name: "get_plan"}},
	}, func(d aiapi.Delta) error {
		if d.Content != "" {
			got = append(got, d.Content)
			if len(got) == 1 {
				close(release) // the first fragment arrived while the upstream is still mid-answer
			}
		}
		fragments = append(fragments, d.ToolCalls...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "Hel|lo" {
		t.Errorf("fragments = %q (reasoning text is not relayed)", got)
	}
	if len(fragments) != 2 || fragments[0].Name != "get_plan" || fragments[1].Arguments != `"PL-1"}` {
		t.Errorf("tool-call fragments = %+v", fragments)
	}
	want := aiapi.ToolCall{ID: "c1", Name: "get_plan", Arguments: `{"id":"PL-1"}`}
	if resp.Message.Content != "Hello" || len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0] != want || resp.FinishReason != "tool_calls" ||
		resp.Usage != (aiapi.Usage{Prompt: 12, Completion: 6, Reasoning: 2, Total: 18}) {
		t.Errorf("assembled reply = %+v", resp)
	}
	body := r.meta.body(t, 0)
	if body["stream"] != true || body["model"] != "m-chat" || body["reasoning_effort"] != "minimal" {
		t.Errorf("upstream request = %v", body)
	}
}

func TestStreamFailures(t *testing.T) {
	t.Run("upstream refuses before streaming: an ordinary status", func(t *testing.T) {
		r := newRig(t, func(o *rigOptions) {
			o.upstream = func(_ int, w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "4")
				metaError(w, 429, "rate_limit_error", "rate_limit_exceeded", "slow down")
			}
		})
		body, _ := json.Marshal(aiapi.ChatRequest{Profile: aiapi.ProfileCopilot, Messages: []aiapi.Message{aiapi.UserText("hi")}, Stream: true})
		status, header, _ := r.post(t, aiapi.ChatPath, callerKey, "application/json", body)
		if status != http.StatusTooManyRequests || header.Get("Retry-After") != "4" {
			t.Errorf("status %d Retry-After %q", status, header.Get("Retry-After"))
		}
		_, err := r.client.ChatStream(t.Context(), asking(aiapi.ProfileCopilot, "hi"), nil)
		e := wantKind(t, err, aiapi.ErrRateLimited)
		if e.RetryAfter != 4*time.Second {
			t.Errorf("RetryAfter = %v", e.RetryAfter)
		}
		if r.meta.count() != 2 {
			t.Errorf("a stream is never retried: %d upstream requests for 2 calls", r.meta.count())
		}
	})
	t.Run("upstream breaks mid-stream: an error event after the deltas", func(t *testing.T) {
		r := newRig(t, func(o *rigOptions) {
			o.upstream = func(_ int, w http.ResponseWriter, _ *http.Request) {
				sse(w, `{"id":"s1","choices":[{"index":0,"delta":{"content":"par"}}]}`,
					`{"error":{"message":"backend dropped","type":"server_error","code":"backend_unavailable"}}`)
			}
		})
		var text string
		resp, err := r.client.ChatStream(t.Context(), asking(aiapi.ProfileCopilot, "hi"), func(d aiapi.Delta) error { text += d.Content; return nil })
		wantKind(t, err, aiapi.ErrUnavailable)
		if text != "par" || resp.Message.Content != "par" {
			t.Errorf("text %q partial %+v", text, resp)
		}
	})
	t.Run("the caller leaving cancels the upstream call", func(t *testing.T) {
		upstreamDone := make(chan struct{})
		r := newRig(t, func(o *rigOptions) {
			o.upstream = func(_ int, w http.ResponseWriter, r *http.Request) {
				defer close(upstreamDone)
				sse(w, `{"id":"s1","choices":[{"index":0,"delta":{"content":"a"}}]}`)
				<-r.Context().Done()
			}
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		_, err := r.client.ChatStream(ctx, asking(aiapi.ProfileCopilot, "hi"), func(aiapi.Delta) error { cancel(); return nil })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
		select {
		case <-upstreamDone:
		case <-time.After(5 * time.Second):
			t.Error("the upstream call kept running after the caller left")
		}
	})
}

func TestUpstreamFailuresAreMappedAndNeverLeakTheKey(t *testing.T) {
	cases := []struct {
		name     string
		upstream func(w http.ResponseWriter)
		want     *aiapi.Error
		status   int
	}{
		{"our key refused", func(w http.ResponseWriter) {
			metaError(w, 401, "authentication_error", "invalid_api_key", "Invalid API key "+metaKey)
		}, aiapi.ErrUnavailable, 502},
		{"rate limited", func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "9")
			metaError(w, 429, "rate_limit_error", "rate_limit_exceeded", "slow down")
		}, aiapi.ErrRateLimited, 429},
		{"request refused", func(w http.ResponseWriter) {
			metaError(w, 400, "invalid_request_error", "content_policy_violation", "denied")
		}, aiapi.ErrInvalid, 422},
		{"server error", func(w http.ResponseWriter) { metaError(w, 500, "server_error", "", "boom") }, aiapi.ErrUnavailable, 502},
		{"garbage", func(w http.ResponseWriter) { writeRaw(w, 200, `<html>`) }, aiapi.ErrUnavailable, 502},
		{"reasoning ate the budget", func(w http.ResponseWriter) {
			writeRaw(w, 200, `{"choices":[{"message":{"role":"assistant","content":null},"finish_reason":"length"}],"usage":{"prompt_tokens":5,"completion_tokens":1500,"total_tokens":1505}}`)
		}, aiapi.ErrTruncated, 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, func(o *rigOptions) {
				o.noRetries = true
				o.upstream = func(_ int, w http.ResponseWriter, _ *http.Request) { tc.upstream(w) }
			})
			_, err := r.client.Chat(t.Context(), asking(aiapi.ProfileIntent, "hi"))
			e := wantKind(t, err, tc.want)
			if e.Status != tc.status {
				t.Errorf("status = %d, want %d", e.Status, tc.status)
			}
			if tc.want == aiapi.ErrRateLimited && e.RetryAfter != 9*time.Second {
				t.Errorf("RetryAfter = %v", e.RetryAfter)
			}
			for _, secret := range []string{metaKey, callerKey} {
				if strings.Contains(r.logs.String(), secret) || strings.Contains(err.Error(), secret) {
					t.Errorf("a secret leaked into the logs or the error")
				}
			}
		})
	}
}

func TestSlowModelBecomesAGatewayTimeout(t *testing.T) {
	profiles := profile.Defaults("m-chat", "m-voice")
	p := profiles[aiapi.ProfileIntent]
	p.Timeout = 120 * time.Millisecond
	profiles[aiapi.ProfileIntent] = p
	r := newRig(t, func(o *rigOptions) {
		o.profiles = profiles
		o.upstream = func(_ int, _ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	})
	start := time.Now()
	_, err := r.client.Chat(t.Context(), asking(aiapi.ProfileIntent, "hi"))
	e := wantKind(t, err, aiapi.ErrTimeout)
	if e.Status != http.StatusGatewayTimeout || time.Since(start) > 3*time.Second {
		t.Errorf("status %d after %v", e.Status, time.Since(start))
	}
}

func TestBusyGatewayShedsLoadInsteadOfQueueing(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	r := newRig(t, func(o *rigOptions) {
		o.inFlight = 1
		o.upstream = func(n int, w http.ResponseWriter, _ *http.Request) {
			if n == 1 {
				close(started)
				<-release
			}
			writeRaw(w, 200, replyOK)
		}
	})
	first := make(chan error, 1)
	go func() {
		_, err := r.client.Chat(t.Context(), asking(aiapi.ProfileIntent, "slow"))
		first <- err
	}()
	<-started
	_, err := r.client.Chat(t.Context(), asking(aiapi.ProfileIntent, "second"))
	wantKind(t, err, aiapi.ErrRateLimited)
	close(release)
	if err := <-first; err != nil {
		t.Errorf("the first call: %v", err)
	}
	if _, err := r.client.Chat(t.Context(), asking(aiapi.ProfileIntent, "third")); err != nil {
		t.Errorf("a freed slot is usable: %v", err)
	}
}

// wavBytes is a valid recording: mono 16-bit PCM at 16 kHz.
func wavBytes() []byte { return wavWith(1, 16000, 3200, "") }

func wavWith(channels uint16, rate uint32, samples int, marker string) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	data := make([]byte, samples)
	copy(data, marker)
	b.WriteString("RIFF")
	binary.Write(&b, le, uint32(36+len(data)))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, le, uint32(16))
	binary.Write(&b, le, uint16(1))
	binary.Write(&b, le, channels)
	binary.Write(&b, le, rate)
	binary.Write(&b, le, rate*uint32(channels)*2)
	binary.Write(&b, le, channels*2)
	binary.Write(&b, le, uint16(16))
	b.WriteString("data")
	binary.Write(&b, le, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

// multipartBody builds a transcription request; fields are name/value pairs, audio the recording (nil = no part).
func multipartBody(audio []byte, fields ...string) (contentType string, body []byte) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if audio != nil {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="audio"; filename="clip.wav"`)
		h.Set("Content-Type", "audio/wav")
		part, _ := mw.CreatePart(h)
		part.Write(audio)
	}
	for i := 0; i+1 < len(fields); i += 2 {
		mw.WriteField(fields[i], fields[i+1])
	}
	mw.Close()
	return mw.FormDataContentType(), buf.Bytes()
}

func TestTranscribeSendsTheRecordingToMuseVoice(t *testing.T) {
	r := newRig(t, func(o *rigOptions) {
		o.upstream = func(_ int, w http.ResponseWriter, _ *http.Request) {
			writeRaw(w, 200, `{"sessionId":"s","transcript":"dua kardus dari Kemang","audioDurationMs":1500,"turns":[]}`)
		}
	})
	audio := wavBytes()
	got, err := r.client.Transcribe(t.Context(), audio, "clip.wav", "audio/wav", aiapi.WithKeywords("Kemang"), aiapi.WithLanguageBias("Indonesian"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "dua kardus dari Kemang" || got.DurationMs != 1500 {
		t.Errorf("transcript = %+v", got)
	}
	rq := r.meta.request(0)
	if rq.Path != "/v1/asr/transcribe" || rq.Header.Get("Authorization") != "Bearer "+metaKey {
		t.Errorf("upstream request %s auth %q", rq.Path, rq.Header.Get("Authorization"))
	}
	if !bytes.Contains(rq.Body, audio) || !bytes.Contains(rq.Body, []byte(`"model":"m-voice"`)) || !bytes.Contains(rq.Body, []byte(`"keywords":["Kemang"]`)) || !bytes.Contains(rq.Body, []byte(`"languageBias":["Indonesian"]`)) {
		t.Errorf("the upstream body lacks the audio, the voice model or the hints")
	}
}

func TestTranscribeRefusals(t *testing.T) {
	r := newRig(t)
	stereo := wavWith(2, 16000, 3200, "")
	cases := []struct {
		name        string
		contentType string
		body        []byte
		wantStatus  int
		wantKey     string
		wantField   string
	}{
		{"not a recording at all", "application/json", []byte(`{}`), 400, "srv.rpc.badRequest", ""},
		{"no audio part", "", nil, 422, "srv.rpc.badRequest", "audio"},
		{"empty audio", "", nil, 422, "srv.api.noSpeech", "audio"},
		{"not a wav (webm)", "", nil, 422, "srv.rpc.badRequest", "audio"},
		{"stereo", "", nil, 422, "srv.rpc.badRequest", "audio"},
		{"over 8 MiB", "", nil, 413, "srv.rpc.badRequest", "audio"},
		{"too many keywords", "", nil, 422, "srv.rpc.badRequest", "keywords"},
		{"an enormous hint", "", nil, 422, "srv.rpc.badRequest", "keywords"},
	}
	build := map[string]func() (string, []byte){
		"no audio part":     func() (string, []byte) { return multipartBody(nil, "keywords", "x") },
		"empty audio":       func() (string, []byte) { return multipartBody([]byte{}) },
		"not a wav (webm)":  func() (string, []byte) { return multipartBody([]byte("\x1aE\xdf\xa3 webm data")) },
		"stereo":            func() (string, []byte) { return multipartBody(stereo) },
		"over 8 MiB":        func() (string, []byte) { return multipartBody(wavWith(1, 16000, 8<<20, "")) },
		"too many keywords": func() (string, []byte) { return multipartBody(wavBytes(), kv(maxHints+1, "keywords", "k")...) },
		"an enormous hint":  func() (string, []byte) { return multipartBody(wavBytes(), "keywords", strings.Repeat("k", 500)) },
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct, body := tc.contentType, tc.body
			if f := build[tc.name]; f != nil {
				ct, body = f()
			}
			status, _, raw := r.post(t, aiapi.TranscribePath, callerKey, ct, body)
			var env aiapi.ErrorEnvelope
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("status %d, body %q", status, raw)
			}
			if status != tc.wantStatus || env.Error.Kind != aiapi.KindInvalid || env.Error.Msg.Key != tc.wantKey || env.Error.Field != tc.wantField {
				t.Errorf("status %d, %+v; want %d %s %q", status, env.Error, tc.wantStatus, tc.wantKey, tc.wantField)
			}
		})
	}
	if r.meta.count() != 0 {
		t.Errorf("%d refused recordings reached Meta", r.meta.count())
	}
}

func kv(n int, key, value string) []string {
	var out []string
	for i := range n {
		out = append(out, key, value+strconv.Itoa(i))
	}
	return out
}

// logLines parses the JSON log into records.
func logLines(t *testing.T, logs *syncBuffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		out = append(out, rec)
	}
	return out
}

func TestLogsCarryUsageAndLatencyButNeverContent(t *testing.T) {
	const (
		secretText  = "SECRET-MESSAGE-TEXT-7731"
		secretSys   = "SECRET-SYSTEM-PROMPT-5512"
		secretTool  = "secret_tool_name_9981"
		secretArgs  = "SECRET-TOOL-ARGUMENTS-1204"
		secretReply = "SECRET-MODEL-REPLY-8842"
		secretSpeak = "SECRET-SPOKEN-WORDS-3390"
		secretAudio = "SECRET-AUDIO-MARKER-6623"
		secretImage = "SECRET-IMAGE-PIXELS-4417"
		secretCall  = "SECRET-CALL-RESULT-2208"
	)
	r := newRig(t, func(o *rigOptions) {
		o.noRetries = true
		o.upstream = func(n int, w http.ResponseWriter, req *http.Request) {
			switch {
			case strings.HasSuffix(req.URL.Path, "/asr/transcribe"):
				writeRaw(w, 200, `{"transcript":"`+secretSpeak+`","audioDurationMs":900,"turns":[]}`)
			case n == 4:
				sse(w, `{"id":"s","choices":[{"index":0,"delta":{"content":"`+secretReply+`"}}]}`,
					`{"id":"s","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
					`{"id":"s","choices":[],"usage":{"prompt_tokens":40,"completion_tokens":8,"total_tokens":48}}`, "[DONE]")
			case n == 5:
				metaError(w, 400, "invalid_request_error", "", "bad content: "+secretText)
			default:
				writeRaw(w, 200, `{"id":"r","model":"served-model","choices":[{"message":{"role":"assistant","content":"`+secretReply+`","tool_calls":[{"id":"c1","type":"function","function":{"name":"`+secretTool+`","arguments":"{\"q\":\"`+secretArgs+`\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":21,"completion_tokens":9,"total_tokens":30,"completion_tokens_details":{"reasoning_tokens":4}}}`)
			}
		}
	})
	ctx := t.Context()
	// 1: copilot with tools and a tool result; 2: vision with an image; 3: transcription; 4: a stream; 5: a refusal.
	if _, err := r.client.Chat(ctx, aiapi.ChatRequest{Profile: aiapi.ProfileCopilot,
		Messages: []aiapi.Message{aiapi.SystemText(secretSys), aiapi.UserText(secretText),
			{Role: aiapi.RoleAssistant, ToolCalls: []aiapi.ToolCall{{ID: "c0", Name: secretTool, Arguments: `{"q":"` + secretArgs + `"}`}}},
			{Role: aiapi.RoleTool, ToolCallID: "c0", Content: secretCall}},
		Tools: []aiapi.Tool{{Name: secretTool, Description: secretSys}}}); err != nil {
		t.Fatal(err)
	}
	image := png(secretImage)
	if _, err := r.client.Chat(ctx, aiapi.ChatRequest{Profile: aiapi.ProfileVision, Messages: []aiapi.Message{{Role: aiapi.RoleUser, Content: secretText, Images: []aiapi.Image{{MIME: "image/png", Data: image}}}}}); err != nil {
		t.Fatal(err)
	}
	audio := wavWith(1, 16000, 3200, secretAudio)
	if _, err := r.client.Transcribe(ctx, audio, secretAudio+".wav", "audio/wav", aiapi.WithKeywords(secretSpeak)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.client.ChatStream(ctx, asking(aiapi.ProfileCopilot, secretText), func(aiapi.Delta) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.client.Chat(ctx, asking(aiapi.ProfileIntent, secretText)); err == nil {
		t.Fatal("the refused call should fail")
	}
	if _, err := r.client.Chat(ctx, aiapi.ChatRequest{Profile: aiapi.ProfileVision, Messages: []aiapi.Message{{Role: aiapi.RoleUser, Images: []aiapi.Image{{MIME: "image/png", Data: []byte(secretImage)}}}}}); err == nil {
		t.Fatal("a non-image should be refused")
	}
	// The optimizer's reviewer and the console's report are no different: their facts and texts stay out of the logs.
	for _, name := range []aiapi.Profile{aiapi.ProfileReview, aiapi.ProfileReport} {
		if _, err := r.client.Chat(ctx, asking(name, secretText)); err != nil {
			t.Fatal(err)
		}
	}

	logs := r.logs.String()
	forbidden := []string{secretText, secretSys, secretTool, secretArgs, secretReply, secretSpeak, secretAudio, secretImage, secretCall,
		base64.StdEncoding.EncodeToString(image), base64.StdEncoding.EncodeToString([]byte(secretImage)), base64.StdEncoding.EncodeToString(audio),
		metaKey, callerKey}
	for _, f := range forbidden {
		if strings.Contains(logs, f) {
			t.Errorf("the logs contain %q", f)
		}
	}

	var chat, transcribe, failed, refused map[string]any
	var profilesLogged []string
	for _, rec := range logLines(t, r.logs) {
		switch rec["msg"] {
		case "chat":
			profilesLogged = append(profilesLogged, rec["profile"].(string))
			if chat == nil {
				chat = rec
			}
		case "transcribe":
			transcribe = rec
		case "call failed":
			failed = rec
		case "request refused":
			refused = rec
		}
	}
	if chat == nil || chat["profile"] != "copilot" || chat["promptTokens"] != 21.0 || chat["completionTokens"] != 9.0 || chat["reasoningTokens"] != 4.0 ||
		chat["model"] != "served-model" || chat["finish"] != "tool_calls" || chat["messages"] != 4.0 || chat["tools"] != 1.0 || chat["caller"] != "token0" {
		t.Errorf("the chat log line lacks usage or counts: %v", chat)
	}
	if _, ok := chat["latencyMs"].(float64); !ok || chat["requestBytes"].(float64) <= 0 {
		t.Errorf("the chat log line lacks latency or size: %v", chat)
	}
	if transcribe == nil || transcribe["audioMs"] != 900.0 || transcribe["chars"] != float64(len(secretSpeak)) || transcribe["requestBytes"] != float64(len(audio)) {
		t.Errorf("the transcribe log line = %v", transcribe)
	}
	if failed == nil || failed["status"] != 422.0 || failed["upstreamCode"] == nil {
		t.Errorf("the failure log line = %v", failed)
	}
	if failed != nil && failed["upstreamMessage"] != nil {
		t.Errorf("the text of an upstream refusal (which may describe the request) must not be logged: %v", failed)
	}
	if refused == nil || refused["field"] != "messages.0.images.0" || refused["msgKey"] != "srv.api.fileNotImage" || refused["images"] != 1.0 {
		t.Errorf("the refusal log line = %v", refused)
	}
	if !slices.Contains(profilesLogged, "review") || !slices.Contains(profilesLogged, "report") {
		t.Errorf("chat log lines were written for %v; the review and report calls are missing", profilesLogged)
	}
}

func TestEveryImageKindTheVisionModelReadsIsAccepted(t *testing.T) {
	r := newRig(t)
	for mime, data := range map[string][]byte{
		"image/png":  png("p"),
		"image/gif":  []byte("GIF89a\x01\x00\x01\x00"),
		"image/jpeg": []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"),
		"image/webp": []byte("RIFF\x24\x00\x00\x00WEBPVP8 "),
	} {
		_, err := r.client.Chat(t.Context(), aiapi.ChatRequest{
			Profile:  aiapi.ProfileVision,
			Messages: []aiapi.Message{{Role: aiapi.RoleUser, Content: "what is this", Images: []aiapi.Image{{MIME: "application/octet-stream", Data: data}}}},
		})
		if err != nil {
			t.Errorf("%s: %v", mime, err)
			continue
		}
		parts := r.meta.body(t, r.meta.count()-1)["messages"].([]any)[0].(map[string]any)["content"].([]any)
		if url := parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string); !strings.HasPrefix(url, "data:"+mime+";base64,") {
			t.Errorf("%s was sent as %s", mime, url[:30])
		}
	}
}
