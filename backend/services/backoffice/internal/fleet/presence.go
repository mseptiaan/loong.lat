package fleet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
)

// Online (loong.driver.v1) records that a driver went online, with the position he reported if any.
func (s *Service) Online(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.DriverOnline]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	_, err = s.setPresence(ctx, tx, env, ev.DriverID, true, ev.Position)
	return err
}

// Offline (loong.driver.v1) records that a driver went offline.
func (s *Service) Offline(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	_, _, err := s.ApplyOffline(ctx, tx, env)
	return err
}

// ApplyOffline is Offline for a caller that has more to do when a driver really went offline (his jobs go back to
// the queue): it reports the driver and whether the event changed anything, which it does not when a newer
// presence event of his was applied already. The driver's row stays locked until tx ends.
func (s *Service) ApplyOffline(ctx context.Context, tx pgx.Tx, env contracts.Envelope) (driverID string, applied bool, err error) {
	ev, err := env.Payload[contracts.DriverOffline]()
	if err != nil {
		return "", false, eventbus.Permanent(err)
	}
	applied, err = s.setPresence(ctx, tx, env, ev.DriverID, false, nil)
	return ev.DriverID, applied, err
}

// setPresence applies an online/offline event unless the driver's presence is already at that version or a
// newer one, and reports whether it did. Drivers are created here first, so an event about an unknown one can
// never become valid.
func (s *Service) setPresence(ctx context.Context, tx pgx.Tx, env contracts.Envelope, driverID string, online bool, pos *contracts.LatLng) (bool, error) {
	var seen int64
	err := tx.QueryRow(ctx, `SELECT presence_version FROM drivers WHERE id = $1 FOR UPDATE`, driverID).Scan(&seen)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, eventbus.Permanent(fmt.Errorf("%s: unknown driver %q", env.Type, driverID))
	}
	if err != nil {
		return false, wrap("read presence", err)
	}
	if env.AggregateVersion <= seen {
		return false, nil
	}
	at := env.OccurredAt.Time
	_, err = tx.Exec(ctx, `UPDATE drivers SET online = $2, presence_version = $3, last_seen = GREATEST(last_seen, $4) WHERE id = $1`,
		driverID, online, env.AggregateVersion, at)
	if err != nil {
		return false, wrap("set presence", err)
	}
	if pos != nil {
		if err := validPosition(*pos); err != nil {
			return false, eventbus.Permanent(err)
		}
		if _, err := setPosition(ctx, tx, driverID, *pos, at); err != nil {
			return false, err
		}
	}
	s.feed.Fleet()
	return true, nil
}

// Position (loong.driver.position.v1) keeps the latest fix of a driver. Fixes are not versioned; an older
// fix than the one held is dropped by its recording time.
func (s *Service) Position(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.DriverPosition]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if err := validPosition(ev.Position); err != nil {
		return eventbus.Permanent(err)
	}
	if ev.DriverID == "" || ev.RecordedAt.IsZero() {
		return eventbus.Permanent(errors.New("driver.position: no driver id or recording time"))
	}
	moved, err := setPosition(ctx, tx, ev.DriverID, ev.Position, ev.RecordedAt.Time)
	if moved {
		s.feed.Fleet()
	}
	return err
}

func validPosition(p contracts.LatLng) error {
	if p.Lat < -90 || p.Lat > 90 || p.Lng < -180 || p.Lng > 180 {
		return fmt.Errorf("position %v,%v is not on the globe", p.Lat, p.Lng)
	}
	return nil
}

// setPosition stores a fix recorded at `at` unless a newer one is held; it reports whether it stored it. A fix
// for a driver we do not know is ignored.
func setPosition(ctx context.Context, tx pgx.Tx, driverID string, pos contracts.LatLng, at time.Time) (bool, error) {
	tag, err := tx.Exec(ctx,
		`UPDATE drivers SET lat = $2, lng = $3, position_at = $4, last_seen = GREATEST(last_seen, $4)
		  WHERE id = $1 AND (position_at IS NULL OR position_at < $4)`, driverID, pos.Lat, pos.Lng, at)
	if err != nil {
		return false, wrap("set position", err)
	}
	return tag.RowsAffected() > 0, nil
}
