package plans

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/wallet"
)

// clock is a settable time source for the service under test.
type clock struct{ at time.Time }

func (c *clock) now() time.Time          { return c.at }
func (c *clock) advance(d time.Duration) { c.at = c.at.Add(d) }

// world is a plans service on a fresh database with one customer, one active vehicle and a fake optimizer.
type world struct {
	t         *testing.T
	pool      *pgxpool.Pool
	svc       *Service
	optimizer *testkit.Optimizer
	clock     *clock
	customer  string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool := testkit.DB(t)
	pub := testkit.Publisher(pool)
	opt := testkit.NewOptimizer(t)
	svc := New(pool, pub, wallet.New(pool, pub), fleet.New(pool), &optimizerapi.Client{BaseURL: opt.URL})
	clk := &clock{at: time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)}
	svc.now = clk.now
	testkit.ProjectVehicle(t, pool, testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor))
	return &world{t: t, pool: pool, svc: svc, optimizer: opt, clock: clk, customer: testkit.Customer(t, pool, "Dina")}
}

func apiError(t *testing.T, err error) *platform.APIError {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok {
		t.Fatalf("want a platform.APIError, got %T: %v", err, err)
	}
	return apiErr
}

// wantError checks that err is the API error with this code and message key.
func wantError(t *testing.T, err error, code platform.ErrorCode, key string) *platform.APIError {
	t.Helper()
	got := apiError(t, err)
	if got.Code != code || got.Msg.Key != key {
		t.Fatalf("got %s %q, want %s %q", got.Code, got.Msg.Key, code, key)
	}
	return got
}

func (w *world) draft(shipments ...string) contracts.Plan {
	w.t.Helper()
	p, err := w.svc.CreateDraft(w.t.Context(), w.customer)
	if err != nil {
		w.t.Fatal(err)
	}
	if len(shipments) == 0 {
		return p
	}
	var list []contracts.Shipment
	for _, id := range shipments {
		list = append(list, testkit.Shipment(id))
	}
	p, err = w.svc.SaveDraft(w.t.Context(), w.customer, p.ID, DraftPatch{Shipments: list})
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

// compute asks for the options (the default effort) and returns the stream's events.
func (w *world) compute(planID string) ([]ComputeEvent, error) {
	w.t.Helper()
	return w.computeWith(planID, "")
}

// computeWith asks for the options at an effort and returns the stream's events.
func (w *world) computeWith(planID string, effort contracts.Effort) ([]ComputeEvent, error) {
	w.t.Helper()
	var events []ComputeEvent
	err := w.svc.Compute(w.t.Context(), w.customer, planID, effort, func(ev ComputeEvent) error {
		events = append(events, ev)
		return nil
	})
	return events, err
}

// ready is a plan with its options computed and the one option chosen: the state confirm needs.
func (w *world) ready(shipments ...string) contracts.Plan {
	w.t.Helper()
	if len(shipments) == 0 {
		shipments = []string{"s1", "s2"}
	}
	p := w.draft(shipments...)
	events, err := w.compute(p.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	set := events[len(events)-1].OptionSet
	p, err = w.svc.ChooseOption(w.t.Context(), w.customer, p.ID, set.Options[0].ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func (w *world) balance() int64 {
	w.t.Helper()
	var balance int64
	if err := w.pool.QueryRow(w.t.Context(), `SELECT balance_rp FROM wallets WHERE customer_id = $1`, w.customer).Scan(&balance); err != nil {
		w.t.Fatal(err)
	}
	return balance
}

func (w *world) stored(planID string) Record {
	w.t.Helper()
	rec, err := Load(w.t.Context(), w.pool, planID)
	if err != nil {
		w.t.Fatal(err)
	}
	return rec
}
