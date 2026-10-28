// Package notify tells a customer's open dashboards that something changed. A change is announced inside the
// transaction that makes it, with Postgres NOTIFY, which delivers only once that transaction commits; Relay
// then forwards it to the SSE hub. That way a client that refetches on the event never reads the old state,
// whether the change came from an HTTP request or from a Kafka consumer, and every replica's hub hears it.
package notify

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// channel is the Postgres NOTIFY channel; it is per database, and the console owns its database.
const channel = "console_events"

// Event types, named like the frontend's AppEvent.
const (
	typePlansChanged         = "plans.changed"
	typeWalletChanged        = "wallet.changed"
	typeNotificationsChanged = "notifications.changed"
	typeSettingsChanged      = "settings.changed"
)

// message is one announcement. An empty CustomerID addresses everybody (settings.changed); every other type
// names the customer it is for.
type message struct {
	Type       string `json:"type"`
	CustomerID string `json:"customerId,omitempty"`
	PlanID     string `json:"planId,omitempty"`
}

func send(ctx context.Context, tx platform.Querier, m message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, string(payload)); err != nil {
		return fmt.Errorf("notify %s: %w", m.Type, err)
	}
	return nil
}

// PlanChanged announces `plans.changed{planId}` to the customer once tx commits. Postgres drops repeats of
// the same announcement within one transaction.
func PlanChanged(ctx context.Context, tx platform.Querier, customerID, planID string) error {
	return send(ctx, tx, message{Type: typePlansChanged, CustomerID: customerID, PlanID: planID})
}

// WalletChanged announces `wallet.changed` to the customer once tx commits.
func WalletChanged(ctx context.Context, tx platform.Querier, customerID string) error {
	return send(ctx, tx, message{Type: typeWalletChanged, CustomerID: customerID})
}

// NotificationsChanged announces `notifications.changed` to the customer once tx commits.
func NotificationsChanged(ctx context.Context, tx platform.Querier, customerID string) error {
	return send(ctx, tx, message{Type: typeNotificationsChanged, CustomerID: customerID})
}

// SettingsChanged announces `settings.changed` to every open dashboard once tx commits.
func SettingsChanged(ctx context.Context, tx platform.Querier) error {
	return send(ctx, tx, message{Type: typeSettingsChanged})
}

// Notice writes a notification into the customer's bell menu inside tx and, when it was new, announces it. The
// notice's Key makes it idempotent (the id of the event that causes it, plus a suffix when one event causes
// several): a redelivered event adds nothing and announces nothing.
func Notice(ctx context.Context, tx platform.Querier, customerID string, n notices.Notice) error {
	n.Audience = notices.Customer(customerID)
	added, err := notices.Add(ctx, tx, n)
	if err != nil || !added {
		return err
	}
	return NotificationsChanged(ctx, tx, customerID)
}

// ReadAnnouncer is the database notices.Mount reads and marks notifications read with, and announces
// `notifications.changed` to the customer whose notifications were marked read, so their other open tabs refresh.
// notices.MarkRead is the one statement that starts with UPDATE notifications and names the audience first; the
// announcement is best effort, as clients also refetch when they reconnect.
type ReadAnnouncer struct{ *pgxpool.Pool }

func (d ReadAnnouncer) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := d.Pool.Exec(ctx, sql, args...)
	if err != nil || tag.RowsAffected() == 0 || !strings.HasPrefix(sql, "UPDATE notifications") || len(args) == 0 {
		return tag, err
	}
	if audience, ok := args[0].(string); ok {
		if customerID, ok := strings.CutPrefix(audience, "customer:"); ok {
			_ = NotificationsChanged(ctx, d.Pool, customerID)
		}
	}
	return tag, nil
}

// reconnectDelay is how long Relay waits before it listens again after losing its connection.
const reconnectDelay = 2 * time.Second

// Relay publishes every announcement on the database to hub, addressed to the customer it names (or to all, for
// settings), until ctx is done. It holds one dedicated connection (not one from the pool) and reconnects when it is lost; an event
// announced meanwhile is missed, which clients tolerate because they also refetch when they reconnect.
func Relay(ctx context.Context, dsn string, hub *platform.Hub, log *slog.Logger) {
	for ctx.Err() == nil {
		if err := listen(ctx, dsn, hub, log); ctx.Err() == nil {
			log.ErrorContext(ctx, "event relay lost its connection", "error", err, "retryIn", reconnectDelay)
			select {
			case <-ctx.Done():
			case <-time.After(reconnectDelay):
			}
		}
	}
}

func listen(ctx context.Context, dsn string, hub *platform.Hub, log *slog.Logger) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, `LISTEN `+channel); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var m message
		err = json.Unmarshal([]byte(n.Payload), &m)
		broadcast := m.Type == typeSettingsChanged
		if err != nil || m.Type == "" || (m.CustomerID == "" && !broadcast) {
			log.WarnContext(ctx, "ignoring malformed event announcement", "payload", n.Payload, "error", err)
			continue
		}
		if broadcast {
			hub.Publish(platform.Event{Type: m.Type})
			continue
		}
		hub.Publish(platform.Event{Type: m.Type, PlanID: m.PlanID}, platform.CustomerAudience(m.CustomerID))
	}
}
