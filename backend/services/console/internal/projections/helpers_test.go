package projections

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

var t0 = time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)

// at is the time of the nth minute of the test's story.
func at(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }

// shop is a confirmed plan waiting for a driver: one route with two pickups and then two drop-offs.
type shop struct {
	t        *testing.T
	pool     *pgxpool.Pool
	customer string
	planID   string
	routeID  string
	stops    []string
	initial  contracts.Route
}

func newShop(t *testing.T) *shop {
	t.Helper()
	pool := testkit.DB(t)
	s := &shop{t: t, pool: pool, customer: testkit.Customer(t, pool, "Dina")}
	testkit.ProjectVehicle(t, pool, testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor))
	s.insertPlan("pln-1")
	return s
}

// insertPlan stores a confirmed plan with this id and returns it.
func (s *shop) insertPlan(id string) contracts.Plan {
	s.t.Helper()
	s.planID, s.routeID = id, id+":r1"
	kinds := []contracts.StopKind{contracts.StopKindPickup, contracts.StopKindPickup, contracts.StopKindDropoff, contracts.StopKindDropoff}
	places := []string{"Kemang", "Blok M", "Senayan", "Pondok Indah"}
	route := contracts.Route{
		ID: s.routeID, PlanID: id, VehicleType: contracts.VehicleTypeMotor, Status: contracts.RouteStatusAwaitingDriver,
		StartPosition: contracts.LatLng{Lat: -6.26, Lng: 106.81}, StartAt: contracts.At(t0), CostRp: 25_000,
	}
	s.stops = nil
	for i, kind := range kinds {
		stopID := s.routeID + ":s" + string(rune('1'+i))
		s.stops = append(s.stops, stopID)
		route.Stops = append(route.Stops, contracts.RouteStop{
			ID: stopID, Seq: i + 1, Kind: kind, ShipmentID: "s1", Status: contracts.StopStatusWaiting, LegKm: 1, LegMin: 5,
			Waypoint: contracts.Waypoint{PlaceRef: contracts.PlaceRef{Name: places[i], Position: contracts.LatLng{Lat: -6.26 + float64(i)/100, Lng: 106.81}}},
		})
		if kind == contracts.StopKindDropoff {
			route.Stops[i].DeliveryCode = "4321"
		}
	}
	s.initial = route
	plan := contracts.Plan{
		ID: id, Code: "PL-" + strings.TrimPrefix(id, "pln-"), UserID: s.customer, Status: contracts.PlanStatusAwaitingDriver,
		Schedule: contracts.Schedule{Mode: contracts.ScheduleModeNow}, Priority: contracts.Priority{Preset: contracts.PresetBalanced},
		Routes: []contracts.Route{route}, PaymentMethod: contracts.PaymentWallet, TotalCostRp: 25_000,
		CreatedAt: contracts.At(t0), UpdatedAt: contracts.At(t0), ConfirmedAt: contracts.At(t0), DraftStep: 4,
	}
	plans.AppendEvent(&plan, t0, contracts.Msg{Key: "srv.api.event.confirmed"})
	if err := plans.Insert(s.t.Context(), s.pool, plans.Record{Plan: plan, Version: 1}); err != nil {
		s.t.Fatal(err)
	}
	return plan
}

// envelope builds an event that happened at the given time.
func envelope(t *testing.T, eventType, key string, version int64, when time.Time, data any) contracts.Envelope {
	t.Helper()
	env, err := contracts.NewEnvelope("test", eventType, key, version, data)
	if err != nil {
		t.Fatal(err)
	}
	env.OccurredAt = contracts.At(when)
	return env
}

// apply runs a handler on an event in a transaction of its own, as the consumer would, and returns its error.
func (s *shop) apply(h eventbus.Handler, env contracts.Envelope) error {
	return platform.WithTx(s.t.Context(), s.pool, func(tx pgx.Tx) error { return h(s.t.Context(), tx, env) })
}

func (s *shop) mustApply(h eventbus.Handler, env contracts.Envelope) {
	s.t.Helper()
	if err := s.apply(h, env); err != nil {
		s.t.Fatal(err)
	}
}

// withSecondRoute adds a route nobody has been assigned to, so finishing the first leaves the plan open.
func (s *shop) withSecondRoute() {
	s.t.Helper()
	err := platform.WithTx(s.t.Context(), s.pool, func(tx pgx.Tx) error {
		rec, err := plans.Lock(s.t.Context(), tx, s.planID)
		if err != nil {
			return err
		}
		second := s.initial
		second.ID = s.planID + ":r2"
		second.Stops = nil
		for i, kind := range []contracts.StopKind{contracts.StopKindPickup, contracts.StopKindDropoff} {
			stop := s.initial.Stops[i*2]
			stop.ID, stop.Kind, stop.Seq = second.ID+":s"+string(rune('1'+i)), kind, i+1
			second.Stops = append(second.Stops, stop)
		}
		rec.Plan.Routes = append(rec.Plan.Routes, second)
		return plans.Save(s.t.Context(), tx, rec)
	})
	if err != nil {
		s.t.Fatal(err)
	}
}

func (s *shop) record() plans.Record {
	s.t.Helper()
	rec, err := plans.Load(s.t.Context(), s.pool, s.planID)
	if err != nil {
		s.t.Fatal(err)
	}
	return rec
}

func (s *shop) route() contracts.Route { return s.record().Plan.Routes[0] }

// Event builders: version is the owner's counter (assign_version for dispatch, exec_version for execution).

func (s *shop) assigned(version int64, driver, vehicle string, when time.Time) contracts.Envelope {
	r := routeplan.RebaseStart(s.initial, contracts.LatLng{Lat: -6.27, Lng: 106.80}, when.Add(2*time.Minute))
	r = routeplan.Assign(r, driver, vehicle, when)
	return envelope(s.t, contracts.TypeRouteAssigned, s.routeID, version, when, contracts.RouteAssigned{
		PlanID: s.planID, PlanCode: "PL-2001", CustomerName: "Dina", DriverID: driver, VehicleID: vehicle, StartAt: r.StartAt, Route: r,
	})
}

func (s *shop) unassigned(version int64, when time.Time) contracts.Envelope {
	return envelope(s.t, contracts.TypeRouteUnassigned, s.routeID, version, when, contracts.RouteUnassigned{PlanID: s.planID, RouteID: s.routeID, DriverID: "drv-1"})
}

func (s *shop) started(version int64, when time.Time) contracts.Envelope {
	return envelope(s.t, contracts.TypeRouteStarted, s.routeID, version, when, contracts.RouteStarted{PlanID: s.planID, RouteID: s.routeID, DriverID: "drv-1", StartedAt: contracts.At(when)})
}

func (s *shop) arrived(version int64, stop int, when time.Time) contracts.Envelope {
	return envelope(s.t, contracts.TypeStopArrived, s.routeID, version, when, contracts.StopArrived{PlanID: s.planID, RouteID: s.routeID, StopID: s.stops[stop-1], ArrivedAt: contracts.At(when)})
}

func (s *shop) completed(version int64, stop int, when time.Time) contracts.Envelope {
	ev := contracts.StopCompleted{PlanID: s.planID, RouteID: s.routeID, StopID: s.stops[stop-1], CompletedAt: contracts.At(when)}
	if stop > 2 {
		ev.Proof = &contracts.DeliveryProof{Kind: contracts.ProofKindOTP, At: contracts.At(when)}
	}
	return envelope(s.t, contracts.TypeStopCompleted, s.routeID, version, when, ev)
}

func (s *shop) issue(version int64, stop int, when time.Time) contracts.Envelope {
	return envelope(s.t, contracts.TypeStopIssue, s.routeID, version, when, contracts.StopIssue{
		PlanID: s.planID, RouteID: s.routeID, StopID: s.stops[stop-1], Kind: contracts.IssueKindNobodyThere, Note: "gate locked",
		Issue: contracts.Msg{Key: "srv.ops.issue.withNote", Params: map[string]any{"issue": contracts.Msg{Key: "dom.issue.tidak_ada_orang"}, "note": "gate locked"}}, ReportedAt: contracts.At(when),
	})
}

func (s *shop) routeCompleted(version int64, when time.Time) contracts.Envelope {
	return envelope(s.t, contracts.TypeRouteCompleted, s.routeID, version, when, contracts.RouteCompleted{PlanID: s.planID, RouteID: s.routeID, DriverID: "drv-1", FinishedAt: contracts.At(when)})
}

// Handlers under test, by name.
var (
	assignHandler    eventbus.Handler = routeAssigned
	unassignHandler  eventbus.Handler = routeUnassigned
	startHandler     eventbus.Handler = routeStarted
	arriveHandler    eventbus.Handler = stopArrived
	completeHandler  eventbus.Handler = stopCompleted
	issueHandler     eventbus.Handler = stopIssue
	routeDoneHandler eventbus.Handler = routeCompleted

	routeDelayedHandler eventbus.Handler = routeDelayed
)

// eventKeys lists the message keys of the plan's timeline.
func eventKeys(p contracts.Plan) []string {
	keys := []string{}
	for _, e := range p.Events {
		keys = append(keys, e.Text.Key)
	}
	return keys
}

func plansLoad(t *testing.T, s *shop, id string) (plans.Record, error) {
	t.Helper()
	return plans.Load(t.Context(), s.pool, id)
}

// envelopeData is the JSON of a payload, for building an event whose payload differs from its builder's.
func envelopeData(t *testing.T, payload any) []byte {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
