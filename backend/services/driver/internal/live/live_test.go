package live

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
)

// subscriber is an open SSE stream of one driver; next returns the events as they arrive.
type subscriber struct {
	events chan string
}

func openStream(t *testing.T, hub *platform.Hub, driverID string) subscriber {
	t.Helper()
	sessions := platform.NewSessions(platform.NewMemorySessionStore(), time.Hour, drivertest.Logger())
	token, err := sessions.Start(t.Context(), platform.Principal{Role: contracts.RoleDriver, UserID: driverID, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(sessions.Authenticate(hub.Handler()))
	t.Cleanup(srv.Close)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	events := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				events <- data
			}
		}
	}()
	// the stream is open once its retry hint is flushed; the first Publish after that is not lost
	time.Sleep(100 * time.Millisecond)
	return subscriber{events}
}

func (s subscriber) expect(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-s.events:
		if got != want {
			t.Errorf("event = %s, want %s", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no event, want %s", want)
	}
}

func (s subscriber) expectNothing(t *testing.T) {
	t.Helper()
	select {
	case got := <-s.events:
		t.Errorf("unexpected event %s", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// uniqueDriver is an id no other test shares: NOTIFY channels belong to the whole database, which tests of
// other packages use at the same time, so a stream must only listen for its own driver.
func uniqueDriver(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("drv-%d", time.Now().UnixNano())
}

func TestHintsReachTheDriversStreamOnlyAfterTheTransactionCommits(t *testing.T) {
	pool := drivertest.Pool(t)
	hub := platform.NewHub()
	t.Cleanup(hub.Close)
	run, err := Listen(t.Context(), pool, hub, drivertest.Logger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	t.Cleanup(func() {
		stop()
		<-done
	})

	driver1 := uniqueDriver(t)
	driver2 := driver1 + "-other"
	mine, other := openStream(t, hub, driver1), openStream(t, hub, driver2)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ToDriver(ctx, tx, driver1, PlansChanged("pln-1")); err != nil {
		t.Fatal(err)
	}
	mine.expectNothing(t) // not before the commit: a refetch now would still see the old data
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	mine.expect(t, `{"type":"plans.changed","planId":"pln-1"}`)
	other.expectNothing(t)

	if err := platform.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return ToDriver(ctx, tx, driver2, FleetChanged)
	}); err != nil {
		t.Fatal(err)
	}
	other.expect(t, `{"type":"fleet.changed"}`)
	mine.expectNothing(t)
}

func TestARolledBackTransactionSendsNothing(t *testing.T) {
	pool := drivertest.Pool(t)
	hub := platform.NewHub()
	t.Cleanup(hub.Close)
	run, err := Listen(t.Context(), pool, hub, drivertest.Logger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	t.Cleanup(func() {
		stop()
		<-done
	})
	driver := uniqueDriver(t)
	mine := openStream(t, hub, driver)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ToDriver(ctx, tx, driver, PlansChanged("pln-1")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	mine.expectNothing(t)

	// nobody is not a driver: no notification at all
	if err := ToDriver(ctx, pool, "", PlansChanged("pln-1")); err != nil {
		t.Fatal(err)
	}
	mine.expectNothing(t)
}
