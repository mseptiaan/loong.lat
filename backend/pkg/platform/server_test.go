package platform

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return resp.StatusCode, v
}

func TestHealthEndpoints(t *testing.T) {
	var logged strings.Builder
	health := NewHealth(NewLogger(&logged, "test", 0))
	var dbErr error
	health.Check("postgres", func(context.Context) error { return dbErr })
	health.Check("kafka", func(context.Context) error { return nil })
	mux := http.NewServeMux()
	health.Mount(mux)
	srv, err := NewServer(ServerConfig{Addr: "127.0.0.1:0", Handler: mux, Log: discard, Health: health})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	base := "http://" + srv.Addr()

	if status, body := getJSON(t, base+"/healthz"); status != 200 || body["status"] != "ok" {
		t.Errorf("healthz = %d %v", status, body)
	}
	status, body := getJSON(t, base+"/readyz")
	if status != 200 || body["status"] != "ready" || body["checks"].(map[string]any)["postgres"] != "ok" {
		t.Errorf("readyz = %d %v", status, body)
	}

	dbErr = errors.New("dial tcp 10.1.2.3:5432: connection refused")
	status, body = getJSON(t, base+"/readyz")
	checks := body["checks"].(map[string]any)
	if status != 503 || body["status"] != "unavailable" || checks["postgres"] != "failed" || checks["kafka"] != "ok" {
		t.Errorf("readyz with a failing dependency = %d %v", status, body)
	}
	if strings.Contains(body["status"].(string), "10.1.2.3") || strings.Contains(toJSON(t, body), "10.1.2.3") {
		t.Error("the probe must not leak the dependency's error")
	}
	if !strings.Contains(logged.String(), "10.1.2.3") {
		t.Error("the failure belongs in the log")
	}

	// /healthz keeps answering while a dependency is down: restarting the process would not fix it.
	if status, _ := getJSON(t, base+"/healthz"); status != 200 {
		t.Errorf("healthz with a failing dependency = %d", status)
	}

	dbErr = nil
	health.SetDraining()
	if status, body := getJSON(t, base+"/readyz"); status != 503 || body["status"] != "draining" {
		t.Errorf("readyz while draining = %d %v", status, body)
	}
	cancel()
	<-done
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestServeShutsDownGracefully(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-finish
		_, _ = w.Write([]byte("finished"))
	})
	health := NewHealth(discard)
	srv, err := NewServer(ServerConfig{Addr: "127.0.0.1:0", Handler: mux, Log: discard, Health: health, ShutdownTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	shutdownHook := make(chan struct{})
	srv.OnShutdown(func() { close(shutdownHook) })

	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()

	type result struct {
		body string
		err  error
	}
	response := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + srv.Addr() + "/slow")
		if err != nil {
			response <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		response <- result{string(b), err}
	}()
	<-started

	cancel() // SIGTERM
	select {
	case <-shutdownHook:
	case <-time.After(5 * time.Second):
		t.Fatal("OnShutdown hooks did not run")
	}
	select {
	case err := <-served:
		t.Fatalf("Serve returned (%v) while a request was still in flight", err)
	case <-time.After(150 * time.Millisecond):
	}

	if _, err := net.DialTimeout("tcp", srv.Addr(), time.Second); err == nil {
		t.Error("the listener must stop accepting new connections while draining")
	}

	close(finish)
	if r := <-response; r.err != nil || r.body != "finished" {
		t.Errorf("the in-flight request must complete: %q, %v", r.body, r.err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the request finished")
	}
	if !health.draining.Load() {
		t.Error("readiness must flip to draining during shutdown")
	}
}

func TestShutdownEndsOpenEventStreamsViaTheHub(t *testing.T) {
	sessions := NewSessions(NewMemorySessionStore(), time.Hour, discard)
	hub := NewHub()
	mux := http.NewServeMux()
	mux.Handle("GET /v1/events", hub.Handler())
	srv, err := NewServer(ServerConfig{Addr: "127.0.0.1:0", Handler: Chain(mux, sessions.Authenticate), Log: discard, ShutdownTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	srv.OnShutdown(hub.Close)
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()

	token, _ := sessions.Start(t.Context(), Principal{Role: contracts.RoleAdmin, UserID: "a1"})
	stream := openStream(t, "http://"+srv.Addr(), token)
	waitSubscribers(t, hub, 1)

	began := time.Now()
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a never-ending SSE stream blocked shutdown")
	}
	<-stream.closed
	if time.Since(began) > 3*time.Second {
		t.Errorf("shutdown took %v", time.Since(began))
	}
}

func TestNewServerFailsFastWhenThePortIsTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := NewServer(ServerConfig{Addr: ln.Addr().String(), Handler: http.NotFoundHandler(), Log: discard}); err == nil {
		t.Error("binding an address in use must fail at construction")
	}
}
