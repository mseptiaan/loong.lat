package contracts

// Wire types that mirror frontend/packages/domain/src/types.ts, wallet.ts and ai.ts field for field.
// JSON names are the TypeScript names (camelCase); money is whole rupiah (int64); distances are km and
// durations minutes (float64: the engine rounds to one decimal); times are Time (ISO-8601 UTC).
// Optional TypeScript fields are omitted when empty. Keep this file in step with the frontend types.

// Msg is a language-neutral message: a catalog key plus raw parameters. The frontend renders it in the
// reader's language, so backends never translate. Param values are strings, numbers or nested Msg.
type Msg struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}

/* ---------- geography ---------- */

type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// LngLat is a [lng, lat] pair, the GeoJSON/MapLibre order.
type LngLat [2]float64

type PlaceRef struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Position LatLng `json:"position"`
	PlaceID  string `json:"placeId,omitempty"`
}

// Waypoint is a stop location with the person to meet there (the only PII a driver needs).
type Waypoint struct {
	PlaceRef
	ContactName  string `json:"contactName"`
	ContactPhone string `json:"contactPhone"`
	Note         string `json:"note,omitempty"`
}

/* ---------- enums ---------- */

type Role string

const (
	RoleCustomer Role = "customer"
	RoleAdmin    Role = "admin"
	RoleDriver   Role = "driver"
)

type VehicleType string

const (
	VehicleTypeMotor    VehicleType = "motor"
	VehicleTypeBlindvan VehicleType = "blindvan"
)

type VehicleStatus string

const (
	VehicleStatusActive   VehicleStatus = "aktif"
	VehicleStatusService  VehicleStatus = "servis"
	VehicleStatusInactive VehicleStatus = "nonaktif"
)

type ItemSource string

const (
	ItemSourceManual ItemSource = "manual"
	ItemSourcePhoto  ItemSource = "foto"
	ItemSourceVoice  ItemSource = "suara"
)

type ScheduleMode string

const (
	ScheduleModeNow       ScheduleMode = "sekarang"
	ScheduleModeScheduled ScheduleMode = "terjadwal"
)

type Preset string

const (
	PresetCheapest Preset = "termurah"
	PresetShortest Preset = "terpendek"
	PresetFastest  Preset = "tercepat"
	PresetBalanced Preset = "seimbang"
)

type StopKind string

const (
	StopKindPickup  StopKind = "ambil"
	StopKindDropoff StopKind = "antar"
)

type StopStatus string

const (
	StopStatusWaiting StopStatus = "menunggu"
	StopStatusEnRoute StopStatus = "menuju"
	StopStatusArrived StopStatus = "tiba"
	StopStatusDone    StopStatus = "selesai"
	StopStatusProblem StopStatus = "bermasalah"
)

type RouteStatus string

const (
	RouteStatusAwaitingDriver RouteStatus = "menunggu_driver"
	RouteStatusAssigned       RouteStatus = "ditugaskan"
	RouteStatusRunning        RouteStatus = "berjalan"
	RouteStatusDone           RouteStatus = "selesai"
)

type PlanStatus string

const (
	PlanStatusDraft          PlanStatus = "draft"
	PlanStatusOptionsReady   PlanStatus = "opsi_siap"
	PlanStatusAwaitingDriver PlanStatus = "menunggu_driver"
	PlanStatusAssigned       PlanStatus = "ditugaskan"
	PlanStatusRunning        PlanStatus = "berjalan"
	PlanStatusDone           PlanStatus = "selesai"
	PlanStatusCancelled      PlanStatus = "dibatalkan"
)

type PaymentMethod string

const (
	PaymentWallet   PaymentMethod = "saldo"
	PaymentTransfer PaymentMethod = "transfer"
	PaymentCash     PaymentMethod = "tunai"
)

type IssueKind string

const (
	IssueKindNobodyThere      IssueKind = "tidak_ada_orang"
	IssueKindDamagedGoods     IssueKind = "barang_rusak"
	IssueKindWrongAddress     IssueKind = "alamat_salah"
	IssueKindRecipientRefused IssueKind = "ditolak_penerima"
	IssueKindOther            IssueKind = "lainnya"
)

type TopUpMethod string

const (
	TopUpMethodVABCA     TopUpMethod = "va_bca"
	TopUpMethodVAMandiri TopUpMethod = "va_mandiri"
	TopUpMethodVABNI     TopUpMethod = "va_bni"
	TopUpMethodVABRI     TopUpMethod = "va_bri"
	TopUpMethodQRIS      TopUpMethod = "qris"
)

type TopUpStatus string

const (
	TopUpStatusPending   TopUpStatus = "menunggu"
	TopUpStatusSucceeded TopUpStatus = "berhasil"
	TopUpStatusExpired   TopUpStatus = "kedaluwarsa"
	TopUpStatusCancelled TopUpStatus = "dibatalkan"
)

/* ---------- identity ---------- */

// Session is who a request acts as: the principal of a bearer token and the body of `auth.*` results.
type Session struct {
	Role   Role   `json:"role"`
	UserID string `json:"userId"`
	Name   string `json:"name"`
}

/* ---------- fleet ---------- */

type FareConfig struct {
	Base             int64   `json:"base"`
	PerKm            int64   `json:"perKm"`
	PerMin           int64   `json:"perMin"`
	PerExtraStop     int64   `json:"perExtraStop"`
	SurchargePerKg   int64   `json:"surchargePerKg"`
	SurchargeAboveKg float64 `json:"surchargeAboveKg"`
}

type Vehicle struct {
	ID             string        `json:"id"`
	Type           VehicleType   `json:"type"`
	Plate          string        `json:"plate"`
	Model          string        `json:"model"`
	MaxWeightKg    float64       `json:"maxWeightKg"`
	MaxVolumeM3    float64       `json:"maxVolumeM3"`
	MaxDimensionCm float64       `json:"maxDimensionCm"`
	Fare           FareConfig    `json:"fare"`
	Status         VehicleStatus `json:"status"`
	DriverID       string        `json:"driverId,omitempty"`
}

/* ---------- shipments ---------- */

type Item struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Qty        int        `json:"qty"`
	WeightKg   float64    `json:"weightKg"`
	LengthCm   float64    `json:"lengthCm"`
	WidthCm    float64    `json:"widthCm"`
	HeightCm   float64    `json:"heightCm"`
	Fragile    bool       `json:"fragile"`
	Source     ItemSource `json:"source"`
	Confidence *float64   `json:"confidence,omitempty"`
	CatalogKey string     `json:"catalogKey,omitempty"`
}

type Shipment struct {
	ID       string   `json:"id"`
	Pickup   Waypoint `json:"pickup"`
	Dropoff  Waypoint `json:"dropoff"`
	Items    []Item   `json:"items"`
	Deadline Time     `json:"deadline,omitzero"`
}

type TimeWindow struct {
	From Time `json:"from"`
	To   Time `json:"to"`
}

type Schedule struct {
	Mode   ScheduleMode `json:"mode"`
	Window *TimeWindow  `json:"window,omitempty"`
}

type Priority struct {
	Preset Preset `json:"preset"`
	Text   string `json:"text,omitempty"`
}

/* ---------- routes ---------- */

// StopItem is one line the driver checks at a stop; Detail is e.g. "4 kg · 40×30×30 cm".
type StopItem struct {
	Name       string `json:"name"`
	CatalogKey string `json:"catalogKey,omitempty"`
	Qty        int    `json:"qty"`
	Detail     Msg    `json:"detail"`
	Fragile    bool   `json:"fragile"`
}

type ProofCheckKey string

const (
	ProofCheckRecipient ProofCheckKey = "recipient"
	ProofCheckParcel    ProofCheckKey = "parcel"
	ProofCheckQuality   ProofCheckKey = "quality"
)

type ProofCheckResult string

const (
	ProofResultOK      ProofCheckResult = "ok"
	ProofResultUnclear ProofCheckResult = "unclear"
	ProofResultMissing ProofCheckResult = "missing"
)

type ProofCheck struct {
	Key    ProofCheckKey    `json:"key"`
	Result ProofCheckResult `json:"result"`
	Note   Msg              `json:"note"`
}

type ProofAnalysis struct {
	Summary    Msg          `json:"summary"`
	Checks     []ProofCheck `json:"checks"`
	Confidence float64      `json:"confidence"`
	Model      string       `json:"model"`
	AnalyzedAt Time         `json:"analyzedAt"`
}

type ProofKind string

const (
	ProofKindOTP   ProofKind = "otp"
	ProofKindPhoto ProofKind = "foto"
)

// DeliveryProof is what the driver recorded at a drop-off. Photo is a downscaled data URL; events are
// capped at MaxEnvelopeBytes, so a producer must keep it small or leave it out.
type DeliveryProof struct {
	Kind     ProofKind      `json:"kind"`
	At       Time           `json:"at"`
	Photo    string         `json:"photo,omitempty"`
	Analysis *ProofAnalysis `json:"analysis,omitempty"`
}

type RouteStop struct {
	ID          string     `json:"id"`
	Seq         int        `json:"seq"`
	Kind        StopKind   `json:"kind"`
	ShipmentID  string     `json:"shipmentId"`
	Waypoint    Waypoint   `json:"waypoint"`
	LegKm       float64    `json:"legKm"`
	LegMin      float64    `json:"legMin"`
	EtaMin      float64    `json:"etaMin"`
	ArriveAt    Time       `json:"arriveAt"`
	Status      StopStatus `json:"status"`
	ArrivedAt   Time       `json:"arrivedAt,omitzero"`
	CompletedAt Time       `json:"completedAt,omitzero"`
	// Issue is the problem the driver reported (issue kind + note as a Msg).
	Issue        *Msg           `json:"issue,omitempty"`
	ItemsSummary Msg            `json:"itemsSummary"`
	Items        []StopItem     `json:"items,omitempty"`
	DeliveryCode string         `json:"deliveryCode,omitempty"`
	Proof        *DeliveryProof `json:"proof,omitempty"`
}

type FareLine struct {
	Label    Msg   `json:"label"`
	AmountRp int64 `json:"amountRp"`
}

type Route struct {
	ID            string      `json:"id"`
	PlanID        string      `json:"planId"`
	VehicleType   VehicleType `json:"vehicleType"`
	VehicleID     string      `json:"vehicleId,omitempty"`
	DriverID      string      `json:"driverId,omitempty"`
	Status        RouteStatus `json:"status"`
	StartPosition LatLng      `json:"startPosition"`
	StartAt       Time        `json:"startAt"`
	Stops         []RouteStop `json:"stops"`
	DistanceKm    float64     `json:"distanceKm"`
	DurationMin   float64     `json:"durationMin"`
	CostRp        int64       `json:"costRp"`
	// Fare lines sum to CostRp.
	Fare          []FareLine `json:"fare"`
	TotalWeightKg float64    `json:"totalWeightKg"`
	Geometry      []LngLat   `json:"geometry,omitempty"`
	AssignedAt    Time       `json:"assignedAt,omitzero"`
	StartedAt     Time       `json:"startedAt,omitzero"`
	FinishedAt    Time       `json:"finishedAt,omitzero"`
	// LegStartedAt is when the vehicle began the leg to the active stop.
	LegStartedAt Time `json:"legStartedAt,omitzero"`
	// DelayMin is the total delay in minutes.
	DelayMin float64 `json:"delayMin,omitzero"`
}

/* ---------- options and plans ---------- */

type Totals struct {
	CostRp         int64   `json:"costRp"`
	DistanceKm     float64 `json:"distanceKm"`
	DurationMin    float64 `json:"durationMin"`
	AvgDeliveryMin float64 `json:"avgDeliveryMin"`
	Stops          int     `json:"stops"`
	Motor          int     `json:"motor"`
	Blindvan       int     `json:"blindvan"`
}

type Savings struct {
	CostRp     int64   `json:"costRp"`
	Pct        float64 `json:"pct"`
	DistanceKm float64 `json:"distanceKm"`
}

type PlanOption struct {
	ID          string  `json:"id"`
	Preset      Preset  `json:"preset"`
	Label       Msg     `json:"label"`
	Recommended bool    `json:"recommended"`
	Headline    Msg     `json:"headline"`
	Routes      []Route `json:"routes"`
	Totals      Totals  `json:"totals"`
	Savings     Savings `json:"savings"`
	Reasons     []Msg   `json:"reasons"`
}

type OptionSet struct {
	ComputedAt Time         `json:"computedAt"`
	Baseline   Totals       `json:"baseline"`
	Options    []PlanOption `json:"options"`
	InputHash  string       `json:"inputHash"`
	// Method says how hard the engine looked for these options (the frontend's OptionSet.method).
	Method *Method `json:"method,omitempty"`
	// Report is the AI's explanation of the options, written on request after they were computed.
	Report *Report `json:"report,omitempty"`
}

// Effort is how hard a quote searches. Each level includes the one before it.
type Effort string

const (
	// EffortFast solves each preset once: seconds, and exact for the smallest plans.
	EffortFast Effort = "fast"
	// EffortThorough also tries several clusterings per preset and polishes the best answer.
	EffortThorough Effort = "thorough"
	// EffortReviewed also runs review rounds: a reviewer audits the candidates and asks for further experiments.
	EffortReviewed Effort = "reviewed"
)

// Efforts lists every effort, quickest first.
var Efforts = []Effort{EffortFast, EffortThorough, EffortReviewed}

// Reviewers of a reviewed quote.
const (
	ReviewerAI    = "ai"    // the model chose the experiments of each round
	ReviewerRules = "rules" // the built-in rules did (no AI, or the model was unavailable)
)

// Method describes how an OptionSet was found. The numbers are the engine's own counts.
type Method struct {
	Effort Effort `json:"effort"`
	// Trials counts the solves run, over all presets.
	Trials int `json:"trials"`
	// Rounds counts the review rounds that ran after the first pass.
	Rounds int `json:"rounds,omitzero"`
	// Polished says the best answers were refined from where they stood.
	Polished bool `json:"polished,omitzero"`
	// Reviewer is ReviewerAI or ReviewerRules for a reviewed quote.
	Reviewer string `json:"reviewer,omitempty"`
	// Proven says an exhaustive search confirmed every option optimal.
	Proven bool `json:"proven,omitzero"`
	// RoadSource is "mapbox", "mapbox-cache" or "estimate".
	RoadSource string `json:"roadSource,omitempty"`
}

// Report is an AI-written explanation of an OptionSet, in the language it was asked in.
type Report struct {
	Text   string `json:"text"`
	Locale string `json:"locale"`
	At     Time   `json:"at"`
}

type PlanEvent struct {
	ID   string `json:"id"`
	At   Time   `json:"at"`
	Text Msg    `json:"text"`
}

type Plan struct {
	ID               string        `json:"id"`
	Code             string        `json:"code"`
	UserID           string        `json:"userId"`
	Status           PlanStatus    `json:"status"`
	Schedule         Schedule      `json:"schedule"`
	Priority         Priority      `json:"priority"`
	Shipments        []Shipment    `json:"shipments"`
	OptionSet        *OptionSet    `json:"optionSet,omitempty"`
	SelectedOptionID string        `json:"selectedOptionId,omitempty"`
	Routes           []Route       `json:"routes"`
	PaymentMethod    PaymentMethod `json:"paymentMethod,omitempty"`
	TotalCostRp      int64         `json:"totalCostRp,omitzero"`
	CreatedAt        Time          `json:"createdAt"`
	UpdatedAt        Time          `json:"updatedAt"`
	ConfirmedAt      Time          `json:"confirmedAt,omitzero"`
	FinishedAt       Time          `json:"finishedAt,omitzero"`
	CancelReason     string        `json:"cancelReason,omitempty"`
	// SavingsRp and SavingsPct are what the chosen option saves against sending each shipment alone. Console
	// sets them on the snapshot it shares at confirmation (the alternatives themselves are not shared) so that the
	// back office can show and total savings; nil means not known.
	SavingsRp  *int64   `json:"savingsRp,omitempty"`
	SavingsPct *float64 `json:"savingsPct,omitempty"`
	// PendingAction is set while a two-phase action is in flight ("cancel": waiting for the drivers' votes); the
	// screens show "cancelling..." and offer nothing else on the plan.
	PendingAction string      `json:"pendingAction,omitempty"`
	Events        []PlanEvent `json:"events"`
	DraftStep     int         `json:"draftStep"`
}

/* ---------- notifications ---------- */

// AppNotification is one line of a bell menu. Audience is "customer:<id>", "admin" or "driver:<id>": each service
// keeps and serves only its own audience's (console-api the customers', backoffice-api the admins', driver-api
// the drivers').
type AppNotification struct {
	ID       string `json:"id"`
	Audience string `json:"audience"`
	At       Time   `json:"at"`
	Title    Msg    `json:"title"`
	Body     Msg    `json:"body"`
	PlanID   string `json:"planId,omitempty"`
	Read     bool   `json:"read"`
}

/* ---------- wallet ---------- */

type TopUp struct {
	ID        string      `json:"id"`
	UserID    string      `json:"userId"`
	AmountRp  int64       `json:"amountRp"`
	Method    TopUpMethod `json:"method"`
	Status    TopUpStatus `json:"status"`
	CreatedAt Time        `json:"createdAt"`
	ExpiresAt Time        `json:"expiresAt"`
	PaidAt    Time        `json:"paidAt,omitzero"`
	// Reference is the virtual-account number or QRIS code.
	Reference string `json:"reference"`
}

/* ---------- settings ---------- */

type AIFeatures struct {
	Vision  bool `json:"vision"`
	Voice   bool `json:"voice"`
	Intent  bool `json:"intent"`
	Copilot bool `json:"copilot"`
}

type DispatchSettings struct {
	Auto bool `json:"auto"`
}
