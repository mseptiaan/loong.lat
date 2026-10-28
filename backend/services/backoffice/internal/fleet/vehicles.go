package fleet

import (
	"context"
	"encoding/json/v2"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/pg"
)

// Message keys of the frontend catalog.
const (
	msgVehicleNotFound    = "srv.api.vehicleNotFound"
	msgPlateInvalid       = "srv.api.plateInvalid"
	msgPlateTaken         = "srv.api.plateTaken"
	msgMustBePositive     = "srv.api.mustBePositive"
	msgModelRequired      = "srv.api.modelRequired"
	msgFareNegative       = "srv.api.fareNegative"
	msgVehicleInUse       = "srv.api.vehicleInUse"
	msgVehicleInUseChange = "srv.api.vehicleInUseChange"
)

// VehicleInput is the frontend's VehicleInput.
type VehicleInput struct {
	Type           contracts.VehicleType   `json:"type"`
	Plate          string                  `json:"plate"`
	Model          string                  `json:"model"`
	MaxWeightKg    float64                 `json:"maxWeightKg"`
	MaxVolumeM3    float64                 `json:"maxVolumeM3"`
	MaxDimensionCm float64                 `json:"maxDimensionCm"`
	Fare           contracts.FareConfig    `json:"fare"`
	Status         contracts.VehicleStatus `json:"status"`
}

// VehiclePatch is Partial<VehicleInput>: a nil field is left as it is.
type VehiclePatch struct {
	Type           *contracts.VehicleType   `json:"type"`
	Plate          *string                  `json:"plate"`
	Model          *string                  `json:"model"`
	MaxWeightKg    *float64                 `json:"maxWeightKg"`
	MaxVolumeM3    *float64                 `json:"maxVolumeM3"`
	MaxDimensionCm *float64                 `json:"maxDimensionCm"`
	Fare           *contracts.FareConfig    `json:"fare"`
	Status         *contracts.VehicleStatus `json:"status"`
}

func (p VehiclePatch) apply(v contracts.Vehicle) contracts.Vehicle {
	if p.Type != nil {
		v.Type = *p.Type
	}
	if p.Plate != nil {
		v.Plate = *p.Plate
	}
	if p.Model != nil {
		v.Model = *p.Model
	}
	if p.MaxWeightKg != nil {
		v.MaxWeightKg = *p.MaxWeightKg
	}
	if p.MaxVolumeM3 != nil {
		v.MaxVolumeM3 = *p.MaxVolumeM3
	}
	if p.MaxDimensionCm != nil {
		v.MaxDimensionCm = *p.MaxDimensionCm
	}
	if p.Fare != nil {
		v.Fare = *p.Fare
	}
	if p.Status != nil {
		v.Status = *p.Status
	}
	return v
}

var platePattern = regexp.MustCompile(`^[A-Z]{1,2} \d{1,4} [A-Z]{1,3}$`)

// validateVehicle checks a vehicle as the console's form rules do and returns it normalized (plate upper-cased
// with single spaces, model trimmed).
func validateVehicle(v contracts.Vehicle) (contracts.Vehicle, error) {
	v.Plate = strings.ToUpper(strings.Join(strings.Fields(v.Plate), " "))
	if !platePattern.MatchString(v.Plate) {
		return v, platform.Invalid("plate", contracts.Msg{Key: msgPlateInvalid})
	}
	if v.Type != contracts.VehicleTypeMotor && v.Type != contracts.VehicleTypeBlindvan {
		return v, platform.Invalid("type", contracts.Msg{Key: platform.MsgBadRequest})
	}
	switch v.Status {
	case contracts.VehicleStatusActive, contracts.VehicleStatusService, contracts.VehicleStatusInactive:
	default:
		return v, platform.Invalid("status", contracts.Msg{Key: platform.MsgBadRequest})
	}
	for _, c := range []struct {
		field, label string
		value        float64
	}{
		{"maxWeightKg", "srv.api.field.maxWeightKg", v.MaxWeightKg},
		{"maxVolumeM3", "srv.api.field.maxVolumeM3", v.MaxVolumeM3},
		{"maxDimensionCm", "srv.api.field.maxDimensionCm", v.MaxDimensionCm},
	} {
		if !(c.value > 0) {
			return v, platform.Invalid(c.field, contracts.Msg{Key: msgMustBePositive, Params: map[string]any{"field": contracts.Msg{Key: c.label}}})
		}
	}
	if v.Model = strings.TrimSpace(v.Model); v.Model == "" {
		return v, platform.Invalid("model", contracts.Msg{Key: msgModelRequired})
	}
	for _, f := range []struct {
		field string
		value float64
	}{
		{"fare.base", float64(v.Fare.Base)},
		{"fare.perKm", float64(v.Fare.PerKm)},
		{"fare.perMin", float64(v.Fare.PerMin)},
		{"fare.perExtraStop", float64(v.Fare.PerExtraStop)},
		{"fare.surchargePerKg", float64(v.Fare.SurchargePerKg)},
		{"fare.surchargeAboveKg", v.Fare.SurchargeAboveKg},
	} {
		if !(f.value >= 0) {
			return v, platform.Invalid(f.field, contracts.Msg{Key: msgFareNegative})
		}
	}
	return v, nil
}

// vehicleColumns reads a vehicle with the driver who drives it and its version; scanVehicle matches it.
const vehicleColumns = `v.id, v.type, v.plate, v.model, v.max_weight_kg, v.max_volume_m3, v.max_dimension_cm, v.fare, v.status,
	COALESCE((SELECT d.id FROM drivers d WHERE d.vehicle_id = v.id), ''), v.version`

func scanVehicle(row scanner) (v contracts.Vehicle, version int64, err error) {
	var fare []byte
	if err = row.Scan(&v.ID, &v.Type, &v.Plate, &v.Model, &v.MaxWeightKg, &v.MaxVolumeM3, &v.MaxDimensionCm,
		&fare, &v.Status, &v.DriverID, &version); err != nil {
		return v, 0, err
	}
	return v, version, json.Unmarshal(fare, &v.Fare)
}

// vehicles reads every vehicle, oldest plate first.
func (s *Service) vehicles(ctx context.Context, q pg.Querier) ([]contracts.Vehicle, error) {
	rows, err := q.Query(ctx, `SELECT `+vehicleColumns+` FROM vehicles v ORDER BY v.plate`)
	if err != nil {
		return nil, wrap("list vehicles", err)
	}
	defer rows.Close()
	out := []contracts.Vehicle{}
	for rows.Next() {
		v, _, err := scanVehicle(rows)
		if err != nil {
			return nil, wrap("scan vehicle", err)
		}
		out = append(out, v)
	}
	return out, wrap("list vehicles", rows.Err())
}

// Vehicles lists the fleet.
func (s *Service) Vehicles(ctx context.Context) ([]contracts.Vehicle, error) {
	return s.vehicles(ctx, s.db)
}

// lockVehicle reads a vehicle and locks its row until tx ends.
func lockVehicle(ctx context.Context, tx pgx.Tx, id string) (contracts.Vehicle, int64, error) {
	v, version, err := scanVehicle(tx.QueryRow(ctx, `SELECT `+vehicleColumns+` FROM vehicles v WHERE v.id = $1 FOR UPDATE OF v`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, 0, platform.NotFound(contracts.Msg{Key: msgVehicleNotFound})
	}
	if err != nil {
		return v, 0, wrap("read vehicle", err)
	}
	return v, version, nil
}

// CreateVehicle adds a vehicle and announces it on the compacted fleet topic.
func (s *Service) CreateVehicle(ctx context.Context, in VehicleInput) (contracts.Vehicle, error) {
	v, err := validateVehicle(contracts.Vehicle{
		ID: apix.NewID("veh"), Type: in.Type, Plate: in.Plate, Model: in.Model, MaxWeightKg: in.MaxWeightKg,
		MaxVolumeM3: in.MaxVolumeM3, MaxDimensionCm: in.MaxDimensionCm, Fare: in.Fare, Status: in.Status,
	})
	if err != nil {
		return v, err
	}
	fare, err := json.Marshal(v.Fare)
	if err != nil {
		return v, wrap("encode fare", err)
	}
	err = platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO vehicles (id, type, plate, model, max_weight_kg, max_volume_m3, max_dimension_cm, fare, status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			v.ID, string(v.Type), v.Plate, v.Model, v.MaxWeightKg, v.MaxVolumeM3, v.MaxDimensionCm, fare, string(v.Status))
		if err != nil {
			return plateTaken(err, v.Plate)
		}
		return s.announce(ctx, tx, contracts.TypeVehicleUpserted, v.ID, 1, contracts.VehicleUpserted{Vehicle: v})
	})
	if err != nil {
		return v, err
	}
	s.feed.Fleet()
	return v, nil
}

// UpdateVehicle changes a vehicle. A vehicle on an assigned or running route cannot be taken out of service
// or change type. A change that changes nothing announces nothing.
func (s *Service) UpdateVehicle(ctx context.Context, id string, patch VehiclePatch) (contracts.Vehicle, error) {
	var out contracts.Vehicle
	changed := false
	err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		cur, version, err := lockVehicle(ctx, tx, id)
		if err != nil {
			return err
		}
		out = cur
		next, err := validateVehicle(patch.apply(cur))
		if err != nil {
			return err
		}
		if next == cur {
			return nil
		}
		if next.Status != contracts.VehicleStatusActive || next.Type != cur.Type {
			inUse, err := onActiveRoute(ctx, tx, id)
			if err != nil {
				return err
			}
			if inUse {
				return apix.Field(platform.Conflict(contracts.Msg{Key: msgVehicleInUseChange}), "status")
			}
		}
		fare, err := json.Marshal(next.Fare)
		if err != nil {
			return wrap("encode fare", err)
		}
		_, err = tx.Exec(ctx,
			`UPDATE vehicles SET type = $2, plate = $3, model = $4, max_weight_kg = $5, max_volume_m3 = $6,
			        max_dimension_cm = $7, fare = $8, status = $9, version = version + 1
			  WHERE id = $1`,
			id, string(next.Type), next.Plate, next.Model, next.MaxWeightKg, next.MaxVolumeM3, next.MaxDimensionCm, fare, string(next.Status))
		if err != nil {
			return plateTaken(err, next.Plate)
		}
		out, changed = next, true
		return s.announce(ctx, tx, contracts.TypeVehicleUpserted, id, version+1, contracts.VehicleUpserted{Vehicle: next})
	})
	if err != nil {
		return out, err
	}
	if changed {
		s.feed.Fleet()
	}
	return out, nil
}

// RemoveVehicle deletes a vehicle that no active route uses. The driver who drove it is left without one, and
// the removal is the last event of the vehicle on the compacted topic.
func (s *Service) RemoveVehicle(ctx context.Context, id string) error {
	err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		v, version, err := lockVehicle(ctx, tx, id)
		if err != nil {
			return err
		}
		inUse, err := onActiveRoute(ctx, tx, id)
		if err != nil {
			return err
		}
		if inUse {
			return platform.Conflict(contracts.Msg{Key: msgVehicleInUse, Params: map[string]any{"plate": v.Plate}})
		}
		if v.DriverID != "" {
			if err := s.detachDriver(ctx, tx, v.DriverID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM vehicles WHERE id = $1`, id); err != nil {
			return wrap("delete vehicle", err)
		}
		return s.announce(ctx, tx, contracts.TypeVehicleRemoved, id, version+1, contracts.VehicleRemoved{VehicleID: id})
	})
	if err == nil {
		s.feed.Fleet()
	}
	return err
}

// onActiveRoute reports whether an assigned or running route uses the vehicle.
func onActiveRoute(ctx context.Context, q pg.Querier, vehicleID string) (bool, error) {
	var used bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM routes WHERE vehicle_id = $1 AND status IN ('ditugaskan', 'berjalan'))`, vehicleID).Scan(&used)
	return used, wrap("check vehicle use", err)
}

// touchVehicles announces vehicles again after their driver changed: the event carries the driver's id. It also
// fails with "vehicle not found" for an id that does not exist, which is how a driver is refused a vehicle that
// is not there. The vehicles are locked in id order so two changes sharing vehicles cannot wait on each other.
func (s *Service) touchVehicles(ctx context.Context, tx pgx.Tx, ids ...string) error {
	ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id == "" })
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		v, version, err := scanVehicle(tx.QueryRow(ctx,
			`UPDATE vehicles v SET version = version + 1 WHERE v.id = $1 RETURNING `+vehicleColumns, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.NotFound(contracts.Msg{Key: msgVehicleNotFound})
		}
		if err != nil {
			return wrap("touch vehicle", err)
		}
		if err := s.announce(ctx, tx, contracts.TypeVehicleUpserted, id, version, contracts.VehicleUpserted{Vehicle: v}); err != nil {
			return err
		}
	}
	return nil
}

// plateTaken turns the unique-plate violation into the conflict the form shows under the plate field.
func plateTaken(err error, plate string) error {
	if constraint, ok := pg.UniqueViolation(err); ok && constraint == "vehicles_plate_key" {
		return apix.Field(platform.Conflict(contracts.Msg{Key: msgPlateTaken, Params: map[string]any{"plate": plate}}), "plate")
	}
	return wrap("write vehicle", err)
}
