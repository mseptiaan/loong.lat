package projections

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/live"
)

func (p *Projections) vehicleUpserted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.VehicleUpserted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if e.Vehicle.ID == "" {
		return eventbus.Permanent(errors.New("vehicle.upserted without vehicle id"))
	}
	applied, err := fleet.Upsert(ctx, tx, e.Vehicle, env.AggregateVersion)
	return p.vehicleChanged(ctx, tx, e.Vehicle.ID, applied, err)
}

func (p *Projections) vehicleRemoved(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.VehicleRemoved]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if e.VehicleID == "" {
		return eventbus.Permanent(errors.New("vehicle.removed without vehicle id"))
	}
	applied, err := fleet.Remove(ctx, tx, e.VehicleID, env.AggregateVersion)
	return p.vehicleChanged(ctx, tx, e.VehicleID, applied, err)
}

// vehicleChanged tells the driver who has the vehicle, if the change was applied.
func (p *Projections) vehicleChanged(ctx context.Context, tx pgx.Tx, vehicleID string, applied bool, err error) error {
	if err != nil || !applied {
		return err
	}
	driverID, err := drivers.ByVehicle(ctx, tx, vehicleID)
	if errors.Is(err, drivers.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return live.ToDriver(ctx, tx, driverID, live.FleetChanged)
}
