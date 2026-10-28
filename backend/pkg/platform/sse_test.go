package platform

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// sseClient reads the data lines of an open event stream.
type sseClient struct {
	events chan Event
	body   interface{ Close() error }
	closed chan struct{}
}

func openStream(t *testing.T, url, token string) *sseClient {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url+"/v1/events", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	c := &sseClient{events: make(chan Event, 64), body: resp.Body, closed: make(chan struct{})}
	t.Cleanup(func() { resp.Body.Close() })
	go func() {
		defer close(c.closed)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				var ev Event
				if err := json.Unmarshal([]byte(data), &ev); err == nil {
					c.events <- ev
				}
			}
		}
	}()
	return c
}

func (c *sseClient) expect(t *testing.T, want Event) {
	t.Helper()
	select {
	case got := <-c.events:
		if got != want {
			t.Errorf("event = %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("no event, want %+v", want)
	}
}

func (c *sseClient) expectNothing(t *testing.T) {
	t.Helper()
	select {
	case got := <-c.events:
		t.Errorf("unexpected event %+v", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func newHubServer(t *testing.T) (*Hub, *Sessions, *httptest.Server) {
	t.Helper()
	sessions := NewSessions(NewMemorySessionStore(), time.Hour, discard)
	hub := NewHub()
	mux := http.NewServeMux()
	mux.Handle("GET /v1/events", hub.Handler())
	srv := httptest.NewServer(Chain(mux, sessions.Authenticate))
	t.Cleanup(srv.Close)
	t.Cleanup(hub.Close)
	return hub, sessions, srv
}

func TestHubDeliversByAudience(t *testing.T) {
	hub, sessions, srv := newHubServer(t)
	token := func(role contracts.Role, id string) string {
		tok, err := sessions.Start(t.Context(), Principal{Role: role, UserID: id, Name: id})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	dina := openStream(t, srv.URL, token(contracts.RoleCustomer, "c1"))
	raka := openStream(t, srv.URL, token(contracts.RoleCustomer, "c2"))
	admin := openStream(t, srv.URL, token(contracts.RoleAdmin, "a1"))
	budi := openStream(t, srv.URL, token(contracts.RoleDriver, "d1"))
	waitSubscribers(t, hub, 4)

	hub.Publish(Event{Type: "plans.changed", PlanID: "p1"}, CustomerAudience("c1"), AudienceAdmin)
	dina.expect(t, Event{Type: "plans.changed", PlanID: "p1"})
	admin.expect(t, Event{Type: "plans.changed", PlanID: "p1"})
	raka.expectNothing(t)
	budi.expectNothing(t)

	hub.Publish(Event{Type: "wallet.changed"}, DriverAudience("d1"))
	budi.expect(t, Event{Type: "wallet.changed"})
	dina.expectNothing(t)

	hub.Publish(Event{Type: "settings.changed"}) // no audience: everyone
	for _, c := range []*sseClient{dina, raka, admin, budi} {
		c.expect(t, Event{Type: "settings.changed"})
	}
}

func waitSubscribers(t *testing.T, hub *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		hub.mu.Lock()
		got := len(hub.subs)
		hub.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d subscribers, want %d", got, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHubRequiresASession(t *testing.T) {
	_, _, srv := newHubServer(t)
	resp, err := http.Get(srv.URL + "/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous stream status = %d, want 401", resp.StatusCode)
	}
}

func TestHubCloseEndsStreamsAndRefusesNewOnes(t *testing.T) {
	hub, sessions, srv := newHubServer(t)
	tok, _ := sessions.Start(t.Context(), Principal{Role: contracts.RoleAdmin, UserID: "a1"})
	client := openStream(t, srv.URL, tok)
	waitSubscribers(t, hub, 1)

	hub.Close()
	hub.Close() // idempotent
	select {
	case <-client.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream stayed open after Close")
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("a closed hub must refuse new streams, got %d", resp.StatusCode)
	}
}

func TestHubUnsubscribesWhenTheClientLeaves(t *testing.T) {
	hub, sessions, srv := newHubServer(t)
	tok, _ := sessions.Start(t.Context(), Principal{Role: contracts.RoleAdmin, UserID: "a1"})

	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	waitSubscribers(t, hub, 1)
	cancel()
	resp.Body.Close()
	waitSubscribers(t, hub, 0)
}

func TestHubKeepsTheNewestEventsForASlowSubscriber(t *testing.T) {
	hub := NewHub()
	sub, unsubscribe, ok := hub.subscribe(AudienceAdmin)
	if !ok {
		t.Fatal("subscribe failed")
	}
	defer unsubscribe()

	total := subscriberBuffer + 10
	for i := range total {
		hub.Publish(Event{Type: "plans.changed", PlanID: string(rune('A' + i%26))})
	}
	hub.Publish(Event{Type: "last"})

	var got []Event
	for len(sub.events) > 0 {
		got = append(got, <-sub.events)
	}
	if len(got) != subscriberBuffer {
		t.Errorf("queued %d events, want the buffer size %d", len(got), subscriberBuffer)
	}
	if got[len(got)-1].Type != "last" {
		t.Errorf("the newest event must survive overflow, last = %+v", got[len(got)-1])
	}
}

func TestHubSendsKeepAlives(t *testing.T) {
	hub, sessions, srv := newHubServer(t)
	hub.keepAlive = 20 * time.Millisecond
	tok, _ := sessions.Start(t.Context(), Principal{Role: contracts.RoleAdmin, UserID: "a1"})

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	deadline := time.After(5 * time.Second)
	found := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
			if scanner.Text() == ": ping" {
				found <- true
				return
			}
		}
		found <- false
	}()
	select {
	case ok := <-found:
		if !ok {
			t.Error("stream ended without a keep-alive comment")
		}
	case <-deadline:
		t.Error("no keep-alive comment")
	}
}

func TestAudienceOf(t *testing.T) {
	tests := []struct {
		p    Principal
		want Audience
	}{
		{Principal{Role: contracts.RoleCustomer, UserID: "c1"}, "customer:c1"},
		{Principal{Role: contracts.RoleDriver, UserID: "d1"}, "driver:d1"},
		{Principal{Role: contracts.RoleAdmin, UserID: "a1"}, "admin"},
	}
	for _, tt := range tests {
		if got := AudienceOf(tt.p); got != tt.want {
			t.Errorf("AudienceOf(%+v) = %q, want %q", tt.p, got, tt.want)
		}
	}
}
