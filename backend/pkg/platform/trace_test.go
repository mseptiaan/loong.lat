package platform

import (
	"bytes"
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var line map[string]any
		if err := json.Unmarshal(raw, &line); err != nil {
			t.Fatalf("log line %q is not JSON: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestLoggerWritesJSONWithServiceAndTraceID(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "console-api", slog.LevelInfo)

	log.InfoContext(WithTraceID(t.Context(), "4bf92f3577b34da6a3ce929d0e0e4736"), "plan confirmed", "planId", "plan-1")
	log.With("component", "wallet").InfoContext(WithTraceID(t.Context(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), "charged")
	log.Info("no context")
	log.Debug("below the level")

	lines := logLines(t, &buf)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (debug is below the level): %s", len(lines), buf.String())
	}
	if lines[0]["service"] != "console-api" || lines[0]["traceId"] != "4bf92f3577b34da6a3ce929d0e0e4736" || lines[0]["planId"] != "plan-1" || lines[0]["msg"] != "plan confirmed" {
		t.Errorf("first line = %v", lines[0])
	}
	if lines[1]["traceId"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || lines[1]["component"] != "wallet" {
		t.Errorf("a derived logger must keep stamping the trace id: %v", lines[1])
	}
	if _, has := lines[2]["traceId"]; has {
		t.Errorf("no trace in the context, no traceId field: %v", lines[2])
	}
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{"": slog.LevelInfo, "debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	for in, want := range tests {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("an unknown level must be an error")
	}
}

func TestTraceMiddleware(t *testing.T) {
	const incoming = "4bf92f3577b34da6a3ce929d0e0e4736"
	tests := []struct {
		name, header string
		wantSame     bool
	}{
		{"adopts a valid traceparent", "00-" + incoming + "-00f067aa0ba902b7-01", true},
		{"ignores a malformed one", "00-nonsense-00f067aa0ba902b7-01", false},
		{"ignores an all-zero trace id", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", false},
		{"generates one when absent", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			h := TraceMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = TraceID(r.Context()) }))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Traceparent", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if !contracts.ValidTraceID(seen) {
				t.Fatalf("handler saw trace id %q", seen)
			}
			if (seen == incoming) != tt.wantSame {
				t.Errorf("seen %q, adopt incoming = %v", seen, tt.wantSame)
			}
			if rec.Header().Get("X-Trace-Id") != seen {
				t.Errorf("X-Trace-Id = %q, want %q", rec.Header().Get("X-Trace-Id"), seen)
			}
		})
	}
}

func TestNewTraceIDIsValidAndUnique(t *testing.T) {
	a, b := NewTraceID(), NewTraceID()
	if !contracts.ValidTraceID(a) || a == b {
		t.Errorf("NewTraceID gave %q and %q", a, b)
	}
}
