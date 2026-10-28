package plans

import (
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func draftOf(ids ...string) contracts.Plan {
	p := contracts.Plan{Schedule: contracts.Schedule{Mode: contracts.ScheduleModeNow}, Priority: contracts.Priority{Preset: contracts.PresetBalanced}}
	for _, id := range ids {
		p.Shipments = append(p.Shipments, testkit.Shipment(id))
	}
	return p
}

// edited returns a copy of the draft after change has been applied to its first shipment.
func edited(p contracts.Plan, change func(s *contracts.Shipment)) contracts.Plan {
	p.Shipments = slices.Clone(p.Shipments)
	s := p.Shipments[0]
	s.Items = slices.Clone(s.Items)
	change(&s)
	p.Shipments[0] = s
	return p
}

func TestInputHashIgnoresWhatDoesNotChangeTheRoutes(t *testing.T) {
	base := draftOf("s1", "s2")
	want := inputHash(base)
	if inputHash(draftOf("s1", "s2")) != want {
		t.Fatal("the hash is not deterministic")
	}
	same := map[string]contracts.Plan{
		"contact":     edited(base, func(s *contracts.Shipment) { s.Dropoff.ContactName, s.Dropoff.ContactPhone = "Other", "0899" }),
		"note":        edited(base, func(s *contracts.Shipment) { s.Pickup.Note = "Gate B" }),
		"place name":  edited(base, func(s *contracts.Shipment) { s.Pickup.Name, s.Pickup.Address = "Renamed", "Elsewhere" }),
		"item name":   edited(base, func(s *contracts.Shipment) { s.Items[0].Name, s.Items[0].CatalogKey = "Box", "box" }),
		"pin jitter":  edited(base, func(s *contracts.Shipment) { s.Pickup.Position.Lat += 0.000004 }),
		"wizard step": func() contracts.Plan { p := base; p.DraftStep = 4; return p }(),
		"preset text": func() contracts.Plan { p := base; p.Priority.Text = "cheap please"; return p }(),
	}
	for name, p := range same {
		if got := inputHash(p); got != want {
			t.Errorf("%s changed the hash", name)
		}
	}
}

func TestInputHashChangesWithWhatDecidesTheOptions(t *testing.T) {
	base := draftOf("s1", "s2")
	want := inputHash(base)
	at := time.Date(2026, 10, 21, 5, 0, 0, 0, time.UTC)
	window := func(from time.Time) *contracts.TimeWindow {
		return &contracts.TimeWindow{From: contracts.At(from), To: contracts.At(from.Add(time.Hour))}
	}
	scheduled := base
	scheduled.Schedule = contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: window(at)}

	different := map[string]contracts.Plan{
		"pickup place":    edited(base, func(s *contracts.Shipment) { s.Pickup.Position.Lat += 0.0001 }),
		"dropoff place":   edited(base, func(s *contracts.Shipment) { s.Dropoff.Position.Lng += 0.0001 }),
		"weight":          edited(base, func(s *contracts.Shipment) { s.Items[0].WeightKg++ }),
		"size":            edited(base, func(s *contracts.Shipment) { s.Items[0].LengthCm++ }),
		"quantity":        edited(base, func(s *contracts.Shipment) { s.Items[0].Qty++ }),
		"fragile":         edited(base, func(s *contracts.Shipment) { s.Items[0].Fragile = true }),
		"deadline":        edited(base, func(s *contracts.Shipment) { s.Deadline = contracts.At(at) }),
		"another item":    edited(base, func(s *contracts.Shipment) { s.Items = append(s.Items, s.Items[0]) }),
		"fewer shipments": draftOf("s1"),
		"scheduled":       scheduled,
		"preset":          func() contracts.Plan { p := base; p.Priority.Preset = contracts.PresetCheapest; return p }(),
	}
	later := scheduled
	later.Schedule.Window = window(at.Add(time.Hour))
	different["window"] = later

	seen := map[string]string{"base": want}
	for name, p := range different {
		got := inputHash(p)
		if got == want {
			t.Errorf("%s did not change the hash", name)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("%s and %s hash alike", name, other)
		}
		seen[got] = name
	}
}
