package auth

import (
	"cmp"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// decide hands back office's decision about a customer to the handler, as the consumer would.
func decide(t *testing.T, pool platform.TxBeginner, eventType, customerID string, version int64, when time.Time) error {
	t.Helper()
	handler, data := eventbus.Handler(onBlocked), any(contracts.CustomerBlocked{CustomerID: customerID})
	if eventType == contracts.TypeCustomerUnblocked {
		handler, data = onUnblocked, contracts.CustomerUnblocked{CustomerID: customerID}
	}
	env, err := contracts.NewEnvelope("backoffice-api", eventType, cmp.Or(customerID, "cus-none"), version, data)
	if err != nil {
		t.Fatal(err)
	}
	env.OccurredAt = contracts.At(when)
	return platform.WithTx(t.Context(), pool, func(tx pgx.Tx) error { return handler(t.Context(), tx, env) })
}

func blockedOf(t *testing.T, s *Service, id string) bool {
	t.Helper()
	var blocked bool
	if err := s.pool.QueryRow(t.Context(), `SELECT blocked FROM customers WHERE id = $1`, id).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	return blocked
}

func TestBlockingACustomerEndsEverySessionAndRefusesSignIn(t *testing.T) {
	s, pool, sessions := newAuth(t)
	registered, err := s.Register(t.Context(), dina)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Login(t.Context(), dina.Email, dina.Password) // a second device
	if err != nil {
		t.Fatal(err)
	}
	bystander, err := s.Register(t.Context(), RegisterInput{Name: "Raka", Email: "raka@contoh.id", Phone: "0812-3456-7802", Password: "demo1234"})
	if err != nil {
		t.Fatal(err)
	}

	if err := decide(t, pool, contracts.TypeCustomerBlocked, registered.UserID, 1, at(1)); err != nil {
		t.Fatal(err)
	}
	if !blockedOf(t, s, registered.UserID) {
		t.Fatal("the customer is not blocked")
	}
	for name, token := range map[string]string{"first session": registered.Token, "second session": second.Token} {
		if _, err := sessions.Resolve(t.Context(), token); !errors.Is(err, platform.ErrSessionNotFound) {
			t.Errorf("the %s survived the block: %v", name, err)
		}
	}
	if _, err := sessions.Resolve(t.Context(), bystander.Token); err != nil {
		t.Errorf("another customer's session was ended: %v", err)
	}

	// Signing in is refused with the right password, with the same answer as ever for a wrong one.
	_, err = s.Login(t.Context(), dina.Email, dina.Password)
	if got := apiError(t, err); got.Code != platform.CodeDenied || got.Status != 403 || got.Msg.Key != "srv.api.accountBlocked" {
		t.Errorf("blocked sign-in: %s %d %q", got.Code, got.Status, got.Msg.Key)
	}
	_, err = s.Login(t.Context(), dina.Email, "wrong password")
	if got := apiError(t, err); got.Msg.Key != "srv.api.badCredentials" {
		t.Errorf("a wrong password for a blocked account says %q: it must not reveal the block", got.Msg.Key)
	}
	if err := EnsureActive(t.Context(), pool, registered.UserID); apiError(t, err).Msg.Key != "srv.ops.err.accountBlocked" {
		t.Errorf("EnsureActive = %v", err)
	}
	if err := EnsureActive(t.Context(), pool, bystander.UserID); err != nil {
		t.Errorf("EnsureActive for a customer in good standing: %v", err)
	}

	// Unblocked, they can sign in again.
	if err := decide(t, pool, contracts.TypeCustomerUnblocked, registered.UserID, 2, at(2)); err != nil {
		t.Fatal(err)
	}
	if blockedOf(t, s, registered.UserID) {
		t.Fatal("still blocked")
	}
	if _, err := s.Login(t.Context(), dina.Email, dina.Password); err != nil {
		t.Errorf("sign-in after unblocking: %v", err)
	}
}

func TestAnOlderDecisionCannotUndoANewerOne(t *testing.T) {
	s, pool, _ := newAuth(t)
	c, err := s.Register(t.Context(), dina)
	if err != nil {
		t.Fatal(err)
	}
	// Unblock (2) arrives before block (1): the customer ends up unblocked, not blocked.
	if err := decide(t, pool, contracts.TypeCustomerUnblocked, c.UserID, 2, at(2)); err != nil {
		t.Fatal(err)
	}
	if err := decide(t, pool, contracts.TypeCustomerBlocked, c.UserID, 1, at(1)); err != nil {
		t.Fatalf("a late older decision must be dropped quietly: %v", err)
	}
	if blockedOf(t, s, c.UserID) {
		t.Error("the older block overrode the newer unblock")
	}
	// A block that is newer by version wins, and a repeat of it changes nothing.
	for range 2 {
		if err := decide(t, pool, contracts.TypeCustomerBlocked, c.UserID, 3, at(3)); err != nil {
			t.Fatal(err)
		}
	}
	if !blockedOf(t, s, c.UserID) {
		t.Error("the newer block was not applied")
	}
	// Equal versions (a producer that does not number them) fall back to the time of the decision.
	if err := decide(t, pool, contracts.TypeCustomerUnblocked, c.UserID, 3, at(1)); err != nil {
		t.Fatal(err)
	}
	if !blockedOf(t, s, c.UserID) {
		t.Error("an unblock from before the block won at an equal version")
	}
	if err := decide(t, pool, contracts.TypeCustomerUnblocked, c.UserID, 3, at(5)); err != nil {
		t.Fatal(err)
	}
	if blockedOf(t, s, c.UserID) {
		t.Error("an unblock from after the block lost at an equal version")
	}
}

func TestADecisionAboutNobodyIsRefused(t *testing.T) {
	_, pool, _ := newAuth(t)
	err := decide(t, pool, contracts.TypeCustomerBlocked, "cus-nobody", 1, at(1))
	if err == nil {
		t.Fatal("a decision about nobody was accepted")
	}
	if err := decide(t, pool, contracts.TypeCustomerBlocked, "", 1, at(1)); err == nil {
		t.Fatal("a decision without a customer id was accepted")
	}
}

func at(minutes int) time.Time {
	return time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC).Add(time.Duration(minutes) * time.Minute)
}
