package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// relayed is a database with the notices table, a hub fed by Relay, and a server that opens the hub's event
// stream as the customer named in X-Customer. Announcements made before the relay listens are lost, so it only
// returns once the relay has proved it is listening.
type relayed struct {
	pool *pgxpool.Pool
	srv  *httptest.Server
}

func startRelay(t *testing.T) relayed {
	t.Helper()
	pool := testkit.DB(t)
	hub := platform.NewHub()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := platform.Principal{Role: contracts.RoleCustomer, UserID: r.Header.Get("X-Customer")}
		hub.Handler().ServeHTTP(w, r.WithContext(platform.WithPrincipal(r.Context(), p)))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(hub.Close)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); Relay(ctx, os.Getenv(platformtest.EnvDatabaseURL), hub, testkit.Log) }()
	t.Cleanup(func() { cancel(); <-done })

	probe := subscribe(t, srv, "cus-probe")
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
	return relayed{pool: pool, srv: srv}
}

func (r relayed) inTx(t *testing.T, fn func(tx pgx.Tx) error) {
	t.Helper()
	if err := platform.WithTx(t.Context(), r.pool, fn); err != nil {
		t.Fatal(err)
	}
}

func note(key string) notices.Notice {
	return notices.Notice{
		Key: key, Title: contracts.Msg{Key: "srv.api.notif.confirmed.title"}, Body: contracts.Msg{Key: "srv.api.notif.confirmed.body"},
	}
}

func TestANoticeIsAnnouncedOnceAndOnlyToItsCustomer(t *testing.T) {
	r := startRelay(t)
	dina, raka := subscribe(t, r.srv, "cus-dina"), subscribe(t, r.srv, "cus-raka")

	r.inTx(t, func(tx pgx.Tx) error { return Notice(t.Context(), tx, "cus-dina", note("event-1")) })
	if ev, ok := next(dina, 5*time.Second); !ok || ev != (platform.Event{Type: "notifications.changed"}) {
		t.Fatalf("after the notice: %+v, %v", ev, ok)
	}
	list, err := notices.List(t.Context(), r.pool, notices.Customer("cus-dina"))
	if err != nil || len(list) != 1 || list[0].Audience != "customer:cus-dina" {
		t.Fatalf("stored = %+v, %v", list, err)
	}

	// The same event delivered again writes nothing and says nothing.
	r.inTx(t, func(tx pgx.Tx) error { return Notice(t.Context(), tx, "cus-dina", note("event-1")) })
	if ev, ok := next(dina, 300*time.Millisecond); ok {
		t.Errorf("a duplicate was announced: %+v", ev)
	}
	if again, _ := notices.List(t.Context(), r.pool, notices.Customer("cus-dina")); len(again) != 1 {
		t.Errorf("%d notifications after the duplicate", len(again))
	}
	if ev, ok := next(raka, 200*time.Millisecond); ok {
		t.Errorf("another customer heard %+v", ev)
	}
}

func TestSettingsChangesReachEveryOpenDashboard(t *testing.T) {
	r := startRelay(t)
	dina, raka := subscribe(t, r.srv, "cus-dina"), subscribe(t, r.srv, "cus-raka")

	r.inTx(t, func(tx pgx.Tx) error { return SettingsChanged(t.Context(), tx) })
	for name, events := range map[string]<-chan platform.Event{"Dina": dina, "Raka": raka} {
		if ev, ok := rawNext(events, 5*time.Second); !ok || ev != (platform.Event{Type: "settings.changed"}) {
			t.Errorf("%s got %+v, %v", name, ev, ok)
		}
	}
}

func TestMarkingNotificationsReadIsAnnouncedToTheirCustomerSoOtherTabsRefresh(t *testing.T) {
	r := startRelay(t)
	dina, raka := subscribe(t, r.srv, "cus-dina"), subscribe(t, r.srv, "cus-raka")
	r.inTx(t, func(tx pgx.Tx) error {
		if err := Notice(t.Context(), tx, "cus-dina", note("a")); err != nil {
			return err
		}
		return Notice(t.Context(), tx, "cus-dina", note("b"))
	})
	if _, ok := next(dina, 5*time.Second); !ok {
		t.Fatal("no announcement for the new notices")
	}

	db := ReadAnnouncer{Pool: r.pool}
	if err := notices.MarkRead(t.Context(), db, notices.Customer("cus-dina"), nil); err != nil {
		t.Fatal(err)
	}
	if ev, ok := next(dina, 5*time.Second); !ok || ev != (platform.Event{Type: "notifications.changed"}) {
		t.Fatalf("after marking read: %+v, %v", ev, ok)
	}
	list, _ := notices.List(t.Context(), r.pool, notices.Customer("cus-dina"))
	if len(list) != 2 || !list[0].Read || !list[1].Read {
		t.Fatalf("notifications = %+v, want both read", list)
	}
	// Nothing was left to mark: nothing to tell.
	if err := notices.MarkRead(t.Context(), db, notices.Customer("cus-dina"), nil); err != nil {
		t.Fatal(err)
	}
	if ev, ok := next(dina, 300*time.Millisecond); ok {
		t.Errorf("marking nothing was announced: %+v", ev)
	}
	if ev, ok := next(raka, 200*time.Millisecond); ok {
		t.Errorf("another customer heard %+v", ev)
	}
}
