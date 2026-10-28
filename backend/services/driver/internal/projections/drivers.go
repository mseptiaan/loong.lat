package projections

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/live"
)

// pinHashPrefix is how every hash from platform.HashSecret starts.
const pinHashPrefix = "$argon2id$"

// check says why master data can never be applied, as a permanent error: retrying the same event cannot fix it.
// The PIN hash is required when the driver is created and optional on an update, where empty means unchanged.
func check(m drivers.Master, pinRequired bool) error {
	switch {
	case m.ID == "" || strings.TrimSpace(m.Name) == "" || m.Phone == "":
		return eventbus.Permanent(errors.New("driver event without id, name or phone"))
	case (pinRequired || m.PinHash != "") && !strings.HasPrefix(m.PinHash, pinHashPrefix):
		return eventbus.Permanent(errors.New("driver event without an argon2id PIN hash"))
	}
	return nil
}

func (p *Projections) driverProvisioned(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.DriverProvisioned]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	m := drivers.Master{
		ID: e.DriverID, Name: e.Name, Phone: drivers.NormalizePhone(e.Phone), VehicleID: e.VehicleID,
		PinHash: e.PinHash, JoinedAt: e.JoinedAt.Time, Version: env.AggregateVersion,
	}
	if m.JoinedAt.IsZero() {
		m.JoinedAt = env.OccurredAt.Time
	}
	if err := check(m, true); err != nil {
		return err
	}
	return permanentIfPhoneTaken(drivers.Provision(ctx, tx, m))
}

func (p *Projections) driverUpdated(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.DriverUpdated]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	m := drivers.Master{
		ID: e.DriverID, Name: e.Name, Phone: drivers.NormalizePhone(e.Phone), VehicleID: e.VehicleID,
		PinHash: e.PinHash, Version: env.AggregateVersion,
	}
	if err := check(m, false); err != nil {
		return err
	}
	applied, err := drivers.Update(ctx, tx, m)
	switch {
	case errors.Is(err, drivers.ErrNotFound):
		// The provisioning is on its way (same topic, so normally ahead of this): retry.
		return fmt.Errorf("driver %s is not provisioned yet: %w", m.ID, err)
	case err != nil:
		return permanentIfPhoneTaken(err)
	case !applied:
		p.log.DebugContext(ctx, "stale driver.updated dropped", "driverId", m.ID, "version", m.Version)
		return nil
	}
	if m.PinHash != "" {
		// A reset PIN signs the driver out everywhere: whoever held the old one must not stay in.
		if err := platform.NewPgSessionStore(tx).DeleteUser(ctx, m.ID); err != nil {
			return err
		}
	}
	return live.ToDriver(ctx, tx, m.ID, live.FleetChanged)
}

// permanentIfPhoneTaken dead-letters a phone number another driver already has: back office guarantees
// uniqueness, so repeating the event cannot help.
func permanentIfPhoneTaken(err error) error {
	if drivers.IsPhoneTaken(err) {
		return eventbus.Permanent(err)
	}
	return err
}
