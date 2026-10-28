package contracts

// Event types. The name is `<aggregate>.<fact>` in the past tense (or `*.requested` for a command-like
// request), matching the plan's topic table. Each type has one payload struct in payloads.go.
const (
	// TopicPlan, keyed by plan id. Published by console-api, except plan.cancel.asked (backoffice-api, an admin asks
	// console-api to cancel) and the votes plan.cancel.accepted/rejected (driver-api). Cancelling is a two-phase
	// commit coordinated by console-api: requested (hold every route) -> one vote per route from driver-api ->
	// cancelled (all accepted: commit) or aborted (any rejected, or no answer in time: release the holds).
	TypePlanConfirmed       = "plan.confirmed"
	TypePlanCancelAsked     = "plan.cancel.asked"
	TypePlanCancelRequested = "plan.cancel.requested"
	TypePlanCancelAccepted  = "plan.cancel.accepted"
	TypePlanCancelRejected  = "plan.cancel.rejected"
	TypePlanCancelAborted   = "plan.cancel.aborted"
	TypePlanCancelled       = "plan.cancelled"
	TypePlanRescheduled     = "plan.rescheduled"

	// TopicDispatch, keyed by route id. Published by backoffice-api.
	TypeRouteAssigned   = "route.assigned"
	TypeRouteUnassigned = "route.unassigned"

	// TopicExecution, keyed by route id. Published by driver-api.
	TypeRouteStarted   = "route.started"
	TypeStopArrived    = "stop.arrived"
	TypeStopCompleted  = "stop.completed"
	TypeStopIssue      = "stop.issue"
	TypeRouteCompleted = "route.completed"
	TypeRouteDelayed   = "route.delayed"

	// TopicDriver, keyed by driver id. provisioned/updated come from backoffice-api, online/offline from driver-api.
	TypeDriverProvisioned = "driver.provisioned"
	TypeDriverUpdated     = "driver.updated"
	TypeDriverOnline      = "driver.online"
	TypeDriverOffline     = "driver.offline"

	// TopicDriverPosition, keyed by driver id. Published by driver-api.
	TypeDriverPosition = "driver.position"

	// TopicFleet (compacted), keyed by vehicle id. Published by backoffice-api.
	TypeVehicleUpserted = "vehicle.upserted"
	TypeVehicleRemoved  = "vehicle.removed"

	// TopicSettings (compacted), keyed by settings key. Published by backoffice-api.
	TypeSettingsUpdated = "settings.updated"

	// TopicCustomer, keyed by customer id. registered/updated come from console-api, blocked/unblocked from backoffice-api.
	TypeCustomerRegistered = "customer.registered"
	TypeCustomerUpdated    = "customer.updated"
	TypeCustomerBlocked    = "customer.blocked"
	TypeCustomerUnblocked  = "customer.unblocked"

	// TopicWallet, keyed by customer id. Published by console-api.
	TypeTopUpCreated   = "topup.created"
	TypeTopUpPaid      = "topup.paid"
	TypeTopUpExpired   = "topup.expired"
	TypeTopUpCancelled = "topup.cancelled"
	TypeWalletCharged  = "wallet.charged"
	TypeWalletRefunded = "wallet.refunded"
)

var eventTopics = map[string]string{
	TypePlanConfirmed:       TopicPlan,
	TypePlanCancelAsked:     TopicPlan,
	TypePlanCancelRequested: TopicPlan,
	TypePlanCancelAccepted:  TopicPlan,
	TypePlanCancelRejected:  TopicPlan,
	TypePlanCancelAborted:   TopicPlan,
	TypePlanCancelled:       TopicPlan,
	TypePlanRescheduled:     TopicPlan,
	TypeRouteAssigned:       TopicDispatch,
	TypeRouteUnassigned:     TopicDispatch,
	TypeRouteStarted:        TopicExecution,
	TypeStopArrived:         TopicExecution,
	TypeStopCompleted:       TopicExecution,
	TypeStopIssue:           TopicExecution,
	TypeRouteCompleted:      TopicExecution,
	TypeRouteDelayed:        TopicExecution,
	TypeDriverProvisioned:   TopicDriver,
	TypeDriverUpdated:       TopicDriver,
	TypeDriverOnline:        TopicDriver,
	TypeDriverOffline:       TopicDriver,
	TypeDriverPosition:      TopicDriverPosition,
	TypeVehicleUpserted:     TopicFleet,
	TypeVehicleRemoved:      TopicFleet,
	TypeSettingsUpdated:     TopicSettings,
	TypeCustomerRegistered:  TopicCustomer,
	TypeCustomerUpdated:     TopicCustomer,
	TypeCustomerBlocked:     TopicCustomer,
	TypeCustomerUnblocked:   TopicCustomer,
	TypeTopUpCreated:        TopicWallet,
	TypeTopUpPaid:           TopicWallet,
	TypeTopUpExpired:        TopicWallet,
	TypeTopUpCancelled:      TopicWallet,
	TypeWalletCharged:       TopicWallet,
	TypeWalletRefunded:      TopicWallet,
}

// TopicFor returns the topic that carries an event type, so a producer cannot publish a fact to the wrong topic.
func TopicFor(eventType string) (topic string, ok bool) {
	topic, ok = eventTopics[eventType]
	return topic, ok
}
