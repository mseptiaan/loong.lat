package platform

import (
	"context"
	"io"
	"log/slog"
)

// NewLogger returns a JSON logger that stamps every record with the service name and, when the context passed
// to the *Context methods carries one, the trace id.
func NewLogger(w io.Writer, service string, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(traceHandler{handler}).With("service", service)
}

// ParseLevel reads "debug", "info", "warn" or "error"; empty means info.
func ParseLevel(s string) (slog.Level, error) {
	var level slog.Level
	if s == "" {
		return slog.LevelInfo, nil
	}
	err := level.UnmarshalText([]byte(s))
	return level, err
}

type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := TraceID(ctx); id != "" {
		r.AddAttrs(slog.String("traceId", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}
