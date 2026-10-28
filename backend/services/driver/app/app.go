// Package app assembles driver-api so that both cmd/driver-api and the end-to-end tests can start it:
// configuration, wiring and a graceful lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/api"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/auth"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/earnings"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/execution"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/live"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/projections"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/settings"
	"github.com/mseptiaan/loong.lat/backend/services/driver/migrations"
)

const (
	serviceName = "driver-api"
	// A burst of sign-in attempts per phone number, then one every loginEvery.
	loginBurst = 5
	loginEvery = 15 * time.Second
)

// Config is everything the service is told. ConfigFromEnv reads it from the environment (see .env.example).
type Config struct {
	// HTTPAddr is the listen address; ":0" picks a free port.
	HTTPAddr string
	// DatabaseURL is this service's own database.
	DatabaseURL  string
	KafkaBrokers []string
	// GroupPrefix names the consumer groups "<GroupPrefix>.drivers", ".dispatch", ".fleet", ".plan" and
	// ".settings"; "driver" when empty.
	GroupPrefix string
	// SessionTTL is how long a sign-in lasts; 720h when zero.
	SessionTTL time.Duration
	// CORSOrigins are the web origins allowed to call this service from a browser; the driver app's own by default.
	CORSOrigins []string
	// AIURL is the AI gateway. This service builds no AI feature; it only reports the AI settings as off while no
	// gateway is configured (settings.get). Empty by default.
	AIURL string
	// DelaySweepInterval is how often running routes are checked for delays (route.delayed); 30s when zero.
	DelaySweepInterval time.Duration
}

// ConfigFromEnv reads the configuration from env; problems are collected in env.Err.
func ConfigFromEnv(env *platform.Env) Config {
	return Config{
		HTTPAddr:           env.String("HTTP_ADDR", ":8081"),
		DatabaseURL:        env.Require("DATABASE_URL"),
		KafkaBrokers:       env.List("KAFKA_BROKERS"),
		SessionTTL:         env.Duration("SESSION_TTL", 720*time.Hour),
		CORSOrigins:        env.List("CORS_ORIGINS", "http://driver.loong.localhost"),
		GroupPrefix:        env.String("GROUP_PREFIX", ""),
		AIURL:              env.String("AI_URL", ""),
		DelaySweepInterval: env.Duration("DELAY_SWEEP_INTERVAL", execution.DefaultSweepInterval),
	}
}

// App is a running driver-api.
type App struct {
	srv  *platform.Server
	done chan error
}

// Addr is the address the service listens on.
func (a *App) Addr() string { return a.srv.Addr() }

// Wait blocks until the context given to Start is cancelled and everything has stopped; it returns the first
// failure of a part that stopped on its own, if any.
func (a *App) Wait() error { return <-a.done }

// worker is one long-running part of the service.
type worker struct {
	name string
	run  func(context.Context) error
}

// Start migrates the database and brings the service up: the HTTP server, the outbox relay, the five
// consumers, the delay sweeper and the push of change hints. It returns once the listener is bound and they are running. If
// any part fails, the rest is stopped and Wait reports why.
func Start(ctx context.Context, cfg Config, log *slog.Logger) (*App, error) {
	if cfg.DatabaseURL == "" || len(cfg.KafkaBrokers) == 0 {
		return nil, errors.New("driver-api: DATABASE_URL and KAFKA_BROKERS are required")
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8081"
	}
	if cfg.GroupPrefix == "" {
		cfg.GroupPrefix = "driver"
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 720 * time.Hour
	}
	if cfg.DelaySweepInterval == 0 {
		cfg.DelaySweepInterval = execution.DefaultSweepInterval
	}

	pool, err := platform.NewPool(ctx, platform.PoolConfig{DSN: cfg.DatabaseURL, AppName: serviceName})
	if err != nil {
		return nil, err
	}
	own, err := migrations.All()
	if err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := platform.Migrate(ctx, pool, slices.Concat(platform.Migrations(), eventbus.Migrations(), notices.Migrations(), own)...); err != nil {
		pool.Close()
		return nil, err
	}
	producer, err := eventbus.NewProducerClient(cfg.KafkaBrokers, serviceName)
	if err != nil {
		pool.Close()
		return nil, err
	}

	workers, srv, err := assemble(ctx, cfg, log, pool, producer)
	if err != nil {
		producer.Close()
		pool.Close()
		return nil, err
	}

	a := &App{srv: srv, done: make(chan error, 1)}
	go func() { a.done <- supervise(ctx, log, workers, func() { producer.Close(); pool.Close() }) }()
	return a, nil
}

// assemble builds the workers on top of the pool and the producer, and the HTTP server among them. When a step
// fails, what was built before it is run once with a finished context, which releases it.
func assemble(ctx context.Context, cfg Config, log *slog.Logger, pool *pgxpool.Pool, producer *kgo.Client) (workers []worker, srv *platform.Server, err error) {
	defer func() {
		if err != nil {
			finished, cancel := context.WithCancel(ctx)
			cancel()
			_ = supervise(finished, log, workers, func() {})
		}
	}()

	pub := eventbus.NewPublisher(serviceName, pool, producer, log)
	projection := projections.New(log, pub)
	groups := []struct {
		suffix, topic string
		register      func(*eventbus.Consumer)
	}{
		{"drivers", projections.TopicDrivers, projection.RegisterDrivers},
		{"dispatch", projections.TopicDispatch, projection.RegisterDispatch},
		{"fleet", projections.TopicFleet, projection.RegisterFleet},
		{"plan", projections.TopicPlan, projection.RegisterPlan},
		{"settings", projections.TopicSettings, projection.RegisterSettings},
	}
	for _, g := range groups {
		c, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
			Brokers: cfg.KafkaBrokers, Group: cfg.GroupPrefix + "." + g.suffix, Topics: []string{g.topic}, DB: pool, Log: log,
		})
		if err != nil {
			return workers, nil, err
		}
		g.register(c)
		workers = append(workers, worker{"consumer " + g.suffix, c.Run})
	}

	workers = append(workers, worker{"outbox relay", pub.Run})
	workers = append(workers, worker{"delay sweeper", execution.NewSweeper(pool, pub, time.Now, log, cfg.DelaySweepInterval).Run})

	hub := platform.NewHub()
	sessions := platform.NewSessions(platform.NewPgSessionStore(pool), cfg.SessionTTL, log)
	rpc := platform.NewRPC(log)
	api.Mount(rpc, api.Services{
		Auth:      auth.NewService(pool, sessions, platform.NewLoginLimiter(loginEvery, loginBurst)),
		Drivers:   drivers.NewService(pool, pub, time.Now),
		Execution: execution.NewService(pool, pub, time.Now),
		Earnings:  earnings.NewService(pool, time.Now),
		Settings:  settings.NewService(pool, cfg.AIURL != ""),
		Notices:   pool,
	})
	health := platform.NewHealth(log)
	health.Check("postgres", pool.Ping)
	mux := http.NewServeMux()
	health.Mount(mux)
	rpc.Mount(mux)
	mux.Handle("GET /v1/events", hub.Handler())

	srv, err = platform.NewServer(platform.ServerConfig{
		Addr: cfg.HTTPAddr, Log: log, Health: health,
		Handler: platform.Chain(mux, platform.TraceMiddleware, platform.LocaleMiddleware, platform.AccessLog(log), platform.Recover(log), platform.CORS(cfg.CORSOrigins), auth.RememberToken, sessions.Authenticate),
	})
	if err != nil {
		return workers, nil, err
	}
	srv.OnShutdown(hub.Close)
	workers = append(workers, worker{"http", srv.Serve})

	listen, err := live.Listen(ctx, pool, hub, log)
	if err != nil {
		return workers, nil, err
	}
	return append(workers, worker{"change hints", listen}), srv, nil
}

// supervise runs the workers until ctx ends or one fails; a failure stops the others. It waits for all of them,
// then calls release, and returns the failures.
func supervise(ctx context.Context, log *slog.Logger, workers []worker, release func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, w := range workers {
		wg.Go(func() {
			if err := w.run(ctx); err != nil {
				log.ErrorContext(ctx, "service part failed", "part", w.name, "error", err)
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", w.name, err))
				mu.Unlock()
			}
			cancel()
		})
	}
	wg.Wait()
	release()
	return errors.Join(errs...)
}
