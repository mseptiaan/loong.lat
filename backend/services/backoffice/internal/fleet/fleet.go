// Package fleet owns the fleet master data (vehicles, drivers) and what is known about where drivers are. It
// announces every change of the master data on the compacted fleet topic and the driver topic, and learns
// driver-api's online/offline and position events.
package fleet

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/pg"
)

// Status is a driver's status as the console shows it (the frontend's DriverStatus).
type Status string

const (
	StatusOnline  Status = "online"
	StatusOnDuty  Status = "bertugas"
	StatusOffline Status = "offline"
)

// Driver is the frontend's Driver. Status is derived: driver-api reports only online or offline; a driver
// whose assigned route departs soon, or is under way, is on duty whatever he last reported.
type Driver struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Phone          string            `json:"phone"`
	Rating         float64           `json:"rating"`
	VehicleID      string            `json:"vehicleId,omitempty"`
	Status         Status            `json:"status"`
	Position       *contracts.LatLng `json:"position,omitempty"`
	LastSeen       contracts.Time    `json:"lastSeen,omitzero"`
	JoinedAt       contracts.Time    `json:"joinedAt"`
	CompletedTrips int               `json:"completedTrips"`
}

// Member is a driver with the vehicle he drives, if he has one.
type Member struct {
	Driver  Driver
	Vehicle *contracts.Vehicle
}

// Service is the fleet's read and write side.
type Service struct {
	db   *pgxpool.Pool
	pub  *eventbus.Publisher
	feed *changes.Feed
	now  func() time.Time
	// commitLead is how soon before its departure an assigned route makes its driver "on duty".
	commitLead time.Duration
}

func New(db *pgxpool.Pool, pub *eventbus.Publisher, feed *changes.Feed, now func() time.Time, commitLead time.Duration) *Service {
	return &Service{db: db, pub: pub, feed: feed, now: now, commitLead: commitLead}
}

// Mount registers fleet.* and live.idleDrivers, all for admins.
func (s *Service) Mount(rpc *platform.RPC) {
	type idIn struct {
		ID string `json:"id"`
	}
	type vehicleIn struct {
		Input VehicleInput `json:"input"`
	}
	type vehiclePatchIn struct {
		ID    string       `json:"id"`
		Patch VehiclePatch `json:"patch"`
	}
	type driverIn struct {
		Input DriverInput `json:"input"`
	}
	type driverPatchIn struct {
		ID    string      `json:"id"`
		Patch DriverPatch `json:"patch"`
	}

	rpc.Handle("fleet.vehicles.list", apix.Admin(func(ctx context.Context, _ struct{}) ([]contracts.Vehicle, error) {
		return s.Vehicles(ctx)
	}))
	rpc.Handle("fleet.vehicles.create", apix.Admin(func(ctx context.Context, in vehicleIn) (contracts.Vehicle, error) {
		return s.CreateVehicle(ctx, in.Input)
	}))
	rpc.Handle("fleet.vehicles.update", apix.Admin(func(ctx context.Context, in vehiclePatchIn) (contracts.Vehicle, error) {
		return s.UpdateVehicle(ctx, in.ID, in.Patch)
	}))
	rpc.Handle("fleet.vehicles.remove", apix.Admin(func(ctx context.Context, in idIn) (struct{}, error) {
		return struct{}{}, s.RemoveVehicle(ctx, in.ID)
	}))
	rpc.Handle("fleet.drivers.list", apix.Admin(func(ctx context.Context, _ struct{}) ([]Driver, error) {
		return s.Drivers(ctx)
	}))
	rpc.Handle("fleet.drivers.create", apix.Admin(func(ctx context.Context, in driverIn) (Driver, error) {
		return s.CreateDriver(ctx, in.Input)
	}))
	rpc.Handle("fleet.drivers.update", apix.Admin(func(ctx context.Context, in driverPatchIn) (Driver, error) {
		return s.UpdateDriver(ctx, in.ID, in.Patch)
	}))
	rpc.Handle("live.idleDrivers", apix.Admin(func(ctx context.Context, _ struct{}) ([]Driver, error) {
		return s.IdleDrivers(ctx)
	}))
}

// Roster returns every driver with his vehicle.
func (s *Service) Roster(ctx context.Context, q pg.Querier) ([]Member, error) {
	drivers, err := s.drivers(ctx, q)
	if err != nil {
		return nil, err
	}
	vehicles, err := s.vehicles(ctx, q)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*contracts.Vehicle, len(vehicles))
	for i := range vehicles {
		byID[vehicles[i].ID] = &vehicles[i]
	}
	members := make([]Member, len(drivers))
	for i, d := range drivers {
		members[i] = Member{Driver: d, Vehicle: byID[d.VehicleID]}
	}
	return members, nil
}

// IdleDrivers are the online drivers with a known position and no job: the still dots on the live map.
func (s *Service) IdleDrivers(ctx context.Context) ([]Driver, error) {
	drivers, err := s.drivers(ctx, s.db)
	if err != nil {
		return nil, err
	}
	idle := []Driver{}
	for _, d := range drivers {
		if d.Status == StatusOnline && d.Position != nil {
			idle = append(idle, d)
		}
	}
	return idle, nil
}

// announce enqueues an event of the driver or fleet topics in tx.
func (s *Service) announce(ctx context.Context, tx pgx.Tx, eventType, key string, version int64, data any) error {
	return s.pub.Enqueue(ctx, tx, eventbus.Event{Type: eventType, Key: key, AggregateVersion: version, Data: data})
}

type scanner interface{ Scan(dest ...any) error }

// wrap adds the operation to an unexpected database error; it keeps nil as nil.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("fleet: %s: %w", op, err)
}
