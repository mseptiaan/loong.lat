package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func newAuth(t *testing.T) (*Service, *pgxpool.Pool, *platform.Sessions) {
	t.Helper()
	pool := testkit.DB(t)
	sessions := platform.NewSessions(platform.NewPgSessionStore(pool), time.Hour, testkit.Log)
	s, err := New(pool, testkit.Publisher(pool), sessions)
	if err != nil {
		t.Fatal(err)
	}
	return s, pool, sessions
}

func apiError(t *testing.T, err error) *platform.APIError {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok {
		t.Fatalf("want a platform.APIError, got %T: %v", err, err)
	}
	return apiErr
}

// TestMain makes password hashing cheap: the production cost (64 MiB, 3 passes) takes seconds under the race
// detector, and nothing here is about how strong the hash is. Hashes carry their own parameters, so they still
// verify.
func TestMain(m *testing.M) {
	platform.DefaultArgon2 = platform.Argon2Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}
	os.Exit(m.Run())
}

var dina = RegisterInput{Name: "Dina Pramesti", Email: "Dina@Contoh.id", Phone: "+62 812-3456-7801", Password: "demo1234"}

func TestRegisterChecksTheInputBeforeAnythingElse(t *testing.T) {
	s := &Service{} // refuses before it touches the database
	tests := []struct {
		name   string
		mutate func(*RegisterInput)
		field  string
		msgKey string
	}{
		{"no name", func(in *RegisterInput) { in.Name = "  " }, "name", "srv.api.nameEmpty"},
		{"bad email", func(in *RegisterInput) { in.Email = "dina@" }, "email", "srv.api.emailInvalid"},
		{"short phone", func(in *RegisterInput) { in.Phone = "0812" }, "phone", "srv.api.phoneIncomplete"},
		{"short password", func(in *RegisterInput) { in.Password = "1234567" }, "password", "srv.api.passwordShort"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := dina
			tt.mutate(&in)
			_, err := s.Register(t.Context(), in)
			got := apiError(t, err)
			if got.Code != platform.CodeInvalid || got.Field != tt.field || got.Msg.Key != tt.msgKey {
				t.Errorf("got %s %q %q", got.Code, got.Field, got.Msg.Key)
			}
		})
	}
}

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"+62 812-3456-7801": "081234567801",
		"0812 3456 7801":    "081234567801",
		"6281234567801":     "081234567801",
		"abc":               "",
	} {
		if got := normalizePhone(in); got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegisterCreatesTheCustomerTheirWalletAndASession(t *testing.T) {
	s, pool, sessions := newAuth(t)
	in, err := s.Register(t.Context(), dina)
	if err != nil {
		t.Fatal(err)
	}
	if in.Role != contracts.RoleCustomer || in.Name != "Dina Pramesti" || !strings.HasPrefix(in.UserID, "cus-") || in.Token == "" {
		t.Fatalf("signed in = %+v", in)
	}

	var email, phone, hash string
	if err := pool.QueryRow(t.Context(), `SELECT email, phone, password_hash FROM customers WHERE id = $1`, in.UserID).Scan(&email, &phone, &hash); err != nil {
		t.Fatal(err)
	}
	if email != "dina@contoh.id" || phone != "081234567801" {
		t.Errorf("stored email %q phone %q, want them normalized", email, phone)
	}
	if !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, dina.Password) {
		t.Errorf("password hash = %q", hash)
	}
	if n := testkit.Count(t, pool, `SELECT count(*) FROM wallets WHERE customer_id = $1 AND balance_rp = 0`, in.UserID); n != 1 {
		t.Errorf("%d empty wallets for the new customer, want 1", n)
	}
	p, err := sessions.Resolve(t.Context(), in.Token)
	if err != nil || p != in.Session {
		t.Errorf("the token resolves to %+v, %v; want %+v", p, err, in.Session)
	}

	envs := testkit.Outbox(t, pool, contracts.TypeCustomerRegistered)
	if len(envs) != 1 || envs[0].Key != in.UserID || envs[0].AggregateVersion != 1 {
		t.Fatalf("outbox = %+v", envs)
	}
	if ev, err := envs[0].Payload[contracts.CustomerRegistered](); err != nil || ev.Email != "dina@contoh.id" || ev.Phone != "081234567801" || ev.Name != "Dina Pramesti" {
		t.Errorf("payload = %+v, %v", ev, err)
	}
	if strings.Contains(string(envs[0].Data), dina.Password) || strings.Contains(string(envs[0].Data), "argon2") {
		t.Error("credentials must not leave the service")
	}
}

func TestAnEmailRegistersOnce(t *testing.T) {
	s, pool, _ := newAuth(t)
	if _, err := s.Register(t.Context(), dina); err != nil {
		t.Fatal(err)
	}
	again := dina
	again.Email = "DINA@contoh.ID" // the same address in another case
	_, err := s.Register(t.Context(), again)
	if got := apiError(t, err); got.Code != platform.CodeConflict || got.Field != "email" || got.Msg.Key != "srv.api.emailTaken" {
		t.Errorf("got %+v", got)
	}
	if n := testkit.Count(t, pool, `SELECT count(*) FROM customers`); n != 1 {
		t.Errorf("%d customers, want 1", n)
	}
	if n := len(testkit.Outbox(t, pool, contracts.TypeCustomerRegistered)); n != 1 {
		t.Errorf("%d customer.registered events, want 1: the failed attempt must publish nothing", n)
	}
}

func TestLoginRefusesWrongPasswordsAndUnknownEmailsAlike(t *testing.T) {
	s, _, sessions := newAuth(t)
	registered, err := s.Register(t.Context(), dina)
	if err != nil {
		t.Fatal(err)
	}

	in, err := s.Login(t.Context(), " dina@contoh.id ", "demo1234")
	if err != nil || in.UserID != registered.UserID || in.Token == registered.Token {
		t.Fatalf("login = %+v, %v; want the same customer with a session of its own", in, err)
	}
	if _, err := sessions.Resolve(t.Context(), in.Token); err != nil {
		t.Errorf("the new token does not resolve: %v", err)
	}

	_, wrong := s.Login(t.Context(), "dina@contoh.id", "demo12345")
	_, unknown := s.Login(t.Context(), "nobody@contoh.id", "demo1234")
	w, u := apiError(t, wrong), apiError(t, unknown)
	if w.Code != platform.CodeInvalid || w.Msg.Key != "srv.api.badCredentials" {
		t.Errorf("wrong password: %+v", w)
	}
	if w.Code != u.Code || w.Status != u.Status || w.Field != u.Field || w.Msg.Key != u.Msg.Key {
		t.Errorf("a wrong password (%+v) and an unknown email (%+v) must be refused the same way", w, u)
	}
}

func TestLoginAttemptsAreThrottledPerAccount(t *testing.T) {
	s, _, _ := newAuth(t)
	if _, err := s.Register(t.Context(), dina); err != nil {
		t.Fatal(err)
	}
	for range loginBurst {
		if _, err := s.Login(t.Context(), "dina@contoh.id", "wrong-password"); apiError(t, err).Status != http.StatusUnprocessableEntity {
			t.Fatalf("within the burst: %v", err)
		}
	}
	// Even the right password waits its turn once the account is being hammered.
	_, err := s.Login(t.Context(), "dina@contoh.id", "demo1234")
	got := apiError(t, err)
	if got.Status != http.StatusTooManyRequests || got.RetryAfter <= 0 {
		t.Errorf("got %+v, want 429 with a Retry-After", got)
	}
	if _, err := s.Login(t.Context(), "someone-else@contoh.id", "x"); apiError(t, err).Status == http.StatusTooManyRequests {
		t.Error("another account must not share the throttle")
	}
}

func TestLogoutEndsOnlyTheCallersSession(t *testing.T) {
	s, _, sessions := newAuth(t)
	first, err := s.Register(t.Context(), dina)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Login(t.Context(), dina.Email, dina.Password)
	if err != nil {
		t.Fatal(err)
	}

	// The bearer token reaches Logout through the middleware, as it does in the service.
	var logoutErr error
	h := TokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { logoutErr = s.Logout(r.Context()) }))
	req := httptest.NewRequest(http.MethodPost, "/v1/rpc/auth.logout", nil)
	req.Header.Set("Authorization", "Bearer "+first.Token)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if logoutErr != nil {
		t.Fatal(logoutErr)
	}

	if _, err := sessions.Resolve(t.Context(), first.Token); !errors.Is(err, platform.ErrSessionNotFound) {
		t.Errorf("the signed-out token still resolves: %v", err)
	}
	if _, err := sessions.Resolve(t.Context(), second.Token); err != nil {
		t.Errorf("another session of the customer was ended too: %v", err)
	}
	if err := s.Logout(context.Background()); err != nil {
		t.Errorf("logging out without a token should be harmless: %v", err)
	}
}

func TestOnlyCustomersPassAsCustomer(t *testing.T) {
	op := AsCustomer(func(_ context.Context, customerID string, _ struct{}) (string, error) { return customerID, nil })
	as := func(role contracts.Role) context.Context {
		return platform.WithPrincipal(context.Background(), platform.Principal{Role: role, UserID: "u-1", Name: "U"})
	}

	if got, err := op(as(contracts.RoleCustomer), struct{}{}); err != nil || got != "u-1" {
		t.Errorf("customer: %q, %v", got, err)
	}
	if _, err := op(context.Background(), struct{}{}); apiError(t, err).Status != http.StatusUnauthorized {
		t.Errorf("anonymous: %v", err)
	}
	for _, role := range []contracts.Role{contracts.RoleAdmin, contracts.RoleDriver} {
		if _, err := op(as(role), struct{}{}); apiError(t, err).Status != http.StatusForbidden {
			t.Errorf("%s: %v", role, err)
		}
	}
}
