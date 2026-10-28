package platform

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// PgIdempotencyStore keeps idempotency keys in the `idempotency_keys` table (see Migrations), so they survive
// restarts and are shared by every replica of a service.
type PgIdempotencyStore struct{ db Querier }

func NewPgIdempotencyStore(db Querier) *PgIdempotencyStore { return &PgIdempotencyStore{db: db} }

func (s *PgIdempotencyStore) Begin(ctx context.Context, scope, key string, fingerprint []byte) (IdempotencyState, StoredResponse, error) {
	// A row that is expired, or claimed but never completed within the lock TTL, is taken over; otherwise
	// the insert conflicts and returns nothing.
	const claim = `
		INSERT INTO idempotency_keys (scope, key, fingerprint, expires_at)
		VALUES ($1, $2, $3, now() + make_interval(secs => $4))
		ON CONFLICT (scope, key) DO UPDATE
		   SET fingerprint = EXCLUDED.fingerprint, status = NULL, content_type = NULL, body = NULL,
		       created_at = now(), expires_at = EXCLUDED.expires_at
		 WHERE idempotency_keys.expires_at <= now()
		    OR (idempotency_keys.status IS NULL AND idempotency_keys.created_at < now() - make_interval(secs => $5))
		RETURNING true`
	const lookup = `SELECT fingerprint, status, content_type, body FROM idempotency_keys WHERE scope = $1 AND key = $2`

	// The row can vanish (Release) between the failed claim and the lookup; then claim again.
	for range 3 {
		var claimed bool
		err := s.db.QueryRow(ctx, claim, scope, key, fingerprint, IdempotencyTTL.Seconds(), IdempotencyLockTTL.Seconds()).Scan(&claimed)
		if err == nil {
			return IdempotencyClaimed, StoredResponse{}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, StoredResponse{}, err
		}

		var (
			stored      []byte
			status      *int32
			contentType *string
			body        []byte
		)
		err = s.db.QueryRow(ctx, lookup, scope, key).Scan(&stored, &status, &contentType, &body)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, StoredResponse{}, err
		}
		switch {
		case !bytes.Equal(stored, fingerprint):
			return IdempotencyMismatch, StoredResponse{}, nil
		case status == nil:
			return IdempotencyInFlight, StoredResponse{}, nil
		}
		resp := StoredResponse{Status: int(*status), Body: body}
		if contentType != nil {
			resp.ContentType = *contentType
		}
		return IdempotencyReplay, resp, nil
	}
	return 0, StoredResponse{}, errors.New("platform: idempotency key kept changing while claiming it")
}

func (s *PgIdempotencyStore) Complete(ctx context.Context, scope, key string, resp StoredResponse) error {
	_, err := s.db.Exec(ctx,
		`UPDATE idempotency_keys SET status = $3, content_type = $4, body = $5 WHERE scope = $1 AND key = $2`,
		scope, key, int32(resp.Status), resp.ContentType, resp.Body)
	return err
}

func (s *PgIdempotencyStore) Release(ctx context.Context, scope, key string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM idempotency_keys WHERE scope = $1 AND key = $2 AND status IS NULL`, scope, key)
	return err
}
