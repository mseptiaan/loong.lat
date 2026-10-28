package platform

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

func TestNewTokenIs256BitsOfEntropy(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		token, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(token) { // 32 bytes, base64url without padding
			t.Fatalf("token %q is not 43 URL-safe characters", token)
		}
		if seen[token] {
			t.Fatal("duplicate token")
		}
		seen[token] = true
	}
}

func TestHashTokenIsStableAndDoesNotRevealTheToken(t *testing.T) {
	a, b := HashToken("token-a"), HashToken("token-b")
	if a == b || a != HashToken("token-a") {
		t.Error("hashes must be deterministic and distinct")
	}
	// sha256("abc"): the hash is plain SHA-256, so other tools can reproduce it when debugging.
	want := [...]byte{0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea, 0x41, 0x41, 0x40, 0xde, 0x5d, 0xae, 0x22, 0x23, 0xb0, 0x03, 0x61, 0xa3, 0x96, 0x17, 0x7a, 0x9c, 0xb4, 0x10, 0xff, 0x61, 0xf2, 0x00, 0x15, 0xad}
	if HashToken("abc") != TokenHash(want) {
		t.Error("HashToken must be SHA-256")
	}
}

// sessionStoreContract is the behaviour every SessionStore must have; the memory and Postgres stores both run it.
func sessionStoreContract(t *testing.T, store SessionStore) {
	t.Helper()
	ctx := t.Context()
	dina := Principal{Role: contracts.RoleCustomer, UserID: "c1", Name: "Dina"}
	hash := HashToken("live")

	if _, err := store.Lookup(ctx, hash); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("unknown token: %v, want ErrSessionNotFound", err)
	}
	if err := store.Create(ctx, hash, dina, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Lookup(ctx, hash); err != nil || got != dina {
		t.Errorf("Lookup = %+v, %v", got, err)
	}

	if err := store.Create(ctx, HashToken("stale"), dina, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(ctx, HashToken("stale")); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expired token: %v, want ErrSessionNotFound", err)
	}

	if err := store.Delete(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(ctx, hash); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("signed-out token: %v, want ErrSessionNotFound", err)
	}

	for _, tok := range []string{"phone", "laptop"} {
		if err := store.Create(ctx, HashToken(tok), dina, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	other := Principal{Role: contracts.RoleCustomer, UserID: "c2", Name: "Raka"}
	if err := store.Create(ctx, HashToken("other"), other, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"phone", "laptop"} {
		if _, err := store.Lookup(ctx, HashToken(tok)); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("%s survived DeleteUser", tok)
		}
	}
	if _, err := store.Lookup(ctx, HashToken("other")); err != nil {
		t.Errorf("DeleteUser removed another user's session: %v", err)
	}
}

func TestMemorySessionStore(t *testing.T) { sessionStoreContract(t, NewMemorySessionStore()) }

func TestSessionsLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	store := NewMemorySessionStore()
	store.now = func() time.Time { return now }
	sessions := NewSessions(store, 30*time.Minute, discard)
	sessions.now = store.now
	dina := Principal{Role: contracts.RoleCustomer, UserID: "c1", Name: "Dina"}

	token, err := sessions.Start(t.Context(), dina)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := sessions.Resolve(t.Context(), token); err != nil || got != dina {
		t.Errorf("Resolve = %+v, %v", got, err)
	}

	if _, ok := store.sessions[HashToken(token)]; !ok || len(store.sessions) != 1 {
		t.Error("the session must be stored under HashToken(token)")
	}
	var raw TokenHash // the token's own bytes must not be a key: only its hash is stored
	decoded, _ := base64.RawURLEncoding.DecodeString(token)
	copy(raw[:], decoded)
	if _, leaked := store.sessions[raw]; leaked {
		t.Error("the store holds the token itself")
	}

	now = now.Add(29 * time.Minute)
	if _, err := sessions.Resolve(t.Context(), token); err != nil {
		t.Errorf("still valid at 29 minutes: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := sessions.Resolve(t.Context(), token); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expired at 31 minutes: %v", err)
	}

	token2, _ := sessions.Start(t.Context(), dina)
	token3, _ := sessions.Start(t.Context(), dina)
	if err := sessions.End(t.Context(), token2); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Resolve(t.Context(), token2); !errors.Is(err, ErrSessionNotFound) {
		t.Error("End must sign the token out")
	}
	if err := sessions.EndAll(t.Context(), "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Resolve(t.Context(), token3); !errors.Is(err, ErrSessionNotFound) {
		t.Error("EndAll must sign every token of the user out")
	}
}

func TestAuthenticateMiddleware(t *testing.T) {
	sessions := NewSessions(NewMemorySessionStore(), time.Hour, discard)
	dina := Principal{Role: contracts.RoleCustomer, UserID: "c1", Name: "Dina"}
	token, err := sessions.Start(t.Context(), dina)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, header string
		want         bool
	}{
		{"valid", "Bearer " + token, true},
		{"scheme is case-insensitive", "bearer " + token, true},
		{"no header", "", false},
		{"unknown token", "Bearer nonsense", false},
		{"wrong scheme", "Basic " + token, false},
		{"missing token", "Bearer ", false},
		{"bare token", token, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Principal
			var ok bool
			h := sessions.Authenticate(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, ok = PrincipalFrom(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Authenticate must not reject by itself, status %d", rec.Code)
			}
			if ok != tt.want || (ok && got != dina) {
				t.Errorf("principal = %+v, %v; want signed in = %v", got, ok, tt.want)
			}
		})
	}
}

type failingStore struct{ SessionStore }

func (failingStore) Lookup(context.Context, TokenHash) (Principal, error) {
	return Principal{}, errors.New("database is down")
}

func TestAuthenticateStaysAnonymousWhenTheStoreFails(t *testing.T) {
	sessions := NewSessions(failingStore{NewMemorySessionStore()}, time.Hour, discard)
	called := false
	h := sessions.Authenticate(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		if _, ok := PrincipalFrom(r.Context()); ok {
			t.Error("a store failure must not authenticate")
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Error("the request should still reach the handler, which answers 401 itself")
	}
}

func TestRequireRole(t *testing.T) {
	admin := WithPrincipal(t.Context(), Principal{Role: contracts.RoleAdmin, UserID: "a1"})
	tests := []struct {
		name   string
		ctx    context.Context
		roles  []contracts.Role
		status int // 0 = allowed
	}{
		{"allowed", admin, []contracts.Role{contracts.RoleAdmin}, 0},
		{"one of several", admin, []contracts.Role{contracts.RoleCustomer, contracts.RoleAdmin}, 0},
		{"wrong role", admin, []contracts.Role{contracts.RoleDriver}, 403},
		{"anonymous", t.Context(), []contracts.Role{contracts.RoleAdmin}, 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := RequireRole(tt.ctx, tt.roles...)
			if tt.status == 0 {
				if err != nil || p.UserID != "a1" {
					t.Errorf("RequireRole = %+v, %v", p, err)
				}
				return
			}
			apiErr, ok := errors.AsType[*APIError](err)
			if !ok || apiErr.Status != tt.status || apiErr.Code != CodeDenied {
				t.Errorf("RequireRole error = %v, want ditolak %d", err, tt.status)
			}
		})
	}
}
