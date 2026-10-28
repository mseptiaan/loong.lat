package platform

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

type greetIn struct {
	Name string `json:"name"`
}

type greetOut struct {
	Greeting string `json:"greeting"`
}

// newRPCServer mounts an RPC (behind the trace middleware, like a service) on a real test server.
func newRPCServer(t *testing.T, register func(*RPC)) *httptest.Server {
	t.Helper()
	rpc := NewRPC(discard)
	register(rpc)
	mux := http.NewServeMux()
	rpc.Mount(mux)
	srv := httptest.NewServer(Chain(mux, TraceMiddleware))
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, srv *httptest.Server, method, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/v1/rpc/"+method, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

// wireOf decodes an error body the way the frontend's HTTP client will.
func wireOf(t *testing.T, body []byte) wireError {
	t.Helper()
	var w wireError
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("error body %q: %v", body, err)
	}
	return w
}

func TestRPCCallsATypedHandler(t *testing.T) {
	srv := newRPCServer(t, func(r *RPC) {
		r.Handle("greet.say", func(_ context.Context, in greetIn) (greetOut, error) {
			return greetOut{Greeting: "halo " + in.Name}, nil
		})
	})

	resp, body := call(t, srv, "greet.say", `{"name":"Dina","extra":"ignored"}`)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content type %q: %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	var out greetOut
	if err := json.Unmarshal(body, &out); err != nil || out.Greeting != "halo Dina" {
		t.Errorf("body = %s", body)
	}

	if _, body := call(t, srv, "greet.say", ``); !strings.Contains(string(body), `"halo "`) {
		t.Errorf("an empty body is the zero input: %s", body)
	}
}

func TestRPCMapsErrorsToTheFrontendContract(t *testing.T) {
	boom := errors.New("pq: connection to 10.0.0.5 refused")
	handlers := map[string]error{
		"invalid":         Invalid("email", contracts.Msg{Key: "srv.api.emailInvalid"}),
		"notfound":        NotFound(contracts.Msg{Key: "srv.api.routeNotFound"}),
		"denied":          Denied(contracts.Msg{Key: MsgRoleDenied}),
		"unauthenticated": Unauthenticated(),
		"conflict":        Conflict(contracts.Msg{Key: "srv.api.emailTaken"}),
		"unavailable":     Unavailable(contracts.Msg{Key: MsgServiceError}),
		"ratelimited":     TooManyRequests(1500 * time.Millisecond),
		"unexpected":      boom,
		"wrapped":         errors.Join(errors.New("context"), Conflict(contracts.Msg{Key: "srv.api.emailTaken"})),
	}
	srv := newRPCServer(t, func(r *RPC) {
		for name, err := range handlers {
			r.Handle("err."+name, func(context.Context, struct{}) (struct{}, error) { return struct{}{}, err })
		}
	})

	tests := []struct {
		name       string
		status     int
		code       ErrorCode
		msgKey     string
		field      string
		retryAfter string
	}{
		{"invalid", 422, CodeInvalid, "srv.api.emailInvalid", "email", ""},
		{"notfound", 404, CodeNotFound, "srv.api.routeNotFound", "", ""},
		{"denied", 403, CodeDenied, MsgRoleDenied, "", ""},
		{"unauthenticated", 401, CodeDenied, MsgSignInFirst, "", ""},
		{"conflict", 409, CodeConflict, "srv.api.emailTaken", "", ""},
		{"unavailable", 500, CodeService, MsgServiceError, "", ""},
		{"ratelimited", 429, CodeDenied, MsgTooManyRequest, "", "2"},
		{"unexpected", 500, CodeService, MsgInternal, "", ""},
		{"wrapped", 409, CodeConflict, "srv.api.emailTaken", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := call(t, srv, "err."+tt.name, `{}`)
			if resp.StatusCode != tt.status {
				t.Errorf("HTTP status = %d, want %d", resp.StatusCode, tt.status)
			}
			w := wireOf(t, body)
			if w.Name != "ApiError" || w.Code != tt.code || w.Status != tt.status || w.Field != tt.field || w.Msg == nil || w.Msg.Key != tt.msgKey {
				t.Errorf("wire error = %+v msg %+v", w, w.Msg)
			}
			if got := resp.Header.Get("Retry-After"); got != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.retryAfter)
			}
			if strings.Contains(string(body), "10.0.0.5") {
				t.Errorf("an unexpected error must not leak its text: %s", body)
			}
		})
	}

	_, body := call(t, srv, "err.unexpected", `{}`)
	w := wireOf(t, body)
	if detail, _ := w.Msg.Params["detail"].(string); !strings.HasPrefix(detail, "ref ") || len(detail) != 4+32 {
		t.Errorf("the generic 500 should carry the trace id for support, got %v", w.Msg.Params)
	}
}

func TestRPCRejectsBadRequests(t *testing.T) {
	srv := newRPCServer(t, func(r *RPC) {
		r.Handle("greet.say", func(_ context.Context, in greetIn) (greetOut, error) { return greetOut{}, nil })
	})

	tests := []struct {
		name, method, body string
		status             int
		code               ErrorCode
		msgKey             string
	}{
		{"unknown method", "nope.nothing", `{}`, 404, CodeNotFound, MsgUnknownMethod},
		{"malformed json", "greet.say", `{"name":`, 422, CodeInvalid, MsgBadRequest},
		{"wrong shape", "greet.say", `{"name": 12}`, 422, CodeInvalid, MsgBadRequest},
		{"too large", "greet.say", `{"name":"` + strings.Repeat("a", DefaultMaxBodyBytes) + `"}`, 413, CodeInvalid, MsgBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := call(t, srv, tt.method, tt.body)
			w := wireOf(t, body)
			if resp.StatusCode != tt.status || w.Code != tt.code || w.Msg.Key != tt.msgKey {
				t.Errorf("status %d, wire %+v", resp.StatusCode, w)
			}
		})
	}

	if _, body := call(t, srv, "nope.nothing", `{}`); wireOf(t, body).Msg.Params["method"] != "nope.nothing" {
		t.Errorf("the unknown-method message must name the method: %s", body)
	}

	resp, err := http.Get(srv.URL + "/v1/rpc/greet.say")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", resp.StatusCode)
	}
}

func TestRPCRegistrationMistakesPanic(t *testing.T) {
	rpc := NewRPC(discard)
	rpc.Handle("a.b", func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	for name, fn := range map[string]func(){
		"duplicate": func() {
			rpc.Handle("a.b", func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
		},
		"empty": func() { rpc.Handle("", func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s registration should panic", name)
				}
			}()
			fn()
		}()
	}
}

type progressEvent struct {
	Type  string `json:"type"`
	Step  int    `json:"step,omitzero"`
	Total int    `json:"total,omitzero"`
}

// The stream must reach the client while the handler is still working: the first event is read before the
// handler is allowed to continue.
func TestRPCStreamFlushesEachEventAsEmitted(t *testing.T) {
	release := make(chan struct{})
	srv := newRPCServer(t, func(r *RPC) {
		r.Stream("plans.computeOptions", func(_ context.Context, in greetIn, emit func(progressEvent) error) error {
			if err := emit(progressEvent{Type: "progress", Step: 1, Total: 2}); err != nil {
				return err
			}
			<-release // the client reads event 1 before we get past here
			if err := emit(progressEvent{Type: "progress", Step: 2, Total: 2}); err != nil {
				return err
			}
			return emit(progressEvent{Type: "done"})
		})
	})

	resp, err := http.Post(srv.URL+"/v1/rpc/plans.computeOptions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content type = %q", ct)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Error("streams must ask proxies not to buffer")
	}

	lines := bufio.NewScanner(resp.Body)
	next := func() progressEvent {
		t.Helper()
		got := make(chan progressEvent, 1)
		go func() {
			if lines.Scan() {
				var ev progressEvent
				if err := json.Unmarshal(lines.Bytes(), &ev); err != nil {
					t.Errorf("line %q: %v", lines.Bytes(), err)
				}
				got <- ev
			}
			close(got)
		}()
		select {
		case ev, ok := <-got:
			if !ok {
				t.Fatal("stream ended early")
			}
			return ev
		case <-time.After(5 * time.Second):
			t.Fatal("event was not flushed while the handler was still running")
			return progressEvent{}
		}
	}

	if ev := next(); ev.Type != "progress" || ev.Step != 1 {
		t.Errorf("first event = %+v", ev)
	}
	close(release)
	if ev := next(); ev.Step != 2 {
		t.Errorf("second event = %+v", ev)
	}
	if ev := next(); ev.Type != "done" {
		t.Errorf("last event = %+v", ev)
	}
	if lines.Scan() {
		t.Errorf("unexpected extra line %q", lines.Text())
	}
}

func TestRPCStreamErrors(t *testing.T) {
	srv := newRPCServer(t, func(r *RPC) {
		r.Stream("early", func(context.Context, greetIn, func(progressEvent) error) error {
			return Invalid("", contracts.Msg{Key: "srv.api.noRunningRoute"})
		})
		r.Stream("late", func(_ context.Context, _ greetIn, emit func(progressEvent) error) error {
			_ = emit(progressEvent{Type: "progress", Step: 1, Total: 4})
			return Unavailable(contracts.Msg{Key: "srv.api.routeEngineError"})
		})
		r.Stream("empty", func(context.Context, greetIn, func(progressEvent) error) error { return nil })
	})

	resp, body := call(t, srv, "early", `{}`)
	if resp.StatusCode != 422 || wireOf(t, body).Msg.Key != "srv.api.noRunningRoute" {
		t.Errorf("an error before the first event is a normal error response: %d %s", resp.StatusCode, body)
	}

	resp, body = call(t, srv, "late", `{}`)
	if resp.StatusCode != 200 {
		t.Errorf("once streaming has begun the status stays 200, got %d", resp.StatusCode)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	var last struct {
		Type  string    `json:"type"`
		Error wireError `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Type != "error" || last.Error.Code != CodeService || last.Error.Msg.Key != "srv.api.routeEngineError" {
		t.Errorf("final line = %+v", last)
	}

	resp, body = call(t, srv, "empty", `{}`)
	if resp.StatusCode != 200 || len(body) != 0 {
		t.Errorf("an empty stream is an empty 200: %d %q", resp.StatusCode, body)
	}
}

func TestRPCStreamStopsWhenTheClientGoes(t *testing.T) {
	stopped := make(chan error, 1)
	srv := newRPCServer(t, func(r *RPC) {
		r.Stream("endless", func(ctx context.Context, _ greetIn, emit func(progressEvent) error) error {
			for {
				if err := emit(progressEvent{Type: "progress"}); err != nil {
					stopped <- err
					return err
				}
				select {
				case <-ctx.Done():
					stopped <- ctx.Err()
					return ctx.Err()
				case <-time.After(5 * time.Millisecond):
				}
			}
		})
	})
	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/rpc/endless", strings.NewReader(`{}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = bufio.NewScanner(resp.Body).Scan()
	cancel()
	resp.Body.Close()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler kept running after the client disconnected")
	}
}

func TestRPCRoleChecksThroughSessions(t *testing.T) {
	store := NewMemorySessionStore()
	sessions := NewSessions(store, time.Hour, discard)
	customer, err := sessions.Start(t.Context(), Principal{Role: contracts.RoleCustomer, UserID: "c1", Name: "Dina"})
	if err != nil {
		t.Fatal(err)
	}
	rpc := NewRPC(discard)
	rpc.Handle("admin.only", func(ctx context.Context, _ struct{}) (Principal, error) {
		return RequireRole(ctx, contracts.RoleAdmin)
	})
	rpc.Handle("customer.me", func(ctx context.Context, _ struct{}) (Principal, error) {
		return RequireRole(ctx, contracts.RoleCustomer)
	})
	mux := http.NewServeMux()
	rpc.Mount(mux)
	srv := httptest.NewServer(Chain(mux, TraceMiddleware, sessions.Authenticate))
	t.Cleanup(srv.Close)

	do := func(method, token string) (int, wireError, Principal) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/rpc/"+method, strings.NewReader(`{}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var p Principal
		_ = json.Unmarshal(body, &p)
		return resp.StatusCode, wireOf(t, body), p
	}

	if status, w, _ := do("customer.me", ""); status != 401 || w.Code != CodeDenied {
		t.Errorf("anonymous: %d %+v, want 401 ditolak", status, w)
	}
	if status, w, _ := do("customer.me", "not-a-real-token"); status != 401 {
		t.Errorf("bad token: %d %+v, want 401", status, w)
	}
	if status, w, _ := do("admin.only", customer); status != 403 || w.Msg.Key != MsgRoleDenied {
		t.Errorf("wrong role: %d %+v, want 403", status, w)
	}
	if status, _, p := do("customer.me", customer); status != 200 || p.UserID != "c1" || p.Role != contracts.RoleCustomer {
		t.Errorf("right role: %d %+v", status, p)
	}
}

func TestRecoverTurnsPanicsIntoAGeneric500(t *testing.T) {
	var logged strings.Builder
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("secret internals") }),
		TraceMiddleware, Recover(NewLogger(&logged, "test", 0)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret internals") {
		t.Errorf("status %d body %s", rec.Code, rec.Body)
	}
	if w := wireOf(t, rec.Body.Bytes()); w.Code != CodeService || w.Msg.Key != MsgInternal {
		t.Errorf("wire = %+v", w)
	}
	if !strings.Contains(logged.String(), "secret internals") || !strings.Contains(logged.String(), "traceId") {
		t.Errorf("the panic must be logged with the trace id: %s", logged.String())
	}
}

func TestAccessLogRecordsStatusAndTrace(t *testing.T) {
	var buf strings.Builder
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short and stout"))
	}), TraceMiddleware, AccessLog(NewLogger(&buf, "test", 0)))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/brew", nil))

	var line map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &line); err != nil {
		t.Fatal(err)
	}
	if line["status"] != 418.0 || line["path"] != "/brew" || line["method"] != "GET" || line["bytes"] != 15.0 || line["traceId"] == nil {
		t.Errorf("access log = %v", line)
	}
}
