package drivers

import (
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

func pos(lat, lng float64) contracts.LatLng { return contracts.LatLng{Lat: lat, Lng: lng} }

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"0812-3456-7801":    "081234567801",
		"081234567801":      "081234567801",
		"+62 812 3456 7801": "081234567801",
		"6281234567801":     "081234567801",
		"(0812) 3456.7801":  "081234567801",
		"":                  "",
		"abc":               "",
		// only a leading country code is rewritten
		"0862123": "0862123",
	} {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFixValidity(t *testing.T) {
	for name, tc := range map[string]struct {
		fix Fix
		ok  bool
	}{
		"Jakarta":           {Fix{Position: pos(-6.2, 106.8), SpeedKmh: 27}, true},
		"parked":            {Fix{}, true},
		"north of the pole": {Fix{Position: pos(91, 0)}, false},
		"east of the globe": {Fix{Position: pos(0, 181)}, false},
		"negative speed":    {Fix{SpeedKmh: -1}, false},
		"absurd speed":      {Fix{SpeedKmh: 401}, false},
	} {
		if got := tc.fix.valid(); got != tc.ok {
			t.Errorf("%s: valid = %v, want %v", name, got, tc.ok)
		}
	}
}
