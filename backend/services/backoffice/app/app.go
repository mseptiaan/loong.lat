// Package app assembles backoffice-api so that cmd/backoffice-api and the end-to-end tests start it the same
// way: configuration, wiring of the database, Kafka, HTTP and the dispatch worker, and a graceful lifecycle.
package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/ai"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/alerts"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/auth"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/dispatch"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/finance"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/settings"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/users"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/migrations"
)

const serviceName = "backoffice-api"

// Config is everything the service is told. ConfigFromEnv reads it from the environment (see .env.example).
type Config struct {
	// HTTPAddr is the listen address; ":0" picks a free port. Empty means ":8082".
	HTTPAddr string
	// DatabaseURL is this service's own database.
	DatabaseURL  string
	KafkaBrokers []string
	// GroupPrefix names the consumer groups "<GroupPrefix>.plans", ".drivers", ".positions", ".execution",
	// ".customers" and ".wallet". Empty means "backoffice".
	GroupPrefix string
	// SessionTTL is how long a sign-in lasts; empty means 720h.
	SessionTTL time.Duration

	// AdminEmail, AdminPassword and AdminName describe the admin account created at start when none has that
	// email. Leave email and password empty to create none.
	AdminEmail    string
	AdminPassword string
	AdminName     string

	// DispatchWorker runs the automatic dispatch worker inside this process.
	DispatchWorker bool
	// CORSOrigins are the web origins allowed to call this service from a browser; the back office app's own by default.
	CORSOrigins []string

	// SweepInterval is how often the worker looks for waiting routes besides when something wakes it. Empty means
	// 5s.
	SweepInterval time.Duration
	// SeedDemo fills an empty fleet with demo vehicles and drivers (see fleet.Service.SeedDemo). Demo only.
	SeedDemo bool
	// Now is the clock of the dispatch rules (who is silent, which routes depart soon) and of the finance
	// figures. Empty means time.Now; tests set one they can move.
	Now func() time.Time

	// AIURL is the address of the AI gateway (services/ai) and AIToken the bearer secret it expects. Empty AIURL
	// means no gateway: ai.copilot and ai.insights then answer "this feature is having problems" without calling
	// anything. The admin's own switches stay as the admin set them (settings.get shows them unchanged).
	AIURL   string
	AIToken string
	// AIRatePerMinute and AIRateBurst bound how many AI requests (a Copilot question, an insights run) one admin
	// may make: a burst, then a number a minute. Empty takes the defaults (10 and 20).
	AIRatePerMinute int
	AIRateBurst     int
}

// ConfigFromEnv reads the configuration from env. Problems are collected in env.Err.
func ConfigFromEnv(env *platform.Env) Config {
	brokers := env.List("KAFKA_BROKERS")
	if len(brokers) == 0 {
		env.Require("KAFKA_BROKERS") // records the error
	}
	return Config{
		HTTPAddr:       env.String("HTTP_ADDR", ":8082"),
		CORSOrigins:    env.List("CORS_ORIGINS", "http://backoffice.loong.localhost"),
		DatabaseURL:    env.Require("DATABASE_URL"),
		KafkaBrokers:   brokers,
		GroupPrefix:    env.String("GROUP_PREFIX", "backoffice"),
		SessionTTL:     env.Duration("SESSION_TTL", 720*time.Hour),
		AdminEmail:     env.String("ADMIN_EMAIL", ""),
		AdminPassword:  env.String("ADMIN_PASSWORD", ""),
		AdminName:      env.String("ADMIN_NAME", "Admin"),
		DispatchWorker: env.Bool("DISPATCH_WORKER", true),
		SeedDemo:       env.Bool("SEED_DEMO", false),

		AIURL:           env.String("AI_URL", ""),
		AIToken:         env.String("AI_TOKEN", ""),
		AIRatePerMinute: env.Int("AI_USER_RATE_PER_MINUTE", ai.DefaultRatePerMinute),
		AIRateBurst:     env.Int("AI_USER_RATE_BURST", ai.DefaultBurst),
	}
}

// normalize fills defaults and rejects a configuration the service cannot start with.
func (c Config) normalize() (Config, error) {
	switch {
	case c.DatabaseURL == "":
		return c, errors.New("app: DatabaseURL is required")
	case len(c.KafkaBrokers) == 0:
		return c, errors.New("app: KafkaBrokers is required")
	case (c.AdminEmail == "") != (c.AdminPassword == ""):
		return c, errors.New("app: AdminEmail and AdminPassword must be set together")
	}
	c.HTTPAddr = cmp.Or(c.HTTPAddr, ":8082")
	c.GroupPrefix = cmp.Or(c.GroupPrefix, "backoffice")
	c.AdminName = cmp.Or(c.AdminName, "Admin")
	if c.SessionTTL == 0 {
		c.SessionTTL = 720 * time.Hour
	}
	if c.SweepInterval == 0 {
		c.SweepInterval = 5 * time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c, nil
}

// App is a running backoffice-api.
type App struct {
	addr string
	done chan struct{}
	err  error
}

// Addr is the address the HTTP server listens on.
func (a *App) Addr() string { return a.addr }

// Wait blocks until the context given to Start is cancelled and everything has stopped, and returns the first
// failure of a component, if one made the service stop early.
func (a *App) Wait() error {
	<-a.done
	return a.err
}

// cleanups releases resources in reverse order of acquisition.
type cleanups []func()

func (c *cleanups) add(fn func()) { *c = append(*c, fn) }

func (c cleanups) run() {
	for _, fn := range slices.Backward(c) {
		fn()
	}
}

// Start connects, migrates and seeds the database, starts the outbox relay, the consumers, the dispatch worker
// and the HTTP server, and returns once the listener is bound. Cancel ctx to stop it and call Wait to wait for
// the shutdown.
func Start(ctx context.Context, cfg Config, log *slog.Logger) (*App, error) {
	cfg, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	var cleanup cleanups
	var running []*eventbus.Consumer
	started := false
	defer func() {
		if started {
			return
		}
		// A consumer that never ran is released by running it on a context that is already done.
		for _, c := range running {
			_ = c.Run(cancelled())
		}
		cleanup.run()
	}()

	pool, err := platform.NewPool(ctx, platform.PoolConfig{DSN: cfg.DatabaseURL, AppName: serviceName})
	if err != nil {
		return nil, err
	}
	cleanup.add(pool.Close)

	own, err := migrations.All()
	if err != nil {
		return nil, err
	}
	applied, err := platform.Migrate(ctx, pool, slices.Concat(platform.Migrations(), eventbus.Migrations(), notices.Migrations(), own)...)
	if err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "database migrated", "applied", len(applied))

	producer, err := eventbus.NewProducerClient(cfg.KafkaBrokers, serviceName)
	if err != nil {
		return nil, err
	}
	cleanup.add(producer.Close)
	publisher := eventbus.NewPublisher(serviceName, pool, producer, log)

	hub := platform.NewHub()
	feed := changes.New(hub, changes.DefaultDelay)
	sessions := platform.NewSessions(platform.NewPgSessionStore(pool), cfg.SessionTTL, log)

	authSvc, err := auth.New(pool, sessions)
	if err != nil {
		return nil, err
	}
	fleetSvc := fleet.New(pool, publisher, feed, cfg.Now, dispatch.CommitLead)
	dispatchSvc := dispatch.New(pool, publisher, fleetSvc, feed, log, cfg.Now)
	settingsSvc := settings.New(pool, publisher, feed, dispatchSvc.Kick)
	plansSvc := plans.NewService(pool, publisher, feed, cfg.Now)
	projection := plans.NewProjection(feed, publisher)
	usersSvc := users.New(pool, publisher, feed, plansSvc)
	financeSvc := finance.New(pool, feed, cfg.Now)

	if cfg.AdminEmail != "" {
		if err := authSvc.SeedAdmin(ctx, cfg.AdminEmail, cfg.AdminPassword, cfg.AdminName); err != nil {
			return nil, err
		}
	}
	if cfg.SeedDemo {
		if err := fleetSvc.SeedDemo(ctx); err != nil {
			return nil, err
		}
	}

	// kicking wakes the dispatch worker after a handler that may have made a route assignable.
	kicking := func(h eventbus.Handler) eventbus.Handler {
		return func(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
			if err := h(ctx, tx, env); err != nil {
				return err
			}
			dispatchSvc.Kick()
			return nil
		}
	}
	consumers := []struct {
		purpose  string
		topic    string
		handlers map[string]eventbus.Handler
	}{
		// plan.cancel.asked and the votes (accepted) are for console-api; a rejection without a route id is its
		// answer to an admin's request.
		{"plans", contracts.TopicPlan, map[string]eventbus.Handler{
			contracts.TypePlanConfirmed:       kicking(projection.PlanConfirmed),
			contracts.TypePlanCancelRequested: projection.CancelRequested,
			contracts.TypePlanCancelRejected:  projection.CancelRejected,
			contracts.TypePlanCancelAborted:   kicking(projection.CancelAborted),
			contracts.TypePlanCancelled:       kicking(projection.CancelCommitted),
			contracts.TypePlanRescheduled:     kicking(projection.Rescheduled),
		}},
		{"drivers", contracts.TopicDriver, map[string]eventbus.Handler{
			contracts.TypeDriverOnline:  kicking(fleetSvc.Online),
			contracts.TypeDriverOffline: kicking(dispatchSvc.DriverOffline),
		}},
		{"positions", contracts.TopicDriverPosition, map[string]eventbus.Handler{
			contracts.TypeDriverPosition: fleetSvc.Position,
		}},
		{"execution", contracts.TopicExecution, map[string]eventbus.Handler{
			contracts.TypeRouteStarted:   projection.RouteStarted,
			contracts.TypeStopArrived:    projection.StopArrived,
			contracts.TypeStopCompleted:  projection.StopCompleted,
			contracts.TypeStopIssue:      projection.StopIssue,
			contracts.TypeRouteCompleted: projection.RouteCompleted,
			contracts.TypeRouteDelayed:   projection.RouteDelayed,
		}},
		{"customers", contracts.TopicCustomer, map[string]eventbus.Handler{
			contracts.TypeCustomerRegistered: projection.CustomerRegistered,
			contracts.TypeCustomerUpdated:    projection.CustomerUpdated,
		}},
		{"wallet", contracts.TopicWallet, map[string]eventbus.Handler{
			contracts.TypeTopUpCreated:   financeSvc.TopUpCreated,
			contracts.TypeTopUpPaid:      financeSvc.TopUpPaid,
			contracts.TypeTopUpExpired:   financeSvc.TopUpExpired,
			contracts.TypeTopUpCancelled: financeSvc.TopUpCancelled,
			contracts.TypeWalletCharged:  financeSvc.WalletCharged,
			contracts.TypeWalletRefunded: financeSvc.WalletRefunded,
		}},
	}
	for _, spec := range consumers {
		c, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
			Brokers: cfg.KafkaBrokers, Group: cfg.GroupPrefix + "." + spec.purpose, Topics: []string{spec.topic}, DB: pool, Log: log,
		})
		if err != nil {
			return nil, err
		}
		for eventType, h := range spec.handlers {
			c.Handle(eventType, h)
		}
		running = append(running, c)
	}

	rpc := platform.NewRPC(log)
	authSvc.Mount(rpc)
	fleetSvc.Mount(rpc)
	plansSvc.Mount(rpc)
	dispatchSvc.Mount(rpc)
	settingsSvc.Mount(rpc)
	usersSvc.Mount(rpc)
	financeSvc.Mount(rpc)
	alerts.Mount(rpc, pool, feed)
	// The AI features. Without AI_URL there is no gateway and the methods answer that the feature is having
	// problems; the admin's switches are not touched either way.
	var gateway aiapi.Model
	if cfg.AIURL != "" {
		gateway = &aiapi.Client{BaseURL: cfg.AIURL, Token: cfg.AIToken}
	}
	ai.New(ai.Options{
		Model: gateway, Pool: pool, Plans: plansSvc, Fleet: fleetSvc, Dispatch: dispatchSvc, Finance: financeSvc, Log: log,
		RatePerMinute: cfg.AIRatePerMinute, Burst: cfg.AIRateBurst, Now: cfg.Now,
	}).Mount(rpc)

	health := platform.NewHealth(log)
	health.Check("postgres", pool.Ping)
	health.Check("kafka", producer.Ping)
	mux := http.NewServeMux()
	health.Mount(mux)
	rpc.Mount(mux)
	mux.Handle("GET /v1/events", hub.Handler())
	server, err := platform.NewServer(platform.ServerConfig{
		Addr: cfg.HTTPAddr, Log: log, Health: health,
		Handler: platform.Chain(mux, platform.TraceMiddleware, platform.LocaleMiddleware, platform.AccessLog(log), platform.Recover(log),
			platform.CORS(cfg.CORSOrigins), sessions.Authenticate, auth.CaptureToken),
	})
	if err != nil {
		return nil, err
	}
	server.OnShutdown(hub.Close)

	// From here on nothing can fail: start everything and supervise it. A component that returns an error stops
	// the rest, and Wait reports it.
	started = true
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	spawn := func(name string, run func(context.Context) error) {
		wg.Go(func() {
			if err := run(runCtx); err != nil {
				mu.Lock()
				failures = append(failures, fmt.Errorf("%s: %w", name, err))
				mu.Unlock()
				cancel()
			}
		})
	}
	spawn("http server", server.Serve)
	spawn("outbox relay", publisher.Run)
	for i, c := range running {
		spawn("consumer "+consumers[i].purpose, c.Run)
	}
	if cfg.DispatchWorker {
		spawn("dispatch worker", func(ctx context.Context) error { return dispatchSvc.Run(ctx, cfg.SweepInterval) })
	}

	a := &App{addr: server.Addr(), done: make(chan struct{})}
	go func() {
		wg.Wait()
		cancel()
		cleanup.run()
		a.err = errors.Join(failures...)
		close(a.done)
	}()
	log.InfoContext(ctx, "backoffice-api started", "addr", a.addr, "dispatchWorker", cfg.DispatchWorker)
	return a, nil
}

// cancelled returns a context that is already done.
func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
