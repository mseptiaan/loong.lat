// Package notify writes the driver's bell menu: a new job, a job that was taken away or cancelled. Each entry is
// written in the transaction of the event that causes it (idempotent through the entry's key) and announced to
// the driver's open app as notifications.changed, which is delivered only once that transaction commits.
//
// The texts are catalog keys, with the parameters the mock backend gives them. The keys that the mock has no
// driver-facing equivalent for (the reasons a job is taken away) are new and listed in removed.
package notify

import (
	"context"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/live"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// Keys already in the frontend's catalogs.
const (
	keyNewJobTitle         = "srv.ops.ntf.newJobTitle"
	keyNewJobBody          = "srv.ops.ntf.newJobBody"
	keyCancelledTitle      = "srv.api.notif.taskCancelled.title"
	keyCancelledBody       = "srv.api.notif.taskCancelled.body"
	keyCancelledBodyReason = "srv.api.notif.taskCancelled.bodyReason"
)

// Keys the catalogs do not have yet: a job taken off the driver's list by an admin, by dispatch because the
// driver's app went quiet, or because the sender moved the plan to another time. All take {code}.
const (
	keyRemovedTitle     = "srv.api.notif.taskRemoved.title"
	keyRemovedBody      = "srv.api.notif.taskRemoved.body"
	keySilentTitle      = "srv.api.notif.taskSilent.title"
	keySilentBody       = "srv.api.notif.taskSilent.body"
	keyRescheduledTitle = "srv.api.notif.taskRescheduled.title"
	keyRescheduledBody  = "srv.api.notif.taskRescheduled.body"
)

// CancelledKey is the dedupe key of the cancellation notice of a route: whichever way the driver learns of the
// cancel (route.unassigned with reason cancelled, or plan.cancelled), the route is announced once.
func CancelledKey(routeID string) string { return "cancelled:" + routeID }

// write adds the entry and, if it is new, tells the driver's app.
func write(ctx context.Context, tx platform.Querier, driverID string, n notices.Notice) error {
	if driverID == "" {
		return nil
	}
	n.Audience = notices.Driver(driverID)
	written, err := notices.Add(ctx, tx, n)
	if err != nil || !written {
		return err
	}
	return live.ToDriver(ctx, tx, driverID, live.NotificationsChanged)
}

// NewJob tells the task's driver that a route was assigned to them.
func NewJob(ctx context.Context, tx platform.Querier, t tasks.Task, key string, at time.Time) error {
	return write(ctx, tx, t.DriverID, notices.Notice{
		At: at, Key: key, PlanID: t.PlanID,
		Title: contracts.Msg{Key: keyNewJobTitle},
		Body: contracts.Msg{Key: keyNewJobBody, Params: map[string]any{
			"code": t.PlanCode, "count": len(t.Route.Stops), "start": t.Route.StartAt,
		}},
	})
}

// Job is a route and the driver it was with: what an entry is about.
type Job struct {
	RouteID  string
	PlanID   string
	PlanCode string
	DriverID string
}

// JobOf is the job of a task.
func JobOf(t tasks.Task) Job {
	return Job{RouteID: t.Route.ID, PlanID: t.PlanID, PlanCode: t.PlanCode, DriverID: t.DriverID}
}

// Cancelled tells the driver that the plan of the route was cancelled; reason is the sender's, when they gave one.
func Cancelled(ctx context.Context, tx platform.Querier, j Job, reason string, at time.Time) error {
	title, body := cancelledMsgs(j.PlanCode, reason)
	return write(ctx, tx, j.DriverID, notices.Notice{At: at, Key: CancelledKey(j.RouteID), PlanID: j.PlanID, Title: title, Body: body})
}

func cancelledMsgs(code, reason string) (title, body contracts.Msg) {
	title = contracts.Msg{Key: keyCancelledTitle}
	if reason != "" {
		return title, contracts.Msg{Key: keyCancelledBodyReason, Params: map[string]any{"code": code, "reason": reason}}
	}
	return title, contracts.Msg{Key: keyCancelledBody, Params: map[string]any{"code": code}}
}

// Unassigned tells the driver why the route was taken from them, per RouteUnassigned.Reason; eventID is the id
// of the event that took it. The driver who went offline themselves hears nothing: it is their own doing. An
// empty or unknown reason reads as an admin's.
func Unassigned(ctx context.Context, tx platform.Querier, j Job, reason, eventID string, at time.Time) error {
	if reason == contracts.UnassignCancelled {
		return Cancelled(ctx, tx, j, "", at)
	}
	title, body, ok := unassignedMsgs(j.PlanCode, reason)
	if !ok {
		return nil
	}
	return write(ctx, tx, j.DriverID, notices.Notice{At: at, Key: eventID, PlanID: j.PlanID, Title: title, Body: body})
}

// unassignedMsgs is the entry for a reason; ok is false when the reason deserves none.
func unassignedMsgs(code, reason string) (title, body contracts.Msg, ok bool) {
	params := map[string]any{"code": code}
	switch reason {
	case contracts.UnassignDriverOffline:
		return contracts.Msg{}, contracts.Msg{}, false
	case contracts.UnassignDriverSilent:
		return contracts.Msg{Key: keySilentTitle}, contracts.Msg{Key: keySilentBody, Params: params}, true
	case contracts.UnassignRescheduled:
		return contracts.Msg{Key: keyRescheduledTitle}, contracts.Msg{Key: keyRescheduledBody, Params: params}, true
	}
	return contracts.Msg{Key: keyRemovedTitle}, contracts.Msg{Key: keyRemovedBody, Params: params}, true
}
