package platform

import (
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Health answers the two probes every service exposes: /healthz (the process is up, never touches a
// dependency) and /readyz (dependencies are reachable and the process is not shutting down).
type Health struct {
	log      *slog.Logger
	mu       sync.Mutex
	checks   []healthCheck
	draining atomic.Bool
}

type healthCheck struct {
	name string
	fn   func(context.Context) error
}

func NewHealth(log *slog.Logger) *Health { return &Health{log: log} }

// Check adds a readiness check, e.g. h.Check("postgres", pool.Ping).
func (h *Health) Check(name string, fn func(context.Context) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks = append(h.checks, healthCheck{name, fn})
}

// SetDraining makes /readyz fail so a load balancer stops sending traffic; Server.Serve calls it on shutdown.
func (h *Health) SetDraining() { h.draining.Store(true) }

const readyzTimeout = 2 * time.Second

// Mount registers GET /healthz and GET /readyz on mux.
func (h *Health) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", h.readyz)
}

func (h *Health) readyz(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "draining"})
		return
	}
	h.mu.Lock()
	checks := append([]healthCheck(nil), h.checks...)
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), readyzTimeout)
	defer cancel()
	results := make(map[string]string, len(checks))
	status, code := "ready", http.StatusOK
	for _, c := range checks {
		if err := c.fn(ctx); err != nil {
			// Details stay in the log: the probe answer is not for end users but may still be reachable.
			h.log.WarnContext(r.Context(), "readiness check failed", "check", c.name, "error", err)
			results[c.name] = "failed"
			status, code = "unavailable", http.StatusServiceUnavailable
			continue
		}
		results[c.name] = "ok"
	}
	writeJSON(w, code, map[string]any{"status": status, "checks": results})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ServerConfig configures NewServer.
type ServerConfig struct {
	// Addr is the listen address, e.g. ":8081" (":0" picks a free port).
	Addr    string
	Handler http.Handler
	Log     *slog.Logger
	// Health, when set, is told to drain at shutdown.
	Health *Health
	// ShutdownTimeout bounds how long in-flight requests get to finish; 15s when zero.
	ShutdownTimeout time.Duration
}

// Server is an HTTP server with graceful shutdown.
type Server struct {
	http     *http.Server
	listener net.Listener
	log      *slog.Logger
	health   *Health
	timeout  time.Duration
}

// NewServer binds the listen address immediately, so a port clash fails at startup, not on first request.
// There is no write timeout: streaming responses (NDJSON, SSE) are long-lived by design.
func NewServer(cfg ServerConfig) (*Server, error) {
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, err
	}
	timeout := cfg.ShutdownTimeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &Server{
		http: &http.Server{
			Handler:           cfg.Handler,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       2 * time.Minute,
		},
		listener: ln, log: cfg.Log, health: cfg.Health, timeout: timeout,
	}, nil
}

// Addr is the address the server listens on.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// OnShutdown registers fn to run when shutdown begins, e.g. Hub.Close so open SSE streams end.
func (s *Server) OnShutdown(fn func()) { s.http.RegisterOnShutdown(fn) }

// Serve handles requests until ctx is cancelled, then drains: /readyz starts failing, no new connections are
// accepted, and in-flight requests get ShutdownTimeout to finish.
func (s *Server) Serve(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() { errc <- s.http.Serve(s.listener) }()
	s.log.InfoContext(ctx, "http server listening", "addr", s.Addr())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	s.log.Info("http server shutting down", "timeout", s.timeout)
	if s.health != nil {
		s.health.SetDraining()
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.timeout)
	defer cancel()
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		_ = s.http.Close()
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
