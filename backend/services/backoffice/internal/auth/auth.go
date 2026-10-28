// Package auth signs admins in and out of the back office. Accounts live in this service's database with only
// an argon2id hash of the password; sessions are the platform's opaque bearer tokens.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
)

const msgBadCredentials = "srv.api.badCredentials"

// minPasswordLen is the shortest password the seeded admin may have.
const minPasswordLen = 8

// Service holds the admin accounts and the sign-in rules.
type Service struct {
	db       *pgxpool.Pool
	sessions *platform.Sessions
	limiter  *platform.LoginLimiter
	// absent is a hash to verify against when the email is unknown, so an unknown email costs as much time as a
	// wrong password and does not reveal which accounts exist.
	absent string
}

func New(db *pgxpool.Pool, sessions *platform.Sessions) (*Service, error) {
	absent, err := platform.HashSecret("no such admin")
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &Service{db: db, sessions: sessions, limiter: platform.NewLoginLimiter(10*time.Second, 5), absent: absent}, nil
}

// LoginResult is the answer of auth.loginAdmin: the session, plus the bearer token the client sends from then
// on. (The frontend's Session has no token field; the HTTP client keeps it from this additive one.)
type LoginResult struct {
	contracts.Session
	Token string `json:"token"`
}

type tokenKey struct{}

// CaptureToken remembers the request's bearer token in its context, which auth.logout needs to end exactly
// that session. It sits next to Sessions.Authenticate, which only keeps who the token belongs to.
func CaptureToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") && token != "" {
			r = r.WithContext(context.WithValue(r.Context(), tokenKey{}, token))
		}
		next.ServeHTTP(w, r)
	})
}

// Mount registers auth.loginAdmin, auth.session and auth.logout.
func (s *Service) Mount(rpc *platform.RPC) {
	type loginIn struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	rpc.Handle("auth.loginAdmin", func(ctx context.Context, in loginIn) (LoginResult, error) {
		return s.Login(ctx, in.Email, in.Password)
	})
	rpc.Handle("auth.session", func(ctx context.Context, _ struct{}) (*contracts.Session, error) {
		p, ok := platform.PrincipalFrom(ctx)
		if !ok {
			return nil, nil
		}
		return &p, nil
	})
	rpc.Handle("auth.logout", func(ctx context.Context, _ struct{}) (struct{}, error) {
		if token, ok := ctx.Value(tokenKey{}).(string); ok {
			return struct{}{}, s.sessions.End(ctx, token)
		}
		return struct{}{}, nil
	})
}

// Login checks the credentials and starts a session. Attempts are limited per email.
func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if ok, retryAfter := s.limiter.Allow(email); !ok {
		return LoginResult{}, platform.TooManyRequests(retryAfter)
	}

	var id, name, hash string
	err := s.db.QueryRow(ctx, `SELECT id, name, password_hash FROM admins WHERE lower(email) = $1`, email).Scan(&id, &name, &hash)
	known := err == nil
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		hash = s.absent
	case err != nil:
		return LoginResult{}, fmt.Errorf("auth: find admin: %w", err)
	}
	match, err := platform.VerifySecret(password, hash)
	if err != nil {
		return LoginResult{}, fmt.Errorf("auth: verify password of %s: %w", id, err)
	}
	if !known || !match {
		return LoginResult{}, platform.Invalid("", contracts.Msg{Key: msgBadCredentials})
	}

	session := contracts.Session{Role: contracts.RoleAdmin, UserID: id, Name: name}
	token, err := s.sessions.Start(ctx, session)
	if err != nil {
		return LoginResult{}, fmt.Errorf("auth: start session: %w", err)
	}
	return LoginResult{Session: session, Token: token}, nil
}

// SeedAdmin creates the admin account of the configuration when no admin has that email. An existing account
// is left alone, password included: changing the password of a seeded admin is not part of this service yet.
// Only the hash of the password is stored; the password itself is never logged.
func (s *Service) SeedAdmin(ctx context.Context, email, password, name string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(password) < minPasswordLen {
		return fmt.Errorf("auth: the admin password must have at least %d characters", minPasswordLen)
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM admins WHERE lower(email) = $1)`, email).Scan(&exists); err != nil {
		return fmt.Errorf("auth: look for admin: %w", err)
	}
	if exists {
		return nil
	}
	hash, err := platform.HashSecret(password)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	// A concurrent start may have created the account since the check: that is the same outcome.
	_, err = s.db.Exec(ctx, `INSERT INTO admins (id, email, name, password_hash) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		apix.NewID("adm"), email, strings.TrimSpace(name), hash)
	if err != nil {
		return fmt.Errorf("auth: create admin: %w", err)
	}
	return nil
}
