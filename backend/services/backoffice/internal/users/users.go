// Package users is the admin console's view of the customers: who they are, what they have ordered and spent, how
// much is in their wallet, and whether they may use the service. The customers come from customer.registered /
// customer.updated (kept by the plans projection), their plans from the plan projection and their balances from the
// wallet projection; the one thing decided here is blocking, which console-api enforces on hearing
// customer.blocked / customer.unblocked.
package users

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/pg"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
)

// msgUserNotFound is the key of the frontend catalog for an unknown user.
const msgUserNotFound = "srv.api.userNotFound"

// User is the frontend's User. Saved addresses belong to console-api, so the list is always empty here.
type User struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Email          string         `json:"email"`
	Phone          string         `json:"phone"`
	CreatedAt      contracts.Time `json:"createdAt"`
	Blocked        bool           `json:"blocked"`
	SavedAddresses []struct{}     `json:"savedAddresses"`
}

// Summary is the frontend's UserSummary.
type Summary struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Email          string         `json:"email"`
	Phone          string         `json:"phone"`
	CreatedAt      contracts.Time `json:"createdAt"`
	Blocked        bool           `json:"blocked"`
	PlansCount     int            `json:"plansCount"`
	CompletedCount int            `json:"completedCount"`
	// TotalSpendRp is the cost of the plans that finished.
	TotalSpendRp int64 `json:"totalSpendRp"`
	// BalanceRp is the customer's wallet balance.
	BalanceRp  int64          `json:"balanceRp"`
	LastPlanAt contracts.Time `json:"lastPlanAt,omitzero"`
}

// Detail is the frontend's UserDetail.
type Detail struct {
	User    User            `json:"user"`
	Summary Summary         `json:"summary"`
	Plans   []plans.Summary `json:"plans"`
}

// Service answers users.list, users.get and users.setBlocked.
type Service struct {
	pool  *pgxpool.Pool
	pub   *eventbus.Publisher
	feed  *changes.Feed
	plans *plans.Service
}

func New(pool *pgxpool.Pool, pub *eventbus.Publisher, feed *changes.Feed, plans *plans.Service) *Service {
	return &Service{pool: pool, pub: pub, feed: feed, plans: plans}
}

// Mount registers the methods; all of them are for admins.
func (s *Service) Mount(rpc *platform.RPC) {
	type listIn struct {
		Q string `json:"q"`
	}
	type idIn struct {
		ID string `json:"id"`
	}
	type blockIn struct {
		ID      string `json:"id"`
		Blocked bool   `json:"blocked"`
	}
	rpc.Handle("users.list", apix.Admin(func(ctx context.Context, in listIn) ([]Summary, error) {
		return s.List(ctx, in.Q)
	}))
	rpc.Handle("users.get", apix.Admin(func(ctx context.Context, in idIn) (Detail, error) {
		return s.Get(ctx, in.ID)
	}))
	rpc.Handle("users.setBlocked", apix.Admin(func(ctx context.Context, in blockIn) (Summary, error) {
		return s.SetBlocked(ctx, in.ID, in.Blocked)
	}))
}

// summaryQuery reads customers with what the plan and wallet projections know about each. A plan counts once it is
// confirmed (only those are projected), whatever became of it; the spend is that of the finished ones.
const summaryQuery = `
	SELECT c.id, c.name, c.email, c.phone, c.created_at, c.blocked,
	       COALESCE(s.plans, 0), COALESCE(s.completed, 0), COALESCE(s.spend, 0), COALESCE(b.balance_rp, 0), s.last_plan
	  FROM customers c
	  LEFT JOIN (SELECT user_id,
	                    count(*)                                              AS plans,
	                    count(*) FILTER (WHERE status = 'selesai')            AS completed,
	                    COALESCE(sum(COALESCE((plan->>'totalCostRp')::bigint, 0)) FILTER (WHERE status = 'selesai'), 0)::bigint AS spend,
	                    max(created_at)                                       AS last_plan
	               FROM plans GROUP BY user_id) s ON s.user_id = c.id
	  LEFT JOIN wallet_balances b ON b.customer_id = c.id`

func scanSummary(row interface{ Scan(...any) error }) (Summary, error) {
	var sum Summary
	var createdAt time.Time
	var lastPlan *time.Time
	if err := row.Scan(&sum.ID, &sum.Name, &sum.Email, &sum.Phone, &createdAt, &sum.Blocked,
		&sum.PlansCount, &sum.CompletedCount, &sum.TotalSpendRp, &sum.BalanceRp, &lastPlan); err != nil {
		return sum, err
	}
	sum.CreatedAt = contracts.At(createdAt)
	if lastPlan != nil {
		sum.LastPlanAt = contracts.At(*lastPlan)
	}
	return sum, nil
}

// List returns the customers, those who spent most first; q keeps those whose name, email or phone contains it
// (ignoring case).
func (s *Service) List(ctx context.Context, q string) ([]Summary, error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	rows, err := s.pool.Query(ctx, summaryQuery+`
		WHERE $1 = '' OR strpos(lower(c.name), $1) > 0 OR strpos(lower(c.email), $1) > 0 OR strpos(lower(c.phone), $1) > 0
		ORDER BY COALESCE(s.spend, 0) DESC, c.created_at, c.id`, needle)
	if err != nil {
		return nil, fmt.Errorf("users: list: %w", err)
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		sum, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("users: scan: %w", err)
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

// summary reads one customer's summary.
func summary(ctx context.Context, q pg.Querier, id string) (Summary, error) {
	sum, err := scanSummary(q.QueryRow(ctx, summaryQuery+` WHERE c.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return sum, platform.NotFound(contracts.Msg{Key: msgUserNotFound})
	}
	if err != nil {
		return sum, fmt.Errorf("users: read %s: %w", id, err)
	}
	return sum, nil
}

// Get returns a customer with his summary and plans, the latest first.
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	sum, err := summary(ctx, s.pool, id)
	if err != nil {
		return Detail{}, err
	}
	rows, err := s.plans.List(ctx, plans.Filter{UserID: id})
	if err != nil {
		return Detail{}, err
	}
	// plans.List answers by last update; this view lists by creation.
	slices.SortStableFunc(rows, func(a, b plans.Summary) int { return b.CreatedAt.Compare(a.CreatedAt.Time) })
	return Detail{
		User: User{
			ID: sum.ID, Name: sum.Name, Email: sum.Email, Phone: sum.Phone, CreatedAt: sum.CreatedAt, Blocked: sum.Blocked,
			SavedAddresses: []struct{}{},
		},
		Summary: sum,
		Plans:   rows,
	}, nil
}

// SetBlocked blocks or unblocks a customer and announces it (customer.blocked / customer.unblocked), so that
// console-api stops serving him and ends his sessions. It answers with the summary as it is afterwards. Setting
// what already is announces nothing.
func (s *Service) SetBlocked(ctx context.Context, id string, blocked bool) (Summary, error) {
	var out Summary
	changed := false
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var current bool
		var version int64
		err := tx.QueryRow(ctx, `SELECT blocked, block_version FROM customers WHERE id = $1 FOR UPDATE`, id).Scan(&current, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.NotFound(contracts.Msg{Key: msgUserNotFound})
		}
		if err != nil {
			return fmt.Errorf("users: lock %s: %w", id, err)
		}
		if current != blocked {
			version++
			if _, err := tx.Exec(ctx, `UPDATE customers SET blocked = $2, block_version = $3 WHERE id = $1`, id, blocked, version); err != nil {
				return fmt.Errorf("users: block %s: %w", id, err)
			}
			event := eventbus.Event{Type: contracts.TypeCustomerUnblocked, Key: id, AggregateVersion: version, Data: contracts.CustomerUnblocked{CustomerID: id}}
			if blocked {
				event = eventbus.Event{Type: contracts.TypeCustomerBlocked, Key: id, AggregateVersion: version, Data: contracts.CustomerBlocked{CustomerID: id}}
			}
			if err := s.pub.Enqueue(ctx, tx, event); err != nil {
				return err
			}
			changed = true
		}
		out, err = summary(ctx, tx, id)
		return err
	})
	if err != nil {
		return Summary{}, err
	}
	if changed {
		s.feed.Users()
	}
	return out, nil
}
