package drivers

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// ErrNotFound means no driver has the id (or phone).
var ErrNotFound = errors.New("drivers: no such driver")

// IsPhoneTaken reports whether err is the unique violation of the phone number: a master-data conflict that
// no retry resolves.
func IsPhoneTaken(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505" && pgErr.ConstraintName == "drivers_phone_key"
}

const driverColumns = `id, name, phone, vehicle_id, status, lat, lng, last_seen, joined_at, completed_trips`

func scanDriver(row pgx.Row) (Driver, error) {
	var (
		d        Driver
		vehicle  *string
		status   string
		lat, lng *float64
		lastSeen *time.Time
		joinedAt time.Time
	)
	err := row.Scan(&d.ID, &d.Name, &d.Phone, &vehicle, &status, &lat, &lng, &lastSeen, &joinedAt, &d.CompletedTrips)
	if errors.Is(err, pgx.ErrNoRows) {
		return Driver{}, ErrNotFound
	}
	if err != nil {
		return Driver{}, err
	}
	d.Rating, d.Status, d.JoinedAt = Rating, Status(status), contracts.At(joinedAt)
	if vehicle != nil {
		d.VehicleID = *vehicle
	}
	if lat != nil && lng != nil {
		d.Position = &contracts.LatLng{Lat: *lat, Lng: *lng}
	}
	if lastSeen != nil {
		d.LastSeen = contracts.At(*lastSeen)
	}
	return d, nil
}

// Get returns the driver.
func Get(ctx context.Context, q platform.Querier, id string) (Driver, error) {
	return scanDriver(q.QueryRow(ctx, `SELECT `+driverColumns+` FROM drivers WHERE id = $1`, id))
}

// Lock is Get that also locks the row until the transaction ends.
func Lock(ctx context.Context, q platform.Querier, id string) (Driver, error) {
	return scanDriver(q.QueryRow(ctx, `SELECT `+driverColumns+` FROM drivers WHERE id = $1 FOR UPDATE`, id))
}

// ByVehicle returns the id of the driver the vehicle is assigned to.
func ByVehicle(ctx context.Context, q platform.Querier, vehicleID string) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM drivers WHERE vehicle_id = $1 ORDER BY id LIMIT 1`, vehicleID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// Account is what signing in needs.
type Account struct {
	ID      string
	Name    string
	PinHash string
}

// FindAccount looks a driver up by normalised phone number.
func FindAccount(ctx context.Context, q platform.Querier, phone string) (Account, error) {
	var a Account
	err := q.QueryRow(ctx, `SELECT id, name, pin_hash FROM drivers WHERE phone = $1`, phone).Scan(&a.ID, &a.Name, &a.PinHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// Master is the master data back office owns.
type Master struct {
	ID        string
	Name      string
	Phone     string // normalised
	VehicleID string
	// PinHash is the argon2id encoding of the PIN; empty on an update means the PIN did not change.
	PinHash  string
	JoinedAt time.Time
	// Version is the driver.version of the event that carried the data.
	Version int64
}

// Provision creates the driver. One that exists already keeps its state and takes the master data only if
// Version is newer, so a replayed provisioning cannot undo a later update.
func Provision(ctx context.Context, q platform.Querier, m Master) error {
	_, err := q.Exec(ctx,
		`INSERT INTO drivers (id, name, phone, pin_hash, vehicle_id, joined_at, version)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7)
		 ON CONFLICT (id) DO UPDATE
		    SET name = EXCLUDED.name, phone = EXCLUDED.phone, pin_hash = EXCLUDED.pin_hash,
		        vehicle_id = EXCLUDED.vehicle_id, version = EXCLUDED.version
		  WHERE drivers.version < EXCLUDED.version`,
		m.ID, m.Name, m.Phone, m.PinHash, m.VehicleID, m.JoinedAt, m.Version)
	return err
}

// Update applies master data of a newer version to a known driver. applied is false for a stale version;
// ErrNotFound means the driver has not been provisioned (yet).
func Update(ctx context.Context, q platform.Querier, m Master) (applied bool, err error) {
	tag, err := q.Exec(ctx,
		`UPDATE drivers
		    SET name = $2, phone = $3, vehicle_id = NULLIF($4, ''), pin_hash = COALESCE(NULLIF($5, ''), pin_hash), version = $6
		  WHERE id = $1 AND version < $6`,
		m.ID, m.Name, m.Phone, m.VehicleID, m.PinHash, m.Version)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	var known bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM drivers WHERE id = $1)`, m.ID).Scan(&known); err != nil {
		return false, err
	}
	if !known {
		return false, ErrNotFound
	}
	return false, nil
}

// SetPresence records online or offline and returns the new presence version.
func SetPresence(ctx context.Context, q platform.Querier, id string, status Status, at time.Time) (version int64, err error) {
	err = q.QueryRow(ctx,
		`UPDATE drivers SET status = $2, last_seen = GREATEST(last_seen, $3), presence_version = presence_version + 1
		  WHERE id = $1 RETURNING presence_version`,
		id, string(status), at).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return version, err
}

// RecordPosition stores pos as the driver's latest position if at is later than the one held, and reports
// whether it did. Fixes can reach the database out of order; only the newest may stand.
func RecordPosition(ctx context.Context, q platform.Querier, id string, pos contracts.LatLng, at time.Time) (stored bool, err error) {
	tag, err := q.Exec(ctx,
		`UPDATE drivers SET lat = $2, lng = $3, position_at = $4, last_seen = GREATEST(last_seen, $4)
		  WHERE id = $1 AND (position_at IS NULL OR position_at < $4)`,
		id, pos.Lat, pos.Lng, at)
	return tag.RowsAffected() == 1, err
}

// MarkOnDuty sets the driver on duty when a route starts.
func MarkOnDuty(ctx context.Context, q platform.Querier, id string, at time.Time) error {
	_, err := q.Exec(ctx, `UPDATE drivers SET status = 'bertugas', last_seen = GREATEST(last_seen, $2) WHERE id = $1`, id, at)
	return err
}

// FinishTrip counts a finished route. The driver is back online unless another of their routes is running; the
// route just finished must already be saved as such.
func FinishTrip(ctx context.Context, q platform.Querier, id string, at time.Time) error {
	_, err := q.Exec(ctx,
		`UPDATE drivers
		    SET completed_trips = completed_trips + 1, last_seen = GREATEST(last_seen, $2),
		        status = CASE WHEN EXISTS (SELECT 1 FROM routes WHERE driver_id = drivers.id AND status = 'berjalan')
		                      THEN 'bertugas' ELSE 'online' END
		  WHERE id = $1`,
		id, at)
	return err
}
