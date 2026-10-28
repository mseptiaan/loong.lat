package plans

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
)

// ComputeEvent is one line of the plans.computeOptions stream (the frontend's ComputeEvent): a few `progress`
// lines, then `done` with the options.
type ComputeEvent struct {
	Type  string `json:"type"`
	Step  int    `json:"step,omitzero"`
	Total int    `json:"total,omitzero"`
	// Label is plain English, for a client that does not render Msg.
	Label string `json:"label,omitempty"`
	// Msg, on a progress line, says what a thorough search is doing inside the stage (trying experiments, a review
	// round, polishing); the screen translates it, and Label then says the same in English.
	Msg       *contracts.Msg       `json:"msg,omitempty"`
	OptionSet *contracts.OptionSet `json:"optionSet,omitempty"`
}

// Message key of the answer to an effort that is not one of contracts.Efforts.
const msgBadEffort = "srv.api.badEffort"

// parseEffort reads the effort of a compute request: empty means fast, anything else must be one of the levels.
func parseEffort(e contracts.Effort) (contracts.Effort, error) {
	switch {
	case e == "":
		return contracts.EffortFast, nil
	case slices.Contains(contracts.Efforts, e):
		return e, nil
	}
	return "", platform.Invalid("effort", contracts.Msg{Key: msgBadEffort})
}

// Compute asks the optimizer for the plan's options and stores them, replacing the options of an earlier
// computation (the choice among them is reset, as the new options may not contain it). effort says how hard to
// search; empty means fast. It relays the optimizer's stages, and for a thorough search its steps, to emit as they
// begin and ends with the options. Nothing is held while the optimizer works, so the shipper can keep editing the
// draft; if they changed what decides the options meanwhile, the result is thrown away as stale.
func (s *Service) Compute(ctx context.Context, customerID, id string, effort contracts.Effort, emit func(ComputeEvent) error) error {
	effort, err := parseEffort(effort)
	if err != nil {
		return err
	}
	rec, err := Load(ctx, s.pool, id)
	if err := ownedBy(rec, customerID, err); err != nil {
		return err
	}
	p := rec.Plan
	if !editable(p.Status) {
		return platform.Conflict(contracts.Msg{Key: "srv.api.planAlreadyConfirmed"})
	}
	vehicles, err := s.fleet.Active(ctx)
	if err != nil {
		return err
	}
	if len(vehicles) == 0 {
		return platform.Unavailable(contracts.Msg{Key: "srv.engine.noVehicles"})
	}
	hash := inputHash(p)

	// A progress line the client cannot receive any more means it left: stop asking the optimizer.
	quoteCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var emitErr error
	result, err := s.optimizer.Quote(quoteCtx, optimizerapi.QuoteRequest{
		PlanID: p.ID, Shipments: p.Shipments, Schedule: p.Schedule, Priority: p.Priority,
		Vehicles: vehicles, Now: contracts.At(s.now()),
		Effort: effort, AIReview: s.aiReview != nil && s.aiReview(ctx),
	}, func(pr optimizerapi.Progress) {
		if emitErr != nil {
			return
		}
		ev := ComputeEvent{Type: optimizerapi.EventProgress, Step: pr.Stage, Total: pr.Total, Label: stageLabel(pr.Stage, len(p.Shipments))}
		if pr.Msg != nil {
			ev.Msg, ev.Label = pr.Msg, searchLabel(*pr.Msg, ev.Label)
		}
		if emitErr = emit(ev); emitErr != nil {
			cancel(emitErr)
		}
	})
	if emitErr != nil {
		return emitErr
	}
	if err != nil {
		return quoteFailure(ctx, err)
	}

	set := result.OptionSet
	set.Report = nil // a report explains one computation; it is written afterwards, on request
	err = platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rec, err := lockOwned(ctx, tx, customerID, id)
		if err != nil {
			return err
		}
		p := &rec.Plan
		if !editable(p.Status) {
			return platform.Conflict(contracts.Msg{Key: "srv.api.planAlreadyConfirmed"})
		}
		if inputHash(*p) != hash {
			return platform.Conflict(contracts.Msg{Key: "srv.api.optionsStale"})
		}
		p.OptionSet, p.SelectedOptionID, p.Status = &set, "", contracts.PlanStatusOptionsReady
		p.DraftStep = max(p.DraftStep, 3)
		p.UpdatedAt = contracts.At(s.now())
		rec.DraftInputHash = hash
		rec.Quote = &Quote{Engine: result.Engine, RoadSource: result.RoadSource, Proven: result.Proven, Truncated: result.Truncated}
		if err := Save(ctx, tx, rec); err != nil {
			return err
		}
		return notify.PlanChanged(ctx, tx, customerID, id)
	})
	if err != nil {
		return err
	}
	return emit(ComputeEvent{Type: optimizerapi.EventDone, OptionSet: &set})
}

// stageLabel is the plain-English line shown while the optimizer works on a stage. The stages are the
// optimizer's: read the places, measure between them, combine them into routes, compare the results.
func stageLabel(stage, shipments int) string {
	plural := func(n int, one, other string) string {
		if n == 1 {
			return fmt.Sprintf(one, n)
		}
		return fmt.Sprintf(other, n)
	}
	switch stage {
	case 1:
		return plural(shipments*2, "Reading %d location", "Reading %d locations")
	case 2:
		return "Calculating distances and travel times between points"
	case 3:
		return plural(shipments, "Building vehicle combinations for %d shipment", "Building vehicle combinations for %d shipments")
	case 4:
		return "Comparing cost, distance, and time"
	}
	return "Working on your routes"
}

// searchLabel is the plain-English line for a step of a thorough search, which the optimizer reports as a message
// (srv.engine.search.*). A message this console does not know keeps the label of the stage it belongs to.
func searchLabel(m contracts.Msg, fallback string) string {
	count := func(name string) (int, bool) {
		switch v := m.Params[name].(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		}
		return 0, false
	}
	plural := func(n int, one, other string) string {
		if n == 1 {
			return fmt.Sprintf(one, n)
		}
		return fmt.Sprintf(other, n)
	}
	switch m.Key {
	case "srv.engine.search.experiments":
		if n, ok := count("n"); ok {
			return plural(n, "Trying %d way of grouping the stops", "Trying %d ways of grouping the stops")
		}
	case "srv.engine.search.polish":
		return "Polishing the best routes"
	case "srv.engine.search.round":
		if n, ok := count("round"); ok {
			return fmt.Sprintf("Review round %d", n)
		}
	case "srv.engine.search.reviewAI":
		return "The AI reviewer is checking the best routes"
	case "srv.engine.search.reviewRules":
		return "The built-in reviewer is checking the best routes"
	case "srv.engine.search.satisfied":
		return "The reviewer is satisfied with the result"
	case "srv.engine.search.more":
		if n, ok := count("n"); ok {
			return plural(n, "Trying %d more experiment", "Trying %d more experiments")
		}
	}
	return fallback
}

// quoteFailure turns what went wrong with a quote into the answer for the client: input the shipper can fix
// keeps its field and message, anything else is "the optimizer is unavailable".
func quoteFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err() // the client left; there is nobody to answer
	}
	if oe, ok := errors.AsType[*optimizerapi.Error](err); ok {
		if platform.ErrorCode(oe.Code) == platform.CodeInvalid {
			return platform.Invalid(oe.Field, oe.Msg)
		}
		return platform.Unavailable(oe.Msg).WithCause(err)
	}
	return platform.Unavailable(contracts.Msg{Key: "srv.engine.unavailable"}).WithCause(err)
}
