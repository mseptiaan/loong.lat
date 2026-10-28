// Package drivers holds the driver accounts this service serves: the master data projected from back office's
// events, and the state it owns itself (presence, position, trips).
package drivers

import (
	"strings"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// Status is the frontend's DriverStatus.
type Status string

const (
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
	// StatusOnDuty is "bertugas": the driver is on a running route.
	StatusOnDuty Status = "bertugas"
)

// Rating is every driver's rating: the master data events carry none yet, and the app's profile screen needs a
// number.
const Rating = 5.0

// Driver is the frontend's Driver (packages/domain/src/types.ts), which contracts has no counterpart of.
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

// Profile is the result of driver.me: the driver and, if one is assigned and known, the vehicle.
type Profile struct {
	Driver  Driver             `json:"driver"`
	Vehicle *contracts.Vehicle `json:"vehicle,omitempty"`
}

// NormalizePhone reduces a phone number to digits with a leading 0 in place of the 62 country code, so that
// "0812-3456-7801" and "+62 812 3456 7801" are the same login.
func NormalizePhone(s string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if rest, ok := strings.CutPrefix(digits, "62"); ok {
		return "0" + rest
	}
	return digits
}
