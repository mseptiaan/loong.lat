package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/auth"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
)

// These tests need Postgres (TEST_DATABASE_URL) and skip without it.

type fixture struct {
	pool     *pgxpool.Pool
	sessions *platform.Sessions
	svc      *auth.Service
}

// newFixture has driver drv-1 (Budi Santoso, PIN 123456, phone 0812-3456-7801) and a limiter that allows
// three attempts per phone number and a wait of an hour after that.
func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := drivertest.Pool(t)
	sessions := platform.NewSessions(platform.NewPgSessionStore(pool), time.Hour, drivertest.Logger())
	svc := auth.NewService(pool, sessions, platform.NewLoginLimiter(time.Hour, 3))

	e := drivertest.Provisioned(t, "drv-1", "0812-3456-7801", "123456")
	m := drivers.Master{ID: e.DriverID, Name: e.Name, Phone: drivers.NormalizePhone(e.Phone), PinHash: e.PinHash, JoinedAt: drivertest.Epoch, Version: 1}
	if err := drivers.Provision(t.Context(), pool, m); err != nil {
		t.Fatal(err)
	}
	return fixture{pool, sessions, svc}
}

func (f fixture) sessionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantRefusal(t *testing.T, err error, status int, key string) *platform.APIError {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok || apiErr.Status != status || apiErr.Msg.Key != key {
		t.Fatalf("error = %v, want %d %q", err, status, key)
	}
	return apiErr
}

func TestLoginWithPhoneAndPINStartsADriverSession(t *testing.T) {
	f := newFixture(t)
	for _, phone := range []string{"0812-3456-7801", "081234567801", "+62 812 3456 7801"} {
		login, err := f.svc.Login(t.Context(), phone, "123456")
		if err != nil {
			t.Fatalf("phone %q: %v", phone, err)
		}
		want := contracts.Session{Role: contracts.RoleDriver, UserID: "drv-1", Name: "Budi Santoso"}
		if login.Session != want || login.Token == "" {
			t.Errorf("login = %+v, want %+v and a token", login, want)
		}
		principal, err := f.sessions.Resolve(t.Context(), login.Token)
		if err != nil || principal != want {
			t.Errorf("token resolves to %+v, %v", principal, err)
		}
	}
}

func TestAWrongPINOrAnUnknownPhoneIsRefusedTheSameWay(t *testing.T) {
	f := newFixture(t)
	for name, c := range map[string][2]string{
		"wrong PIN":     {"081234567801", "654321"},
		"empty PIN":     {"081234567801", ""},
		"unknown phone": {"081299999999", "123456"},
		"no phone":      {"", "123456"},
	} {
		_, err := f.svc.Login(t.Context(), c[0], c[1])
		apiErr := wantRefusal(t, err, http.StatusUnprocessableEntity, "srv.api.driverBadCredentials")
		if apiErr.Code != platform.CodeInvalid {
			t.Errorf("%s: code = %s", name, apiErr.Code)
		}
	}
	if n := f.sessionCount(t); n != 0 {
		t.Errorf("%d sessions after refused logins", n)
	}
}

func TestSignInAttemptsAreLimitedPerPhoneNumber(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for range 3 {
		_, err := f.svc.Login(ctx, "081234567801", "000000")
		wantRefusal(t, err, http.StatusUnprocessableEntity, "srv.api.driverBadCredentials")
	}
	// over the limit: even the right PIN is not looked at, and the answer says when to try again
	_, err := f.svc.Login(ctx, "+62 812 3456 7801", "123456")
	apiErr := wantRefusal(t, err, http.StatusTooManyRequests, platform.MsgTooManyRequest)
	if apiErr.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want a wait", apiErr.RetryAfter)
	}
	if n := f.sessionCount(t); n != 0 {
		t.Errorf("%d sessions: a limited attempt must not sign in", n)
	}

	// another phone number has its own allowance
	if _, err := f.svc.Login(ctx, "081299999999", "123456"); err != nil {
		wantRefusal(t, err, http.StatusUnprocessableEntity, "srv.api.driverBadCredentials")
	}
}

func TestAStoredHashThatCannotBeReadIsOurFailureNotTheDriversMistake(t *testing.T) {
	f := newFixture(t)
	m := drivers.Master{ID: "drv-2", Name: "Sari", Phone: "081299999999", PinHash: "$argon2id$garbage", JoinedAt: drivertest.Epoch, Version: 1}
	if err := drivers.Provision(t.Context(), f.pool, m); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Login(t.Context(), "081299999999", "123456")
	if _, isAPI := errors.AsType[*platform.APIError](err); isAPI || !errors.Is(err, platform.ErrBadHash) {
		t.Errorf("error = %v, want an internal failure wrapping ErrBadHash", err)
	}
}

func TestLogoutEndsExactlyTheSessionOfTheRequest(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a, err := f.svc.Login(ctx, "081234567801", "123456")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.svc.Login(ctx, "081234567801", "123456")
	if err != nil {
		t.Fatal(err)
	}

	handler := auth.RememberToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := f.svc.Logout(r.Context()); err != nil {
			t.Error(err)
		}
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+a.Token)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if _, err := f.sessions.Resolve(ctx, a.Token); !errors.Is(err, platform.ErrSessionNotFound) {
		t.Errorf("the session survived logout: %v", err)
	}
	if _, err := f.sessions.Resolve(ctx, b.Token); err != nil {
		t.Errorf("logout ended another session: %v", err)
	}

	// without a token there is nothing to end, and that is not an error
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
}

func TestCurrentIsWhoTheRequestActsAs(t *testing.T) {
	if got := auth.Current(t.Context()); got != nil {
		t.Errorf("anonymous request = %+v, want nil", got)
	}
	want := contracts.Session{Role: contracts.RoleDriver, UserID: "drv-1", Name: "Budi"}
	if got := auth.Current(platform.WithPrincipal(t.Context(), want)); got == nil || *got != want {
		t.Errorf("signed-in request = %v, want %+v", got, want)
	}
}
