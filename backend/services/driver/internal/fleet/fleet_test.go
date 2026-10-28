package fleet

import (
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
)

func vehicle(model string) contracts.Vehicle {
	return contracts.Vehicle{ID: "veh-1", Type: contracts.VehicleTypeMotor, Plate: "B 1234 ABC", Model: model, Status: contracts.VehicleStatusActive}
}

func TestVehicleFollowsTheLatestVersion(t *testing.T) {
	pool := drivertest.Pool(t)
	ctx := t.Context()

	if v, err := Get(ctx, pool, "veh-1"); err != nil || v != nil {
		t.Fatalf("unknown vehicle = %v, %v; want nil", v, err)
	}
	if applied, err := Upsert(ctx, pool, vehicle("Vario"), 2); err != nil || !applied {
		t.Fatalf("upsert v2 = %v, %v", applied, err)
	}
	if applied, err := Upsert(ctx, pool, vehicle("Old"), 1); err != nil || applied {
		t.Errorf("an older upsert = %v, %v; want dropped", applied, err)
	}
	if applied, err := Upsert(ctx, pool, vehicle("Vario 160"), 2); err != nil || !applied {
		t.Errorf("an equal version = %v, %v; want applied (a producer may not version vehicles)", applied, err)
	}
	if v, _ := Get(ctx, pool, "veh-1"); v == nil || v.Model != "Vario 160" {
		t.Errorf("vehicle = %+v", v)
	}
}

func TestARemovedVehicleStaysRemovedAgainstLateUpserts(t *testing.T) {
	pool := drivertest.Pool(t)
	ctx := t.Context()
	if _, err := Upsert(ctx, pool, vehicle("Vario"), 1); err != nil {
		t.Fatal(err)
	}
	if applied, err := Remove(ctx, pool, "veh-1", 3); err != nil || !applied {
		t.Fatalf("remove = %v, %v", applied, err)
	}
	if v, err := Get(ctx, pool, "veh-1"); err != nil || v != nil {
		t.Errorf("removed vehicle = %v, %v; want nil", v, err)
	}
	if applied, _ := Upsert(ctx, pool, vehicle("Late"), 2); applied {
		t.Error("an upsert older than the removal brought the vehicle back")
	}
	if v, _ := Get(ctx, pool, "veh-1"); v != nil {
		t.Errorf("vehicle = %+v, want it to stay removed", v)
	}
	if applied, _ := Upsert(ctx, pool, vehicle("Back"), 4); !applied {
		t.Error("a newer upsert must bring a removed vehicle back")
	}
}

func TestRemovingAnUnknownVehicleIsRemembered(t *testing.T) {
	pool := drivertest.Pool(t)
	ctx := t.Context()
	if _, err := Remove(ctx, pool, "veh-1", 5); err != nil {
		t.Fatal(err)
	}
	if applied, _ := Upsert(ctx, pool, vehicle("Late"), 4); applied {
		t.Error("an upsert older than the removal was applied")
	}
}
