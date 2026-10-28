// Package app assembles console-api: the shipper-facing service behind the dashboard. It is the only package that
// knows every part; the parts themselves live in internal/ and know only each other's contracts.
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

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/account"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/ai"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/auth"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/projections"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/proofs"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/settings"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/wallet"
	"github.com/mseptiaan/loong.lat/backend/services/console/migrations"
)

const serviceName = "console-api"

// Config is everything console-api needs to run.
type Config struct {
	// HTTPAddr is the listen address; ":0" picks a free port. Default ":8083".
	HTTPAddr string
	// DatabaseURL is the console's own database.
	DatabaseURL  string
	KafkaBrokers []string
	// GroupPrefix names the consumer groups "<GroupPrefix>.fleet", ".dispatch", ".execution", ".cancel", ".customer"
	// and ".settings". Default "console".
	GroupPrefix string
	// SessionTTL is how long a sign-in lasts. Default 720h.
	SessionTTL time.Duration

	// OptimizerURL and OptimizerToken reach the optimizer service, which prices the shipper's options.
	OptimizerURL   string
	OptimizerToken string
	// OptimizerCallTimeout is how long a quote may take from the console's side: it must outlast the optimizer's own
	// limit for a thorough or reviewed quote (OPTIMIZER_TIMEOUT_DEEP, 150s by default). Default 3m.
	OptimizerCallTimeout time.Duration
	// CORSOrigins are the web origins allowed to call this service from a browser; the dashboard's own by default.
	CORSOrigins []string

	// SimulatePayments offers wallet.simulatePay, which credits a top-up without any payment. For development
	// and tests only: off unless asked for.
	SimulatePayments bool

	// CancelTimeout is how long a cancel waits for the drivers' votes before it is aborted. Default 2m.
	CancelTimeout time.Duration

	// AIURL and AIToken reach the AI gateway. Without AIURL (the default) every AI feature is reported off by
	// settings.get, whatever the admin chose.
	AIURL   string
	AIToken string
	// AIRatePerMinute and AIRateBurst bound each customer's AI requests so that one person cannot spend the model
	// budget: a burst of AIRateBurst, then AIRatePerMinute a minute. Defaults 20 and 10.
	AIRatePerMinute int
	AIRateBurst     int
	// ProofAIPoll is how often the worker that reads drivers' proof photos looks for a new one; it runs only with a
	// gateway (AIURL). Default 2s.
	ProofAIPoll time.Duration
}

// DefaultOptimizerCallTimeout is how long the console waits for a quote: longer than the optimizer's 150s limit for
// a thorough or reviewed one, so that the optimizer's answer, a truncated quote included, is what the shipper gets.
const DefaultOptimizerCallTimeout = 3 * time.Minute

// ConfigFromEnv reads the configuration from the environment. Problems are collected in env (see Env.Err).
func ConfigFromEnv(env *platform.Env) Config {
	cfg := Config{
		HTTPAddr:             env.String("HTTP_ADDR", ":8083"),
		CORSOrigins:          env.List("CORS_ORIGINS", "http://console.loong.localhost"),
		DatabaseURL:          env.Require("DATABASE_URL"),
		KafkaBrokers:         env.List("KAFKA_BROKERS"),
		SessionTTL:           env.Duration("SESSION_TTL", 720*time.Hour),
		OptimizerURL:         env.String("OPTIMIZER_URL", "http://127.0.0.1:8090"),
		OptimizerToken:       env.String("OPTIMIZER_TOKEN", ""),
		OptimizerCallTimeout: env.Duration("OPTIMIZER_CALL_TIMEOUT", DefaultOptimizerCallTimeout),
		SimulatePayments:     env.Bool("SIMULATE_PAYMENTS", false),
		CancelTimeout:        env.Duration("CANCEL_TIMEOUT", plans.DefaultCancelTimeout),
		AIURL:                env.String("AI_URL", ""),
		AIToken:              env.String("AI_TOKEN", ""),
		AIRatePerMinute:      env.Int("AI_USER_RATE_PER_MINUTE", ai.DefaultRatePerMinute),
		AIRateBurst:          env.Int("AI_USER_RATE_BURST", ai.DefaultBurst),
		ProofAIPoll:          env.Duration("PROOF_AI_POLL", proofs.DefaultPoll),
	}
	if len(cfg.KafkaBrokers) == 0 {
		env.Require("KAFKA_BROKERS") // records the "required but not set" problem
	}
	return cfg
}

func (c Config) withDefaults() Config {
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":8083"
	}
	if c.GroupPrefix == "" {
		c.GroupPrefix = "console"
	}
	if c.SessionTTL == 0 {
		c.SessionTTL = 720 * time.Hour
	}
	if c.CancelTimeout == 0 {
		c.CancelTimeout = plans.DefaultCancelTimeout
	}
	if c.OptimizerCallTimeout == 0 {
		c.OptimizerCallTimeout = DefaultOptimizerCallTimeout
	}
	if c.ProofAIPoll == 0 {
		c.ProofAIPoll = proofs.DefaultPoll
	}
	return c
}

func (c Config) validate() error {
	var problems []error
	if c.DatabaseURL == "" {
		problems = append(problems, errors.New("DatabaseURL is required"))
	}
	if len(c.KafkaBrokers) == 0 {
		problems = append(problems, errors.New("KafkaBrokers is required"))
	}
	if c.OptimizerURL == "" {
		problems = append(problems, errors.New("OptimizerURL is required"))
	}
	return errors.Join(problems...)
}

// App is a running console-api.
type App struct {
	addr string
	done chan struct{}
	err  error
}

// Addr is the address the HTTP server listens on.
func (a *App) Addr() string { return a.addr }

// Wait blocks until ctx (the one given to Start) is cancelled and everything has stopped, and returns what made
// anything stop on its own.
func (a *App) Wait() error {
	<-a.done
	return a.err
}

// Start applies the migrations and starts the service: it returns once the HTTP listener is bound and the
// outbox relay and the consumers are running, and keeps running until ctx is cancelled.
func Start(ctx context.Context, cfg Config, log *slog.Logger) (*App, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("console-api: %w", err)
	}

	// Resources are released in reverse order when starting fails, or when everything has stopped.
	var cleanup []func()
	release := func() {
		for _, fn := range slices.Backward(cleanup) {
			fn()
		}
	}
	started := false
	defer func() {
		if !started {
			release()
		}
	}()

	pool, err := platform.NewPool(ctx, platform.PoolConfig{DSN: cfg.DatabaseURL, AppName: serviceName})
	if err != nil {
		return nil, err
	}
	cleanup = append(cleanup, pool.Close)
	own, err := migrations.All()
	if err != nil {
		return nil, err
	}
	if _, err := platform.Migrate(ctx, pool, slices.Concat(platform.Migrations(), eventbus.Migrations(), notices.Migrations(), own)...); err != nil {
		return nil, err
	}

	producer, err := eventbus.NewProducerClient(cfg.KafkaBrokers, serviceName)
	if err != nil {
		return nil, err
	}
	cleanup = append(cleanup, producer.Close)
	publisher := eventbus.NewPublisher(serviceName, pool, producer, log)

	hub := platform.NewHub()
	sessions := platform.NewSessions(platform.NewPgSessionStore(pool), cfg.SessionTTL, log)
	accounts, err := auth.New(pool, publisher, sessions)
	if err != nil {
		return nil, err
	}
	wallets := wallet.New(pool, publisher)
	vehicles := fleet.New(pool)
	profiles := account.New(pool, publisher)
	appSettings := settings.New(pool, cfg.AIURL != "")
	optimizer := &optimizerapi.Client{BaseURL: cfg.OptimizerURL, Token: cfg.OptimizerToken, HTTP: &http.Client{Timeout: cfg.OptimizerCallTimeout}}

	if cfg.SimulatePayments {
		log.Warn("SIMULATE_PAYMENTS is on: top-ups can be credited without paying; never run production like this")
	}
	rpc := platform.NewRPC(log)
	accounts.Mount(rpc)
	wallets.Mount(rpc, cfg.SimulatePayments)
	planService := plans.New(pool, publisher, wallets, vehicles, optimizer,
		plans.WithCancelTimeout(cfg.CancelTimeout), plans.WithLogger(log),
		// A reviewed quote may be checked by the AI when the admin's switch for the assistant is on and a gateway is
		// configured (settings.get reports the switch off without one, but the optimizer is told only what is true).
		plans.WithAIReview(func(ctx context.Context) bool {
			if cfg.AIURL == "" {
				return false
			}
			current, err := appSettings.Get(ctx)
			return err == nil && current.AI.Copilot
		}))
	planService.Mount(rpc)
	profiles.Mount(rpc)
	appSettings.Mount(rpc)
	// The AI features. Without AI_URL there is no gateway: settings.get then reports every AI flag off, and a call
	// that comes anyway is refused as "off" before it gets near a model.
	var gateway aiapi.Gateway
	if cfg.AIURL != "" {
		gateway = &aiapi.Client{BaseURL: cfg.AIURL, Token: cfg.AIToken}
	}
	aiFlags := func(ctx context.Context) (contracts.AIFeatures, error) {
		current, err := appSettings.Get(ctx)
		return current.AI, err
	}
	aiService := ai.New(ai.Options{
		Gateway: gateway, Pool: pool, Log: log, RatePerMinute: cfg.AIRatePerMinute, Burst: cfg.AIRateBurst, Flags: aiFlags,
		BalanceRp: func(ctx context.Context, customerID string) (int64, error) {
			w, err := wallets.Get(ctx, customerID)
			return w.BalanceRp, err
		},
	})
	aiService.Mount(rpc)
	// The photos drivers take as proof of delivery are read by the AI after the fact, off the event path: the
	// projection queues a job and this worker works it off. It runs only with a gateway; without one the jobs wait
	// (and settings.get reports the vision switch off anyway).
	var proofWorker *proofs.Worker
	if cfg.AIURL != "" {
		proofWorker = proofs.New(proofs.Options{Pool: pool, Analyzer: aiService, Flags: aiFlags, Log: log, Poll: cfg.ProofAIPoll})
	}
	// Only customers sign in here, and the bell menu holds theirs.
	notices.Mount(rpc, notify.ReadAnnouncer{Pool: pool}, func(p platform.Principal) (string, bool) {
		return notices.Customer(p.UserID), p.Role == contracts.RoleCustomer
	})

	type follower struct {
		suffix, topic string
		register      func(*eventbus.Consumer)
	}
	var consumers []*eventbus.Consumer
	var groups []string
	for _, f := range []follower{
		{"fleet", contracts.TopicFleet, vehicles.Register},
		{"dispatch", contracts.TopicDispatch, projections.RegisterDispatch},
		{"execution", contracts.TopicExecution, projections.RegisterExecution},
		{"cancel", contracts.TopicPlan, planService.RegisterCancel},
		{"customer", contracts.TopicCustomer, accounts.RegisterEvents},
		{"settings", contracts.TopicSettings, appSettings.Register},
	} {
		group := cfg.GroupPrefix + "." + f.suffix
		c, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
			Brokers: cfg.KafkaBrokers, Group: group, Topics: []string{f.topic}, DB: pool, Log: log,
		})
		if err != nil {
			return nil, err
		}
		f.register(c)
		consumers, groups = append(consumers, c), append(groups, group)
	}

	health := platform.NewHealth(log)
	health.Check("postgres", pool.Ping)
	mux := http.NewServeMux()
	health.Mount(mux)
	rpc.Mount(mux)
	mux.Handle("GET /v1/events", hub.Handler())
	handler := platform.Chain(mux,
		platform.TraceMiddleware,
		platform.LocaleMiddleware,
		platform.AccessLog(log),
		platform.Recover(log),
		platform.CORS(cfg.CORSOrigins),
		auth.TokenMiddleware,
		sessions.Authenticate,
		platform.Idempotency(platform.NewPgIdempotencyStore(pool), "plans.confirm", "wallet.createTopUp"),
	)
	server, err := platform.NewServer(platform.ServerConfig{Addr: cfg.HTTPAddr, Handler: handler, Log: log, Health: health})
	if err != nil {
		return nil, err
	}
	server.OnShutdown(hub.Close)

	runCtx, cancel := context.WithCancel(ctx)
	app := &App{addr: server.Addr(), done: make(chan struct{})}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	run := func(name string, fn func(context.Context) error) {
		wg.Go(func() {
			if err := fn(runCtx); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
				mu.Unlock()
				cancel() // one part failing stops the rest: a half-running service is worse than a stopped one
			}
		})
	}
	run("outbox relay", publisher.Run)
	run("cancel sweeper", func(ctx context.Context) error {
		return planService.RunCancelSweeper(ctx, sweepInterval(cfg.CancelTimeout))
	})
	run("event relay", func(ctx context.Context) error {
		notify.Relay(ctx, cfg.DatabaseURL, hub, log)
		return nil
	})
	if proofWorker != nil {
		run("proof analysis", proofWorker.Run)
	}
	for i, c := range consumers {
		run("consumer "+groups[i], c.Run)
	}
	run("http server", server.Serve)

	go func() {
		wg.Wait()
		cancel()
		release()
		app.err = errors.Join(errs...)
		close(app.done)
	}()
	started = true
	return app, nil
}

// sweepInterval is how often overdue cancel requests are looked for: often enough that one is aborted soon after
// its deadline, rarely enough to cost nothing.
func sweepInterval(timeout time.Duration) time.Duration {
	return min(max(timeout/10, 100*time.Millisecond), 5*time.Second)
}
