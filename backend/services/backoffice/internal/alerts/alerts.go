// Package alerts is the admins' bell menu: what the event handlers and the dispatch worker tell the back office
// ("a plan needs a driver", "a customer cancelled", "a driver went offline"), and notifications.list /
// notifications.markRead. Notifications are written in the transaction of the change they report (pkg/notices)
// and announced to the open consoles as notifications.changed.
//
// Titles and bodies are Msg values with the keys and parameters of the mock's server.notify calls, so a screen
// renders them the same way in both modes.
package alerts

import (
	"context"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
)

// Keys of the frontend catalog (srv.api.notif.*, srv.ops.ntf.*).
const (
	msgNewUserTitle   = "srv.api.notif.newUser.title"
	msgNewUserBody    = "srv.api.notif.newUser.body"
	msgNewPlanTitle   = "srv.api.notif.newPlan.title"
	msgNewPlanBody    = "srv.api.notif.newPlan.body"
	msgCancelledTitle = "srv.api.notif.cancelled.title"
	msgCancelledBody  = "srv.api.notif.cancelled.body"
	msgReleasedTitle  = "srv.api.notif.released.title"
	msgReleasedBody   = "srv.api.notif.released.body"
	msgSilentBody     = "srv.api.notif.releasedSilent.body"
	msgDelayTitle     = "srv.api.notif.delayAdmin.title"
	msgDelayBody      = "srv.api.notif.delayAdmin.body"
	msgDriverFallback = "srv.api.driverFallback"
	msgProblemTitle   = "srv.ops.ntf.problemAdminTitle"
	msgProblemBody    = "srv.ops.ntf.problemAdminBody"
	msgOpsDriver      = "srv.ops.driver"
)

// Keys that the catalog does not have yet (to be added to it, en and id).
const (
	// MsgCancelRefusedTitle ("Cancellation of {code} refused") and its body ("{reason}"): console-api refused an
	// admin's cancel, so the plan goes on.
	MsgCancelRefusedTitle = "srv.api.notif.cancelRefused.title"
	MsgCancelRefusedBody  = "srv.api.notif.cancelRefused.body"
	// MsgRescheduledTitle ("{code} rescheduled") and its body ("{name} changed the schedule; {count} route(s) need
	// a driver again"): written only when automatic dispatch is off, which leaves the admin to assign.
	MsgRescheduledTitle = "srv.api.notif.rescheduled.title"
	MsgRescheduledBody  = "srv.api.notif.rescheduled.body"
)

// Notifier writes the admins' notifications.
type Notifier struct{ feed *changes.Feed }

func New(feed *changes.Feed) *Notifier { return &Notifier{feed: feed} }

// add writes the notification inside tx and announces it when it was new. key names the event that causes it
// (empty: not idempotent, for the worker's own decisions).
func (n *Notifier) add(ctx context.Context, tx platform.Querier, key string, at time.Time, planID string, title, body contracts.Msg) error {
	written, err := notices.Add(ctx, tx, notices.Notice{
		Audience: notices.AudienceAdmin, At: at, Title: title, Body: body, PlanID: planID, Key: key,
	})
	if written {
		n.feed.Notifications()
	}
	return err
}

func msg(key string, params map[string]any) contracts.Msg {
	return contracts.Msg{Key: key, Params: params}
}

// driverParam is a driver's name for a message, or the catalog's word for "driver" when he is not known.
func driverParam(name, fallbackKey string) any {
	if name == "" {
		return msg(fallbackKey, nil)
	}
	return name
}

// NewUser tells that a customer signed up.
func (n *Notifier) NewUser(ctx context.Context, tx platform.Querier, key string, at time.Time, name string) error {
	return n.add(ctx, tx, key, at, "", msg(msgNewUserTitle, nil), msg(msgNewUserBody, map[string]any{"name": name}))
}

// NewPlan tells that a confirmed plan is waiting for a driver ("a plan that needs action").
func (n *Notifier) NewPlan(ctx context.Context, tx platform.Querier, key string, at time.Time, planID, code, customer string, routes int) error {
	return n.add(ctx, tx, key, at, planID,
		msg(msgNewPlanTitle, map[string]any{"code": code}),
		msg(msgNewPlanBody, map[string]any{"name": customer, "count": routes}))
}

// Cancelled tells that the customer cancelled a confirmed plan.
func (n *Notifier) Cancelled(ctx context.Context, tx platform.Querier, key string, at time.Time, planID, code, customer string) error {
	return n.add(ctx, tx, key, at, planID,
		msg(msgCancelledTitle, map[string]any{"code": code}),
		msg(msgCancelledBody, map[string]any{"name": customer}))
}

// CancelRefused tells the admins that console-api would not cancel the plan they asked it to.
func (n *Notifier) CancelRefused(ctx context.Context, tx platform.Querier, key string, at time.Time, planID, code string, reason contracts.Msg) error {
	return n.add(ctx, tx, key, at, planID,
		msg(MsgCancelRefusedTitle, map[string]any{"code": code}),
		msg(MsgCancelRefusedBody, map[string]any{"reason": reason}))
}

// Rescheduled tells that a plan was moved to another time and its routes need a driver again, which only an
// admin can arrange while automatic dispatch is off.
func (n *Notifier) Rescheduled(ctx context.Context, tx platform.Querier, key string, at time.Time, planID, code, customer string, routes int) error {
	return n.add(ctx, tx, key, at, planID,
		msg(MsgRescheduledTitle, map[string]any{"code": code}),
		msg(MsgRescheduledBody, map[string]any{"name": customer, "count": routes}))
}

// Released tells that a driver's jobs went back to the queue (he went offline or fell silent).
//
// silent is true when the driver did not go offline but stopped reporting, which changes what the notice says.
func (n *Notifier) Released(ctx context.Context, tx platform.Querier, key string, at time.Time, driver string, count int, silent bool) error {
	body := msgReleasedBody
	if silent {
		body = msgSilentBody
	}
	return n.add(ctx, tx, key, at, "",
		msg(msgReleasedTitle, nil),
		msg(body, map[string]any{"name": driver, "count": count}))
}

// Delay tells that a route runs late. minutes is the delay so far.
func (n *Notifier) Delay(ctx context.Context, tx platform.Querier, key string, at time.Time, planID, code, driver string, minutes int) error {
	return n.add(ctx, tx, key, at, planID,
		msg(msgDelayTitle, map[string]any{"code": code}),
		msg(msgDelayBody, map[string]any{"name": driverParam(driver, msgDriverFallback), "count": minutes}))
}

// StopProblem tells that the driver reported a problem at a stop. issue is the message driver-api built.
func (n *Notifier) StopProblem(ctx context.Context, tx platform.Querier, key string, at time.Time, planID, code, driver string, issue contracts.Msg, place string) error {
	return n.add(ctx, tx, key, at, planID,
		msg(msgProblemTitle, map[string]any{"code": code}),
		msg(msgProblemBody, map[string]any{"driver": driverParam(driver, msgOpsDriver), "issue": issue, "place": place}))
}

// Mount serves notifications.list and notifications.markRead for admins. It is what notices.Mount does, plus the
// notifications.changed hint after markRead so that another open console drops its unread badge too.
func Mount(rpc *platform.RPC, db notices.DB, feed *changes.Feed) {
	rpc.Handle("notifications.list", apix.Admin(func(ctx context.Context, _ struct{}) ([]contracts.AppNotification, error) {
		return notices.List(ctx, db, notices.AudienceAdmin)
	}))
	type markIn struct {
		IDs []string `json:"ids"`
	}
	rpc.Handle("notifications.markRead", apix.Admin(func(ctx context.Context, in markIn) (struct{}, error) {
		if err := notices.MarkRead(ctx, db, notices.AudienceAdmin, in.IDs); err != nil {
			return struct{}{}, err
		}
		feed.Notifications()
		return struct{}{}, nil
	}))
}
