// Package auth is the console's sign-in: registering shippers, checking passwords, and handing out sessions.
// Credentials never leave this service; other services only learn that a customer exists (customer.registered).
package auth

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Sign-in throttling: a burst of attempts per account, then one every loginRefill.
const (
	loginBurst  = 5
	loginRefill = 12 * time.Second
)

const (
	minPasswordRunes = 8
	minPhoneDigits   = 9
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// RegisterInput is the frontend's RegisterInput.
type RegisterInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

// SignedIn is what registering and signing in answer: the frontend's Session plus the bearer token the client
// sends from then on. The token is shown once; only its hash is stored.
type SignedIn struct {
	contracts.Session
	Token string `json:"token"`
}

// Service registers customers and signs them in.
type Service struct {
	pool     *pgxpool.Pool
	pub      *eventbus.Publisher
	sessions *platform.Sessions
	limiter  *platform.LoginLimiter
	// missingHash is verified against when the email is unknown, so an unknown account takes as long to
	// refuse as a wrong password does and sign-in does not reveal which emails are registered.
	missingHash string
	now         func() time.Time
}

// New returns the service. It hashes one throwaway password, so it takes a moment.
func New(pool *pgxpool.Pool, pub *eventbus.Publisher, sessions *platform.Sessions) (*Service, error) {
	missing, err := platform.HashSecret("no such account")
	if err != nil {
		return nil, err
	}
	return &Service{
		pool: pool, pub: pub, sessions: sessions, missingHash: missing,
		limiter: platform.NewLoginLimiter(loginRefill, loginBurst), now: time.Now,
	}, nil
}

// Register creates the customer and their empty wallet, announces customer.registered, and signs them in.
func (s *Service) Register(ctx context.Context, in RegisterInput) (SignedIn, error) {
	name, email, phone := strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Email)), normalizePhone(in.Phone)
	switch {
	case name == "":
		return SignedIn{}, platform.Invalid("name", contracts.Msg{Key: "srv.api.nameEmpty"})
	case !emailPattern.MatchString(email):
		return SignedIn{}, platform.Invalid("email", contracts.Msg{Key: "srv.api.emailInvalid"})
	case len(phone) < minPhoneDigits:
		return SignedIn{}, platform.Invalid("phone", contracts.Msg{Key: "srv.api.phoneIncomplete"})
	case utf8.RuneCountInString(in.Password) < minPasswordRunes:
		return SignedIn{}, platform.Invalid("password", contracts.Msg{Key: "srv.api.passwordShort"})
	}
	hash, err := platform.HashSecret(in.Password)
	if err != nil {
		return SignedIn{}, err
	}

	id, now := "cus-"+uuid.NewV7().String(), s.now()
	err = platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO customers (id, name, email, phone, password_hash, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, name, email, phone, hash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, id); err != nil {
			return err
		}
		return s.pub.Enqueue(ctx, tx, eventbus.Event{
			Type: contracts.TypeCustomerRegistered, Key: id, AggregateVersion: 1,
			Data: contracts.CustomerRegistered{CustomerID: id, Name: name, Email: email, Phone: phone, CreatedAt: contracts.At(now)},
		})
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrUniqueViolation {
		taken := platform.Conflict(contracts.Msg{Key: "srv.api.emailTaken"})
		taken.Field = "email"
		return SignedIn{}, taken.WithCause(err)
	}
	if err != nil {
		return SignedIn{}, err
	}
	return s.signIn(ctx, id, name)
}

// pgerrUniqueViolation is Postgres' SQLSTATE for a unique constraint; the email is the only unique input.
const pgerrUniqueViolation = "23505"

// Login checks the password and starts a session. Failures for an unknown email and a wrong password are
// the same answer, and attempts are throttled per email.
func (s *Service) Login(ctx context.Context, email, password string) (SignedIn, error) {
	key := strings.ToLower(strings.TrimSpace(email))
	if ok, wait := s.limiter.Allow(key); !ok {
		return SignedIn{}, platform.TooManyRequests(wait)
	}
	var (
		id, name, hash string
		blocked        bool
	)
	err := s.pool.QueryRow(ctx, `SELECT id, name, password_hash, blocked FROM customers WHERE lower(email) = $1`, key).Scan(&id, &name, &hash, &blocked)
	known := err == nil
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		hash = s.missingHash
	case err != nil:
		return SignedIn{}, err
	}
	match, err := platform.VerifySecret(password, hash)
	if err != nil {
		return SignedIn{}, err
	}
	if !known || !match {
		return SignedIn{}, platform.Invalid("", contracts.Msg{Key: "srv.api.badCredentials"})
	}
	// Said only to someone who proved they own the account, so it does not reveal which emails are registered.
	if blocked {
		return SignedIn{}, platform.Denied(contracts.Msg{Key: "srv.api.accountBlocked"})
	}
	return s.signIn(ctx, id, name)
}

func (s *Service) signIn(ctx context.Context, id, name string) (SignedIn, error) {
	session := contracts.Session{Role: contracts.RoleCustomer, UserID: id, Name: name}
	token, err := s.sessions.Start(ctx, session)
	if err != nil {
		return SignedIn{}, err
	}
	return SignedIn{Session: session, Token: token}, nil
}

// Logout ends the session of the request's token. Without one there is nothing to end.
func (s *Service) Logout(ctx context.Context) error {
	token, ok := ctx.Value(tokenKey{}).(string)
	if !ok {
		return nil
	}
	return s.sessions.End(ctx, token)
}

// EnsureActive refuses a customer back office has blocked (403 ditolak). Whoever starts something for the
// customer (a plan, a top-up) asks it inside its transaction, so a session that outlived the block, or was
// started in the same instant, still cannot act.
func EnsureActive(ctx context.Context, q platform.Querier, customerID string) error {
	var blocked bool
	if err := q.QueryRow(ctx, `SELECT blocked FROM customers WHERE id = $1`, customerID).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return platform.Denied(contracts.Msg{Key: "srv.ops.err.accountBlocked"})
	}
	return nil
}

// Customer is the signed-in customer's id; any other caller is refused (401 anonymous, 403 another role).
func Customer(ctx context.Context) (string, error) {
	p, err := platform.RequireRole(ctx, contracts.RoleCustomer)
	return p.UserID, err
}

// AsCustomer adapts an operation done for the signed-in customer to an RPC handler: callers who are not
// customers are refused before it runs.
func AsCustomer[In, Out any](op func(ctx context.Context, customerID string, in In) (Out, error)) func(context.Context, In) (Out, error) {
	return func(ctx context.Context, in In) (Out, error) {
		customerID, err := Customer(ctx)
		if err != nil {
			var none Out
			return none, err
		}
		return op(ctx, customerID, in)
	}
}

type tokenKey struct{}

// TokenMiddleware makes the request's bearer token available to Logout, which has no other way to learn which
// session to end: platform.Principal carries who the caller is, not the token they proved it with.
func TokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") && token != "" {
			r = r.WithContext(context.WithValue(r.Context(), tokenKey{}, token))
		}
		next.ServeHTTP(w, r)
	})
}

// NormalizePhone is the form phone numbers are stored in: the digits, with an Indonesian +62 number written
// in its local 0-prefixed form.
func NormalizePhone(s string) string { return normalizePhone(s) }

// ValidEmail reports whether s (already trimmed and lower-cased) looks like an email address.
func ValidEmail(s string) bool { return emailPattern.MatchString(s) }

// PhoneComplete reports whether a normalized phone number has enough digits to be one.
func PhoneComplete(normalized string) bool { return len(normalized) >= minPhoneDigits }

// normalizePhone keeps the digits and writes an Indonesian +62 number in its local 0-prefixed form.
func normalizePhone(s string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if rest, ok := strings.CutPrefix(digits, "62"); ok {
		return "0" + rest
	}
	return digits
}
