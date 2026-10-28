package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

type traceKey struct{}

// WithTraceID returns ctx carrying a W3C trace id (32 hex chars); logs and published events pick it up.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceKey{}, id)
}

// TraceID returns the trace id in ctx, or "".
func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(traceKey{}).(string)
	return id
}

// NewTraceID returns a random trace id.
func NewTraceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error
	return hex.EncodeToString(b[:])
}

// TraceMiddleware puts a trace id in each request's context: the one in an incoming W3C `traceparent` header,
// else a new one. It is echoed in the X-Trace-Id response header so a user can quote it in a report.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := traceIDFromTraceparent(r.Header.Get("Traceparent"))
		if id == "" {
			id = NewTraceID()
		}
		w.Header().Set("X-Trace-Id", id)
		next.ServeHTTP(w, r.WithContext(WithTraceID(r.Context(), id)))
	})
}

// traceIDFromTraceparent extracts the trace id from "00-<trace id>-<span id>-<flags>", or "" if malformed.
func traceIDFromTraceparent(header string) string {
	parts := strings.Split(header, "-")
	if len(parts) != 4 || len(parts[2]) != 16 || !contracts.ValidTraceID(parts[1]) {
		return ""
	}
	return parts[1]
}
