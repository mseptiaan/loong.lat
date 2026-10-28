package drivers

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// Message keys of the frontend catalog.
const (
	keyDriverNotFound  = "srv.api.driverNotFound"
	keyFinishActiveJob = "srv.api.finishActiveFirst"
	keyNoVehicle       = "srv.api.noVehicle"
)

// Bounds of a plausible GPS fix.
const (
	maxLat      = 90
	maxLng      = 180
	maxSpeedKmh = 400
)

// Service is what the signed-in driver does with their own account: look at it, go online or offline, and
// report where they are.
type Service struct {
	db  *pgxpool.Pool
	pub *eventbus.Publisher
	now func() time.Time
}

func NewService(db *pgxpool.Pool, pub *eventbus.Publisher, now func() time.Time) *Service {
	return &Service{db: db, pub: pub, now: now}
}

func notFound() error { return platform.NotFound(contracts.Msg{Key: keyDriverNotFound}) }

// Me is driver.me.
func (s *Service) Me(ctx context.Context, id string) (Profile, error) {
	d, err := Get(ctx, s.db, id)
	if errors.Is(err, ErrNotFound) {
		return Profile{}, notFound()
	}
	if err != nil {
		return Profile{}, err
	}
	p := Profile{Driver: d}
	if d.VehicleID != "" {
		if p.Vehicle, err = fleet.Get(ctx, s.db, d.VehicleID); err != nil {
			return Profile{}, err
		}
	}
	return p, nil
}

// SetOnline is driver.setOnline. It publishes driver.online (with the last known position) or driver.offline
// only when the presence actually changes, so a retried call changes and publishes nothing. A driver on a
// running route cannot go offline.
func (s *Service) SetOnline(ctx context.Context, id string, online bool) (Driver, error) {
	at := contracts.At(s.now()).Time
	var out Driver
	err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		d, err := Lock(ctx, tx, id)
		if errors.Is(err, ErrNotFound) {
			return notFound()
		}
		if err != nil {
			return err
		}
		out = d
		switch {
		case online && d.Status != StatusOffline, !online && d.Status == StatusOffline:
			return nil // already there
		case online && d.VehicleID == "":
			return platform.Conflict(contracts.Msg{Key: keyNoVehicle})
		case !online && d.Status == StatusOnDuty:
			return platform.Conflict(contracts.Msg{Key: keyFinishActiveJob})
		}

		status, event := StatusOffline, eventbus.Event{Type: contracts.TypeDriverOffline, Key: id, Data: contracts.DriverOffline{DriverID: id}}
		if online {
			status = StatusOnline
			event = eventbus.Event{Type: contracts.TypeDriverOnline, Key: id, Data: contracts.DriverOnline{DriverID: id, Position: d.Position}}
		}
		if event.AggregateVersion, err = SetPresence(ctx, tx, id, status, at); err != nil {
			return err
		}
		if err := s.pub.Enqueue(ctx, tx, event); err != nil {
			return err
		}
		out.Status, out.LastSeen = status, contracts.At(at)
		return nil
	})
	return out, err
}

// Fix is the body of driver.ping: where the driver is and how fast they move.
type Fix struct {
	Position contracts.LatLng `json:"position"`
	SpeedKmh float64          `json:"speedKmh"`
}

// valid is false for anything outside the globe or a plausible speed, and for NaN, which fails every comparison.
func (f Fix) valid() bool {
	return math.Abs(f.Position.Lat) <= maxLat && math.Abs(f.Position.Lng) <= maxLng &&
		f.SpeedKmh >= 0 && f.SpeedKmh <= maxSpeedKmh
}

// Ping is driver.ping. The fix is stamped with the server's clock; it becomes the driver's latest position and
// is published as driver.position (carrying the running route, if any) only if it is newer than the position
// already held. A fix that lost the race against a newer one is dropped silently.
func (s *Service) Ping(ctx context.Context, id string, fix Fix) error {
	if !fix.valid() {
		return platform.Invalid("position", contracts.Msg{Key: platform.MsgBadRequest})
	}
	at := contracts.At(s.now())
	return platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		stored, err := RecordPosition(ctx, tx, id, fix.Position, at.Time)
		if err != nil || !stored {
			return err
		}
		routeID, err := tasks.RunningRouteID(ctx, tx, id)
		if err != nil {
			return err
		}
		return s.pub.Enqueue(ctx, tx, eventbus.Event{
			Type: contracts.TypeDriverPosition, Key: id, // positions are not versioned: only the newest matters
			Data: contracts.DriverPosition{DriverID: id, RouteID: routeID, Position: fix.Position, SpeedKmh: fix.SpeedKmh, RecordedAt: at},
		})
	})
}
