package fleet

import (
	"errors"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

func validVehicle() contracts.Vehicle {
	return contracts.Vehicle{
		ID: "veh-1", Type: contracts.VehicleTypeMotor, Plate: "B 3412 KJR", Model: "Honda Vario 160",
		MaxWeightKg: 20, MaxVolumeM3: 0.12, MaxDimensionCm: 55, Status: contracts.VehicleStatusActive,
		Fare: contracts.FareConfig{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargePerKg: 800, SurchargeAboveKg: 10},
	}
}

func TestValidateVehicleNormalizesAndChecks(t *testing.T) {
	got, err := validateVehicle(func() contracts.Vehicle {
		v := validVehicle()
		v.Plate, v.Model = "  b   3412  kjr ", "  Vario "
		return v
	}())
	if err != nil || got.Plate != "B 3412 KJR" || got.Model != "Vario" {
		t.Fatalf("got %+v, %v; want the plate upper-cased with single spaces and the model trimmed", got, err)
	}

	tests := []struct {
		name      string
		tweak     func(*contracts.Vehicle)
		field     string
		messageID string
	}{
		{"a plate that is not Indonesian", func(v *contracts.Vehicle) { v.Plate = "1234" }, "plate", "srv.api.plateInvalid"},
		{"a plate without its number", func(v *contracts.Vehicle) { v.Plate = "B KJR" }, "plate", "srv.api.plateInvalid"},
		{"an unknown type", func(v *contracts.Vehicle) { v.Type = "truck" }, "type", platform.MsgBadRequest},
		{"an unknown status", func(v *contracts.Vehicle) { v.Status = "rusak" }, "status", platform.MsgBadRequest},
		{"zero weight", func(v *contracts.Vehicle) { v.MaxWeightKg = 0 }, "maxWeightKg", "srv.api.mustBePositive"},
		{"negative volume", func(v *contracts.Vehicle) { v.MaxVolumeM3 = -1 }, "maxVolumeM3", "srv.api.mustBePositive"},
		{"zero dimension", func(v *contracts.Vehicle) { v.MaxDimensionCm = 0 }, "maxDimensionCm", "srv.api.mustBePositive"},
		{"a blank model", func(v *contracts.Vehicle) { v.Model = "  " }, "model", "srv.api.modelRequired"},
		{"a negative base fare", func(v *contracts.Vehicle) { v.Fare.Base = -1 }, "fare.base", "srv.api.fareNegative"},
		{"a negative surcharge threshold", func(v *contracts.Vehicle) { v.Fare.SurchargeAboveKg = -0.5 }, "fare.surchargeAboveKg", "srv.api.fareNegative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := validVehicle()
			tt.tweak(&v)
			_, err := validateVehicle(v)
			apiErr, ok := errors.AsType[*platform.APIError](err)
			if !ok {
				t.Fatalf("error = %v, want an APIError", err)
			}
			if apiErr.Code != platform.CodeInvalid || apiErr.Field != tt.field || apiErr.Msg.Key != tt.messageID {
				t.Errorf("error = %s field %q key %q, want tidak_valid on %q with %q", apiErr.Code, apiErr.Field, apiErr.Msg.Key, tt.field, tt.messageID)
			}
		})
	}

	free := validVehicle()
	free.Fare = contracts.FareConfig{}
	if _, err := validateVehicle(free); err != nil {
		t.Errorf("zero fares are allowed (>= 0): %v", err)
	}
}

func TestVehiclePatchChangesOnlyWhatItCarries(t *testing.T) {
	cur := validVehicle()
	model, weight := "Yamaha NMAX", 25.0
	got := VehiclePatch{Model: &model, MaxWeightKg: &weight}.apply(cur)
	want := cur
	want.Model, want.MaxWeightKg = model, weight
	if got != want {
		t.Errorf("patched = %+v, want only model and weight changed", got)
	}
	if (VehiclePatch{}).apply(cur) != cur {
		t.Error("an empty patch must change nothing")
	}
}

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"0812-3456-7801":    "081234567801",
		"+62 812 3456 7801": "081234567801",
		"6281234567801":     "081234567801",
		"(021) 555 1234":    "0215551234",
		"abc":               "",
	} {
		if got := normalizePhone(in); got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}
