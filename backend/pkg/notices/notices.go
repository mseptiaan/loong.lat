// Package notices is the bell menu of one service: the notifications of its own audience (console-api: the
// customer, backoffice-api: the admins, driver-api: the driver), written by the event handlers in the same
// transaction as the change they report, and served as notifications.list and notifications.markRead.
package notices

import (
	"context"
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

//go:embed notifications.sql
var tableSQL string

// Migrations is the notifications table; add it to the service's migrations.
func Migrations() []platform.Migration {
	return []platform.Migration{{ID: "notices/0001_notifications", SQL: tableSQL}}
}

// ListLimit is how many of the latest notifications of an audience are served.
const ListLimit = 50

// Audience names, matching the frontend's NotificationAudience.
const AudienceAdmin = "admin"

func Customer(id string) string { return "customer:" + id }
func Driver(id string) string   { return "driver:" + id }

// Notice is a notification to write.
type Notice struct {
	Audience string
	// At is when it happened; zero means now.
	At     time.Time
	Title  contracts.Msg
	Body   contracts.Msg
	PlanID string
	// Key makes writing idempotent: the same Key for the same audience is stored once. Use the id of the event
	// that causes the notification, plus a suffix when one event notifies several things.
	Key string
}

// Add writes n inside tx. A repeated Key is ignored, so a redelivered event does not notify twice. It reports
// whether a row was written.
func Add(ctx context.Context, tx platform.Querier, n Notice) (bool, error) {
	if n.Audience == "" || n.Title.Key == "" || n.Body.Key == "" {
		return false, fmt.Errorf("notices: audience, title and body are required")
	}
	at := n.At
	if at.IsZero() {
		at = time.Now()
	}
	title, err := json.Marshal(n.Title)
	if err != nil {
		return false, err
	}
	body, err := json.Marshal(n.Body)
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO notifications (id, audience, at, title, body, plan_id, dedupe_key)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''))
		ON CONFLICT (audience, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`,
		"ntf-"+uuid.NewV7().String(), n.Audience, at.UTC(), title, body, n.PlanID, n.Key)
	if err != nil {
		return false, fmt.Errorf("notices: add: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DB is what reading needs: *pgxpool.Pool and pgx.Tx both are one.
type DB interface {
	platform.Querier
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// List returns the latest notifications of an audience, newest first.
func List(ctx context.Context, q DB, audience string) ([]contracts.AppNotification, error) {
	rows, err := q.Query(ctx, `
		SELECT id, at, title, body, COALESCE(plan_id, ''), read_at IS NOT NULL
		FROM notifications WHERE audience = $1 ORDER BY at DESC, id DESC LIMIT $2`, audience, ListLimit)
	if err != nil {
		return nil, fmt.Errorf("notices: list: %w", err)
	}
	defer rows.Close()
	out := []contracts.AppNotification{}
	for rows.Next() {
		n := contracts.AppNotification{Audience: audience}
		var at time.Time
		var title, body []byte
		if err := rows.Scan(&n.ID, &at, &title, &body, &n.PlanID, &n.Read); err != nil {
			return nil, fmt.Errorf("notices: scan: %w", err)
		}
		n.At = contracts.At(at)
		if err := json.Unmarshal(title, &n.Title); err != nil {
			return nil, fmt.Errorf("notices: title of %s: %w", n.ID, err)
		}
		if err := json.Unmarshal(body, &n.Body); err != nil {
			return nil, fmt.Errorf("notices: body of %s: %w", n.ID, err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkRead marks the given notifications of an audience as read, or all of them when ids is empty.
func MarkRead(ctx context.Context, q platform.Querier, audience string, ids []string) error {
	var err error
	if len(ids) == 0 {
		_, err = q.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE audience = $1 AND read_at IS NULL`, audience)
	} else {
		_, err = q.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE audience = $1 AND id = ANY($2) AND read_at IS NULL`, audience, ids)
	}
	if err != nil {
		return fmt.Errorf("notices: mark read: %w", err)
	}
	return nil
}

// Mount serves notifications.list and notifications.markRead. audienceOf maps the signed-in principal to the audience
// it may read (ok=false: this role has no notifications here). Both methods need a signed-in user.
func Mount(rpc *platform.RPC, db DB, audienceOf func(platform.Principal) (audience string, ok bool)) {
	audience := func(ctx context.Context) (string, error) {
		p, err := platform.RequireRole(ctx, contracts.RoleCustomer, contracts.RoleAdmin, contracts.RoleDriver)
		if err != nil {
			return "", err
		}
		a, ok := audienceOf(p)
		if !ok {
			return "", platform.Denied(contracts.Msg{Key: platform.MsgRoleDenied})
		}
		return a, nil
	}
	rpc.Handle("notifications.list", func(ctx context.Context, _ struct{}) ([]contracts.AppNotification, error) {
		a, err := audience(ctx)
		if err != nil {
			return nil, err
		}
		return List(ctx, db, a)
	})
	type markIn struct {
		IDs []string `json:"ids"`
	}
	rpc.Handle("notifications.markRead", func(ctx context.Context, in markIn) (struct{}, error) {
		a, err := audience(ctx)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, MarkRead(ctx, db, a, in.IDs)
	})
}
