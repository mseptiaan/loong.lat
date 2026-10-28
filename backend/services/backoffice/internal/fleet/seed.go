package fleet

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// Defaults of the frontend's fleet.ts (DEFAULT_FARE and DEFAULT_CAPACITY).
var (
	defaultFare = map[contracts.VehicleType]contracts.FareConfig{
		contracts.VehicleTypeMotor:    {Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargePerKg: 800, SurchargeAboveKg: 10},
		contracts.VehicleTypeBlindvan: {Base: 45000, PerKm: 4500, PerMin: 500, PerExtraStop: 8000, SurchargePerKg: 150, SurchargeAboveKg: 300},
	}
	defaultCapacity = map[contracts.VehicleType]struct{ weightKg, volumeM3, dimensionCm float64 }{
		contracts.VehicleTypeMotor:    {20, 0.12, 55},
		contracts.VehicleTypeBlindvan: {500, 3.2, 190},
	}
)

// SeedDemo fills an empty fleet with what the mock backend starts with: a motorbike and a blind van with the
// default fares, each driven by a demo driver. It does nothing when there already are vehicles or drivers.
//
// Demo only: both drivers sign in with the PIN 123456 (like the mock's drivers), so never enable it where the
// accounts matter. The PIN is hashed like any other and is printed nowhere.
func (s *Service) SeedDemo(ctx context.Context) error {
	var existing int
	err := s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM vehicles) + (SELECT count(*) FROM drivers)`).Scan(&existing)
	if err != nil {
		return wrap("count fleet", err)
	}
	if existing > 0 {
		return nil
	}
	for _, d := range []struct {
		vehicleType  contracts.VehicleType
		plate, model string
		driver       string
		phone        string
	}{
		{contracts.VehicleTypeMotor, "B 3412 KJR", "Honda Vario 160", "Budi Santoso", "081234567801"},
		{contracts.VehicleTypeBlindvan, "B 9087 TXA", "Daihatsu Gran Max Blind Van", "Gilang Ramadhan", "081234567806"},
	} {
		capacity := defaultCapacity[d.vehicleType]
		v, err := s.CreateVehicle(ctx, VehicleInput{
			Type: d.vehicleType, Plate: d.plate, Model: d.model, MaxWeightKg: capacity.weightKg, MaxVolumeM3: capacity.volumeM3,
			MaxDimensionCm: capacity.dimensionCm, Fare: defaultFare[d.vehicleType], Status: contracts.VehicleStatusActive,
		})
		if err != nil {
			return err
		}
		if _, err := s.CreateDriver(ctx, DriverInput{Name: d.driver, Phone: d.phone, VehicleID: v.ID, PIN: "123456"}); err != nil {
			return err
		}
	}
	return nil
}
