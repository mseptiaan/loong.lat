// Package drivertest holds the fixtures the tests of driver-api share: a route as dispatch assigns it, the
// events around it, and a photo the proof checks accept. Tests only.
package drivertest

import (
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
)

// Epoch is the clock of the fixtures.
var Epoch = time.Date(2026, time.October, 21, 3, 0, 0, 0, time.UTC)

// Delivery codes of the two drop-offs of Route.
const (
	CodeA = "4821"
	CodeB = "7390"
)

// Route is a route as it arrives in route.assigned: assigned to a driver, departure at Epoch, and three stops:
// a pickup (<id>:s1), a drop-off with CodeA (<id>:s2) and one with CodeB (<id>:s3).
func Route(routeID, driverID string) contracts.Route {
	stop := func(n int, kind contracts.StopKind, shipment string, lat, lng float64, code string) contracts.RouteStop {
		return contracts.RouteStop{
			ID: routeID + ":s" + string(rune('0'+n)), Seq: n, Kind: kind, ShipmentID: shipment, Status: contracts.StopStatusWaiting,
			Waypoint: contracts.Waypoint{PlaceRef: contracts.PlaceRef{Name: "Stop " + string(rune('0'+n)), Position: contracts.LatLng{Lat: lat, Lng: lng}}, ContactName: "Kontak"},
			LegKm:    2, LegMin: 6, ItemsSummary: contracts.Msg{Key: "dom.itemsSummary"},
			DeliveryCode: code,
		}
	}
	route := contracts.Route{
		ID: routeID, PlanID: strings.SplitN(routeID, ":", 2)[0], VehicleType: contracts.VehicleTypeMotor,
		Status: contracts.RouteStatusAwaitingDriver, StartPosition: contracts.LatLng{Lat: -6.27, Lng: 106.81},
		Stops: []contracts.RouteStop{
			stop(1, contracts.StopKindPickup, "shp-1", -6.2694, 106.8143, ""),
			stop(2, contracts.StopKindDropoff, "shp-1", -6.2253, 106.7995, CodeA),
			stop(3, contracts.StopKindDropoff, "shp-2", -6.2000, 106.8200, CodeB),
		},
		DistanceKm: 6, DurationMin: 30, CostRp: 21500, Fare: []contracts.FareLine{{Label: contracts.Msg{Key: "dom.fare.base"}, AmountRp: 21500}},
	}
	route = routeplan.RebaseStart(route, route.StartPosition, Epoch)
	return routeplan.Assign(route, driverID, "veh-1", Epoch)
}

// Assigned is the route.assigned event for Route.
func Assigned(routeID, driverID string) contracts.RouteAssigned {
	route := Route(routeID, driverID)
	return contracts.RouteAssigned{
		PlanID: route.PlanID, PlanCode: "PL-2001", CustomerName: "Dina Pramesti",
		DriverID: driverID, VehicleID: "veh-1", StartAt: route.StartAt, Route: route,
	}
}

// Provisioned is the driver.provisioned event of a driver whose PIN is pin. The hash is argon2id with
// minimal cost, which verifies like any other but keeps tests fast.
func Provisioned(t testing.TB, id, phone, pin string) contracts.DriverProvisioned {
	t.Helper()
	cheap := platform.Argon2Params{Memory: 8, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 8}
	hash, err := cheap.Hash(pin)
	if err != nil {
		t.Fatal(err)
	}
	return contracts.DriverProvisioned{
		DriverID: id, Name: "Budi Santoso", Phone: phone, VehicleID: "veh-1", PinHash: hash, JoinedAt: contracts.At(Epoch.AddDate(0, -3, 0)),
	}
}

// Photo is a proof photo that passes the checks (it is not a decodable image: the checks only read the shape).
func Photo() string { return "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQEASABIAAD/2wBD" }
