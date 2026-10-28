// Package app assembles the optimizer service so that both cmd/optimizer and the
// end-to-end tests can start it: configuration, wiring, and a graceful lifecycle.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/api"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/quote"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/roads"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/version"
)

// Config is everything the service is told. ConfigFromEnv reads it from the
// environment; see services/optimizer/.env.example.
type Config struct {
	// HTTPAddr is the listen address; ":0" picks a free port.
	HTTPAddr string
	// Token is the bearer token callers present. Empty is allowed only with Dev.
	Token string
	// Dev lets the service run without a token, for a laptop and tests.
	Dev bool
	// Runner is where jobs run: "local" (in process), "exec" (the solver binary) or "modal".
	Runner    string
	SolverBin string
	// Fallback is "none" to give up when the sandbox is down; otherwise jobs fall back to the local runner.
	Fallback string
	// Concurrency bounds jobs at once; zero takes a default for the runner.
	Concurrency int
	// Timeout bounds a quick quote, DeepTimeout a thorough or reviewed one (several jobs, and review rounds).
	Timeout     time.Duration
	DeepTimeout time.Duration
	// Rounds caps the review rounds of a reviewed quote.
	Rounds int
	// AIURL is the AI gateway, which lets the model review a reviewed quote; empty means the built-in rules review.
	// AIToken is the bearer token the gateway expects from the optimizer, which is its own, not another caller's.
	AIURL   string
	AIToken string
	// MapboxToken enables measured road distances and drawn routes; without it distances are estimates.
	MapboxToken string
	RoadTimeout time.Duration
	RoadTTL     time.Duration
	// RoadAllowTolls lets routes use toll roads. Off by default: our vehicles may not, and Mapbox's Matrix API cannot
	// leave tolls out, so the distances are then measured with the Directions API (see internal/roads).
	RoadAllowTolls bool
	// ExactMax and Iterations tune the solver; zero takes its defaults.
	ExactMax   int
	Iterations int
	Modal      runner.ModalConfig
}

// ConfigFromEnv reads the configuration from env. Problems are collected in env.Err.
func ConfigFromEnv(env *platform.Env) Config {
	return Config{
		HTTPAddr:       env.String("OPTIMIZER_ADDR", ":8090"),
		Token:          env.String("OPTIMIZER_TOKEN", ""),
		Dev:            env.Bool("OPTIMIZER_DEV", false),
		Runner:         env.String("RUNNER", "local"),
		SolverBin:      env.String("SOLVER_BIN", "solver"),
		Fallback:       env.String("OPTIMIZER_FALLBACK", ""),
		Concurrency:    env.Int("OPTIMIZER_CONCURRENCY", 0),
		Timeout:        env.Duration("OPTIMIZER_TIMEOUT", 45*time.Second),
		DeepTimeout:    env.Duration("OPTIMIZER_TIMEOUT_DEEP", 150*time.Second),
		Rounds:         env.Int("OPTIMIZER_ROUNDS", 3),
		AIURL:          env.String("AI_URL", ""),
		AIToken:        env.String("AI_TOKEN", ""),
		MapboxToken:    env.String("MAPBOX_TOKEN", ""),
		RoadTimeout:    env.Duration("ROAD_TIMEOUT", 8*time.Second),
		RoadTTL:        env.Duration("ROAD_CACHE_TTL", 24*time.Hour),
		RoadAllowTolls: env.Bool("ROAD_ALLOW_TOLLS", false),
		ExactMax:       env.Int("OPTIMIZER_EXACT_MAX", 0),
		Iterations:     env.Int("OPTIMIZER_ITERATIONS", 0),
		Modal:          ModalFromEnv(env),
	}
}

// ModalFromEnv reads the Modal settings; cmd/modalprobe and cmd/modalpublish share them with the service.
func ModalFromEnv(env *platform.Env) runner.ModalConfig {
	image := env.String("MODAL_IMAGE", "")
	volume := env.String("MODAL_VOLUME", "")
	if image == "" && volume == "" {
		volume = "loong-solver"
	}
	return runner.ModalConfig{
		App:            env.String("MODAL_APP", "loong-optimizer"),
		Environment:    env.String("MODAL_ENVIRONMENT", ""),
		Volume:         volume,
		SolverVersion:  env.String("MODAL_SOLVER_VERSION", ""),
		Image:          image,
		RegistrySecret: env.String("MODAL_REGISTRY_SECRET", ""),
		CPU:            float64(env.Int("MODAL_CPU", 2)),
		MemoryMiB:      env.Int("MODAL_MEMORY_MIB", 1024),
		Timeout:        env.Duration("MODAL_TIMEOUT", 90*time.Second),
		Regions:        env.List("MODAL_REGIONS"),
		PoolSize:       env.Int("MODAL_POOL_SIZE", 2),
		IdleTimeout:    env.Duration("MODAL_IDLE_TIMEOUT", 5*time.Minute),
		Prewarm:        env.Int("MODAL_PREWARM", 1),
		TokenID:        env.String("MODAL_TOKEN_ID", ""),
		TokenSecret:    env.String("MODAL_TOKEN_SECRET", ""),
	}
}

// App is a running optimizer service.
type App struct {
	srv  *platform.Server
	done chan error
}

// Addr is the address the service listens on.
func (a *App) Addr() string { return a.srv.Addr() }

// Wait blocks until the context given to Start is cancelled and the service has stopped.
func (a *App) Wait() error { return <-a.done }

// Start brings the service up and returns once it is listening.
func Start(ctx context.Context, cfg Config, log *slog.Logger) (*App, error) {
	if cfg.Token == "" && !cfg.Dev {
		return nil, fmt.Errorf("OPTIMIZER_TOKEN is required (set OPTIMIZER_DEV=true to run without it locally)")
	}
	run, closeRunner, err := buildRunner(ctx, cfg, log)
	if err != nil {
		return nil, err
	}

	var provider roads.Provider = roads.Estimate{}
	var router roads.Router
	if cfg.MapboxToken != "" {
		mb := &roads.Mapbox{Token: cfg.MapboxToken, Cache: roads.NewMemoryCache(200_000, cfg.RoadTTL), AllowTolls: cfg.RoadAllowTolls, Log: log}
		if cfg.RoadAllowTolls {
			log.Warn("ROAD_ALLOW_TOLLS is on: routes may use toll roads")
		}
		provider, router = mb, mb
	} else {
		log.Warn("MAPBOX_TOKEN is not set: distances are straight-line estimates and routes are not drawn on roads")
	}

	planner := &quote.Planner{
		Roads: provider, Runner: run, Router: router, Log: log,
		RoadTimeout: cfg.RoadTimeout, GeometryTimeout: 4 * time.Second, Rounds: cfg.Rounds,
		Limits: job.Limits{ExactMaxOrders: cfg.ExactMax, Iterations: cfg.Iterations},
	}
	if cfg.AIURL != "" {
		planner.AI = &aiapi.Client{BaseURL: cfg.AIURL, Token: cfg.AIToken}
	} else {
		log.Info("AI_URL is not set: reviewed quotes are reviewed by the built-in rules")
	}
	handler := &api.Handler{
		Planner: planner, Token: cfg.Token, Timeout: cfg.Timeout, DeepTimeout: cfg.DeepTimeout, Log: log,
		Info: optimizerapi.Info{Engine: version.Engine, Runner: cfg.Runner, Efforts: contracts.Efforts, AIReview: planner.AI != nil},
	}

	health := platform.NewHealth(log)
	mux := http.NewServeMux()
	health.Mount(mux)
	mux.Handle("/", handler.Routes())

	srv, err := platform.NewServer(platform.ServerConfig{
		Addr: cfg.HTTPAddr, Log: log, Health: health,
		Handler: platform.Chain(mux, platform.AccessLog(log), platform.Recover(log)),
	})
	if err != nil {
		return nil, err
	}
	log.Info("optimizer ready", "engine", version.Engine, "runner", cfg.Runner, "roads", fmt.Sprintf("%T", provider), "aiReview", planner.AI != nil, "addr", srv.Addr())

	a := &App{srv: srv, done: make(chan error, 1)}
	go func() {
		err := srv.Serve(ctx)
		closeRunner()
		a.done <- err
	}()
	return a, nil
}

// buildRunner picks where jobs run and returns what to call at shutdown. A sandbox that cannot be reached falls back
// to the local runner, so a Modal outage slows quotes down instead of stopping them.
func buildRunner(ctx context.Context, cfg Config, log *slog.Logger) (runner.Runner, func(), error) {
	noop := func() {}
	switch cfg.Runner {
	case "local":
		return runner.Limit(runner.Local{}, orDefault(cfg.Concurrency, 2)), noop, nil
	case "exec":
		return runner.Limit(runner.Exec{Path: cfg.SolverBin}, orDefault(cfg.Concurrency, 2)), noop, nil
	case "modal":
		m, err := runner.NewModal(ctx, cfg.Modal, log)
		if err != nil {
			return nil, nil, fmt.Errorf("modal runner: %w", err)
		}
		// Each warm sandbox serves one job at a time, so more concurrent jobs than sandboxes would only queue.
		limit := orDefault(cfg.Concurrency, max(cfg.Modal.PoolSize, 4))
		primary := runner.Limit(m, limit)
		if cfg.Fallback == "none" {
			return primary, m.Close, nil
		}
		return runner.Fallback{Primary: primary, Backup: runner.Limit(runner.Local{}, 2)}, m.Close, nil
	}
	return nil, nil, fmt.Errorf("RUNNER %q is not local, exec or modal", cfg.Runner)
}

func orDefault(n, def int) int {
	if n > 0 {
		return n
	}
	return def
}
