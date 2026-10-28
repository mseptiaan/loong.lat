// Package fleet is the console's copy of the vehicles back office offers (loong.fleet.v1). Quotes are priced
// with them; the console never changes them.
package fleet

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Projection holds the vehicles seen on the fleet topic.
type Projection struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Projection { return &Projection{pool: pool} }

// Register handles the fleet topic's events. The topic is compacted, so the last event of a vehicle is its
// state: each event replaces what was there, except one that is older than the stored state (a redelivery
// after a newer event) by its aggregate version.
func (p *Projection) Register(c *eventbus.Consumer) {
	c.Handle(contracts.TypeVehicleUpserted, upsert)
	c.Handle(contracts.TypeVehicleRemoved, remove)
}

func upsert(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.VehicleUpserted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.Vehicle.ID == "" {
		return eventbus.Permanent(errors.New("fleet: vehicle.upserted without a vehicle id"))
	}
	vehicle, err := json.Marshal(ev.Vehicle)
	if err != nil {
		return eventbus.Permanent(err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO fleet_vehicles (id, vehicle, status, removed, version, updated_at)
		 VALUES ($1, $2, $3, false, $4, $5)
		 ON CONFLICT (id) DO UPDATE
		    SET vehicle = EXCLUDED.vehicle, status = EXCLUDED.status, removed = false,
		        version = EXCLUDED.version, updated_at = EXCLUDED.updated_at
		  WHERE fleet_vehicles.version <= EXCLUDED.version`,
		ev.Vehicle.ID, vehicle, string(ev.Vehicle.Status), env.AggregateVersion, env.OccurredAt.Time)
	return err
}

func remove(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.VehicleRemoved]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.VehicleID == "" {
		return eventbus.Permanent(errors.New("fleet: vehicle.removed without a vehicle id"))
	}
	// The row is kept as a tombstone, and created if the removal is the first thing seen of the vehicle.
	_, err = tx.Exec(ctx,
		`INSERT INTO fleet_vehicles (id, vehicle, status, removed, version, updated_at)
		 VALUES ($1, '{}', $2, true, $3, $4)
		 ON CONFLICT (id) DO UPDATE
		    SET removed = true, status = EXCLUDED.status, version = EXCLUDED.version, updated_at = EXCLUDED.updated_at
		  WHERE fleet_vehicles.version <= EXCLUDED.version`,
		ev.VehicleID, string(contracts.VehicleStatusInactive), env.AggregateVersion, env.OccurredAt.Time)
	return err
}

// Active returns the vehicles a quote may use: present and in service.
func (p *Projection) Active(ctx context.Context) ([]contracts.Vehicle, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT vehicle FROM fleet_vehicles WHERE NOT removed AND status = $1 ORDER BY id`, string(contracts.VehicleStatusActive))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vehicles []contracts.Vehicle
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v contracts.Vehicle
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("fleet: stored vehicle: %w", err)
		}
		vehicles = append(vehicles, v)
	}
	return vehicles, rows.Err()
}

// Plate is the licence plate of a vehicle, and false when the vehicle is not (or no longer) known.
func Plate(ctx context.Context, db platform.Querier, vehicleID string) (plate string, ok bool, err error) {
	var raw []byte
	err = db.QueryRow(ctx, `SELECT vehicle FROM fleet_vehicles WHERE id = $1 AND NOT removed`, vehicleID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var v contracts.Vehicle
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false, fmt.Errorf("fleet: stored vehicle: %w", err)
	}
	return v.Plate, true, nil
}
