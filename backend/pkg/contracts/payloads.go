package contracts

// Payloads: the `data` of an Envelope, one struct per event type. Rules for every payload:
//   - Additive evolution only. New fields are optional; existing names and meanings never change. Consumers
//     ignore unknown fields.
//   - State-carrying events (confirmed, assigned, upserted, ...) carry what consumers need so they never
//     call the producer back (event-carried state transfer).
//   - PII is limited to what the consumer must show: stop contacts for the driver, customer contact for the
//     back office. Passwords and PINs never travel; the one credential, the driver PIN, is an argon2id hash.
//   - Times that are the producer's own domain facts (arrivedAt, completedAt, ...) are in the payload; when an
//     event is simply "now", the envelope's occurredAt is the time.

/* ---------- loong.plan.v1 (key: plan id) ---------- */

// Payment is how a confirmed plan was paid.
type Payment struct {
	Method   PaymentMethod `json:"method"`
	AmountRp int64         `json:"amountRp"`
	PaidAt   Time          `json:"paidAt"`
}

// PlanConfirmed is the full commercial snapshot of a plan at confirmation: status menunggu_driver, the chosen
// routes with all stops, and the payment. Plan.OptionSet is left empty (alternatives are not shared). Back
// office builds its queue and dispatch from it, driver-api its task list; neither calls console-api.
type PlanConfirmed struct {
	Plan    Plan    `json:"plan"`
	Payment Payment `json:"payment"`
}

// PlanCancelAsked is an admin asking console-api, the owner of the plan's commercial state, to cancel it. Console
// validates it like a customer's own request and answers with PlanCancelRequested, or PlanCancelRejected with an
// empty RouteID when the plan can no longer be cancelled.
type PlanCancelAsked struct {
	PlanID string `json:"planId"`
	Reason string `json:"reason,omitempty"`
	// AskedBy is the admin's name, for the plan's timeline and the notifications.
	AskedBy string `json:"askedBy"`
	// Locale ("en" or "id") is the admin's language: console composes the default reason text in it.
	Locale string `json:"locale,omitempty"`
}

// Who started a cancel (PlanCancelRequested.RequestedBy).
const (
	CancelByCustomer = "customer"
	CancelByAdmin    = "admin"
)

// PlanCancelRequested starts the two-phase cancel: every service holds the plan's routes (back office stops
// assigning them, driver-api stops them from being started) and driver-api votes once per route with
// PlanCancelAccepted or PlanCancelRejected. RequestID tells votes of one attempt from another.
type PlanCancelRequested struct {
	PlanID      string   `json:"planId"`
	RequestID   string   `json:"requestId"`
	RequestedBy string   `json:"requestedBy"`
	RouteIDs    []string `json:"routeIds"`
	Reason      string   `json:"reason,omitempty"`
}

// PlanCancelAccepted is driver-api's vote for a route that can be cancelled: it has not started (or driver-api
// never heard of it yet, which it records so that a late assignment is ignored).
type PlanCancelAccepted struct {
	PlanID    string `json:"planId"`
	RequestID string `json:"requestId,omitempty"`
	RouteID   string `json:"routeId"`
}

// PlanCancelRejected is the vote for a route that cannot be cancelled, e.g. it has already started.
type PlanCancelRejected struct {
	PlanID    string `json:"planId"`
	RequestID string `json:"requestId,omitempty"`
	RouteID   string `json:"routeId"`
	Reason    Msg    `json:"reason"`
}

// PlanCancelled commits the cancel once every route accepted: holds become cancellations (driver tasks end,
// assigned drivers are freed). The wallet refund follows as WalletRefunded.
type PlanCancelled struct {
	PlanID      string `json:"planId"`
	RequestID   string `json:"requestId,omitempty"`
	RequestedBy string `json:"requestedBy,omitempty"`
	Reason      string `json:"reason,omitempty"`
	// RefundRp is what goes back to the wallet; zero when the plan was not paid from it.
	RefundRp int64 `json:"refundRp,omitempty"`
}

// PlanCancelAborted ends an attempt that did not commit (a route rejected, or the votes did not all arrive in
// time): every hold is released and the plan goes on as before.
type PlanCancelAborted struct {
	PlanID    string `json:"planId"`
	RequestID string `json:"requestId"`
	Reason    Msg    `json:"reason"`
}

// PlanRescheduled carries the new schedule and the routes re-timed to it.
type PlanRescheduled struct {
	PlanID   string   `json:"planId"`
	Schedule Schedule `json:"schedule"`
	Routes   []Route  `json:"routes"`
}

/* ---------- loong.dispatch.v1 (key: route id) ---------- */

// RouteAssigned gives a route to a driver. Route is the snapshot after dispatch (start rebased to the driver's
// position and time); its DriverID, VehicleID, StartAt and AssignedAt are filled to match the fields here.
type RouteAssigned struct {
	PlanID       string `json:"planId"`
	PlanCode     string `json:"planCode"`
	CustomerName string `json:"customerName"`
	DriverID     string `json:"driverId"`
	// DriverName lets the customer's timeline name the driver of his own order; empty from older producers.
	DriverName string `json:"driverName,omitempty"`
	VehicleID  string `json:"vehicleId"`
	StartAt    Time   `json:"startAt"`
	Route      Route  `json:"route"`
}

// Why a route was taken from its driver (RouteUnassigned.Reason).
const (
	UnassignAdmin         = "admin"          // an admin took it back or reassigned it
	UnassignDriverOffline = "driver_offline" // the driver went offline before starting
	UnassignDriverSilent  = "driver_silent"  // the driver's app stopped reporting shortly before departure
	UnassignRescheduled   = "rescheduled"    // the sender moved the plan to another time: dispatch starts over
	UnassignCancelled     = "cancelled"      // the plan was cancelled
)

type RouteUnassigned struct {
	PlanID   string `json:"planId"`
	RouteID  string `json:"routeId"`
	DriverID string `json:"driverId"`
	// Reason is one of the Unassign* values; empty from older producers (read it as UnassignAdmin).
	Reason string `json:"reason,omitempty"`
}

/* ---------- loong.execution.v1 (key: route id) ---------- */

type RouteStarted struct {
	PlanID    string `json:"planId"`
	RouteID   string `json:"routeId"`
	DriverID  string `json:"driverId"`
	StartedAt Time   `json:"startedAt"`
}

type StopArrived struct {
	PlanID    string `json:"planId"`
	RouteID   string `json:"routeId"`
	StopID    string `json:"stopId"`
	ArrivedAt Time   `json:"arrivedAt"`
}

// StopCompleted records a finished stop; Proof is set for drop-offs. See DeliveryProof for the size caveat.
type StopCompleted struct {
	PlanID      string         `json:"planId"`
	RouteID     string         `json:"routeId"`
	StopID      string         `json:"stopId"`
	CompletedAt Time           `json:"completedAt"`
	Proof       *DeliveryProof `json:"proof,omitempty"`
}

// StopIssue is a problem the driver reported at a stop. Issue is the Msg stored as RouteStop.Issue, built once
// by driver-api so every projection shows the same text.
type StopIssue struct {
	PlanID     string    `json:"planId"`
	RouteID    string    `json:"routeId"`
	StopID     string    `json:"stopId"`
	Kind       IssueKind `json:"kind"`
	Note       string    `json:"note,omitempty"`
	Issue      Msg       `json:"issue"`
	ReportedAt Time      `json:"reportedAt"`
}

type RouteCompleted struct {
	PlanID     string `json:"planId"`
	RouteID    string `json:"routeId"`
	DriverID   string `json:"driverId"`
	FinishedAt Time   `json:"finishedAt"`
}

// RouteDelayed reports the route's total delay in minutes so far.
type RouteDelayed struct {
	PlanID   string  `json:"planId"`
	RouteID  string  `json:"routeId"`
	DelayMin float64 `json:"delayMin"`
}

/* ---------- loong.driver.v1 (key: driver id) ---------- */

// DriverProvisioned creates a driver account: back office owns the master data, driver-api the login. PinHash is
// the argon2id encoding (PHC string) of the initial PIN, which is never sent in the clear.
type DriverProvisioned struct {
	DriverID  string `json:"driverId"`
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	VehicleID string `json:"vehicleId,omitempty"`
	PinHash   string `json:"pinHash"`
	JoinedAt  Time   `json:"joinedAt"`
}

// DriverUpdated carries the full master data after a change. PinHash is set only when the PIN was reset.
type DriverUpdated struct {
	DriverID  string `json:"driverId"`
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	VehicleID string `json:"vehicleId,omitempty"`
	PinHash   string `json:"pinHash,omitempty"`
}

type DriverOnline struct {
	DriverID string  `json:"driverId"`
	Position *LatLng `json:"position,omitempty"`
}

type DriverOffline struct {
	DriverID string `json:"driverId"`
}

/* ---------- loong.driver.position.v1 (key: driver id) ---------- */

// DriverPosition is one GPS fix. RouteID is the route being driven (empty when idle); console-api only
// processes fixes of drivers carrying one of its plans.
type DriverPosition struct {
	DriverID   string  `json:"driverId"`
	RouteID    string  `json:"routeId,omitempty"`
	Position   LatLng  `json:"position"`
	SpeedKmh   float64 `json:"speedKmh,omitzero"`
	RecordedAt Time    `json:"recordedAt"`
}

/* ---------- loong.fleet.v1 (key: vehicle id, compacted) ---------- */

// VehicleUpserted is the full vehicle state; on a compacted topic the latest event per vehicle wins.
type VehicleUpserted struct {
	Vehicle Vehicle `json:"vehicle"`
}

// VehicleRemoved is the last event of a removed vehicle (the compacted topic keeps it as the final state).
type VehicleRemoved struct {
	VehicleID string `json:"vehicleId"`
}

/* ---------- loong.settings.v1 (key: settings key, compacted) ---------- */

// Settings keys, which are also the Kafka record keys.
const (
	SettingsKeyAI       = "ai"
	SettingsKeyDispatch = "dispatch"
)

// SettingsUpdated carries the full value of one settings group: exactly the field named by Key is set.
type SettingsUpdated struct {
	Key      string            `json:"key"`
	AI       *AIFeatures       `json:"ai,omitempty"`
	Dispatch *DispatchSettings `json:"dispatch,omitempty"`
}

/* ---------- loong.customer.v1 (key: customer id) ---------- */

type CustomerRegistered struct {
	CustomerID string `json:"customerId"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	CreatedAt  Time   `json:"createdAt"`
}

// CustomerUpdated carries the customer's full contact data after a change.
type CustomerUpdated struct {
	CustomerID string `json:"customerId"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
}

// CustomerBlocked is decided by back office; console-api stops serving the customer and revokes sessions.
type CustomerBlocked struct {
	CustomerID string `json:"customerId"`
}

type CustomerUnblocked struct {
	CustomerID string `json:"customerId"`
}

/* ---------- loong.wallet.v1 (key: customer id) ---------- */

// TopUpCreated, TopUpPaid, TopUpExpired and TopUpCancelled carry the top-up in its new state (status, paidAt).
type TopUpCreated struct {
	TopUp TopUp `json:"topUp"`
}

type TopUpPaid struct {
	TopUp TopUp `json:"topUp"`
	// TxID and BalanceAfterRp are the wallet transaction the payment made (the top-up's ledger line).
	TxID           string `json:"txId,omitempty"`
	BalanceAfterRp int64  `json:"balanceAfterRp,omitempty"`
}

type TopUpExpired struct {
	TopUp TopUp `json:"topUp"`
}

// TopUpCancelled is the customer withdrawing a top-up that was still waiting for payment.
type TopUpCancelled struct {
	TopUp TopUp `json:"topUp"`
}

// WalletCharged is the payment of a plan from the wallet; AmountRp is positive and BalanceAfterRp is the
// balance after it.
type WalletCharged struct {
	TxID           string `json:"txId"`
	CustomerID     string `json:"customerId"`
	PlanID         string `json:"planId"`
	AmountRp       int64  `json:"amountRp"`
	BalanceAfterRp int64  `json:"balanceAfterRp"`
}

// WalletRefunded returns a charge to the wallet after a cancel; AmountRp is positive.
type WalletRefunded struct {
	TxID           string `json:"txId"`
	CustomerID     string `json:"customerId"`
	PlanID         string `json:"planId"`
	AmountRp       int64  `json:"amountRp"`
	BalanceAfterRp int64  `json:"balanceAfterRp"`
}
