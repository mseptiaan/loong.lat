package fleet

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/pg"
)

// Message keys, all in the frontend catalog (packages/i18n).
const (
	msgDriverNotFound      = "srv.api.driverNotFound"
	msgDriverNameRequired  = "srv.api.driverNameRequired"
	msgPhoneIncomplete     = "srv.api.phoneIncomplete"
	msgPhoneTakenDriver    = "srv.api.phoneTakenDriver"
	msgPhoneUsedDriver     = "srv.api.phoneUsedDriver"
	msgVehicleTaken        = "srv.api.vehicleTaken"
	msgDriverOnDutyVehicle = "srv.api.driverOnDutyVehicle"
	msgStatusAuto          = "srv.api.statusAuto"
	msgPinInvalid          = "srv.api.pinInvalid"
)

// DriverInput creates a driver: the frontend's DriverInput plus the PIN the driver signs in with. The PIN is
// hashed at once and only the hash leaves this service.
type DriverInput struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	VehicleID string `json:"vehicleId"`
	PIN       string `json:"pin"`
}

// DriverPatch is Partial<DriverInput> & {status}: a nil field is left as it is. A PIN resets the driver's PIN.
// Status is accepted only to be refused when it differs from the current one: driver-api owns it.
type DriverPatch struct {
	Name      *string `json:"name"`
	Phone     *string `json:"phone"`
	VehicleID *string `json:"vehicleId"`
	PIN       *string `json:"pin"`
	Status    *Status `json:"status"`
}

var pinPattern = regexp.MustCompile(`^\d{6}$`)

// normalizePhone keeps the digits and writes a +62 / 62 prefix as the leading 0, so one number has one form.
func normalizePhone(phone string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if rest, ok := strings.CutPrefix(digits, "62"); ok {
		return "0" + rest
	}
	return digits
}

func cleanName(name string) (string, error) {
	if name = strings.TrimSpace(name); name == "" {
		return "", platform.Invalid("name", contracts.Msg{Key: msgDriverNameRequired})
	}
	return name, nil
}

func cleanPhone(phone string) (string, error) {
	if phone = normalizePhone(phone); len(phone) < 9 {
		return "", platform.Invalid("phone", contracts.Msg{Key: msgPhoneIncomplete})
	}
	return phone, nil
}

// hashPIN checks a PIN and hashes it. It runs before the transaction opens: argon2 takes real time.
func hashPIN(pin string) (string, error) {
	if !pinPattern.MatchString(pin) {
		return "", platform.Invalid("pin", contracts.Msg{Key: msgPinInvalid})
	}
	hash, err := platform.HashSecret(pin)
	if err != nil {
		return "", wrap("hash pin", err)
	}
	return hash, nil
}

// driverColumns reads a driver with his trips and whether he is on duty; scanDriver matches it. $1 is the time
// up to which an assigned route puts its driver on duty.
const driverColumns = `d.id, d.name, d.phone, d.rating, COALESCE(d.vehicle_id, ''), d.online, d.lat, d.lng, d.last_seen, d.joined_at,
	(SELECT count(*) FROM routes r WHERE r.driver_id = d.id AND r.status = 'selesai'),
	EXISTS (SELECT 1 FROM routes r WHERE r.driver_id = d.id AND (r.status = 'berjalan' OR (r.status = 'ditugaskan' AND r.start_at <= $1)))`

// PresenceTTL is how long an online driver may stay silent before he counts as offline. The driver app sends his
// position every few seconds while online, so a longer silence means the app is gone (closed, no battery, no
// signal) and giving him a job would leave it waiting for nobody. A driver on duty is never expired: he keeps
// his routes through a dead spot.
const PresenceTTL = 2 * time.Minute

// scanDriver reads a driver; now is the time his presence is judged against (see PresenceTTL).
func scanDriver(row scanner, now time.Time) (Driver, error) {
	var d Driver
	var online, onDuty bool
	var lat, lng *float64
	var lastSeen *time.Time
	var joinedAt time.Time
	if err := row.Scan(&d.ID, &d.Name, &d.Phone, &d.Rating, &d.VehicleID, &online, &lat, &lng, &lastSeen, &joinedAt, &d.CompletedTrips, &onDuty); err != nil {
		return d, err
	}
	switch {
	case onDuty:
		d.Status = StatusOnDuty
	case online && lastSeen != nil && now.Sub(*lastSeen) <= PresenceTTL:
		d.Status = StatusOnline
	default:
		d.Status = StatusOffline
	}
	if lat != nil && lng != nil {
		d.Position = &contracts.LatLng{Lat: *lat, Lng: *lng}
	}
	if lastSeen != nil {
		d.LastSeen = contracts.At(*lastSeen)
	}
	d.JoinedAt = contracts.At(joinedAt)
	return d, nil
}

// drivers reads every driver, longest-serving first.
func (s *Service) drivers(ctx context.Context, q pg.Querier) ([]Driver, error) {
	rows, err := q.Query(ctx, `SELECT `+driverColumns+` FROM drivers d ORDER BY d.joined_at, d.id`, s.now().Add(s.commitLead))
	if err != nil {
		return nil, wrap("list drivers", err)
	}
	defer rows.Close()
	out := []Driver{}
	for rows.Next() {
		d, err := scanDriver(rows, s.now())
		if err != nil {
			return nil, wrap("scan driver", err)
		}
		out = append(out, d)
	}
	return out, wrap("list drivers", rows.Err())
}

// Drivers lists every driver.
func (s *Service) Drivers(ctx context.Context) ([]Driver, error) { return s.drivers(ctx, s.db) }

// driver reads one driver, locking his row until tx ends when lock is set.
func (s *Service) driver(ctx context.Context, q pg.Querier, id string, lock bool) (Driver, error) {
	query := `SELECT ` + driverColumns + ` FROM drivers d WHERE d.id = $2`
	if lock {
		query += ` FOR UPDATE OF d`
	}
	d, err := scanDriver(q.QueryRow(ctx, query, s.now().Add(s.commitLead), id), s.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return d, platform.NotFound(contracts.Msg{Key: msgDriverNotFound})
	}
	return d, wrap("read driver", err)
}

// CreateDriver adds a driver and announces him with the argon2id hash of his PIN, never the PIN itself.
func (s *Service) CreateDriver(ctx context.Context, in DriverInput) (Driver, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return Driver{}, err
	}
	phone, err := cleanPhone(in.Phone)
	if err != nil {
		return Driver{}, err
	}
	pinHash, err := hashPIN(in.PIN)
	if err != nil {
		return Driver{}, err
	}

	id, joinedAt := apix.NewID("drv"), s.now()
	var out Driver
	err = platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO drivers (id, name, phone, vehicle_id, joined_at) VALUES ($1, $2, $3, NULLIF($4, ''), $5)`,
			id, name, phone, in.VehicleID, joinedAt)
		if err != nil {
			return driverConflict(err, msgPhoneTakenDriver)
		}
		err = s.announce(ctx, tx, contracts.TypeDriverProvisioned, id, 1, contracts.DriverProvisioned{
			DriverID: id, Name: name, Phone: phone, VehicleID: in.VehicleID, PinHash: pinHash, JoinedAt: contracts.At(joinedAt),
		})
		if err != nil {
			return err
		}
		if err := s.touchVehicles(ctx, tx, in.VehicleID); err != nil {
			return err
		}
		out, err = s.driver(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return Driver{}, err
	}
	s.feed.Fleet()
	return out, nil
}

// UpdateDriver changes a driver's master data or resets his PIN, and announces the full data after the change
// (the hash only on a reset). A driver on duty keeps his vehicle. A change that changes nothing announces
// nothing.
func (s *Service) UpdateDriver(ctx context.Context, id string, patch DriverPatch) (Driver, error) {
	var name, phone, pinHash string
	var err error
	if patch.Name != nil {
		if name, err = cleanName(*patch.Name); err != nil {
			return Driver{}, err
		}
	}
	if patch.Phone != nil {
		if phone, err = cleanPhone(*patch.Phone); err != nil {
			return Driver{}, err
		}
	}
	if patch.PIN != nil {
		if pinHash, err = hashPIN(*patch.PIN); err != nil {
			return Driver{}, err
		}
	}

	var out Driver
	changed := false
	err = platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		cur, err := s.driver(ctx, tx, id, true)
		if err != nil {
			return err
		}
		out = cur
		if patch.Status != nil && *patch.Status != cur.Status {
			return apix.Field(platform.Conflict(contracts.Msg{Key: msgStatusAuto}), "status")
		}
		next := cur
		if patch.Name != nil {
			next.Name = name
		}
		if patch.Phone != nil {
			next.Phone = phone
		}
		if patch.VehicleID != nil {
			next.VehicleID = *patch.VehicleID
		}
		vehicleChanged := next.VehicleID != cur.VehicleID
		if vehicleChanged && cur.Status == StatusOnDuty {
			return apix.Field(platform.Conflict(contracts.Msg{Key: msgDriverOnDutyVehicle}), "vehicleId")
		}
		if next.Name == cur.Name && next.Phone == cur.Phone && !vehicleChanged && pinHash == "" {
			return nil
		}

		var version int64
		err = tx.QueryRow(ctx,
			`UPDATE drivers SET name = $2, phone = $3, vehicle_id = NULLIF($4, ''), version = version + 1 WHERE id = $1 RETURNING version`,
			id, next.Name, next.Phone, next.VehicleID).Scan(&version)
		if err != nil {
			return driverConflict(err, msgPhoneUsedDriver)
		}
		err = s.announce(ctx, tx, contracts.TypeDriverUpdated, id, version, contracts.DriverUpdated{
			DriverID: id, Name: next.Name, Phone: next.Phone, VehicleID: next.VehicleID, PinHash: pinHash,
		})
		if err != nil {
			return err
		}
		if vehicleChanged {
			if err := s.touchVehicles(ctx, tx, cur.VehicleID, next.VehicleID); err != nil {
				return err
			}
		}
		out, err = s.driver(ctx, tx, id, false)
		changed = true
		return err
	})
	if err != nil {
		return out, err
	}
	if changed {
		s.feed.Fleet()
	}
	return out, nil
}

// detachDriver takes the vehicle away from its driver, who is announced as updated.
func (s *Service) detachDriver(ctx context.Context, tx pgx.Tx, driverID string) error {
	var name, phone string
	var version int64
	err := tx.QueryRow(ctx, `UPDATE drivers SET vehicle_id = NULL, version = version + 1 WHERE id = $1 RETURNING name, phone, version`, driverID).
		Scan(&name, &phone, &version)
	if err != nil {
		return wrap("detach driver", err)
	}
	return s.announce(ctx, tx, contracts.TypeDriverUpdated, driverID, version,
		contracts.DriverUpdated{DriverID: driverID, Name: name, Phone: phone})
}

// driverConflict turns a unique violation of the drivers table into the conflict the form shows; phoneKey is the
// message for a duplicate phone number (it differs between create and update).
func driverConflict(err error, phoneKey string) error {
	if constraint, ok := pg.UniqueViolation(err); ok {
		switch constraint {
		case "drivers_phone_key":
			return apix.Field(platform.Conflict(contracts.Msg{Key: phoneKey}), "phone")
		case "drivers_vehicle_key":
			return apix.Field(platform.Conflict(contracts.Msg{Key: msgVehicleTaken}), "vehicleId")
		}
	}
	return wrap("write driver", err)
}
