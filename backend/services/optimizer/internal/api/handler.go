// Package api serves the optimizer over HTTP: a quote request in, a stream of
// progress events and then the quote out, newline-delimited JSON.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/quote"
)

// maxRequestBytes bounds a quote request; a wizard's six shipments are a few kilobytes.
const maxRequestBytes = 4 << 20

// Handler serves quotes.
type Handler struct {
	Planner *quote.Planner
	// Token is the shared secret callers present as a bearer token. Empty disables the check, which is for local development only.
	Token string
	// Timeout bounds one quick quote, whatever the caller's patience. DeepTimeout bounds a thorough or reviewed one,
	// which runs several jobs; zero takes Timeout.
	Timeout     time.Duration
	DeepTimeout time.Duration
	Log         *slog.Logger
	// Info is reported by /v1/info.
	Info optimizerapi.Info
}

// Routes returns the service's routes.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+optimizerapi.InfoPath, h.auth(h.info))
	mux.HandleFunc("POST "+optimizerapi.QuotePath, h.auth(h.quote))
	return mux
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Token != "" {
			got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(h.Token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (h *Handler) info(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.MarshalWrite(w, h.Info)
}

// timeout is how long a quote of this effort may take.
func (h *Handler) timeout(effort contracts.Effort) time.Duration {
	if effort != "" && effort != contracts.EffortFast && h.DeepTimeout > 0 {
		return h.DeepTimeout
	}
	return h.Timeout
}

// stream writes events as they happen, flushing each so the wizard sees progress live. It flushes through
// http.ResponseController: the platform's middleware wraps the writer, and a wrapper only exposes Flush by way of
// Unwrap, so asserting http.Flusher on the writer itself finds nothing and the whole stream arrives at the end.
type stream struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s *stream) send(ev optimizerapi.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.w.Write(append(data, '\n'))
	_ = s.rc.Flush() // a writer that cannot flush just delivers the stream late
}

func (h *Handler) quote(w http.ResponseWriter, r *http.Request) {
	var req optimizerapi.QuoteRequest
	if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, maxRequestBytes), &req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	now := req.Now.Time
	if now.IsZero() {
		now = time.Now()
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // proxies must not hold the stream back
	s := &stream{w: w, rc: http.NewResponseController(w)}

	ctx := r.Context()
	if timeout := h.timeout(req.Effort); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	started := time.Now()
	res, err := h.Planner.Compute(ctx, quote.Request{
		PlanID: req.PlanID, Shipments: req.Shipments, Schedule: req.Schedule, Priority: req.Priority, Vehicles: req.Vehicles, Now: now,
		Effort: req.Effort, AIReview: req.AIReview,
	}, func(u quote.Update) {
		s.send(optimizerapi.Event{Type: optimizerapi.EventProgress, Stage: int(u.Stage), Total: quote.NumStages, Msg: u.Msg})
	})
	if err != nil {
		s.send(optimizerapi.Event{Type: optimizerapi.EventError, Error: toAPIError(err)})
		h.log().Warn("quote failed", "plan", req.PlanID, "err", err, "ms", time.Since(started).Milliseconds())
		return
	}
	s.send(optimizerapi.Event{Type: optimizerapi.EventDone, Result: &optimizerapi.QuoteResult{
		OptionSet: res.Set, Truncated: res.Truncated, Proven: res.Proven, RoadSource: res.RoadSource, Engine: res.Engine,
	}})
	var method contracts.Method
	if m := res.Set.Method; m != nil {
		method = *m
	}
	h.log().Info("quote done", "plan", req.PlanID, "shipments", len(req.Shipments), "options", len(res.Set.Options),
		"effort", method.Effort, "trials", method.Trials, "rounds", method.Rounds, "reviewer", method.Reviewer,
		"proven", res.Proven, "roads", res.RoadSource, "truncated", res.Truncated, "ms", time.Since(started).Milliseconds())
}

// toAPIError turns a failed quote into the error callers see. Anything that is
// not a quote.Error is our fault and says so, without detail.
func toAPIError(err error) *optimizerapi.Error {
	var qe *quote.Error
	if errors.As(err, &qe) {
		return &optimizerapi.Error{Code: qe.Code, Field: qe.Field, Msg: qe.Msg}
	}
	return &optimizerapi.Error{Code: quote.CodeService, Msg: contracts.Msg{Key: "srv.engine.unavailable"}}
}
