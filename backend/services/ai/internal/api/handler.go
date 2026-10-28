// Package api serves the AI gateway over HTTP: chat (one JSON document, or NDJSON deltas when streaming) and
// transcription, in front of Meta's Model API. The gateway holds the Meta key, fixes model, reasoning effort,
// token budget, time limit and request size per profile, limits each caller's rate, and keeps conversations out of
// its logs: only sizes, counts, token usage and latency are ever logged, never a message, image or recording.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/meta"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/ai/internal/profile"
)

// Upstream is the Model API as the gateway uses it; *meta.Client implements it.
type Upstream interface {
	Configured() bool
	Chat(ctx context.Context, req meta.ChatRequest) (meta.ChatResponse, error)
	ChatStream(ctx context.Context, req meta.ChatRequest, fn func(meta.Delta) error) (meta.ChatResponse, error)
	Transcribe(ctx context.Context, audio []byte, filename, contentType string, opts ...meta.TranscribeOption) (meta.Transcript, error)
}

// Options configures a Handler.
type Options struct {
	Upstream Upstream
	Profiles profile.Set
	// Tokens are the bearer secrets callers may present; each is its own caller for rate limiting and logs. None
	// disables authentication, for a laptop and tests only.
	Tokens []meta.Secret
	// Limiter throttles each caller; nil means no limit.
	Limiter *platform.LoginLimiter
	// MaxInFlight bounds calls to Meta running at once; zero means unbounded.
	MaxInFlight int
	Log         *slog.Logger
}

// Handler serves the gateway.
type Handler struct {
	up       Upstream
	profiles profile.Set
	tokens   []meta.Secret
	limiter  *platform.LoginLimiter
	inflight chan struct{}
	log      *slog.Logger
	draining atomic.Bool
}

// New returns a Handler.
func New(o Options) *Handler {
	h := &Handler{up: o.Upstream, profiles: o.Profiles, tokens: o.Tokens, limiter: o.Limiter, log: o.Log}
	if h.log == nil {
		h.log = slog.Default()
	}
	if o.MaxInFlight > 0 {
		h.inflight = make(chan struct{}, o.MaxInFlight)
	}
	return h
}

// SetDraining makes /readyz fail, so the orchestrator stops sending traffic while the server shuts down.
func (h *Handler) SetDraining() { h.draining.Store(true) }

// Routes returns the service's routes.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET "+aiapi.InfoPath, h.serve(h.info, false))
	mux.HandleFunc("POST "+aiapi.ChatPath, h.serve(h.chat, true))
	mux.HandleFunc("POST "+aiapi.TranscribePath, h.serve(h.transcribe, true))
	return mux
}

// readyz is ready whenever the process is: it never calls Meta. It reports whether a key is configured, so that
// an operator can tell "up" from "up but every AI call will answer 503".
func (h *Handler) readyz(w http.ResponseWriter, _ *http.Request) {
	if h.draining.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "draining"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "meta": map[string]any{"configured": h.up.Configured()}})
}

// call is what the handlers know about the request in hand, for logs: sizes and counts, never content.
type call struct {
	caller   string
	started  time.Time
	profile  aiapi.Profile
	bytes    int
	messages int
	tools    int
	images   int
	stream   bool
}

// serve wraps a handler with authentication and the per-caller rate limit.
func (h *Handler) serve(next func(w http.ResponseWriter, r *http.Request, c *call), limited bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := &call{started: time.Now()}
		caller, ok := h.authenticate(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, aiapi.ErrorBody{Kind: aiapi.KindUnauthorized, Msg: contracts.Msg{Key: platform.MsgSignInFirst}})
			return
		}
		c.caller = caller
		if limited && h.limiter != nil {
			if allowed, wait := h.limiter.Allow(caller); !allowed {
				secs := max(1, int(wait.Seconds()+0.5))
				h.log.LogAttrs(r.Context(), slog.LevelWarn, "rate limited", slog.String("caller", caller), slog.String("path", r.URL.Path), slog.Int("retryAfterSec", secs))
				writeError(w, http.StatusTooManyRequests, tooMany(secs))
				return
			}
		}
		next(w, r, c)
	}
}

// authenticate matches the bearer token against every configured one (without stopping at the first match, so the
// time taken does not say which matched) and names the caller. With no tokens configured it lets everyone in.
func (h *Handler) authenticate(r *http.Request) (caller string, ok bool) {
	if len(h.tokens) == 0 {
		return "dev", true
	}
	got, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || got == "" {
		return "", false
	}
	match := -1
	for i, t := range h.tokens {
		if subtle.ConstantTimeCompare([]byte(got), []byte(t.Reveal())) == 1 {
			match = i
		}
	}
	if match < 0 {
		return "", false
	}
	return "token" + strconv.Itoa(match), true
}

func (h *Handler) info(w http.ResponseWriter, _ *http.Request, _ *call) {
	writeJSON(w, http.StatusOK, aiapi.Info{Configured: h.up.Configured(), Profiles: h.profiles.Infos()})
}

// acquire takes one of the slots for calls to Meta; ok is false when all are busy.
func (h *Handler) acquire() (release func(), ok bool) {
	if h.inflight == nil {
		return func() {}, true
	}
	select {
	case h.inflight <- struct{}{}:
		return func() { <-h.inflight }, true
	default:
		return nil, false
	}
}

// Message keys of the frontend catalog the gateway answers with.
const (
	msgUnavailable   = aiapi.MsgUnavailable
	msgBadRequest    = platform.MsgBadRequest
	msgImageTooLarge = "srv.api.imageTooLarge"
	msgNotImage      = "srv.api.fileNotImage"
	msgFileEmpty     = "srv.api.fileEmpty"
	msgNoSpeech      = "srv.api.noSpeech"
)

func unavailable(kind aiapi.ErrorKind) aiapi.ErrorBody {
	return aiapi.ErrorBody{Kind: kind, Msg: contracts.Msg{Key: msgUnavailable}}
}

func invalid(field, key string) aiapi.ErrorBody {
	return aiapi.ErrorBody{Kind: aiapi.KindInvalid, Field: field, Msg: contracts.Msg{Key: key}}
}

func tooMany(seconds int) aiapi.ErrorBody {
	return aiapi.ErrorBody{
		Kind: aiapi.KindRateLimited, RetryAfter: seconds,
		Msg: contracts.Msg{Key: platform.MsgTooManyRequest, Params: map[string]any{"seconds": seconds}},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, body aiapi.ErrorBody) {
	if body.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(body.RetryAfter))
	}
	writeJSON(w, status, aiapi.ErrorEnvelope{Error: body})
}

// refuse answers a request the gateway will not run, and logs that it did, with the reason but not the content.
func (h *Handler) refuse(w http.ResponseWriter, r *http.Request, c *call, status int, body aiapi.ErrorBody) {
	h.log.LogAttrs(r.Context(), slog.LevelInfo, "request refused", append(c.attrs(),
		slog.Int("status", status), slog.String("kind", string(body.Kind)), slog.String("field", body.Field), slog.String("msgKey", body.Msg.Key))...)
	writeError(w, status, body)
}

func (c *call) attrs() []slog.Attr {
	return []slog.Attr{
		slog.String("caller", c.caller), slog.String("profile", string(c.profile)), slog.Bool("stream", c.stream),
		slog.Int("requestBytes", c.bytes), slog.Int("messages", c.messages), slog.Int("tools", c.tools), slog.Int("images", c.images),
		slog.Int64("latencyMs", time.Since(c.started).Milliseconds()),
	}
}

// failure maps what went wrong calling Meta to the status and body the caller gets, and logs it. A caller that
// has gone away (its context ended) is logged but not answered.
func (h *Handler) failure(ctx context.Context, c *call, err error) (status int, body aiapi.ErrorBody, answer bool) {
	attrs := c.attrs()
	if ctx.Err() != nil && !isMeta(err) {
		h.log.LogAttrs(ctx, slog.LevelInfo, "caller gone", append(attrs, slog.String("cause", ctx.Err().Error()))...)
		return 499, aiapi.ErrorBody{}, false
	}
	level := slog.LevelWarn
	me, ok := errors.AsType[*meta.Error](err)
	if !ok {
		h.log.LogAttrs(ctx, slog.LevelError, "call failed", append(attrs, slog.String("error", err.Error()))...)
		return http.StatusInternalServerError, unavailable(aiapi.KindUnavailable), true
	}
	attrs = append(attrs, slog.String("upstreamKind", me.Kind.String()), slog.Int("upstreamStatus", me.Status),
		slog.String("upstreamCode", me.Code), slog.String("upstreamType", me.Type), slog.String("upstreamParam", me.Param), slog.Int("attempts", me.Attempts))
	// The text of a refusal (400) can describe the request; everything else is generic.
	if me.Kind != meta.KindBadRequest {
		attrs = append(attrs, slog.String("upstreamMessage", me.Message))
	}
	switch me.Kind {
	case meta.KindRateLimit:
		secs := min(max(1, int((me.RetryAfter+time.Second-1).Seconds())), 60)
		status, body = http.StatusTooManyRequests, tooMany(secs)
	case meta.KindBadRequest:
		status, body = http.StatusUnprocessableEntity, invalid("", msgBadRequest)
	case meta.KindTimeout:
		status, body = http.StatusGatewayTimeout, unavailable(aiapi.KindTimeout)
	case meta.KindLength:
		status, body = http.StatusBadGateway, unavailable(aiapi.KindTruncated)
	case meta.KindAuth:
		level = slog.LevelError // our key is refused: an operator has to act
		status, body = http.StatusBadGateway, unavailable(aiapi.KindUnavailable)
	default:
		status, body = http.StatusBadGateway, unavailable(aiapi.KindUnavailable)
	}
	h.log.LogAttrs(ctx, level, "call failed", append(attrs, slog.Int("status", status), slog.String("kind", string(body.Kind)))...)
	return status, body, true
}

func isMeta(err error) bool {
	_, ok := errors.AsType[*meta.Error](err)
	return ok
}
