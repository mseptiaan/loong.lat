package meta

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestChatSendsTheRequestAndParsesTheReply(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) { writeJSON(w, 200, okReply) })

	got, err := c.Chat(t.Context(), ChatRequest{
		Messages:            []Message{{Role: RoleSystem, Content: "be brief"}, {Role: RoleUser, Content: "hi"}},
		ReasoningEffort:     EffortMinimal,
		MaxCompletionTokens: 1500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Message.Content != "hello" || got.Message.Role != RoleAssistant || got.FinishReason != "stop" || got.ID != "chatcmpl-1" {
		t.Errorf("reply = %+v", got)
	}
	if want := (Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18, ReasoningTokens: 4, CachedTokens: 2}); got.Usage != want {
		t.Errorf("usage = %+v, want %+v", got.Usage, want)
	}

	rq := api.request(0)
	if rq.Path != "/v1/chat/completions" {
		t.Errorf("path = %s", rq.Path)
	}
	if rq.Header.Get("Authorization") != "Bearer "+testKey || rq.Header.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", rq.Header)
	}
	body := decodeMap(t, rq.Body)
	if body["model"] != "test-model" || body["reasoning_effort"] != "minimal" || body["max_completion_tokens"] != 1500.0 {
		t.Errorf("body = %v", body)
	}
	if _, has := body["stream"]; has {
		t.Errorf("a plain call must not set stream: %v", body)
	}
	for _, absent := range []string{"tools", "tool_choice", "response_format", "stream_options"} {
		if _, has := body[absent]; has {
			t.Errorf("%s should be omitted: %v", absent, body)
		}
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["content"] != "hi" || msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("messages = %v", msgs)
	}
}

func TestChatEncodesImagesAndStructuredOutput(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		writeJSON(w, 200, `{"choices":[{"message":{"role":"assistant","content":"{\"items\":[]}"},"finish_reason":"stop"}]}`)
	})
	schema := jsontext.Value(`{"type":"object","properties":{"items":{"type":"array"}},"required":["items"]}`)
	_, err := c.Chat(t.Context(), ChatRequest{
		Model: "vision-model",
		Messages: []Message{{Role: RoleUser, Parts: []Part{
			Text("what is in this photo?"),
			ImageBytes("image/png", []byte{0x89, 'P', 'N', 'G'}),
		}}},
		JSONSchema: &JSONSchema{Name: "items", Schema: schema, Strict: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := decodeMap(t, api.request(0).Body)
	if body["model"] != "vision-model" {
		t.Errorf("model = %v", body["model"])
	}
	parts := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("parts = %v", parts)
	}
	if p := parts[0].(map[string]any); p["type"] != "text" || p["text"] != "what is in this photo?" {
		t.Errorf("text part = %v", p)
	}
	img := parts[1].(map[string]any)
	url := img["image_url"].(map[string]any)["url"].(string)
	if img["type"] != "image_url" || url != "data:image/png;base64,iVBORw==" {
		t.Errorf("image part = %v", img)
	}
	rf := body["response_format"].(map[string]any)
	js := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["name"] != "items" || js["strict"] != true || js["schema"].(map[string]any)["type"] != "object" {
		t.Errorf("response_format = %v", rf)
	}
}

func TestChatToolCallRoundTrip(t *testing.T) {
	c, api, _ := newClient(t, func(n int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		if n == 1 {
			writeJSON(w, 200, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[
				{"id":"call_a","type":"function","function":{"name":"get_plan","arguments":"{\"id\":\"PL-1\"}"}},
				{"type":"function","function":{"name":"list_drivers","arguments":""}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		writeJSON(w, 200, okReply)
	})
	tools := []Tool{
		{Name: "get_plan", Description: "Read a plan.", Parameters: jsontext.Value(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)},
		{Name: "list_drivers"},
	}
	first, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("status of PL-1?"), Tools: tools, ToolChoice: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if first.FinishReason != FinishToolCalls || first.Message.Content != "" || len(first.Message.ToolCalls) != 2 {
		t.Fatalf("first = %+v", first)
	}
	if tc := first.Message.ToolCalls[0]; tc != (ToolCall{ID: "call_a", Name: "get_plan", Arguments: `{"id":"PL-1"}`}) {
		t.Errorf("call 0 = %+v", tc)
	}
	if tc := first.Message.ToolCalls[1]; tc.ID != "call_1" || tc.Name != "list_drivers" {
		t.Errorf("a call without an id gets a stable one: %+v", tc)
	}

	req := decodeMap(t, api.request(0).Body)
	wireTools := req["tools"].([]any)
	fn := wireTools[0].(map[string]any)["function"].(map[string]any)
	if wireTools[0].(map[string]any)["type"] != "function" || fn["name"] != "get_plan" || fn["description"] != "Read a plan." || req["tool_choice"] != "auto" {
		t.Errorf("tools = %v", wireTools)
	}
	if p := wireTools[1].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any); p["type"] != "object" {
		t.Errorf("a tool without parameters declares an empty object schema: %v", p)
	}

	// The follow-up echoes the assistant turn and answers each call with a tool message.
	history := append(userMsg("status of PL-1?"), first.Message,
		Message{Role: RoleTool, ToolCallID: "call_a", Content: `{"status":"berjalan"}`},
		Message{Role: RoleTool, ToolCallID: "call_1", Content: `[]`})
	if _, err := c.Chat(t.Context(), ChatRequest{Messages: history, Tools: tools, ToolChoice: "none"}); err != nil {
		t.Fatal(err)
	}
	msgs := decodeMap(t, api.request(1).Body)["messages"].([]any)
	asst := msgs[1].(map[string]any)
	calls := asst["tool_calls"].([]any)
	if content, has := asst["content"]; !has || content != nil {
		t.Errorf("an assistant turn that only called tools sends content null: %v", asst)
	}
	if len(calls) != 2 || calls[0].(map[string]any)["id"] != "call_a" || calls[0].(map[string]any)["function"].(map[string]any)["arguments"] != `{"id":"PL-1"}` {
		t.Errorf("tool_calls = %v", calls)
	}
	if calls[1].(map[string]any)["function"].(map[string]any)["arguments"] != "{}" {
		t.Errorf("empty arguments are sent as {}: %v", calls[1])
	}
	if tm := msgs[2].(map[string]any); tm["role"] != "tool" || tm["tool_call_id"] != "call_a" || tm["content"] != `{"status":"berjalan"}` {
		t.Errorf("tool message = %v", tm)
	}
}

func TestChatLengthWithNullContentIsATypedError(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		writeJSON(w, 200, `{"id":"x","choices":[{"message":{"role":"assistant","content":null},"finish_reason":"length"}],"usage":{"prompt_tokens":20,"completion_tokens":50,"total_tokens":70,"completion_tokens_details":{"reasoning_tokens":50}}}`)
	})
	got, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("think hard"), MaxCompletionTokens: 50})
	e := wantKind(t, err, ErrLength)
	if !strings.Contains(e.Message, "max_completion_tokens") {
		t.Errorf("message should say what to do: %q", e.Message)
	}
	if got.Usage.ReasoningTokens != 50 || got.FinishReason != "length" {
		t.Errorf("the response and its usage still come back: %+v", got)
	}
	if api.count() != 1 {
		t.Errorf("a length failure must not be retried (%d requests)", api.count())
	}
	if e.Retryable() {
		t.Error("a length failure is not retryable as is")
	}
}

func TestChatLengthWithPartialTextIsAnErrorOnlyForStructuredOutput(t *testing.T) {
	partial := `{"id":"x","choices":[{"message":{"role":"assistant","content":"The route is"},"finish_reason":"length"}]}`
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) { writeJSON(w, 200, partial) })

	got, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	if err != nil || got.Message.Content != "The route is" || got.FinishReason != FinishLength {
		t.Errorf("plain text cut short is returned for the caller to judge: %+v %v", got, err)
	}
	_, err = c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi"), JSONSchema: &JSONSchema{Name: "s", Schema: jsontext.Value(`{"type":"object"}`)}})
	wantKind(t, err, ErrLength)
}

func TestChatRetriesRateLimitHonouringRetryAfter(t *testing.T) {
	c, api, waits := newClient(t, func(n int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		if n == 1 {
			w.Header().Set("Retry-After", "3")
			apiError(w, 429, "rate_limit_error", "rate_limit_exceeded", "Rate limit exceeded. Please retry after 3 seconds.")
			return
		}
		writeJSON(w, 200, okReply)
	})
	got, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	if err != nil || got.Message.Content != "hello" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if api.count() != 2 {
		t.Errorf("%d requests, want 2", api.count())
	}
	if len(*waits) != 1 || (*waits)[0] != 3*time.Second {
		t.Errorf("waits = %v, want [3s] (the API's Retry-After)", *waits)
	}
}

func TestChatRetriesServerErrorsWithBackoffThenGivesUp(t *testing.T) {
	c, api, waits := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		apiError(w, 503, "server_error", "service_overloaded", "The backend is busy.")
	}, func(cfg *Config) { cfg.Backoff = 100 * time.Millisecond })

	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	e := wantKind(t, err, ErrServer)
	if api.count() != 3 || e.Attempts != 3 {
		t.Errorf("%d requests, attempts %d; want 1 try + 2 retries", api.count(), e.Attempts)
	}
	if e.Status != 503 || e.Code != "service_overloaded" || !strings.Contains(e.Message, "busy") {
		t.Errorf("error = %+v", e)
	}
	if len(*waits) != 2 {
		t.Fatalf("waits = %v", *waits)
	}
	// Exponential with jitter: the second step is a doubling of the first, each within [step/2, step].
	if w := (*waits)[0]; w < 50*time.Millisecond || w > 100*time.Millisecond {
		t.Errorf("first wait %v outside [50ms,100ms]", w)
	}
	if w := (*waits)[1]; w < 100*time.Millisecond || w > 200*time.Millisecond {
		t.Errorf("second wait %v outside [100ms,200ms]", w)
	}
}

func TestChatRetriesAreSwitchedOffByANegativeLimit(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		apiError(w, 500, "server_error", "", "boom")
	}, func(cfg *Config) { cfg.MaxRetries = -1 })
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	wantKind(t, err, ErrServer)
	if api.count() != 1 {
		t.Errorf("%d requests, want 1", api.count())
	}
}

func TestChatDoesNotWaitForAHugeRetryAfter(t *testing.T) {
	c, api, waits := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Retry-After", "60")
		apiError(w, 503, "server_error", "backend_unavailable", "unavailable")
	}, func(cfg *Config) { cfg.MaxBackoff = 5 * time.Second })
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	e := wantKind(t, err, ErrServer)
	if api.count() != 1 || len(*waits) != 0 {
		t.Errorf("requests %d, waits %v: a 60s wait must not be slept through", api.count(), *waits)
	}
	if e.RetryAfter != 60*time.Second {
		t.Errorf("RetryAfter = %v, so the caller can pass it on", e.RetryAfter)
	}
}

func TestChatRateLimitSurfacesRetryAfterWhenRetriesRunOut(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Retry-After", "2")
		apiError(w, 429, "rate_limit_error", "rate_limit_exceeded", "slow down")
	})
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	e := wantKind(t, err, ErrRateLimit)
	if e.RetryAfter != 2*time.Second || e.Status != 429 || !e.Retryable() {
		t.Errorf("error = %+v", e)
	}
}

func TestChatAuthFailureIsNotRetriedAndNeverLeaksTheKey(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, r *http.Request, _ []byte) {
		// A careless upstream echoing the credential it was given must still not get it into our errors.
		apiError(w, 401, "authentication_error", "invalid_api_key", "Invalid API key: "+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	})
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	e := wantKind(t, err, ErrAuth)
	if api.count() != 1 {
		t.Errorf("%d requests, want 1", api.count())
	}
	if e.Status != 401 || e.Code != "invalid_api_key" || e.Type != "authentication_error" {
		t.Errorf("error = %+v", e)
	}
	if strings.Contains(err.Error(), testKey) || strings.Contains(e.Message, testKey) || !strings.Contains(e.Message, "[redacted]") {
		t.Errorf("the key leaked: %q / %q", err.Error(), e.Message)
	}
}

func TestChatBadRequestKeepsTheAPIsMessage(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		apiError(w, 400, "invalid_request_error", "", "`reasoning_effort`: none is not supported")
	})
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	e := wantKind(t, err, ErrBadRequest)
	if api.count() != 1 || !strings.Contains(e.Message, "reasoning_effort") || e.Status != 400 {
		t.Errorf("requests %d, error %+v", api.count(), e)
	}
	if e.Retryable() {
		t.Error("a bad request is not retryable")
	}
}

func TestChatNonJSONErrorBodyIsKeptShortAndClean(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.WriteHeader(502)
		w.Write([]byte("<html>\x00Bad gateway\n" + strings.Repeat("x", 5000)))
	}, func(cfg *Config) { cfg.MaxRetries = -1 })
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	e := wantKind(t, err, ErrServer)
	if len(e.Message) > maxMessage+3 || strings.ContainsAny(e.Message, "\x00\n") || !strings.Contains(e.Message, "Bad gateway") {
		t.Errorf("message = %q", e.Message)
	}
}

func TestChatTimeoutIsATypedErrorAndNotRetried(t *testing.T) {
	release := make(chan struct{})
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, r *http.Request, _ []byte) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi"), Timeout: 80 * time.Millisecond})
	e := wantKind(t, err, ErrTimeout)
	if !errors.Is(err, context.DeadlineExceeded) || !e.Retryable() {
		t.Errorf("a timeout wraps context.DeadlineExceeded: %v", err)
	}
	if time.Since(start) > 2*time.Second || api.count() != 1 {
		t.Errorf("took %v with %d requests", time.Since(start), api.count())
	}
}

func TestChatCallerCancellationComesBackAsTheContextError(t *testing.T) {
	started := make(chan struct{})
	c, _, _ := newClient(t, func(_ int, _ http.ResponseWriter, r *http.Request, _ []byte) {
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-started
		cancel()
	}()
	_, err := c.Chat(ctx, ChatRequest{Messages: userMsg("hi")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, isAPI := asError(err); isAPI {
		t.Errorf("the caller's own cancellation is not an API failure: %v", err)
	}
}

func TestChatMalformedReplies(t *testing.T) {
	for name, body := range map[string]string{
		"not json":   `{"choices":[`,
		"html":       `<html>hello</html>`,
		"no choices": `{"id":"x","choices":[]}`,
		"wrong type": `{"choices":"nope"}`,
		"empty":      ``,
	} {
		t.Run(name, func(t *testing.T) {
			c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) { writeJSON(w, 200, body) })
			_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
			wantKind(t, err, ErrMalformed)
		})
	}
}

func TestChatErrorObjectInA200Body(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		writeJSON(w, 200, `{"error":{"message":"model overloaded","type":"server_error","code":null}}`)
	})
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	e := wantKind(t, err, ErrServer)
	if e.Message != "model overloaded" {
		t.Errorf("message = %q", e.Message)
	}
}

func TestChatContentFilterWithoutContent(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		writeJSON(w, 200, `{"choices":[{"message":{"role":"assistant","content":null},"finish_reason":"content_filter"}]}`)
	})
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	wantKind(t, err, ErrFiltered)
}

func TestWithoutAKeyNothingIsSent(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) { writeJSON(w, 200, okReply) },
		func(cfg *Config) { cfg.APIKey = "" })
	if c.Configured() {
		t.Error("Configured must be false without a key")
	}
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	wantKind(t, err, ErrAuth)
	if api.count() != 0 {
		t.Errorf("%d requests were sent without a key", api.count())
	}
}

func TestUnreachableAPIIsAServerError(t *testing.T) {
	c := New(Config{BaseURL: "http://127.0.0.1:1/v1", APIKey: testKey, MaxRetries: -1})
	_, err := c.Chat(t.Context(), ChatRequest{Messages: userMsg("hi")})
	e := wantKind(t, err, ErrServer)
	if strings.Contains(err.Error(), testKey) || e.Status != 0 {
		t.Errorf("error = %+v", e)
	}
}
