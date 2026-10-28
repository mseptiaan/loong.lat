package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// Principal is who a request acts as: the frontend's Session (role, user id, display name).
type Principal = contracts.Session

type principalKey struct{}

// WithPrincipal returns ctx acting as p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the signed-in principal of ctx, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// RequireRole returns the principal if it is signed in with one of roles: 401 when not signed in, 403 when
// signed in with another role.
func RequireRole(ctx context.Context, roles ...contracts.Role) (Principal, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return Principal{}, Unauthenticated()
	}
	if !slices.Contains(roles, p.Role) {
		return Principal{}, Denied(contracts.Msg{Key: MsgRoleDenied})
	}
	return p, nil
}

// TokenHash is the SHA-256 of a session token. Only hashes are stored, so a leaked database cannot be replayed
// as sessions.
type TokenHash [sha256.Size]byte

// NewToken returns a random 256-bit opaque session token (43 URL-safe characters). The token is a bearer
// credential: give it to the client once and keep only HashToken(token).
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// HashToken hashes a token for storage and lookup. A fast hash is right here: the token has 256 bits of entropy.
func HashToken(token string) TokenHash { return sha256.Sum256([]byte(token)) }

// ErrSessionNotFound means no live session matches the token: unknown, signed out, or expired.
var ErrSessionNotFound = errors.New("platform: session not found")

// SessionStore persists sessions by token hash.
type SessionStore interface {
	Create(ctx context.Context, hash TokenHash, p Principal, expiresAt time.Time) error
	// Lookup returns ErrSessionNotFound for an unknown or expired hash.
	Lookup(ctx context.Context, hash TokenHash) (Principal, error)
	Delete(ctx context.Context, hash TokenHash) error
	// DeleteUser signs a user out everywhere, e.g. when the account is blocked.
	DeleteUser(ctx context.Context, userID string) error
}

// Sessions issues and resolves opaque session tokens over a SessionStore.
type Sessions struct {
	store SessionStore
	ttl   time.Duration
	log   *slog.Logger
	now   func() time.Time
}

func NewSessions(store SessionStore, ttl time.Duration, log *slog.Logger) *Sessions {
	return &Sessions{store: store, ttl: ttl, log: log, now: time.Now}
}

// Start creates a session for p and returns its token.
func (s *Sessions) Start(ctx context.Context, p Principal) (string, error) {
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := s.store.Create(ctx, HashToken(token), p, s.now().Add(s.ttl)); err != nil {
		return "", err
	}
	return token, nil
}

// Resolve returns the principal of a token, or ErrSessionNotFound.
func (s *Sessions) Resolve(ctx context.Context, token string) (Principal, error) {
	return s.store.Lookup(ctx, HashToken(token))
}

// End signs out one token.
func (s *Sessions) End(ctx context.Context, token string) error {
	return s.store.Delete(ctx, HashToken(token))
}

// EndAll signs a user out of every session.
func (s *Sessions) EndAll(ctx context.Context, userID string) error {
	return s.store.DeleteUser(ctx, userID)
}

// Authenticate is bearer-token middleware: a valid `Authorization: Bearer <token>` puts the principal in the
// request context. A missing, unknown or expired token leaves the request anonymous rather than failing it, so
// public methods (sign-in) work; handlers that need a user call RequireRole, which answers 401.
func (s *Sessions) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := bearerToken(r); ok {
			switch p, err := s.Resolve(r.Context(), token); {
			case err == nil:
				r = r.WithContext(WithPrincipal(r.Context(), p))
			case !errors.Is(err, ErrSessionNotFound):
				s.log.ErrorContext(r.Context(), "session lookup failed", "error", err)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// MemorySessionStore is a SessionStore in process memory, for tests and single-process development.
type MemorySessionStore struct {
	mu       sync.Mutex
	sessions map[TokenHash]memorySession
	now      func() time.Time
}

type memorySession struct {
	principal Principal
	expiresAt time.Time
}

func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{sessions: map[TokenHash]memorySession{}, now: time.Now}
}

func (m *MemorySessionStore) Create(_ context.Context, hash TokenHash, p Principal, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[hash] = memorySession{principal: p, expiresAt: expiresAt}
	return nil
}

func (m *MemorySessionStore) Lookup(_ context.Context, hash TokenHash) (Principal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[hash]
	if !ok {
		return Principal{}, ErrSessionNotFound
	}
	if !m.now().Before(s.expiresAt) {
		delete(m.sessions, hash)
		return Principal{}, ErrSessionNotFound
	}
	return s.principal, nil
}

func (m *MemorySessionStore) Delete(_ context.Context, hash TokenHash) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, hash)
	return nil
}

func (m *MemorySessionStore) DeleteUser(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, s := range m.sessions {
		if s.principal.UserID == userID {
			delete(m.sessions, hash)
		}
	}
	return nil
}
