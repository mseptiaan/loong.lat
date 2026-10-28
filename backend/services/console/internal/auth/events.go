package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// RegisterEvents handles back office's decisions about customers (loong.customer.v1). The topic also carries
// the console's own customer.registered and customer.updated, which have no handler here.
func (s *Service) RegisterEvents(c *eventbus.Consumer) {
	c.Handle(contracts.TypeCustomerBlocked, onBlocked)
	c.Handle(contracts.TypeCustomerUnblocked, onUnblocked)
}

func onBlocked(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.CustomerBlocked]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return setBlocked(ctx, tx, env, ev.CustomerID, true)
}

func onUnblocked(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.CustomerUnblocked]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return setBlocked(ctx, tx, env, ev.CustomerID, false)
}

// setBlocked applies back office's decision. The last decision wins by the event's aggregate version, and by
// the time it happened when versions tie, so a late older event cannot undo a newer one. Blocking also signs the
// customer out of every session; unblocking lets them sign in again.
func setBlocked(ctx context.Context, tx pgx.Tx, env contracts.Envelope, customerID string, blocked bool) error {
	if customerID == "" {
		return eventbus.Permanent(fmt.Errorf("%s without a customer id", env.Type))
	}
	tag, err := tx.Exec(ctx,
		`UPDATE customers SET blocked = $2, block_version = $3, block_at = $4
		  WHERE id = $1 AND (block_version < $3 OR (block_version = $3 AND (block_at IS NULL OR block_at <= $4)))`,
		customerID, blocked, env.AggregateVersion, env.OccurredAt.Time)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var known bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customers WHERE id = $1)`, customerID).Scan(&known); err != nil {
			return err
		}
		if !known {
			return eventbus.Permanent(fmt.Errorf("%s: customer %s is not known", env.Type, customerID))
		}
		return nil // an older decision than the one applied
	}
	if blocked {
		return platform.NewPgSessionStore(tx).DeleteUser(ctx, customerID)
	}
	return nil
}
