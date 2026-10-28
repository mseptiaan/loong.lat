// Package live carries "something changed" hints to the app's SSE streams.
//
// A hint is sent with Postgres NOTIFY from inside the transaction that made the change, so it is delivered only
// if that transaction commits and never before the data is visible: a client that refetches on a hint always
// sees the change. It also lets any replica's stream receive hints raised by another replica's consumer.
package live

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// channel is the Postgres NOTIFY channel. It is qualified by the current schema (always "public" in production,
// where the service owns its database), which keeps separate schemas of one test database from hearing each other.
const channel = "driver_events"

// channelOf is the channel name as the connection behind q sees it.
func channelOf(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (string, error) {
	var schema string
	if err := q.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return "", fmt.Errorf("live: schema: %w", err)
	}
	return schema + "." + channel, nil
}

// message is the notification payload.
type message struct {
	To     platform.Audience `json:"to"`
	Type   string            `json:"type"`
	PlanID string            `json:"planId,omitempty"`
}

// PlansChanged tells the app a task of the plan appeared or changed; the app refetches its routes.
func PlansChanged(planID string) platform.Event {
	return platform.Event{Type: "plans.changed", PlanID: planID}
}

// FleetChanged tells the app its profile (driver or vehicle) changed.
var FleetChanged = platform.Event{Type: "fleet.changed"}

// NotificationsChanged tells the app its bell menu has a new entry; the app refetches notifications.list.
var NotificationsChanged = platform.Event{Type: "notifications.changed"}

// ToDriver queues ev for the driver's streams as part of q's transaction. An empty id, such as the previous
// driver of a route nobody had, is nobody.
func ToDriver(ctx context.Context, q platform.Querier, driverID string, ev platform.Event) error {
	if driverID == "" {
		return nil
	}
	payload, err := json.Marshal(message{To: platform.DriverAudience(driverID), Type: ev.Type, PlanID: ev.PlanID})
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `SELECT pg_notify(current_schema() || '.' || $1, $2)`, channel, string(payload)); err != nil {
		return fmt.Errorf("live: notify: %w", err)
	}
	return nil
}

const (
	retryBase = 500 * time.Millisecond
	retryMax  = 10 * time.Second
)

// Listen subscribes to the hints and returns the loop that forwards them to hub; run it in a goroutine until
// ctx ends. The subscription exists when Listen returns, so nothing sent afterwards is missed. A lost
// connection is re-established by the loop; hints raised meanwhile are lost, which only delays a refetch
// until the next change. The loop is also what closes the connection, so run it even when the service is
// being torn down again (with a finished context).
func Listen(ctx context.Context, pool *pgxpool.Pool, hub *platform.Hub, log *slog.Logger) (run func(context.Context) error, err error) {
	conn, err := subscribe(ctx, pool)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		for {
			err := forward(ctx, conn, hub, log)
			if ctx.Err() != nil {
				return nil
			}
			for failures := 1; ; failures++ {
				wait := min(retryBase<<min(failures-1, 5), retryMax)
				log.WarnContext(ctx, "live notification listener lost, reconnecting", "error", err, "retryIn", wait)
				if !pause(ctx, wait) {
					return nil
				}
				if conn, err = subscribe(ctx, pool); err == nil {
					break
				}
			}
		}
	}, nil
}

// pause waits for d and reports false if ctx ended first.
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// subscribe takes a connection out of the pool for good: a listening connection must not be handed to a query.
func subscribe(ctx context.Context, pool *pgxpool.Pool) (*pgx.Conn, error) {
	pooled, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("live: acquire connection: %w", err)
	}
	conn := pooled.Hijack()
	name, err := channelOf(ctx, conn)
	if err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	if _, err := conn.Exec(ctx, `LISTEN `+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("live: listen: %w", err)
	}
	return conn, nil
}

// forward passes notifications to hub until the connection fails or ctx ends, then closes the connection.
func forward(ctx context.Context, conn *pgx.Conn, hub *platform.Hub, log *slog.Logger) error {
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var m message
		if err := json.Unmarshal([]byte(n.Payload), &m); err != nil {
			log.WarnContext(ctx, "live: unreadable notification dropped", "error", err)
			continue
		}
		hub.Publish(platform.Event{Type: m.Type, PlanID: m.PlanID}, m.To)
	}
}
