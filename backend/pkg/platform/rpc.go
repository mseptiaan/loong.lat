package platform

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// DefaultMaxBodyBytes bounds a request body; image uploads for the AI methods are the largest callers.
const DefaultMaxBodyBytes = 4 << 20

// RPC serves the frontend's ApiClient methods as `POST /v1/rpc/{method}` (method names like "plans.get").
// The request body is one JSON value decoded into the handler's input type (empty body = zero value); the
// response is the JSON of its output. Errors use the frontend's WireError shape and HTTP statuses (see APIError).
// Authentication is not part of it: wrap the mux with Sessions.Authenticate and have handlers call RequireRole.
type RPC struct {
	log     *slog.Logger
	maxBody int64
	methods map[string]http.HandlerFunc
}

func NewRPC(log *slog.Logger) *RPC {
	return &RPC{log: log, maxBody: DefaultMaxBodyBytes, methods: map[string]http.HandlerFunc{}}
}

// Handle registers a request/response method. It panics on a duplicate name: that is a wiring bug.
func (r *RPC) Handle[In, Out any](method string, fn func(ctx context.Context, in In) (Out, error)) {
	r.register(method, func(w http.ResponseWriter, req *http.Request) {
		in, err := decodeBody[In](w, req, r.maxBody)
		if err != nil {
			r.fail(w, req, method, err)
			return
		}
		out, err := fn(req.Context(), in)
		if err != nil {
			r.fail(w, req, method, err)
			return
		}
		body, err := json.Marshal(out)
		if err != nil {
			r.fail(w, req, method, fmt.Errorf("encode response: %w", err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

// Stream registers a streaming method (the frontend's STREAM_METHODS) answered as NDJSON: one JSON event per
// line, flushed as it is emitted, so the client sees `progress` events while the work runs. emit returns an
// error once the client is gone; the handler should stop then. An error returned before the first event is a
// normal error response. After streaming has begun it is sent as a final line {"type":"error","error":WireError}.
func (r *RPC) Stream[In, Ev any](method string, fn func(ctx context.Context, in In, emit func(Ev) error) error) {
	r.register(method, func(w http.ResponseWriter, req *http.Request) {
		in, err := decodeBody[In](w, req, r.maxBody)
		if err != nil {
			r.fail(w, req, method, err)
			return
		}
		rc := http.NewResponseController(w)
		started := false
		start := func() {
			if started {
				return
			}
			started = true
			h := w.Header()
			h.Set("Content-Type", "application/x-ndjson")
			h.Set("Cache-Control", "no-cache")
			h.Set("X-Accel-Buffering", "no") // tell proxies not to buffer
			w.WriteHeader(http.StatusOK)
		}
		writeLine := func(v any) error {
			line, err := json.Marshal(v)
			if err != nil {
				return err
			}
			start()
			if _, err := w.Write(append(line, '\n')); err != nil {
				return err
			}
			if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
				return err
			}
			return nil
		}

		err = fn(req.Context(), in, func(ev Ev) error { return writeLine(ev) })
		switch {
		case err == nil:
			start() // an empty stream is still a successful one
		case !started:
			r.fail(w, req, method, err)
		default:
			apiErr := r.resolve(req, method, err)
			_ = writeLine(struct {
				Type  string    `json:"type"`
				Error wireError `json:"error"`
			}{"error", apiErr.wire()})
		}
	})
}

// Mount registers the endpoint on mux.
func (r *RPC) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/rpc/{method}", func(w http.ResponseWriter, req *http.Request) {
		method := req.PathValue("method")
		handler, ok := r.methods[method]
		if !ok {
			writeError(w, NotFound(contracts.Msg{Key: MsgUnknownMethod, Params: map[string]any{"method": method}}))
			return
		}
		handler(w, req)
	})
}

func (r *RPC) register(method string, h http.HandlerFunc) {
	if method == "" {
		panic("platform: RPC method name is empty")
	}
	if _, dup := r.methods[method]; dup {
		panic("platform: RPC method registered twice: " + method)
	}
	r.methods[method] = h
}

// resolve maps err to the APIError to send and logs what the client must not see.
func (r *RPC) resolve(req *http.Request, method string, err error) *APIError {
	apiErr, unexpected := resolveError(err, TraceID(req.Context()))
	switch {
	case unexpected:
		r.log.ErrorContext(req.Context(), "rpc method failed", "method", method, "error", err)
	case apiErr.Unwrap() != nil:
		r.log.WarnContext(req.Context(), "rpc method rejected", "method", method, "code", apiErr.Code, "error", apiErr)
	}
	return apiErr
}

func (r *RPC) fail(w http.ResponseWriter, req *http.Request, method string, err error) {
	writeError(w, r.resolve(req, method, err))
}

// decodeBody reads the request body as one JSON value of type In. Unknown fields are ignored, as in events.
func decodeBody[In any](w http.ResponseWriter, r *http.Request, limit int64) (In, error) {
	var in In
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		apiErr := Invalid("", contracts.Msg{Key: MsgBadRequest})
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			apiErr.Status = http.StatusRequestEntityTooLarge
		}
		return in, apiErr.WithCause(err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return in, nil
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return in, Invalid("", contracts.Msg{Key: MsgBadRequest}).WithCause(err)
	}
	return in, nil
}
