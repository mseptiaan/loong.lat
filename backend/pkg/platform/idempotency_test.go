package platform

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// idempotencyStoreContract is what every IdempotencyStore must do; memory and Postgres both run it.
func idempotencyStoreContract(t *testing.T, store IdempotencyStore) {
	t.Helper()
	ctx := t.Context()
	fp := []byte("fingerprint-1")

	state, _, err := store.Begin(ctx, "customer:c1 /v1/rpc/plans.confirm", "key-1", fp)
	if err != nil || state != IdempotencyClaimed {
		t.Fatalf("first Begin = %v, %v; want claimed", state, err)
	}
	if state, _, _ := store.Begin(ctx, "customer:c1 /v1/rpc/plans.confirm", "key-1", fp); state != IdempotencyInFlight {
		t.Errorf("Begin while running = %v, want in flight", state)
	}
	if state, _, _ := store.Begin(ctx, "customer:c1 /v1/rpc/plans.confirm", "key-1", []byte("other")); state != IdempotencyMismatch {
		t.Errorf("Begin with another fingerprint = %v, want mismatch", state)
	}

	resp := StoredResponse{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
	if err := store.Complete(ctx, "customer:c1 /v1/rpc/plans.confirm", "key-1", resp); err != nil {
		t.Fatal(err)
	}
	state, got, err := store.Begin(ctx, "customer:c1 /v1/rpc/plans.confirm", "key-1", fp)
	if err != nil || state != IdempotencyReplay || got.Status != 200 || got.ContentType != "application/json" || string(got.Body) != `{"ok":true}` {
		t.Errorf("Begin after Complete = %v, %+v, %v; want a replay of the stored response", state, got, err)
	}
	if state, _, _ := store.Begin(ctx, "customer:c1 /v1/rpc/plans.confirm", "key-1", []byte("other")); state != IdempotencyMismatch {
		t.Errorf("a finished key reused with another body = %v, want mismatch", state)
	}

	// scope isolates users
	if state, _, _ := store.Begin(ctx, "customer:c2 /v1/rpc/plans.confirm", "key-1", fp); state != IdempotencyClaimed {
		t.Errorf("the same key for another scope = %v, want claimed", state)
	}

	// Release lets a failed request be retried; it never discards a finished response.
	if state, _, _ := store.Begin(ctx, "scope", "key-2", fp); state != IdempotencyClaimed {
		t.Fatalf("claim for release test = %v", state)
	}
	if err := store.Release(ctx, "scope", "key-2"); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := store.Begin(ctx, "scope", "key-2", fp); state != IdempotencyClaimed {
		t.Errorf("Begin after Release = %v, want claimed", state)
	}
	if err := store.Complete(ctx, "scope", "key-2", resp); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(ctx, "scope", "key-2"); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := store.Begin(ctx, "scope", "key-2", fp); state != IdempotencyReplay {
		t.Errorf("Release must not drop a finished response, got %v", state)
	}
}

func TestMemoryIdempotencyStore(t *testing.T) {
	idempotencyStoreContract(t, NewMemoryIdempotencyStore())
}

func TestMemoryIdempotencyStoreExpiryAndAbandonedClaims(t *testing.T) {
	now := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	store := NewMemoryIdempotencyStore()
	store.now = func() time.Time { return now }
	fp := []byte("fp")
	resp := StoredResponse{Status: 200, Body: []byte("{}")}

	store.Begin(t.Context(), "s", "done", fp)
	store.Complete(t.Context(), "s", "done", resp)
	store.Begin(t.Context(), "s", "stuck", fp) // the process died before Complete

	now = now.Add(IdempotencyLockTTL + time.Second)
	if state, _, _ := store.Begin(t.Context(), "s", "stuck", fp); state != IdempotencyClaimed {
		t.Errorf("an abandoned claim must be retryable after the lock TTL, got %v", state)
	}
	if state, _, _ := store.Begin(t.Context(), "s", "done", fp); state != IdempotencyReplay {
		t.Errorf("a finished key keeps replaying within the TTL, got %v", state)
	}

	now = now.Add(IdempotencyTTL)
	if state, _, _ := store.Begin(t.Context(), "s", "done", []byte("different")); state != IdempotencyClaimed {
		t.Errorf("an expired key is forgotten, got %v", state)
	}
}

// moneyServer is a handler that counts how often the "money operation" really runs.
func moneyServer(t *testing.T, store IdempotencyStore, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	sessions := NewSessions(NewMemorySessionStore(), time.Hour, discard)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/rpc/plans.confirm", handler)
	mux.HandleFunc("/v1/rpc/plans.list", handler)
	srv := httptest.NewServer(Chain(mux, TraceMiddleware, sessions.Authenticate, Idempotency(store, "plans.confirm")))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, key, body string, headers ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func TestIdempotencyReplaysTheFirstResponse(t *testing.T) {
	var runs atomic.Int32
	srv := moneyServer(t, NewMemoryIdempotencyStore(), func(w http.ResponseWriter, r *http.Request) {
		n := runs.Add(1)
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"run":` + string(rune('0'+n)) + `,"echo":` + string(body) + `}`))
	})
	url := srv.URL + "/v1/rpc/plans.confirm"

	first, firstBody := post(t, url, "k-1", `{"planId":"p1"}`)
	if first.StatusCode != 201 || first.Header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("first: %d %v", first.StatusCode, first.Header)
	}
	second, secondBody := post(t, url, "k-1", `{"planId":"p1"}`)
	if second.StatusCode != 201 || secondBody != firstBody || second.Header.Get("Idempotent-Replayed") != "true" || second.Header.Get("Content-Type") != "application/json" {
		t.Errorf("replay: %d %q (want %q) %v", second.StatusCode, secondBody, firstBody, second.Header)
	}
	if runs.Load() != 1 {
		t.Errorf("the operation ran %d times, want 1", runs.Load())
	}

	if _, _ = post(t, url, "k-2", `{"planId":"p1"}`); runs.Load() != 2 {
		t.Error("a new key is a new request")
	}
}

func TestIdempotencyRejectsAReusedKeyWithADifferentBody(t *testing.T) {
	srv := moneyServer(t, NewMemoryIdempotencyStore(), func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) })
	url := srv.URL + "/v1/rpc/plans.confirm"
	post(t, url, "k-1", `{"planId":"p1"}`)

	resp, body := post(t, url, "k-1", `{"planId":"p2"}`)
	var w wireError
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 422 || w.Code != CodeInvalid || w.Field != "Idempotency-Key" || w.Msg.Key != MsgIdemReused {
		t.Errorf("status %d wire %+v", resp.StatusCode, w)
	}
}

func TestIdempotencyRequiresTheHeaderOnMoneyOperationsOnly(t *testing.T) {
	var runs atomic.Int32
	srv := moneyServer(t, NewMemoryIdempotencyStore(), func(w http.ResponseWriter, _ *http.Request) {
		runs.Add(1)
		_, _ = w.Write([]byte("{}"))
	})

	resp, body := post(t, srv.URL+"/v1/rpc/plans.confirm", "", `{}`)
	var w wireError
	_ = json.Unmarshal([]byte(body), &w)
	if resp.StatusCode != 422 || w.Field != "Idempotency-Key" || w.Msg.Key != MsgIdemRequired {
		t.Errorf("a money method without the header: %d %+v", resp.StatusCode, w)
	}
	if runs.Load() != 0 {
		t.Error("the handler must not run without the key")
	}

	if resp, _ := post(t, srv.URL+"/v1/rpc/plans.list", "", `{}`); resp.StatusCode != 200 {
		t.Errorf("other methods do not need the header, got %d", resp.StatusCode)
	}
	post(t, srv.URL+"/v1/rpc/plans.list", "optional", `{}`)
	post(t, srv.URL+"/v1/rpc/plans.list", "optional", `{}`)
	if runs.Load() != 2 { // one without a key, one with (the repeat replayed)
		t.Errorf("runs = %d: a key on any method is honoured", runs.Load())
	}

	if resp, _ := post(t, srv.URL+"/v1/rpc/plans.confirm", strings.Repeat("k", 256), `{}`); resp.StatusCode != 422 {
		t.Errorf("an oversized key: status %d, want 422", resp.StatusCode)
	}
}

func TestIdempotencyDoesNotRememberServerErrors(t *testing.T) {
	var runs atomic.Int32
	srv := moneyServer(t, NewMemoryIdempotencyStore(), func(w http.ResponseWriter, _ *http.Request) {
		if runs.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	url := srv.URL + "/v1/rpc/plans.confirm"

	if resp, _ := post(t, url, "k-1", `{}`); resp.StatusCode != 500 {
		t.Fatalf("first status = %d", resp.StatusCode)
	}
	resp, body := post(t, url, "k-1", `{}`)
	if resp.StatusCode != 200 || body != `{"ok":true}` || runs.Load() != 2 {
		t.Errorf("after a 500 the same key must run again: %d %q runs=%d", resp.StatusCode, body, runs.Load())
	}
	if resp, _ := post(t, url, "k-1", `{}`); resp.Header.Get("Idempotent-Replayed") != "true" {
		t.Error("the successful retry is remembered")
	}
}

func TestIdempotencyRemembersClientErrorsToo(t *testing.T) {
	var runs atomic.Int32
	srv := moneyServer(t, NewMemoryIdempotencyStore(), func(w http.ResponseWriter, _ *http.Request) {
		runs.Add(1)
		writeError(w, Conflict(contracts.Msg{Key: "srv.api.insufficientBalance"}))
	})
	url := srv.URL + "/v1/rpc/plans.confirm"
	post(t, url, "k-1", `{}`)
	resp, _ := post(t, url, "k-1", `{}`)
	if resp.StatusCode != 409 || runs.Load() != 1 {
		t.Errorf("a 409 is a definite answer and replays: %d runs=%d", resp.StatusCode, runs.Load())
	}
}

func TestIdempotencyReportsAConcurrentDuplicateAsInFlight(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32
	srv := moneyServer(t, NewMemoryIdempotencyStore(), func(w http.ResponseWriter, _ *http.Request) {
		runs.Add(1)
		close(entered)
		<-release
		_, _ = w.Write([]byte("{}"))
	})
	url := srv.URL + "/v1/rpc/plans.confirm"

	done := make(chan *http.Response, 1)
	go func() {
		resp, _ := post(t, url, "k-1", `{}`)
		done <- resp
	}()
	<-entered
	resp, body := post(t, url, "k-1", `{}`)
	var w wireError
	_ = json.Unmarshal([]byte(body), &w)
	if resp.StatusCode != 409 || w.Code != CodeConflict || w.Msg.Key != MsgIdemInFlight {
		t.Errorf("a duplicate while the first runs: %d %+v", resp.StatusCode, w)
	}
	close(release)
	if first := <-done; first.StatusCode != 200 {
		t.Errorf("the first request status = %d", first.StatusCode)
	}
	if runs.Load() != 1 {
		t.Errorf("runs = %d", runs.Load())
	}
}

func TestIdempotencyScopesKeysPerUserAndPath(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	sessions := NewSessions(NewMemorySessionStore(), time.Hour, discard)
	var runs atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/rpc/plans.confirm", func(w http.ResponseWriter, _ *http.Request) { runs.Add(1); _, _ = w.Write([]byte("{}")) })
	mux.HandleFunc("/v1/rpc/wallet.createTopUp", func(w http.ResponseWriter, _ *http.Request) { runs.Add(1); _, _ = w.Write([]byte("{}")) })
	srv := httptest.NewServer(Chain(mux, sessions.Authenticate, Idempotency(store)))
	t.Cleanup(srv.Close)

	dina, _ := sessions.Start(t.Context(), Principal{Role: contracts.RoleCustomer, UserID: "c1"})
	raka, _ := sessions.Start(t.Context(), Principal{Role: contracts.RoleCustomer, UserID: "c2"})
	confirm, topUp := srv.URL+"/v1/rpc/plans.confirm", srv.URL+"/v1/rpc/wallet.createTopUp"

	post(t, confirm, "same", `{}`, "Authorization", "Bearer "+dina)
	post(t, confirm, "same", `{}`, "Authorization", "Bearer "+raka) // another user: not a replay
	post(t, topUp, "same", `{}`, "Authorization", "Bearer "+dina)   // another method: not a replay
	post(t, confirm, "same", `{}`)                                  // anonymous: not a replay
	if runs.Load() != 4 {
		t.Errorf("runs = %d, want 4: keys must not collide across users or paths", runs.Load())
	}
	post(t, confirm, "same", `{}`, "Authorization", "Bearer "+dina)
	if runs.Load() != 4 {
		t.Error("the same user, path and key is a replay")
	}
}

func TestIdempotencyIgnoresReadsAndReleasesPanickedClaims(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	var panics atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/rpc/plans.confirm", func(w http.ResponseWriter, r *http.Request) {
		if panics.Add(1) == 1 {
			panic("handler bug")
		}
		_, _ = w.Write([]byte("{}"))
	})
	srv := httptest.NewServer(Chain(mux, Recover(discard), TraceMiddleware, Idempotency(store)))
	t.Cleanup(srv.Close)
	url := srv.URL + "/v1/rpc/plans.confirm"

	if resp, _ := post(t, url, "k", `{}`); resp.StatusCode != 500 {
		t.Fatalf("panic status = %d", resp.StatusCode)
	}
	if resp, _ := post(t, url, "k", `{}`); resp.StatusCode != 200 {
		t.Errorf("a panicked request must release its key, retry got %d", resp.StatusCode)
	}

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Idempotent-Replayed") != "" {
		t.Error("GET is never deduplicated")
	}
}

type brokenStore struct{ IdempotencyStore }

func (brokenStore) Begin(context.Context, string, string, []byte) (IdempotencyState, StoredResponse, error) {
	return 0, StoredResponse{}, context.DeadlineExceeded
}

func TestIdempotencyFailsClosedWhenTheStoreIsDown(t *testing.T) {
	var runs atomic.Int32
	srv := moneyServer(t, brokenStore{}, func(w http.ResponseWriter, _ *http.Request) { runs.Add(1) })
	resp, _ := post(t, srv.URL+"/v1/rpc/plans.confirm", "k", `{}`)
	if resp.StatusCode != 500 || runs.Load() != 0 {
		t.Errorf("status %d runs %d: without the store a money operation must not run unprotected", resp.StatusCode, runs.Load())
	}
}
