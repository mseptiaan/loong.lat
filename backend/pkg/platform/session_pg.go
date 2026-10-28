package platform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// PgSessionStore keeps sessions in the `sessions` table (see Migrations).
type PgSessionStore struct{ db Querier }

func NewPgSessionStore(db Querier) *PgSessionStore { return &PgSessionStore{db: db} }

func (s *PgSessionStore) Create(ctx context.Context, hash TokenHash, p Principal, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO sessions (token_hash, role, user_id, name, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		hash[:], string(p.Role), p.UserID, p.Name, expiresAt)
	return err
}

func (s *PgSessionStore) Lookup(ctx context.Context, hash TokenHash) (Principal, error) {
	var role string
	var p Principal
	err := s.db.QueryRow(ctx,
		`SELECT role, user_id, name FROM sessions WHERE token_hash = $1 AND expires_at > now()`, hash[:]).
		Scan(&role, &p.UserID, &p.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrSessionNotFound
	}
	p.Role = contracts.Role(role)
	return p, err
}

func (s *PgSessionStore) Delete(ctx context.Context, hash TokenHash) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash[:])
	return err
}

func (s *PgSessionStore) DeleteUser(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}
