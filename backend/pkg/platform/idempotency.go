package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// IdempotencyState is the outcome of claiming an Idempotency-Key.
type IdempotencyState int

const (
	// IdempotencyClaimed: first sight of the key; the request should run and Complete or Release it.
	IdempotencyClaimed IdempotencyState = iota
	// IdempotencyReplay: the same request already finished; answer with the stored response.
	IdempotencyReplay
	// IdempotencyMismatch: the key was used before for a different request.
	IdempotencyMismatch
	// IdempotencyInFlight: the first request with this key is still running.
	IdempotencyInFlight
)

// StoredResponse is what is replayed for a repeated request.
type StoredResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

// IdempotencyStore remembers keys and the responses they produced. The fingerprint identifies the request
// (method, path, body) so a key reused for something else is detected.
type IdempotencyStore interface {
	Begin(ctx context.Context, scope, key string, fingerprint []byte) (IdempotencyState, StoredResponse, error)
	Complete(ctx context.Context, scope, key string, resp StoredResponse) error
	// Release forgets a claimed key whose request failed, so the client can retry it.
	Release(ctx context.Context, scope, key string) error
}

const (
	// IdempotencyTTL is how long a finished key keeps replaying.
	IdempotencyTTL = 24 * time.Hour
	// IdempotencyLockTTL is how long an unfinished claim blocks retries before it is considered abandoned
	// (the process holding it died).
	IdempotencyLockTTL = time.Minute

	idempotencyHeader = "Idempotency-Key"
	maxIdempotencyKey = 255
	rpcPathPrefix     = "/v1/rpc/"
)

// Idempotency makes retried POSTs safe: a request carrying an `Idempotency-Key` header runs once, and repeats
// with the same key and body get the first response back (with `Idempotent-Replayed: true`). A key is scoped to
// the signed-in user and the path. Methods named in required (e.g. "plans.confirm", "wallet.createTopUp",
// the money operations) are rejected without the header. Server errors are not remembered, so those can be retried.
// Put it after Sessions.Authenticate.
func Idempotency(store IdempotencyStore, required ...string) Middleware {
	requiredPaths := make(map[string]bool, len(required))
	for _, method := range required {
		requiredPaths[rpcPathPrefix+method] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(idempotencyHeader)
			if r.Method != http.MethodPost || (key == "" && !requiredPaths[r.URL.Path]) {
				next.ServeHTTP(w, r)
				return
			}
			if key == "" || len(key) > maxIdempotencyKey {
				writeError(w, Invalid(idempotencyHeader, contracts.Msg{Key: MsgIdemRequired}))
				return
			}
			serveIdempotent(store, next, w, r, key)
		})
	}
}

func serveIdempotent(store IdempotencyStore, next http.Handler, w http.ResponseWriter, r *http.Request, key string) {
	ctx := r.Context()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, DefaultMaxBodyBytes))
	if err != nil {
		writeError(w, Invalid("", contracts.Msg{Key: MsgBadRequest}).WithCause(err))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	scope := "anonymous " + r.URL.Path
	if p, ok := PrincipalFrom(ctx); ok {
		scope = string(AudienceOf(p)) + " " + r.URL.Path
	}
	h := sha256.New()
	h.Write([]byte(r.Method + "\n" + r.URL.Path + "\n"))
	h.Write(body)

	state, stored, err := store.Begin(ctx, scope, key, h.Sum(nil))
	if err != nil {
		writeError(w, internalError(TraceID(ctx)).WithCause(err))
		return
	}
	switch state {
	case IdempotencyReplay:
		if stored.ContentType != "" {
			w.Header().Set("Content-Type", stored.ContentType)
		}
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(stored.Status)
		_, _ = w.Write(stored.Body)
		return
	case IdempotencyMismatch:
		writeError(w, Invalid(idempotencyHeader, contracts.Msg{Key: MsgIdemReused}))
		return
	case IdempotencyInFlight:
		writeError(w, Conflict(contracts.Msg{Key: MsgIdemInFlight}))
		return
	}

	// Claimed: run the request, record what it answered, and release the key if it did not finish cleanly.
	finished := false
	defer func() {
		if !finished {
			_ = store.Release(context.WithoutCancel(ctx), scope, key)
		}
	}()
	rec := &recordingWriter{ResponseWriter: w}
	next.ServeHTTP(rec, r)
	if rec.status() >= http.StatusInternalServerError {
		return
	}
	resp := StoredResponse{Status: rec.status(), ContentType: rec.Header().Get("Content-Type"), Body: rec.body.Bytes()}
	if err := store.Complete(context.WithoutCancel(ctx), scope, key, resp); err == nil {
		finished = true
	}
}

// recordingWriter passes a response through while keeping a copy of it.
type recordingWriter struct {
	http.ResponseWriter
	code int
	body bytes.Buffer
}

func (w *recordingWriter) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

func (w *recordingWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *recordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// MemoryIdempotencyStore is an IdempotencyStore in process memory, for tests and single-process development.
type MemoryIdempotencyStore struct {
	mu      sync.Mutex
	entries map[[2]string]*memoryIdempotency
	now     func() time.Time
}

type memoryIdempotency struct {
	fingerprint []byte
	response    *StoredResponse // nil while the first request runs
	claimedAt   time.Time
	expiresAt   time.Time
}

func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{entries: map[[2]string]*memoryIdempotency{}, now: time.Now}
}

func (m *MemoryIdempotencyStore) Begin(_ context.Context, scope, key string, fingerprint []byte) (IdempotencyState, StoredResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	id := [2]string{scope, key}
	if e, ok := m.entries[id]; ok {
		abandoned := e.response == nil && now.Sub(e.claimedAt) > IdempotencyLockTTL
		if !abandoned && now.Before(e.expiresAt) {
			switch {
			case !bytes.Equal(e.fingerprint, fingerprint):
				return IdempotencyMismatch, StoredResponse{}, nil
			case e.response == nil:
				return IdempotencyInFlight, StoredResponse{}, nil
			default:
				return IdempotencyReplay, *e.response, nil
			}
		}
	}
	m.entries[id] = &memoryIdempotency{fingerprint: fingerprint, claimedAt: now, expiresAt: now.Add(IdempotencyTTL)}
	return IdempotencyClaimed, StoredResponse{}, nil
}

func (m *MemoryIdempotencyStore) Complete(_ context.Context, scope, key string, resp StoredResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[[2]string{scope, key}]; ok {
		e.response = &resp
	}
	return nil
}

func (m *MemoryIdempotencyStore) Release(_ context.Context, scope, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := [2]string{scope, key}
	if e, ok := m.entries[id]; ok && e.response == nil {
		delete(m.entries, id)
	}
	return nil
}
