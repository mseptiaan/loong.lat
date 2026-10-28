// Package projections applies the events of the other services to this service's tables: driver master data
// from back office, route assignments from dispatch, vehicles from the fleet, the plan cancels of console, the
// settings of back office. Execution is not among them: it is this service's own, so dispatch can hand a route
// over but never take it back once it runs.
package projections

import (
	"log/slog"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
)

// Projections holds the event handlers; each Register method wires one consumer group. pub carries the votes the
// plan handlers cast: they leave through the outbox in the handler's transaction.
type Projections struct {
	log *slog.Logger
	pub *eventbus.Publisher
}

func New(log *slog.Logger, pub *eventbus.Publisher) *Projections {
	return &Projections{log: log, pub: pub}
}

// Topics of the groups, for the consumers' configuration.
const (
	TopicDrivers  = contracts.TopicDriver
	TopicDispatch = contracts.TopicDispatch
	TopicFleet    = contracts.TopicFleet
	TopicPlan     = contracts.TopicPlan
	TopicSettings = contracts.TopicSettings
)

// RegisterDrivers handles loong.driver.v1. driver.online and driver.offline are this service's own events and
// have no handler: the consumer skips them.
func (p *Projections) RegisterDrivers(c *eventbus.Consumer) {
	c.Handle(contracts.TypeDriverProvisioned, p.driverProvisioned)
	c.Handle(contracts.TypeDriverUpdated, p.driverUpdated)
}

// RegisterDispatch handles loong.dispatch.v1.
func (p *Projections) RegisterDispatch(c *eventbus.Consumer) {
	c.Handle(contracts.TypeRouteAssigned, p.routeAssigned)
	c.Handle(contracts.TypeRouteUnassigned, p.routeUnassigned)
}

// RegisterFleet handles loong.fleet.v1.
func (p *Projections) RegisterFleet(c *eventbus.Consumer) {
	c.Handle(contracts.TypeVehicleUpserted, p.vehicleUpserted)
	c.Handle(contracts.TypeVehicleRemoved, p.vehicleRemoved)
}

// RegisterPlan handles loong.plan.v1, which this service only follows for the two-phase cancel: it votes on
// plan.cancel.requested and obeys plan.cancelled and plan.cancel.aborted. The votes it casts itself
// (plan.cancel.accepted/rejected) travel on the same topic and have no handler here.
func (p *Projections) RegisterPlan(c *eventbus.Consumer) {
	c.Handle(contracts.TypePlanCancelRequested, p.planCancelRequested)
	c.Handle(contracts.TypePlanCancelled, p.planCancelled)
	c.Handle(contracts.TypePlanCancelAborted, p.planCancelAborted)
}

// RegisterSettings handles loong.settings.v1, a compacted topic: the latest version of each group is what counts.
func (p *Projections) RegisterSettings(c *eventbus.Consumer) {
	c.Handle(contracts.TypeSettingsUpdated, p.settingsUpdated)
}
