package plans

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// ErrNotFound means no plan has the id.
var ErrNotFound = errors.New("plans: plan not found")

// RouteVersions are the last versions applied to a route, one per event family: assign_version counts what
// back office decided (loong.dispatch.v1), exec_version what the driver did (loong.execution.v1). They are
// separate because each owner numbers its own events.
type RouteVersions struct {
	Assign int64 `json:"assign,omitzero"`
	Exec   int64 `json:"exec,omitzero"`
}

// Quote says how the stored option set was produced.
type Quote struct {
	Engine     string `json:"engine,omitempty"`
	RoadSource string `json:"roadSource,omitempty"`
	// Proven: every option was confirmed optimal by exhaustive search.
	Proven bool `json:"proven,omitzero"`
	// Truncated: a time limit cut the search short.
	Truncated bool `json:"truncated,omitzero"`
}

// Record is a plan as stored: the wire snapshot the dashboard shows and what the console tracks beside it.
type Record struct {
	Plan contracts.Plan
	// Version rises with every commercial change; plan.confirmed carries it as its aggregateVersion.
	Version int64
	// DraftInputHash identifies the draft input the stored option set was computed from; empty without one.
	DraftInputHash string
	// Quote describes the stored option set; nil without one.
	Quote         *Quote
	RouteVersions map[string]RouteVersions
}

const selectColumns = `snapshot, version, draft_input_hash, optimizer, route_versions`

// Load reads a plan. It returns ErrNotFound when there is none.
func Load(ctx context.Context, db platform.Querier, id string) (Record, error) {
	return read(ctx, db, id, "")
}

// Lock reads a plan and keeps its row locked until tx ends, so concurrent changes to one plan queue up. Every
// writer of a plan (requests and projections alike) goes through it.
func Lock(ctx context.Context, tx pgx.Tx, id string) (Record, error) {
	return read(ctx, tx, id, " FOR UPDATE")
}

func read(ctx context.Context, db platform.Querier, id, suffix string) (Record, error) {
	var snapshot, quote, versions []byte
	rec := Record{}
	err := db.QueryRow(ctx, `SELECT `+selectColumns+` FROM plans WHERE id = $1`+suffix, id).
		Scan(&snapshot, &rec.Version, &rec.DraftInputHash, &quote, &versions)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	if err := json.Unmarshal(snapshot, &rec.Plan); err != nil {
		return Record{}, fmt.Errorf("plan %s snapshot: %w", id, err)
	}
	if len(quote) > 0 {
		rec.Quote = &Quote{}
		if err := json.Unmarshal(quote, rec.Quote); err != nil {
			return Record{}, fmt.Errorf("plan %s quote: %w", id, err)
		}
	}
	if err := json.Unmarshal(versions, &rec.RouteVersions); err != nil {
		return Record{}, fmt.Errorf("plan %s route versions: %w", id, err)
	}
	return rec, nil
}

// encode renders the parts of a record that are stored as JSON.
func (r Record) encode() (snapshot, quote, versions []byte, err error) {
	if snapshot, err = json.Marshal(r.Plan); err != nil {
		return nil, nil, nil, err
	}
	if r.Quote != nil {
		if quote, err = json.Marshal(r.Quote); err != nil {
			return nil, nil, nil, err
		}
	}
	if versions, err = json.Marshal(r.RouteVersions); err != nil {
		return nil, nil, nil, err
	}
	return snapshot, quote, versions, nil
}

// Insert stores a new plan.
func Insert(ctx context.Context, db platform.Querier, rec Record) error {
	snapshot, quote, versions, err := rec.encode()
	if err != nil {
		return err
	}
	p := rec.Plan
	_, err = db.Exec(ctx,
		`INSERT INTO plans (id, code, customer_id, status, version, draft_input_hash, optimizer, route_versions, snapshot, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		p.ID, p.Code, p.UserID, string(p.Status), rec.Version, rec.DraftInputHash, quote, versions, snapshot,
		p.CreatedAt.Time, p.UpdatedAt.Time)
	return err
}

// Save writes a plan back. The snapshot and the columns that repeat its fields are written together, so they
// never disagree.
func Save(ctx context.Context, db platform.Querier, rec Record) error {
	snapshot, quote, versions, err := rec.encode()
	if err != nil {
		return err
	}
	p := rec.Plan
	tag, err := db.Exec(ctx,
		`UPDATE plans
		    SET status = $2, version = $3, draft_input_hash = $4, optimizer = $5, route_versions = $6,
		        snapshot = $7, updated_at = $8
		  WHERE id = $1`,
		p.ID, string(p.Status), rec.Version, rec.DraftInputHash, quote, versions, snapshot, p.UpdatedAt.Time)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AppendEvent adds a line to the plan's timeline, which the dashboard shows, and moves updatedAt forward to it.
func AppendEvent(p *contracts.Plan, at time.Time, text contracts.Msg) {
	p.Events = append(p.Events, contracts.PlanEvent{ID: "evt-" + uuid.NewV7().String(), At: contracts.At(at), Text: text})
	if at.After(p.UpdatedAt.Time) {
		p.UpdatedAt = contracts.At(at)
	}
}
