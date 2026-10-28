// Package fleet keeps the projection of the vehicles back office publishes on loong.fleet.v1; the app shows the
// driver's own vehicle with it.
package fleet

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Upsert stores the vehicle. It reports false when a newer version is already held. Equal versions apply: the
// fleet topic is keyed by vehicle id and so ordered per vehicle, and a producer that never raises the version
// must still get its updates through.
func Upsert(ctx context.Context, q platform.Querier, v contracts.Vehicle, version int64) (applied bool, err error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return false, fmt.Errorf("fleet: encode vehicle: %w", err)
	}
	tag, err := q.Exec(ctx,
		`INSERT INTO vehicles (id, vehicle, version) VALUES ($1, $2, $3)
		 ON CONFLICT (id) DO UPDATE SET vehicle = EXCLUDED.vehicle, version = EXCLUDED.version
		 WHERE vehicles.version <= EXCLUDED.version`,
		v.ID, raw, version)
	return tag.RowsAffected() == 1, err
}

// Remove drops the vehicle's snapshot, with the same version rule as Upsert.
func Remove(ctx context.Context, q platform.Querier, id string, version int64) (applied bool, err error) {
	tag, err := q.Exec(ctx,
		`INSERT INTO vehicles (id, vehicle, version) VALUES ($1, NULL, $2)
		 ON CONFLICT (id) DO UPDATE SET vehicle = NULL, version = EXCLUDED.version
		 WHERE vehicles.version <= EXCLUDED.version`,
		id, version)
	return tag.RowsAffected() == 1, err
}

// Get returns the vehicle, or nil when it is unknown or was removed.
func Get(ctx context.Context, q platform.Querier, id string) (*contracts.Vehicle, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT vehicle FROM vehicles WHERE id = $1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && raw == nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v contracts.Vehicle
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("fleet: decode vehicle %s: %w", id, err)
	}
	return &v, nil
}
