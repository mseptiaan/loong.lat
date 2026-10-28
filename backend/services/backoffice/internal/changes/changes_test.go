package changes

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// stream opens the hub's event stream as a principal and returns the data lines it receives.
func stream(t *testing.T, hub *platform.Hub, p platform.Principal) <-chan string {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.Handler().ServeHTTP(w, r.WithContext(platform.WithPrincipal(r.Context(), p)))
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				lines <- data
			}
		}
	}()
	return lines
}

func TestHintsAreDelayedMergedAndSentToAdminsOnly(t *testing.T) {
	hub := platform.NewHub()
	t.Cleanup(hub.Close)
	admin := stream(t, hub, platform.Principal{Role: contracts.RoleAdmin, UserID: "adm-1"})
	customer := stream(t, hub, platform.Principal{Role: contracts.RoleCustomer, UserID: "cus-1"})
	time.Sleep(100 * time.Millisecond) // both streams subscribed

	feed := New(hub, 150*time.Millisecond)
	start := time.Now()
	feed.Plan("pln-1")
	feed.Plan("pln-1")
	feed.Plan("pln-2")
	feed.Fleet()
	feed.Fleet()
	feed.Settings()
	feed.Plans()
	feed.Users()
	feed.Notifications()
	feed.Notifications()
	feed.Wallet()

	got := map[string]bool{}
	for range 8 {
		select {
		case data := <-admin:
			got[data] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("admin received only %v", got)
		}
	}
	if waited := time.Since(start); waited < 140*time.Millisecond {
		t.Errorf("first hint arrived after %v, want it held for the delay", waited)
	}
	for _, want := range []string{
		`{"type":"plans.changed","planId":"pln-1"}`, `{"type":"plans.changed","planId":"pln-2"}`, `{"type":"plans.changed"}`,
		`{"type":"fleet.changed"}`, `{"type":"settings.changed"}`,
		`{"type":"users.changed"}`, `{"type":"notifications.changed"}`, `{"type":"wallet.changed"}`,
	} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	select {
	case extra := <-admin:
		t.Errorf("an equal hint was sent twice: %s", extra)
	case <-customer:
		t.Error("a customer received an admin hint")
	case <-time.After(300 * time.Millisecond):
	}

	// after a flush the next hint starts a new delay
	feed.Fleet()
	select {
	case data := <-admin:
		if data != `{"type":"fleet.changed"}` {
			t.Errorf("got %s", data)
		}
	case <-time.After(3 * time.Second):
		t.Error("no hint after the first batch")
	}
}
