package meta

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "sk-test-0123456789-DO-NOT-LEAK"

// fakeAPI is a fake Model API: it records every request and answers with whatever handler says.
type fakeAPI struct {
	mu       sync.Mutex
	requests []recorded
	handler  func(n int, w http.ResponseWriter, r *http.Request, body []byte)
}

type recorded struct {
	Path   string
	Header http.Header
	Body   []byte
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, recorded{Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	n := len(f.requests)
	f.mu.Unlock()
	f.handler(n, w, r, body)
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeAPI) request(i int) recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

// newClient starts a fake API and returns a client for it. Waits between retries are recorded, not slept.
func newClient(t *testing.T, handler func(n int, w http.ResponseWriter, r *http.Request, body []byte), mods ...func(*Config)) (*Client, *fakeAPI, *[]time.Duration) {
	t.Helper()
	api := &fakeAPI{handler: handler}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	cfg := Config{BaseURL: srv.URL + "/v1", APIKey: testKey, Model: "test-model", Backoff: time.Millisecond, MaxBackoff: 10 * time.Second}
	for _, m := range mods {
		m(&cfg)
	}
	c := New(cfg)
	var waits []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	return c, api, &waits
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

func apiError(w http.ResponseWriter, status int, typ, code, message string) {
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"message": message, "type": typ, "param": nil, "code": nilIfEmpty(code)}})
	writeJSON(w, status, string(body))
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const okReply = `{"id":"chatcmpl-1","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"completion_tokens_details":{"reasoning_tokens":4},"prompt_tokens_details":{"cached_tokens":2}}}`

func userMsg(text string) []Message { return []Message{{Role: RoleUser, Content: text}} }

func decodeMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, raw)
	}
	return m
}

func wantKind(t *testing.T, err error, want *Error) *Error {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want kind %v", err, want.Kind)
	}
	e, _ := asError(err)
	return e
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":                              0,
		"15":                            15 * time.Second,
		" 3 ":                           3 * time.Second,
		"0":                             0,
		"-5":                            0,
		"soon":                          0,
		"Wed, 21 Oct 2026 12:00:30 GMT": 30 * time.Second,
		"Wed, 21 Oct 2026 11:00:00 GMT": 0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSecretNeverPrints(t *testing.T) {
	cfg := Config{BaseURL: "https://x", APIKey: testKey}
	var logs strings.Builder
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("config", "key", cfg.APIKey, "cfg", cfg)
	encoded, err := json.Marshal(struct{ Key Secret }{cfg.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"%v":   fmt.Sprintf("%v", cfg),
		"%+v":  fmt.Sprintf("%+v", cfg),
		"%#v":  fmt.Sprintf("%#v", cfg),
		"%s":   fmt.Sprintf("%s", cfg.APIKey),
		"%q":   fmt.Sprintf("%q", cfg.APIKey),
		"%x":   fmt.Sprintf("%x", cfg.APIKey),
		"json": string(encoded),
		"slog": logs.String(),
	} {
		if strings.Contains(out, testKey) || strings.Contains(out, fmt.Sprintf("%x", testKey)) {
			t.Errorf("%s output leaks the key: %s", name, out)
		}
	}
	if Secret("").String() != "" || Secret("").Set() {
		t.Error("an empty secret should print empty and not be Set")
	}
	if cfg.APIKey.Reveal() != testKey {
		t.Error("Reveal must return the value")
	}
}
