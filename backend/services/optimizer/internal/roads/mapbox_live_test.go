package roads

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// TestLiveTollFreeTableMatchesIndependentRequests asks the real Mapbox for a table of six Jakarta places and for each
// pair separately, and requires the same distances: a leg measured inside a longer request must be the route a request
// between just its two places gives. It is skipped unless LOONG_LIVE_MAPBOX=1 and MAPBOX_TOKEN is set:
//
//	cd backend && set -a && . ./.env && set +a && LOONG_LIVE_MAPBOX=1 go test -count=1 -run LiveTollFree -v ./services/optimizer/internal/roads
func TestLiveTollFreeTableMatchesIndependentRequests(t *testing.T) {
	if os.Getenv("LOONG_LIVE_MAPBOX") != "1" || os.Getenv("MAPBOX_TOKEN") == "" {
		t.Skip("set LOONG_LIVE_MAPBOX=1 and MAPBOX_TOKEN to run against the real Mapbox")
	}
	ps := []contracts.LatLng{
		{Lat: -6.2608, Lng: 106.8133}, {Lat: -6.2275, Lng: 106.8029}, {Lat: -6.2441, Lng: 106.7993},
		{Lat: -6.1100, Lng: 106.8800}, {Lat: -6.2383, Lng: 106.9756}, {Lat: -6.2175, Lng: 106.8454},
	}
	m := &Mapbox{Token: os.Getenv("MAPBOX_TOKEN")}
	got, err := m.Matrix(context.Background(), ps)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "mapbox" || got.Estimated != 0 {
		t.Fatalf("source %q, estimated %d", got.Source, got.Estimated)
	}
	for i := range ps {
		for j := range ps {
			if i == j {
				continue
			}
			d, err := m.legs(context.Background(), []contracts.LatLng{ps[i], ps[j]})
			if err != nil {
				t.Fatal(err)
			}
			if want := int32(math.Round(d[0])); got.DistM[i*len(ps)+j] != want {
				t.Errorf("%d->%d: table %d m, separate request %d m", i, j, got.DistM[i*len(ps)+j], want)
			}
		}
	}
}
