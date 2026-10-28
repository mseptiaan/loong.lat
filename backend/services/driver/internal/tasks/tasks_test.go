package tasks

import (
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
)

func task() Task {
	return Task{PlanID: "pln-1", PlanCode: "PL-2001", CustomerName: "Dina", DriverID: "drv-1", Route: drivertest.Route("pln-1:r1", "drv-1"), UpdatedAt: drivertest.Epoch}
}

func TestForAppRemovesEveryDeliveryCodeAndLeavesTheOriginal(t *testing.T) {
	r := drivertest.Route("pln-1:r1", "drv-1")
	before := drivertest.Route("pln-1:r1", "drv-1")

	out := ForApp(r)
	for _, s := range out.Stops {
		if s.DeliveryCode != "" {
			t.Errorf("stop %s still carries a delivery code", s.ID)
		}
	}
	if !reflect.DeepEqual(r, before) {
		t.Error("ForApp must not change the route it is given: the codes are still needed to check the OTP")
	}
	if r.Stops[1].DeliveryCode != drivertest.CodeA || r.Stops[2].DeliveryCode != drivertest.CodeB {
		t.Error("the stored route lost its codes")
	}
}

// Nothing the app can call may carry a code: check the JSON of both shapes it receives.
func TestWhatTheAppReceivesHasNoDeliveryCode(t *testing.T) {
	for name, v := range map[string]any{"driver route": task().ForApp(), "plan view": task().Plan()} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{drivertest.CodeA, drivertest.CodeB, "deliveryCode"} {
			if strings.Contains(string(raw), secret) {
				t.Errorf("%s leaks %q: %s", name, secret, raw)
			}
		}
	}
}

func TestPlanViewCarriesTheRouteAndAStatusFromIt(t *testing.T) {
	plan := task().Plan()
	if plan.ID != "pln-1" || plan.Code != "PL-2001" || len(plan.Routes) != 1 || plan.Routes[0].ID != "pln-1:r1" {
		t.Errorf("plan = %+v", plan)
	}
	if plan.Status != contracts.PlanStatusAssigned {
		t.Errorf("status = %s, want assigned for an assigned route", plan.Status)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"shipments":[]`, `"events":[]`, `"createdAt":"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("plan JSON lacks %s: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), "0001-01-01") {
		t.Errorf("plan JSON holds a zero time: %s", raw)
	}
}

func TestStartedFromTheMomentTheRouteRuns(t *testing.T) {
	for status, want := range map[contracts.RouteStatus]bool{
		contracts.RouteStatusAwaitingDriver: false, contracts.RouteStatusAssigned: false,
		contracts.RouteStatusRunning: true, contracts.RouteStatusDone: true,
	} {
		tk := task()
		tk.Route.Status = status
		if tk.Started() != want {
			t.Errorf("Started() for %s = %v, want %v", status, tk.Started(), want)
		}
	}
}
