// Package plans is a shipment plan from first draft to the last delivered stop, as the shipper sees it: they
// draft it, ask the optimizer for options, choose one, pay for it, and then follow it. The console owns the
// commercial half of that life (draft, quote, confirm); once confirmed, back office and the drivers change the
// plan and the console only follows (see the projections package).
package plans

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/auth"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/wallet"
)

// Filter is the frontend's PlanFilter.
type Filter struct {
	Status      []contracts.PlanStatus `json:"status"`
	UserID      string                 `json:"userId"`
	Q           string                 `json:"q"`
	VehicleType contracts.VehicleType  `json:"vehicleType"`
	// NeedsDriver keeps only plans with a route that has no driver.
	NeedsDriver bool           `json:"needsDriver"`
	From        contracts.Time `json:"from,omitzero"`
	To          contracts.Time `json:"to,omitzero"`
}

// DraftPatch is the frontend's DraftPatch: the parts of a draft that changed. A part left out stays as it was.
type DraftPatch struct {
	Shipments []contracts.Shipment `json:"shipments"`
	Schedule  *contracts.Schedule  `json:"schedule"`
	Priority  *contracts.Priority  `json:"priority"`
	DraftStep *int                 `json:"draftStep"`
}

// wizardSteps is how many steps the wizard has; draftStep says which one the shipper reached.
const wizardSteps = 4

func (d DraftPatch) validate() error {
	bad := func(field string) error { return platform.Invalid(field, contracts.Msg{Key: platform.MsgBadRequest}) }
	if s := d.Schedule; s != nil && s.Mode != contracts.ScheduleModeNow && s.Mode != contracts.ScheduleModeScheduled {
		return bad("schedule")
	}
	if p := d.Priority; p != nil {
		switch p.Preset {
		case contracts.PresetCheapest, contracts.PresetShortest, contracts.PresetFastest, contracts.PresetBalanced:
		default:
			return bad("priority")
		}
	}
	if d.DraftStep != nil && (*d.DraftStep < 1 || *d.DraftStep > wizardSteps) {
		return bad("draftStep")
	}
	return nil
}

// Service runs the customer's side of plans.
type Service struct {
	pool      *pgxpool.Pool
	pub       *eventbus.Publisher
	wallet    *wallet.Service
	fleet     *fleet.Projection
	optimizer *optimizerapi.Client
	now       func() time.Time
	log       *slog.Logger
	// aiReview says whether a thorough quote may let the AI review it: the admin's AI switch is on and a gateway is
	// reachable. Nil means no.
	aiReview func(context.Context) bool

	// cancelTimeout is how long drivers have to vote on a cancel before it is aborted.
	cancelTimeout time.Duration
}

// Option tunes a Service.
type Option func(*Service)

// WithCancelTimeout sets how long a cancel waits for the drivers' votes (default DefaultCancelTimeout).
func WithCancelTimeout(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.cancelTimeout = d
		}
	}
}

// WithAIReview tells the service whether the AI may review a quote (the admin's AI switch for the assistant, with a
// gateway configured); the optimizer then lets the model choose the experiments of each review round instead of the
// built-in rules. Without it the rules always review.
func WithAIReview(allowed func(context.Context) bool) Option {
	return func(s *Service) { s.aiReview = allowed }
}

// WithLogger sets the logger of the background work (the cancel sweeper); the default discards.
func WithLogger(log *slog.Logger) Option { return func(s *Service) { s.log = log } }

func New(pool *pgxpool.Pool, pub *eventbus.Publisher, w *wallet.Service, f *fleet.Projection, optimizer *optimizerapi.Client, opts ...Option) *Service {
	s := &Service{
		pool: pool, pub: pub, wallet: w, fleet: f, optimizer: optimizer, now: time.Now,
		log: slog.New(slog.DiscardHandler), cancelTimeout: DefaultCancelTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// editable plans are those still being drafted; a confirmed plan can no longer be changed by its owner.
func editable(s contracts.PlanStatus) bool {
	return s == contracts.PlanStatusDraft || s == contracts.PlanStatusOptionsReady
}

// ownedBy turns the result of reading a plan into the answer for a customer: 404 when there is none, 403 when
// it is someone else's.
func ownedBy(rec Record, customerID string, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return platform.NotFound(contracts.Msg{Key: "srv.ops.err.planNotFound"})
	case err != nil:
		return err
	case rec.Plan.UserID != customerID:
		return platform.Denied(contracts.Msg{Key: "srv.api.planNoAccess"})
	}
	return nil
}

func lockOwned(ctx context.Context, tx pgx.Tx, customerID, id string) (Record, error) {
	rec, err := Lock(ctx, tx, id)
	return rec, ownedBy(rec, customerID, err)
}

// CreateDraft starts an empty plan.
func (s *Service) CreateDraft(ctx context.Context, customerID string) (contracts.Plan, error) {
	if err := auth.EnsureActive(ctx, s.pool, customerID); err != nil {
		return contracts.Plan{}, err
	}
	var seq int64
	if err := s.pool.QueryRow(ctx, `SELECT nextval('plan_code_seq')`).Scan(&seq); err != nil {
		return contracts.Plan{}, err
	}
	now := s.now()
	p := contracts.Plan{
		ID: "pln-" + uuid.NewV7().String(), Code: "PL-" + strconv.FormatInt(seq, 10), UserID: customerID,
		Status:    contracts.PlanStatusDraft,
		Schedule:  contracts.Schedule{Mode: contracts.ScheduleModeNow},
		Priority:  contracts.Priority{Preset: contracts.PresetBalanced},
		CreatedAt: contracts.At(now), UpdatedAt: contracts.At(now), DraftStep: 1,
	}
	AppendEvent(&p, now, contracts.Msg{Key: "srv.api.event.draftCreated"})
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := Insert(ctx, tx, Record{Plan: p}); err != nil {
			return err
		}
		return notify.PlanChanged(ctx, tx, customerID, p.ID)
	})
	return p, err
}

// SaveDraft applies the patch. When it changes what decides the options (places, items, deadlines, schedule,
// preset) the computed options are dropped and the plan is a plain draft again, because they no longer
// describe it.
func (s *Service) SaveDraft(ctx context.Context, customerID, id string, patch DraftPatch) (contracts.Plan, error) {
	if err := patch.validate(); err != nil {
		return contracts.Plan{}, err
	}
	var saved contracts.Plan
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rec, err := lockOwned(ctx, tx, customerID, id)
		if err != nil {
			return err
		}
		p := &rec.Plan
		if !editable(p.Status) {
			return platform.Conflict(contracts.Msg{Key: "srv.api.planLocked"})
		}
		if patch.Shipments != nil {
			p.Shipments = patch.Shipments
		}
		if patch.Schedule != nil {
			p.Schedule = *patch.Schedule
		}
		if patch.Priority != nil {
			p.Priority = *patch.Priority
		}
		if patch.DraftStep != nil {
			p.DraftStep = *patch.DraftStep
		}
		if p.OptionSet != nil && rec.DraftInputHash != inputHash(*p) {
			p.OptionSet, p.SelectedOptionID, p.Status = nil, "", contracts.PlanStatusDraft
			rec.DraftInputHash, rec.Quote = "", nil
		}
		p.UpdatedAt = contracts.At(s.now())
		if err := Save(ctx, tx, rec); err != nil {
			return err
		}
		saved = *p
		return notify.PlanChanged(ctx, tx, customerID, id)
	})
	return saved, err
}

// Get is one of the customer's plans.
func (s *Service) Get(ctx context.Context, customerID, id string) (contracts.Plan, error) {
	rec, err := Load(ctx, s.pool, id)
	if err := ownedBy(rec, customerID, err); err != nil {
		return contracts.Plan{}, err
	}
	return rec.Plan, nil
}

// List is the customer's plans, drafts included, most recently changed first.
func (s *Service) List(ctx context.Context, customerID string, f Filter) ([]Summary, error) {
	summaries := []Summary{}
	if f.UserID != "" && f.UserID != customerID {
		return summaries, nil
	}
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM customers WHERE id = $1`, customerID).Scan(&name); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT snapshot FROM plans WHERE customer_id = $1 ORDER BY updated_at DESC, id DESC`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var p contracts.Plan
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("plans: stored snapshot: %w", err)
		}
		if sum := Summarize(p, name); sum.matches(f) {
			summaries = append(summaries, sum)
		}
	}
	return summaries, rows.Err()
}

// Remove deletes a draft.
func (s *Service) Remove(ctx context.Context, customerID, id string) error {
	return platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rec, err := lockOwned(ctx, tx, customerID, id)
		if err != nil {
			return err
		}
		if !editable(rec.Plan.Status) {
			return platform.Conflict(contracts.Msg{Key: "srv.api.draftOnlyDelete"})
		}
		if _, err := tx.Exec(ctx, `DELETE FROM plans WHERE id = $1`, id); err != nil {
			return err
		}
		return notify.PlanChanged(ctx, tx, customerID, id)
	})
}

// ChooseOption selects one of the computed options.
func (s *Service) ChooseOption(ctx context.Context, customerID, id, optionID string) (contracts.Plan, error) {
	var chosen contracts.Plan
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rec, err := lockOwned(ctx, tx, customerID, id)
		if err != nil {
			return err
		}
		p := &rec.Plan
		if !editable(p.Status) {
			return platform.Conflict(contracts.Msg{Key: "srv.api.planLocked"})
		}
		if _, ok := option(*p, optionID); !ok {
			return platform.Invalid("optionId", contracts.Msg{Key: "srv.api.optionNotFound"})
		}
		p.SelectedOptionID = optionID
		p.UpdatedAt = contracts.At(s.now())
		if err := Save(ctx, tx, rec); err != nil {
			return err
		}
		chosen = *p
		return notify.PlanChanged(ctx, tx, customerID, id)
	})
	return chosen, err
}

// option finds a computed option of the plan by id.
func option(p contracts.Plan, id string) (contracts.PlanOption, bool) {
	if p.OptionSet == nil || id == "" {
		return contracts.PlanOption{}, false
	}
	for _, o := range p.OptionSet.Options {
		if o.ID == id {
			return o, true
		}
	}
	return contracts.PlanOption{}, false
}

// Confirm pays for the chosen option from the wallet and hands the plan over for dispatch, all in one
// transaction: the plan and the wallet are locked, the draft must still be what the options were computed for,
// the wallet is debited (a short balance fails here, before the plan is touched), the quoted routes become the
// plan's real ones, and plan.confirmed and wallet.charged go to the outbox. Either all of it happens or none.
// Only the wallet can pay for now.
func (s *Service) Confirm(ctx context.Context, customerID, id string, method contracts.PaymentMethod) (contracts.Plan, error) {
	if method != contracts.PaymentWallet {
		return contracts.Plan{}, platform.Invalid("paymentMethod", contracts.Msg{Key: "srv.ops.err.chooseMethod"})
	}
	var confirmed contracts.Plan
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rec, err := lockOwned(ctx, tx, customerID, id)
		if err != nil {
			return err
		}
		if err := auth.EnsureActive(ctx, tx, customerID); err != nil {
			return err
		}
		p := &rec.Plan
		if p.Status != contracts.PlanStatusOptionsReady {
			return platform.Conflict(contracts.Msg{Key: "srv.api.planNotReady"})
		}
		chosen, ok := option(*p, p.SelectedOptionID)
		if !ok {
			return platform.Invalid("optionId", contracts.Msg{Key: "srv.api.pickOption"})
		}
		if rec.DraftInputHash != inputHash(*p) {
			return platform.Conflict(contracts.Msg{Key: "srv.api.optionsStale"})
		}

		cost := chosen.Totals.CostRp
		if err := s.wallet.ChargePlan(ctx, tx, customerID, p.ID, p.Code, cost); err != nil {
			return err
		}
		routes, err := routeplan.RoutesFromOption(p.ID, chosen, nil)
		if err != nil {
			return err
		}

		now := s.now()
		p.Routes, p.Status, p.PaymentMethod, p.TotalCostRp = routes, contracts.PlanStatusAwaitingDriver, method, cost
		p.ConfirmedAt, p.DraftStep = contracts.At(now), wizardSteps
		AppendEvent(p, now, contracts.Msg{Key: "srv.api.event.confirmed", Params: map[string]any{"amount": cost, "option": chosen.Label}})
		rec.Version++
		if err := Save(ctx, tx, rec); err != nil {
			return err
		}

		// The alternatives stay with the shipper; back office and drivers get the plan that was bought.
		shared := *p
		shared.OptionSet = nil
		shared.SavingsRp, shared.SavingsPct = new(chosen.Savings.CostRp), new(chosen.Savings.Pct)
		err = s.pub.Enqueue(ctx, tx, eventbus.Event{
			Type: contracts.TypePlanConfirmed, Key: p.ID, AggregateVersion: rec.Version,
			Data: contracts.PlanConfirmed{Plan: shared, Payment: contracts.Payment{Method: method, AmountRp: cost, PaidAt: p.ConfirmedAt}},
		})
		if err != nil {
			return err
		}
		err = notify.Notice(ctx, tx, customerID, notices.Notice{
			At: now, PlanID: p.ID, Key: "confirm:" + p.ID,
			Title: contracts.Msg{Key: "srv.api.notif.confirmed.title"},
			Body:  contracts.Msg{Key: "srv.api.notif.confirmed.body", Params: map[string]any{"code": p.Code}},
		})
		if err != nil {
			return err
		}
		confirmed = *p
		return notify.PlanChanged(ctx, tx, customerID, id)
	})
	return confirmed, err
}
