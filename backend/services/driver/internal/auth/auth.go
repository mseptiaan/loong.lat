// Package auth signs drivers in and out: phone number and PIN against the hash back office provisioned, a
// throttle per phone number, and opaque session tokens.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
)

const keyBadCredentials = "srv.api.driverBadCredentials"

// unknownPhoneHash is a well-formed argon2id hash (default cost) of a secret nobody knows. An unknown phone
// number is verified against it, so it costs as much time as a wrong PIN and the answer does not tell which
// phone numbers have an account.
const unknownPhoneHash = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNo"

// Login is the result of auth.loginDriver: the frontend's Session plus the bearer token the client sends from
// then on (the one addition to the frontend's contract, which had sessions without tokens).
type Login struct {
	contracts.Session
	Token string `json:"token"`
}

type Service struct {
	db       *pgxpool.Pool
	sessions *platform.Sessions
	limiter  *platform.LoginLimiter
}

func NewService(db *pgxpool.Pool, sessions *platform.Sessions, limiter *platform.LoginLimiter) *Service {
	return &Service{db: db, sessions: sessions, limiter: limiter}
}

// Login is auth.loginDriver. Attempts are throttled per phone number before anything expensive happens.
func (s *Service) Login(ctx context.Context, phone, pin string) (Login, error) {
	key := drivers.NormalizePhone(phone)
	if ok, wait := s.limiter.Allow(key); !ok {
		return Login{}, platform.TooManyRequests(wait)
	}

	account, err := drivers.FindAccount(ctx, s.db, key)
	hash := account.PinHash
	switch {
	case errors.Is(err, drivers.ErrNotFound):
		hash = unknownPhoneHash
	case err != nil:
		return Login{}, err
	}
	match, err := platform.VerifySecret(pin, hash)
	if err != nil {
		return Login{}, fmt.Errorf("auth: driver %s: %w", account.ID, err)
	}
	if !match || account.ID == "" {
		return Login{}, platform.Invalid("", contracts.Msg{Key: keyBadCredentials})
	}

	session := contracts.Session{Role: contracts.RoleDriver, UserID: account.ID, Name: account.Name}
	token, err := s.sessions.Start(ctx, session)
	if err != nil {
		return Login{}, err
	}
	return Login{Session: session, Token: token}, nil
}

// Logout is auth.logout: it ends the session whose token made the request, and does nothing when there is none.
func (s *Service) Logout(ctx context.Context) error {
	token, ok := ctx.Value(tokenKey{}).(string)
	if !ok {
		return nil
	}
	return s.sessions.End(ctx, token)
}

// Current is auth.session: who the request acts as, or nil when nobody is signed in.
func Current(ctx context.Context) *contracts.Session {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil
	}
	return &p
}

type tokenKey struct{}

// RememberToken is middleware that keeps the request's bearer token in its context, which Logout needs to end
// that one session (platform resolves the token to a principal but does not hand it on). It runs outside
// Sessions.Authenticate or inside it, either way.
func RememberToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") && token != "" {
			r = r.WithContext(context.WithValue(r.Context(), tokenKey{}, token))
		}
		next.ServeHTTP(w, r)
	})
}
