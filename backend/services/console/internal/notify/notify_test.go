package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// subscribe opens the hub's event stream as the given customer.
func subscribe(t *testing.T, srv *httptest.Server, customerID string) <-chan platform.Event {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Customer", customerID)
	return testkit.Events(t, req)
}

// rawNext waits for an event, or reports that none arrived within a moment.
func rawNext(events <-chan platform.Event, wait time.Duration) (platform.Event, bool) {
	select {
	case ev := <-events:
		return ev, true
	case <-time.After(wait):
		return platform.Event{}, false
	}
}

// next is rawNext without the broadcast settings.changed: the NOTIFY channel belongs to the whole test database,
// so the settings tests of other packages, running side by side, reach every relay. That is right for a
// broadcast and noise for the tests of one customer's announcements.
func next(events <-chan platform.Event, wait time.Duration) (platform.Event, bool) {
	deadline := time.Now().Add(wait)
	for {
		ev, ok := rawNext(events, time.Until(deadline))
		if !ok || ev.Type != "settings.changed" {
			return ev, ok
		}
	}
}

func TestAnnouncementsReachTheirCustomerOnlyAfterTheTransactionCommits(t *testing.T) {
	pool := platformtest.Pool(t)
	hub := platform.NewHub()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := platform.Principal{Role: contracts.RoleCustomer, UserID: r.Header.Get("X-Customer")}
		hub.Handler().ServeHTTP(w, r.WithContext(platform.WithPrincipal(r.Context(), p)))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(hub.Close)

	ctx, cancel := context.WithCancel(t.Context())
	relayed := make(chan struct{})
	go func() { defer close(relayed); Relay(ctx, os.Getenv(platformtest.EnvDatabaseURL), hub, testkit.Log) }()
	t.Cleanup(func() { cancel(); <-relayed })

	dina, raka, probe := subscribe(t, srv, "cus-dina"), subscribe(t, srv, "cus-raka"), subscribe(t, srv, "cus-probe")

	// Announcements sent before the relay is listening are lost, so wait until it has proved it is.
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := WalletChanged(ctx, pool, "cus-probe"); err != nil {
			t.Fatal(err)
		}
		if _, ok := next(probe, 50*time.Millisecond); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the relay never started listening")
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := PlanChanged(ctx, tx, "cus-dina", "pln-1"); err != nil {
		t.Fatal(err)
	}
	if ev, ok := next(dina, 200*time.Millisecond); ok {
		t.Fatalf("%+v was delivered before the transaction committed", ev)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if ev, ok := next(dina, 5*time.Second); !ok || ev != (platform.Event{Type: "plans.changed", PlanID: "pln-1"}) {
		t.Fatalf("after commit: %+v, %v", ev, ok)
	}

	// Not delivered to the other customer; rolled back means never.
	rolledBack, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := PlanChanged(ctx, rolledBack, "cus-dina", "pln-2"); err != nil {
		t.Fatal(err)
	}
	if err := rolledBack.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := platform.WithTx(ctx, pool, func(tx pgx.Tx) error { return WalletChanged(ctx, tx, "cus-dina") }); err != nil {
		t.Fatal(err)
	}
	if ev, ok := next(dina, 5*time.Second); !ok || ev != (platform.Event{Type: "wallet.changed"}) {
		t.Errorf("after the rolled-back change the next event is %+v, %v; want the wallet's", ev, ok)
	}
	if ev, ok := next(raka, 200*time.Millisecond); ok {
		t.Errorf("another customer received %+v", ev)
	}
}
