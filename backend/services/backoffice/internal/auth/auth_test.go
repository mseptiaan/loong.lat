package auth

import (
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

const secret = "correct horse battery"

type rig struct {
	pool     *pgxpool.Pool
	sessions *platform.Sessions
	svc      *Service
	srv      *httptest.Server
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testkit.Pool(t)
	sessions := platform.NewSessions(platform.NewPgSessionStore(pool), time.Hour, testkit.Discard)
	svc, err := New(pool, sessions)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SeedAdmin(t.Context(), "Rani@Loong.lat", secret, "Rani Wulandari"); err != nil {
		t.Fatal(err)
	}
	rpc := platform.NewRPC(testkit.Discard)
	svc.Mount(rpc)
	mux := http.NewServeMux()
	rpc.Mount(mux)
	srv := httptest.NewServer(platform.Chain(mux, platform.TraceMiddleware, sessions.Authenticate, CaptureToken))
	t.Cleanup(srv.Close)
	return &rig{pool: pool, sessions: sessions, svc: svc, srv: srv}
}

func (r *rig) call(t *testing.T, method, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, r.srv.URL+"/v1/rpc/"+method, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func TestSeedAdminStoresOnlyAHashAndIsIdempotent(t *testing.T) {
	r := newRig(t)
	var stored string
	if err := r.pool.QueryRow(t.Context(), `SELECT string_agg(a::text, ' ') FROM admins a`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, secret) || !strings.Contains(stored, "$argon2id$") {
		t.Errorf("admin row %q: want an argon2id hash and no plaintext password", stored)
	}

	// a second start, even with another password, neither duplicates nor changes the account
	if err := r.svc.SeedAdmin(t.Context(), "rani@loong.lat", "another password", "Someone Else"); err != nil {
		t.Fatal(err)
	}
	var count int
	var name string
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*), min(name) FROM admins`).Scan(&count, &name); err != nil || count != 1 || name != "Rani Wulandari" {
		t.Errorf("%d admins named %q (%v), want the one account untouched", count, name, err)
	}
	if _, err := r.svc.Login(t.Context(), "rani@loong.lat", secret); err != nil {
		t.Errorf("the original password stopped working: %v", err)
	}

	if err := r.svc.SeedAdmin(t.Context(), "weak@loong.lat", "short", "Weak"); err == nil {
		t.Error("a password under 8 characters was accepted")
	}
}

func TestLoginStartsASessionForTheAdmin(t *testing.T) {
	r := newRig(t)
	got, err := r.svc.Login(t.Context(), " RANI@loong.LAT ", secret) // emails are case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != contracts.RoleAdmin || got.Name != "Rani Wulandari" || !strings.HasPrefix(got.UserID, "adm-") || got.Token == "" {
		t.Fatalf("result = %+v, want an admin session with a token", got)
	}
	p, err := r.sessions.Resolve(t.Context(), got.Token)
	if err != nil || p.Role != contracts.RoleAdmin || p.UserID != got.UserID {
		t.Errorf("the token resolves to %+v, %v", p, err)
	}
}

func TestLoginRefusesWrongCredentialsAlike(t *testing.T) {
	r := newRig(t)
	for name, creds := range map[string][2]string{
		"a wrong password": {"rani@loong.lat", "not the password"},
		"an unknown email": {"nobody@loong.lat", secret},
		"an empty body":    {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := r.svc.Login(t.Context(), creds[0], creds[1])
			apiErr, ok := errors.AsType[*platform.APIError](err)
			if !ok || apiErr.Code != platform.CodeInvalid || apiErr.Msg.Key != "srv.api.badCredentials" {
				t.Errorf("error = %v, want tidak_valid badCredentials, the same for every kind of failure", err)
			}
		})
	}
}

func TestLoginIsRateLimitedPerEmail(t *testing.T) {
	r := newRig(t)
	for range 5 {
		if _, err := r.svc.Login(t.Context(), "rani@loong.lat", "wrong"); err == nil {
			t.Fatal("a wrong password was accepted")
		}
	}
	status, body := r.call(t, "auth.loginAdmin", "", `{"email":"rani@loong.lat","password":"`+secret+`"}`)
	if status != http.StatusTooManyRequests {
		t.Fatalf("status %d, body %s; want 429 after five attempts, even with the right password", status, body)
	}
	// other accounts are not affected
	if _, err := r.svc.Login(t.Context(), "nobody@loong.lat", "x"); err == nil {
		t.Error("unexpected success")
	} else if apiErr, ok := errors.AsType[*platform.APIError](err); !ok || apiErr.Status == http.StatusTooManyRequests {
		t.Errorf("another email was limited too: %v", err)
	}
}

func TestSessionAndLogoutOverRPC(t *testing.T) {
	r := newRig(t)
	status, body := r.call(t, "auth.loginAdmin", "", `{"email":"rani@loong.lat","password":"`+secret+`"}`)
	if status != http.StatusOK {
		t.Fatalf("login: %d %s", status, body)
	}
	var login LoginResult
	if err := json.Unmarshal([]byte(body), &login); err != nil || login.Token == "" || login.Role != contracts.RoleAdmin {
		t.Fatalf("login body = %s (%v), want the session fields and a token side by side", body, err)
	}

	_, body = r.call(t, "auth.session", login.Token, ``)
	var session *contracts.Session
	if err := json.Unmarshal([]byte(body), &session); err != nil || session == nil || session.UserID != login.UserID {
		t.Errorf("session = %s, want the admin", body)
	}
	if _, body = r.call(t, "auth.session", "", ``); strings.TrimSpace(body) != "null" {
		t.Errorf("session without a token = %s, want null", body)
	}

	// a second session is untouched by logging the first out
	other, err := r.svc.Login(t.Context(), "rani@loong.lat", secret)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := r.call(t, "auth.logout", login.Token, ``); status != http.StatusOK {
		t.Fatalf("logout: %d %s", status, body)
	}
	if _, body = r.call(t, "auth.session", login.Token, ``); strings.TrimSpace(body) != "null" {
		t.Errorf("session after logout = %s, want null", body)
	}
	if _, body = r.call(t, "auth.session", other.Token, ``); strings.TrimSpace(body) == "null" {
		t.Error("logging out one session ended another")
	}
	if status, _ := r.call(t, "auth.logout", "", ``); status != http.StatusOK {
		t.Errorf("logout without a session: %d, want it to be harmless", status)
	}
}
