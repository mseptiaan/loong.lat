package platform

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so the first one is the outermost: Chain(h, a, b) runs a, then b, then h.
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// statusWriter records the status and size of a response. Unwrap keeps http.ResponseController (flushing for
// streams) working through the wrapper.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// AccessLog logs one line per request: method, path, status, size and duration. It belongs inside
// TraceMiddleware so the line carries the trace id.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			log.LogAttrs(r.Context(), slog.LevelInfo, "request",
				slog.String("method", r.Method), slog.String("path", r.URL.Path),
				slog.Int("status", sw.status), slog.Int("bytes", sw.bytes),
				slog.Duration("duration", time.Since(start)))
		})
	}
}

// Recover turns a handler panic into a logged generic 500, so one bad request cannot take the connection down
// silently. It belongs inside TraceMiddleware.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec) // the server's own signal to drop the connection quietly
				}
				log.ErrorContext(r.Context(), "panic in handler", "panic", rec, "stack", string(debug.Stack()))
				writeError(w, internalError(TraceID(r.Context())))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
