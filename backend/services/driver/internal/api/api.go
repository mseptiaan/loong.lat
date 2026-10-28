// Package api exposes the driver app's methods as RPC: the names and parameters of the frontend's ApiClient
// (auth.*, driver.*, settings.get and the driver's own notifications.*), plus driver.ping, which the frontend
// does not have yet. Methods of the other services are not registered, so a call to them answers 404.
package api

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/auth"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/earnings"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/execution"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/settings"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// Services are the use cases the methods call.
type Services struct {
	Auth      *auth.Service
	Drivers   *drivers.Service
	Execution *execution.Service
	Earnings  *earnings.Service
	Settings  *settings.Service
	// Notices is where the driver's bell menu is read from (the service's pool).
	Notices notices.DB
}

// Request bodies: an object keyed by the TypeScript parameter names.
type (
	loginIn struct {
		Phone string `json:"phone"`
		PIN   string `json:"pin"`
	}
	onlineIn struct {
		Online bool `json:"online"`
	}
	routeIn struct {
		PlanID  string `json:"planId"`
		RouteID string `json:"routeId"`
	}
	stopIn struct {
		routeIn
		StopID string `json:"stopId"`
	}
	completeIn struct {
		stopIn
		Proof *execution.StopProof `json:"proof"`
	}
	issueIn struct {
		stopIn
		Issue contracts.IssueKind `json:"issue"`
		Note  string              `json:"note"`
	}
)

// Mount registers the methods on rpc.
func Mount(rpc *platform.RPC, s Services) {
	rpc.Handle("auth.loginDriver", func(ctx context.Context, in loginIn) (auth.Login, error) {
		return s.Auth.Login(ctx, in.Phone, in.PIN)
	})
	rpc.Handle("auth.session", func(ctx context.Context, _ struct{}) (*contracts.Session, error) {
		return auth.Current(ctx), nil
	})
	rpc.Handle("auth.logout", func(ctx context.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, s.Auth.Logout(ctx)
	})

	rpc.Handle("driver.me", func(ctx context.Context, _ struct{}) (drivers.Profile, error) {
		id, err := driverID(ctx)
		if err != nil {
			return drivers.Profile{}, err
		}
		return s.Drivers.Me(ctx, id)
	})
	rpc.Handle("driver.setOnline", func(ctx context.Context, in onlineIn) (drivers.Driver, error) {
		id, err := driverID(ctx)
		if err != nil {
			return drivers.Driver{}, err
		}
		return s.Drivers.SetOnline(ctx, id, in.Online)
	})
	rpc.Handle("driver.ping", func(ctx context.Context, in drivers.Fix) (struct{}, error) {
		id, err := driverID(ctx)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, s.Drivers.Ping(ctx, id, in)
	})

	rpc.Handle("driver.routes", func(ctx context.Context, _ struct{}) ([]tasks.DriverRoute, error) {
		id, err := driverID(ctx)
		if err != nil {
			return nil, err
		}
		return s.Execution.Routes(ctx, id)
	})
	rpc.Handle("driver.startRoute", func(ctx context.Context, in routeIn) (contracts.Plan, error) {
		return plan(ctx, func(id string) (tasks.Task, error) { return s.Execution.Start(ctx, id, in.PlanID, in.RouteID) })
	})
	rpc.Handle("driver.arrive", func(ctx context.Context, in stopIn) (contracts.Plan, error) {
		return plan(ctx, func(id string) (tasks.Task, error) {
			return s.Execution.Arrive(ctx, id, in.PlanID, in.RouteID, in.StopID)
		})
	})
	rpc.Handle("driver.complete", func(ctx context.Context, in completeIn) (contracts.Plan, error) {
		return plan(ctx, func(id string) (tasks.Task, error) {
			return s.Execution.Complete(ctx, id, in.PlanID, in.RouteID, in.StopID, in.Proof)
		})
	})
	rpc.Handle("driver.reportIssue", func(ctx context.Context, in issueIn) (contracts.Plan, error) {
		return plan(ctx, func(id string) (tasks.Task, error) {
			return s.Execution.ReportIssue(ctx, id, in.PlanID, in.RouteID, in.StopID, in.Issue, in.Note)
		})
	})

	rpc.Handle("driver.history", func(ctx context.Context, _ struct{}) ([]earnings.HistoryItem, error) {
		id, err := driverID(ctx)
		if err != nil {
			return nil, err
		}
		return s.Earnings.History(ctx, id)
	})
	rpc.Handle("driver.earnings", func(ctx context.Context, _ struct{}) (earnings.Summary, error) {
		id, err := driverID(ctx)
		if err != nil {
			return earnings.Summary{}, err
		}
		return s.Earnings.Earnings(ctx, id)
	})

	s.Settings.Mount(rpc)
	notices.Mount(rpc, s.Notices, func(p platform.Principal) (string, bool) {
		return notices.Driver(p.UserID), p.Role == contracts.RoleDriver
	})
}

// driverID is the id of the signed-in driver; anyone else is refused.
func driverID(ctx context.Context) (string, error) {
	p, err := platform.RequireRole(ctx, contracts.RoleDriver)
	return p.UserID, err
}

// plan runs an action of the signed-in driver and answers with the plan view of the route it acted on.
func plan(ctx context.Context, act func(driverID string) (tasks.Task, error)) (contracts.Plan, error) {
	id, err := driverID(ctx)
	if err != nil {
		return contracts.Plan{}, err
	}
	t, err := act(id)
	if err != nil {
		return contracts.Plan{}, err
	}
	return t.Plan(), nil
}
