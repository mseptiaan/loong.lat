// Package app assembles the AI gateway so that both cmd/ai and the end-to-end tests can start it: configuration,
// wiring, and a graceful lifecycle.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/meta"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/ai/internal/api"
	"github.com/mseptiaan/loong.lat/backend/services/ai/internal/profile"
)

// Config is everything the service is told. ConfigFromEnv reads it from the environment; see
// services/ai/.env.example. It holds secrets (meta.Secret prints as "[redacted]"), so printing it is safe.
type Config struct {
	// HTTPAddr is the listen address; ":0" picks a free port.
	HTTPAddr string
	// Tokens are the bearer secrets callers present (AI_TOKEN, comma separated: one per caller, each with its own
	// rate limit). Empty is allowed only with Dev.
	Tokens []meta.Secret
	// Dev lets the service run without a token, for a laptop and tests.
	Dev bool

	// MetaAPIKey is the key of Meta's Model API. Without it the service still starts: it reports itself not
	// configured and answers every AI call with 503.
	MetaAPIKey      meta.Secret
	MetaBaseURL     string
	MetaModel       string
	TranscribeModel string
	// Profiles are the fixed settings per use; nil takes the defaults for MetaModel and TranscribeModel.
	Profiles profile.Set

	// RatePerMinute and RateBurst limit each caller (token); MaxInFlight bounds calls to Meta running at once.
	RatePerMinute int
	RateBurst     int
	MaxInFlight   int
	// MetaHTTP is the transport to Meta (tests).
	MetaHTTP *http.Client
}

// ConfigFromEnv reads the configuration from env. Problems are collected in env.Err.
func ConfigFromEnv(env *platform.Env) Config {
	model := env.String("META_MODEL", meta.DefaultModel)
	speech := env.String("META_TRANSCRIBE_MODEL", meta.DefaultTranscribeModel)
	cfg := Config{
		HTTPAddr:        env.String("AI_ADDR", ":8091"),
		Dev:             env.Bool("AI_DEV", false),
		MetaAPIKey:      meta.Secret(env.String("META_API_KEY", "")),
		MetaBaseURL:     env.String("META_BASE_URL", meta.DefaultBaseURL),
		MetaModel:       model,
		TranscribeModel: speech,
		RatePerMinute:   env.Int("AI_RATE_PER_MINUTE", 120),
		RateBurst:       env.Int("AI_RATE_BURST", 30),
		MaxInFlight:     env.Int("AI_MAX_IN_FLIGHT", 16),
		Profiles:        profile.Defaults(model, speech),
	}
	for _, t := range env.List("AI_TOKEN") {
		cfg.Tokens = append(cfg.Tokens, meta.Secret(t))
	}
	for _, name := range profile.Names() {
		prefix := profile.EnvPrefix(name)
		p := cfg.Profiles[name]
		p.Model = env.String(prefix+"MODEL", p.Model)
		p.ReasoningEffort = env.String(prefix+"EFFORT", p.ReasoningEffort)
		p.MaxTokens = env.Int(prefix+"MAX_TOKENS", p.MaxTokens)
		p.Timeout = env.Duration(prefix+"TIMEOUT", p.Timeout)
		p.MaxBodyBytes = int64(env.Int(prefix+"MAX_BODY_BYTES", int(p.MaxBodyBytes)))
		cfg.Profiles[name] = p
	}
	return cfg
}

// App is a running AI gateway.
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
	if len(cfg.Tokens) == 0 && !cfg.Dev {
		return nil, fmt.Errorf("AI_TOKEN is required (set AI_DEV=true to run without it locally)")
	}
	for i, t := range cfg.Tokens {
		if !t.Set() {
			return nil, fmt.Errorf("AI_TOKEN: token %d is empty", i+1)
		}
	}
	if cfg.MetaModel == "" {
		cfg.MetaModel = meta.DefaultModel
	}
	if cfg.TranscribeModel == "" {
		cfg.TranscribeModel = meta.DefaultTranscribeModel
	}
	if cfg.Profiles == nil {
		cfg.Profiles = profile.Defaults(cfg.MetaModel, cfg.TranscribeModel)
	}
	if err := cfg.Profiles.Validate(); err != nil {
		return nil, err
	}

	upstream := meta.New(meta.Config{
		BaseURL: cfg.MetaBaseURL, APIKey: cfg.MetaAPIKey, Model: cfg.MetaModel, TranscribeModel: cfg.TranscribeModel,
		HTTP: cfg.MetaHTTP, UserAgent: "loong-ai-gateway",
	})
	if !upstream.Configured() {
		log.Warn("META_API_KEY is not set: the gateway starts, but every AI call will answer 503 until a key is configured")
	}
	if len(cfg.Tokens) == 0 {
		log.Warn("AI_TOKEN is not set (AI_DEV): anyone who can reach this port may use the model")
	}

	var limiter *platform.LoginLimiter
	if cfg.RatePerMinute > 0 {
		limiter = platform.NewLoginLimiter(time.Minute/time.Duration(cfg.RatePerMinute), cfg.RateBurst)
	}
	handler := api.New(api.Options{
		Upstream: upstream, Profiles: cfg.Profiles, Tokens: cfg.Tokens,
		Limiter: limiter, MaxInFlight: cfg.MaxInFlight, Log: log,
	})

	srv, err := platform.NewServer(platform.ServerConfig{
		Addr: cfg.HTTPAddr, Log: log,
		Handler: platform.Chain(handler.Routes(), platform.TraceMiddleware, platform.AccessLog(log), platform.Recover(log)),
	})
	if err != nil {
		return nil, err
	}
	srv.OnShutdown(handler.SetDraining)
	log.Info("ai gateway ready", "addr", srv.Addr(), "metaConfigured", upstream.Configured(), "baseURL", cfg.MetaBaseURL,
		"model", cfg.MetaModel, "transcribeModel", cfg.TranscribeModel, "callers", len(cfg.Tokens),
		"ratePerMinute", cfg.RatePerMinute, "profiles", profileNames())

	a := &App{srv: srv, done: make(chan error, 1)}
	go func() { a.done <- srv.Serve(ctx) }()
	return a, nil
}

func profileNames() []string {
	var out []string
	for _, n := range profile.Names() {
		out = append(out, string(n))
	}
	return out
}
