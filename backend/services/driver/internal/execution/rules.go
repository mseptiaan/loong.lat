// Package execution runs a route stop by stop: the rules of what the driver may do and when (this file, free of
// any I/O), and the transactional service that applies them (service.go).
package execution

import (
	"crypto/subtle"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
)

// Message keys of the frontend catalog (the mock backend's, so screens already render them).
const (
	keyRouteNotFound      = "srv.ops.err.routeNotFound"
	keyStopNotFound       = "srv.ops.err.stopNotFound"
	keyNotYourRoute       = "srv.api.notYourRoute"
	keyRouteCannotStart   = "srv.api.routeCannotStart"
	keyTooEarly           = "srv.api.tooEarly"
	keyStartRouteFirst    = "srv.api.startRouteFirst"
	keyFinishPreviousStop = "srv.api.finishPreviousStop"
	keyStopAlreadyArrived = "srv.api.stopAlreadyArrived"
	keyMarkArrivedFirst   = "srv.api.markArrivedFirst"
	keyProofRequired      = "srv.api.proofRequired"
	keyProofPhotoInvalid  = "srv.api.proofPhotoInvalid"
	keyOTPMismatch        = "srv.api.otpMismatch"
	keyIssueOnlyActive    = "srv.api.issueOnlyActive"
	keyIssueNoteRequired  = "srv.api.issueNoteRequired"
	keyIssueWithNote      = "srv.ops.issue.withNote"
	// keyRouteCancelling is NEW (not in the frontend catalogs yet): a plan cancel is in flight and holds the route.
	keyRouteCancelling = "srv.api.routeCancelling"
)

// issueKeyPrefix and a kind make the catalog key of the kind's label, e.g. "dom.issue.barang_rusak".
const issueKeyPrefix = "dom.issue."

const (
	// startLead is how long before a route's planned departure the driver may already start it.
	startLead = 45 * time.Minute
	// maxPhotoChars bounds a proof photo's data URL. It sits far above what the app sends (60-120 KB) and keeps
	// the stop.completed event below contracts.MaxEnvelopeBytes.
	maxPhotoChars = 900_000
	maxNoteRunes  = 500
)

// StopProof is the frontend's StopProof: what the driver offers at a drop-off.
type StopProof struct {
	// Kind is "foto", "otp" or "tanpa" (none).
	Kind string `json:"kind"`
	// Value is the OTP code, or the photo's file name.
	Value string `json:"value,omitempty"`
	// Photo is a downscaled JPEG, PNG or WebP data URL; required for a photo proof.
	Photo string `json:"photo,omitempty"`
}

// Fact is an event the action publishes.
type Fact struct {
	Type string
	Data any
}

// Step is what one action does to a route. An action that finds its work already done (a retry) is a replay:
// the route comes back as it is and there is nothing to store or publish.
type Step struct {
	Route contracts.Route
	// Facts are published in order, each as the next execution version of the route.
	Facts []Fact
	// Position is where the driver now is (at a stop), if the action moved them.
	Position *contracts.LatLng
	// Started and Finished say that the route has begun or completed with this step.
	Started, Finished bool
}

// Replay reports that the action changed nothing.
func (s Step) Replay() bool { return len(s.Facts) == 0 }

func replay(r contracts.Route) Step { return Step{Route: r} }

func conflict(key string) error { return platform.Conflict(contracts.Msg{Key: key}) }

func invalid(field, key string) error {
	return platform.Invalid(field, contracts.Msg{Key: key})
}

// refuse turns a routeplan transition error into the answer the app shows. notRunning and notActive say what
// the action tells the driver when the route has not started, or another stop comes first; anything unknown
// passes through and ends as a 500.
func refuse(err error, notRunning, notActive string) error {
	switch {
	case errors.Is(err, routeplan.ErrStopNotFound):
		return platform.NotFound(contracts.Msg{Key: keyStopNotFound})
	case errors.Is(err, routeplan.ErrNotAssigned):
		return conflict(keyRouteCannotStart)
	case errors.Is(err, routeplan.ErrNotRunning):
		return conflict(notRunning)
	case errors.Is(err, routeplan.ErrNotActiveStop):
		return conflict(notActive)
	case errors.Is(err, routeplan.ErrNotEnRoute):
		return conflict(keyStopAlreadyArrived)
	case errors.Is(err, routeplan.ErrNotArrived):
		return conflict(keyMarkArrivedFirst)
	}
	return err
}

func stopIndex(r contracts.Route, stopID string) int {
	return slices.IndexFunc(r.Stops, func(s contracts.RouteStop) bool { return s.ID == stopID })
}

// start sets an assigned route off, at most startLead before its planned departure. Starting a route that is
// already running is a replay.
func start(r contracts.Route, at time.Time) (Step, error) {
	if r.Status == contracts.RouteStatusRunning {
		return replay(r), nil
	}
	next, err := routeplan.Start(r, at)
	if err != nil {
		return Step{}, refuse(err, "", "")
	}
	if at.Before(r.StartAt.Add(-startLead)) {
		return Step{}, platform.Conflict(contracts.Msg{Key: keyTooEarly, Params: map[string]any{"time": r.StartAt}})
	}
	return Step{
		Route: next, Started: true,
		Facts: []Fact{{contracts.TypeRouteStarted, contracts.RouteStarted{PlanID: r.PlanID, RouteID: r.ID, DriverID: r.DriverID, StartedAt: contracts.At(at)}}},
	}, nil
}

// arrive marks the first open stop as reached. A stop that was reached already is a replay.
func arrive(r contracts.Route, stopID string, at time.Time) (Step, error) {
	if i := stopIndex(r, stopID); i >= 0 && (r.Stops[i].Status == contracts.StopStatusArrived || r.Stops[i].Status == contracts.StopStatusDone) {
		return replay(r), nil
	}
	next, err := routeplan.Arrive(r, stopID, at)
	if err != nil {
		return Step{}, refuse(err, keyStartRouteFirst, keyFinishPreviousStop)
	}
	pos := next.Stops[stopIndex(next, stopID)].Waypoint.Position
	return Step{
		Route: next, Position: &pos,
		Facts: []Fact{{contracts.TypeStopArrived, contracts.StopArrived{PlanID: r.PlanID, RouteID: r.ID, StopID: stopID, ArrivedAt: contracts.At(at)}}},
	}, nil
}

// complete finishes a stop the driver has arrived at. A drop-off needs a proof that checks out; a pickup needs
// none. A stop that is finished already is a replay, whatever proof the retry carries.
func complete(r contracts.Route, stopID string, proof *StopProof, at time.Time) (Step, error) {
	i := stopIndex(r, stopID)
	if i < 0 {
		return Step{}, refuse(routeplan.ErrStopNotFound, "", "")
	}
	stop := r.Stops[i]
	switch stop.Status {
	case contracts.StopStatusDone:
		return replay(r), nil
	case contracts.StopStatusArrived:
	default:
		return Step{}, conflict(keyMarkArrivedFirst)
	}

	var delivery *contracts.DeliveryProof
	if stop.Kind == contracts.StopKindDropoff {
		var err error
		if delivery, err = checkProof(stop, proof, at); err != nil {
			return Step{}, err
		}
	}
	next, finished, err := routeplan.Close(r, stopID, at, contracts.StopStatusDone, delivery, nil)
	if err != nil {
		return Step{}, refuse(err, keyMarkArrivedFirst, keyMarkArrivedFirst)
	}
	facts := []Fact{{contracts.TypeStopCompleted, contracts.StopCompleted{PlanID: r.PlanID, RouteID: r.ID, StopID: stopID, CompletedAt: contracts.At(at), Proof: delivery}}}
	return closed(r, next, stop, finished, facts, at), nil
}

// reportIssue closes the active stop as a problem. The issue message is built here, once, and travels with the
// event, so every projection shows the same text. A stop already closed as a problem is a replay.
func reportIssue(r contracts.Route, stopID string, kind contracts.IssueKind, note string, at time.Time) (Step, error) {
	issue, err := issueMsg(kind, note)
	if err != nil {
		return Step{}, err
	}
	i := stopIndex(r, stopID)
	if i >= 0 && r.Stops[i].Status == contracts.StopStatusProblem {
		return replay(r), nil
	}
	next, finished, err := routeplan.Close(r, stopID, at, contracts.StopStatusProblem, nil, &issue)
	if err != nil {
		return Step{}, refuse(err, keyIssueOnlyActive, keyIssueOnlyActive)
	}
	facts := []Fact{{contracts.TypeStopIssue, contracts.StopIssue{
		PlanID: r.PlanID, RouteID: r.ID, StopID: stopID, Kind: kind, Note: strings.TrimSpace(note), Issue: issue, ReportedAt: contracts.At(at),
	}}}
	return closed(r, next, r.Stops[i], finished, facts, at), nil
}

// closed completes a step that closed a stop: the driver is at the stop, and closing the last one finishes the
// route, which is a fact of its own.
func closed(before, after contracts.Route, stop contracts.RouteStop, finished bool, facts []Fact, at time.Time) Step {
	pos := stop.Waypoint.Position
	step := Step{Route: after, Position: &pos, Finished: finished, Facts: facts}
	if finished {
		step.Facts = append(step.Facts, Fact{contracts.TypeRouteCompleted, contracts.RouteCompleted{
			PlanID: before.PlanID, RouteID: before.ID, DriverID: before.DriverID, FinishedAt: contracts.At(at),
		}})
	}
	return step
}

var validKinds = []contracts.IssueKind{
	contracts.IssueKindNobodyThere, contracts.IssueKindDamagedGoods, contracts.IssueKindWrongAddress,
	contracts.IssueKindRecipientRefused, contracts.IssueKindOther,
}

// issueMsg is the text stored on the stop: the catalog's label of the kind, with the driver's note when given.
func issueMsg(kind contracts.IssueKind, note string) (contracts.Msg, error) {
	if !slices.Contains(validKinds, kind) {
		return contracts.Msg{}, invalid("issue", platform.MsgBadRequest)
	}
	note = strings.TrimSpace(note)
	switch {
	case utf8.RuneCountInString(note) > maxNoteRunes:
		return contracts.Msg{}, invalid("note", platform.MsgBadRequest)
	case note == "" && kind == contracts.IssueKindOther:
		return contracts.Msg{}, invalid("note", keyIssueNoteRequired)
	}
	label := contracts.Msg{Key: issueKeyPrefix + string(kind)}
	if note == "" {
		return label, nil
	}
	return contracts.Msg{Key: keyIssueWithNote, Params: map[string]any{"issue": label, "note": note}}, nil
}

// checkProof validates what the driver offered for a drop-off and returns the proof to store. An OTP must be
// the stop's own delivery code, a photo must look like one; "tanpa" (none) is refused. The photo is kept as
// sent: reading it is out of scope here.
func checkProof(stop contracts.RouteStop, proof *StopProof, at time.Time) (*contracts.DeliveryProof, error) {
	if proof == nil {
		return nil, invalid("proof", keyProofRequired)
	}
	switch proof.Kind {
	case "otp":
		if !codeMatches(stop.DeliveryCode, proof.Value) {
			return nil, invalid("proof", keyOTPMismatch)
		}
		return &contracts.DeliveryProof{Kind: contracts.ProofKindOTP, At: contracts.At(at)}, nil
	case "foto":
		if !isProofPhoto(proof.Photo) {
			return nil, invalid("proof", keyProofPhotoInvalid)
		}
		return &contracts.DeliveryProof{Kind: contracts.ProofKindPhoto, At: contracts.At(at), Photo: proof.Photo}, nil
	}
	return nil, invalid("proof", keyProofRequired)
}

// codeMatches compares the stop's delivery code with the one entered, without leaking how much of it matched.
// A stop without a code accepts nothing: an empty entry must not match an empty code.
func codeMatches(code, entered string) bool {
	return code != "" && subtle.ConstantTimeCompare([]byte(code), []byte(strings.TrimSpace(entered))) == 1
}

var photoPrefixes = []string{"data:image/jpeg;base64,", "data:image/png;base64,", "data:image/webp;base64,"}

// isProofPhoto accepts a JPEG, PNG or WebP data URL of at most maxPhotoChars with a base64 body.
func isProofPhoto(photo string) bool {
	if len(photo) > maxPhotoChars {
		return false
	}
	for _, prefix := range photoPrefixes {
		if body, ok := strings.CutPrefix(photo, prefix); ok {
			body = strings.TrimRight(body, "=")
			return body != "" && !strings.ContainsFunc(body, func(r rune) bool { return !isBase64(r) })
		}
	}
	return false
}

func isBase64(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/'
}
