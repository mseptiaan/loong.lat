package execution

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
)

const (
	routeID = "pln-1:r1"
	stop1   = routeID + ":s1" // pickup
	stop2   = routeID + ":s2" // drop-off, drivertest.CodeA
	stop3   = routeID + ":s3" // drop-off, drivertest.CodeB
)

var t0 = drivertest.Epoch

func assigned() contracts.Route { return drivertest.Route(routeID, "drv-1") }

// must applies a step and returns the route it produced.
func must(t *testing.T, step Step, err error) contracts.Route {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	return step.Route
}

func started(t *testing.T) contracts.Route {
	t.Helper()
	step, err := start(assigned(), t0)
	return must(t, step, err)
}

// arrivedAt returns the route with its first n-1 stops closed and the nth one arrived at, one minute apart.
func arrivedAt(t *testing.T, n int) contracts.Route {
	t.Helper()
	r := started(t)
	at := t0
	for i := 1; i <= n; i++ {
		at = at.Add(time.Minute)
		id := routeID + ":s" + string(rune('0'+i))
		step, err := arrive(r, id, at)
		r = must(t, step, err)
		if i == n {
			break
		}
		at = at.Add(time.Minute)
		step, err = complete(r, id, &StopProof{Kind: "otp", Value: codeOf(r, id)}, at)
		r = must(t, step, err)
	}
	return r
}

func codeOf(r contracts.Route, stopID string) string {
	return r.Stops[stopIndex(r, stopID)].DeliveryCode
}

// wantRefusal checks that err is the platform error with the code, message key and field.
func wantRefusal(t *testing.T, err error, code platform.ErrorCode, key, field string) {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok {
		t.Fatalf("error = %v, want a platform error", err)
	}
	if apiErr.Code != code || apiErr.Msg.Key != key || apiErr.Field != field {
		t.Errorf("refused with %s %q field %q, want %s %q field %q", apiErr.Code, apiErr.Msg.Key, apiErr.Field, code, key, field)
	}
}

func factTypes(s Step) []string {
	var types []string
	for _, f := range s.Facts {
		types = append(types, f.Type)
	}
	return types
}

func TestStartSetsAnAssignedRouteOff(t *testing.T) {
	step, err := start(assigned(), t0)
	if err != nil {
		t.Fatal(err)
	}
	if step.Route.Status != contracts.RouteStatusRunning || step.Route.Stops[0].Status != contracts.StopStatusEnRoute {
		t.Errorf("route %s, first stop %s; want running and en route", step.Route.Status, step.Route.Stops[0].Status)
	}
	if !step.Started || step.Finished || !reflect.DeepEqual(factTypes(step), []string{contracts.TypeRouteStarted}) {
		t.Errorf("step = %+v, want exactly route.started", step)
	}
	fact := step.Facts[0].Data.(contracts.RouteStarted)
	if fact.DriverID != "drv-1" || fact.RouteID != routeID || fact.StartedAt.Time != t0 {
		t.Errorf("route.started = %+v", fact)
	}
}

func TestStartIsOnlyForAnAssignedRoute(t *testing.T) {
	awaiting := routeplan.Unassign(assigned())
	done := started(t)
	done.Status = contracts.RouteStatusDone
	for name, route := range map[string]contracts.Route{"awaiting a driver": awaiting, "done": done} {
		t.Run(name, func(t *testing.T) {
			_, err := start(route, t0)
			wantRefusal(t, err, platform.CodeConflict, "srv.api.routeCannotStart", "")
		})
	}
}

func TestStartingARunningRouteAgainIsAReplay(t *testing.T) {
	r := started(t)
	step, err := start(r, t0.Add(time.Minute))
	if err != nil || !step.Replay() || !reflect.DeepEqual(step.Route, r) || step.Started {
		t.Errorf("step = %+v, err = %v; want the route back unchanged and no facts", step, err)
	}
}

func TestStartIsRefusedMoreThan45MinutesBeforeTheDeparture(t *testing.T) {
	r := assigned()
	r.StartAt = contracts.At(t0.Add(2 * time.Hour))
	earliest := t0.Add(75 * time.Minute)

	_, err := start(r, earliest.Add(-time.Millisecond))
	wantRefusal(t, err, platform.CodeConflict, "srv.api.tooEarly", "")
	apiErr, _ := errors.AsType[*platform.APIError](err)
	if got := apiErr.Msg.Params["time"]; got != r.StartAt {
		t.Errorf("the message names %v, want the departure %v", got, r.StartAt)
	}

	if _, err := start(r, earliest); err != nil {
		t.Errorf("exactly 45 minutes before the departure must be allowed: %v", err)
	}
}

func TestArriveMarksTheFirstStop(t *testing.T) {
	r := started(t)
	step, err := arrive(r, stop1, t0.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if st := step.Route.Stops[0]; st.Status != contracts.StopStatusArrived || st.ArrivedAt.Time != t0.Add(5*time.Minute) {
		t.Errorf("stop = %s arrived %v", st.Status, st.ArrivedAt)
	}
	if !reflect.DeepEqual(factTypes(step), []string{contracts.TypeStopArrived}) {
		t.Errorf("facts = %v", factTypes(step))
	}
	if want := r.Stops[0].Waypoint.Position; step.Position == nil || *step.Position != want {
		t.Errorf("driver position = %v, want the stop's %v", step.Position, want)
	}
	if r.Stops[0].Status != contracts.StopStatusEnRoute {
		t.Error("the route given in must not change")
	}
}

func TestArriveRefusals(t *testing.T) {
	cases := []struct {
		name  string
		route contracts.Route
		stop  string
		code  platform.ErrorCode
		key   string
	}{
		{"route not started", assigned(), stop1, platform.CodeConflict, "srv.api.startRouteFirst"},
		{"another stop comes first", started(t), stop2, platform.CodeConflict, "srv.api.finishPreviousStop"},
		{"no such stop", started(t), "nope", platform.CodeNotFound, "srv.ops.err.stopNotFound"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := arrive(c.route, c.stop, t0)
			wantRefusal(t, err, c.code, c.key, "")
		})
	}
}

func TestArrivingAgainIsAReplay(t *testing.T) {
	r := arrivedAt(t, 2) // stop 1 done, stop 2 arrived
	for _, stop := range []string{stop2, stop1} {
		step, err := arrive(r, stop, t0.Add(time.Hour))
		if err != nil || !step.Replay() || !reflect.DeepEqual(step.Route, r) || step.Position != nil {
			t.Errorf("arrive %s again: step = %+v, err = %v; want the route back and no facts", stop, step, err)
		}
	}
}

func TestPickupNeedsNoProof(t *testing.T) {
	r := arrivedAt(t, 1)
	step, err := complete(r, stop1, nil, t0.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	completed := step.Facts[0].Data.(contracts.StopCompleted)
	if completed.Proof != nil || step.Route.Stops[0].Status != contracts.StopStatusDone {
		t.Errorf("proof = %v, stop = %s", completed.Proof, step.Route.Stops[0].Status)
	}
	if next := step.Route.Stops[1]; next.Status != contracts.StopStatusEnRoute {
		t.Errorf("next stop = %s, want the route moved on to it", next.Status)
	}
}

func TestDropoffNeedsAProofThatChecksOut(t *testing.T) {
	r := arrivedAt(t, 2)
	at := t0.Add(20 * time.Minute)
	cases := []struct {
		name  string
		proof *StopProof
		key   string
	}{
		{"none at all", nil, "srv.api.proofRequired"},
		{"explicitly none", &StopProof{Kind: "tanpa"}, "srv.api.proofRequired"},
		{"unknown kind", &StopProof{Kind: "tanda-tangan"}, "srv.api.proofRequired"},
		{"wrong code", &StopProof{Kind: "otp", Value: "0000"}, "srv.api.otpMismatch"},
		{"another stop's code", &StopProof{Kind: "otp", Value: drivertest.CodeB}, "srv.api.otpMismatch"},
		{"no code entered", &StopProof{Kind: "otp"}, "srv.api.otpMismatch"},
		{"photo missing", &StopProof{Kind: "foto"}, "srv.api.proofPhotoInvalid"},
		{"photo not an image", &StopProof{Kind: "foto", Photo: "data:text/html;base64,PGgxPg=="}, "srv.api.proofPhotoInvalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			step, err := complete(r, stop2, c.proof, at)
			wantRefusal(t, err, platform.CodeInvalid, c.key, "proof")
			if len(step.Facts) != 0 || step.Route.ID != "" {
				t.Errorf("a refused completion must produce nothing, got %+v", step)
			}
		})
	}
}

func TestOTPProofIsStoredWithoutTheCode(t *testing.T) {
	r := arrivedAt(t, 2)
	step, err := complete(r, stop2, &StopProof{Kind: "otp", Value: "  " + drivertest.CodeA + " "}, t0.Add(20*time.Minute))
	if err != nil {
		t.Fatalf("the code is trimmed before comparing: %v", err)
	}
	proof := step.Facts[0].Data.(contracts.StopCompleted).Proof
	if proof == nil || proof.Kind != contracts.ProofKindOTP || proof.Photo != "" || proof.At.Time != t0.Add(20*time.Minute) {
		t.Errorf("proof = %+v", proof)
	}
	if got := step.Route.Stops[1].Proof; !reflect.DeepEqual(got, proof) {
		t.Errorf("the stop holds %+v but the event carries %+v", got, proof)
	}
}

func TestPhotoProofIsStoredAsSent(t *testing.T) {
	r := arrivedAt(t, 2)
	step, err := complete(r, stop2, &StopProof{Kind: "foto", Value: "bukti.jpg", Photo: drivertest.Photo()}, t0.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	proof := step.Facts[0].Data.(contracts.StopCompleted).Proof
	if proof == nil || proof.Kind != contracts.ProofKindPhoto || proof.Photo != drivertest.Photo() || proof.Analysis != nil {
		t.Errorf("proof = %+v", proof)
	}
}

func TestStopWithoutACodeAcceptsNoOTP(t *testing.T) {
	r := arrivedAt(t, 2)
	r.Stops[1].DeliveryCode = ""
	_, err := complete(r, stop2, &StopProof{Kind: "otp", Value: ""}, t0)
	wantRefusal(t, err, platform.CodeInvalid, "srv.api.otpMismatch", "proof")
}

func TestCompleteRefusals(t *testing.T) {
	cases := []struct {
		name  string
		route contracts.Route
		stop  string
		code  platform.ErrorCode
		key   string
	}{
		{"before arriving", started(t), stop1, platform.CodeConflict, "srv.api.markArrivedFirst"},
		{"route not started", assigned(), stop1, platform.CodeConflict, "srv.api.markArrivedFirst"},
		{"a later stop", arrivedAt(t, 1), stop3, platform.CodeConflict, "srv.api.markArrivedFirst"},
		{"no such stop", arrivedAt(t, 1), "nope", platform.CodeNotFound, "srv.ops.err.stopNotFound"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := complete(c.route, c.stop, &StopProof{Kind: "otp", Value: drivertest.CodeA}, t0)
			wantRefusal(t, err, c.code, c.key, "")
		})
	}
}

func TestCompletingAgainIsAReplayWhateverTheProof(t *testing.T) {
	r := arrivedAt(t, 3)
	step, err := complete(r, stop2, &StopProof{Kind: "otp", Value: "wrong"}, t0.Add(time.Hour))
	if err != nil || !step.Replay() || !reflect.DeepEqual(step.Route, r) || step.Position != nil || step.Finished {
		t.Errorf("step = %+v, err = %v; want the route back and no facts", step, err)
	}
}

func TestClosingTheLastStopFinishesTheRoute(t *testing.T) {
	r := arrivedAt(t, 3)
	at := t0.Add(30 * time.Minute)
	step, err := complete(r, stop3, &StopProof{Kind: "otp", Value: drivertest.CodeB}, at)
	if err != nil {
		t.Fatal(err)
	}
	if !step.Finished || step.Route.Status != contracts.RouteStatusDone || step.Route.FinishedAt.Time != at {
		t.Errorf("finished = %v, route %s at %v", step.Finished, step.Route.Status, step.Route.FinishedAt)
	}
	want := []string{contracts.TypeStopCompleted, contracts.TypeRouteCompleted}
	if got := factTypes(step); !reflect.DeepEqual(got, want) {
		t.Fatalf("facts = %v, want %v", got, want)
	}
	if done := step.Facts[1].Data.(contracts.RouteCompleted); done.DriverID != "drv-1" || done.FinishedAt.Time != at {
		t.Errorf("route.completed = %+v", done)
	}
}

func TestClosingAMiddleStopPublishesOneFact(t *testing.T) {
	step, err := complete(arrivedAt(t, 2), stop2, &StopProof{Kind: "otp", Value: drivertest.CodeA}, t0.Add(20*time.Minute))
	if err != nil || step.Finished || len(step.Facts) != 1 {
		t.Errorf("finished = %v, facts = %v, err = %v", step.Finished, factTypes(step), err)
	}
}

func TestReportIssueClosesTheActiveStopWithOneMessage(t *testing.T) {
	r := started(t)
	r2, err := reportIssue(r, stop1, contracts.IssueKindDamagedGoods, "  kardus penyok ", t0.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if st := r2.Route.Stops[0]; st.Status != contracts.StopStatusProblem || st.Issue == nil {
		t.Fatalf("stop = %s issue %v", st.Status, st.Issue)
	}
	fact := r2.Facts[0].Data.(contracts.StopIssue)
	wantMsg := contracts.Msg{Key: "srv.ops.issue.withNote", Params: map[string]any{"issue": contracts.Msg{Key: "dom.issue.barang_rusak"}, "note": "kardus penyok"}}
	if !reflect.DeepEqual(fact.Issue, wantMsg) || !reflect.DeepEqual(*r2.Route.Stops[0].Issue, wantMsg) {
		t.Errorf("event issue %+v and stored issue %+v must both be %+v", fact.Issue, *r2.Route.Stops[0].Issue, wantMsg)
	}
	if fact.Kind != contracts.IssueKindDamagedGoods || fact.Note != "kardus penyok" {
		t.Errorf("stop.issue = %+v", fact)
	}
	if !reflect.DeepEqual(factTypes(r2), []string{contracts.TypeStopIssue}) || r2.Route.Stops[1].Status != contracts.StopStatusEnRoute {
		t.Errorf("facts = %v, next stop = %s", factTypes(r2), r2.Route.Stops[1].Status)
	}
}

func TestReportIssueWithoutANoteUsesTheKindsLabel(t *testing.T) {
	step, err := reportIssue(started(t), stop1, contracts.IssueKindNobodyThere, "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := step.Facts[0].Data.(contracts.StopIssue).Issue; !reflect.DeepEqual(got, contracts.Msg{Key: "dom.issue.tidak_ada_orang"}) {
		t.Errorf("issue = %+v", got)
	}
}

func TestReportIssueValidatesTheReport(t *testing.T) {
	r := started(t)
	cases := []struct {
		name  string
		kind  contracts.IssueKind
		note  string
		key   string
		field string
	}{
		{"other needs a note", contracts.IssueKindOther, "  ", "srv.api.issueNoteRequired", "note"},
		{"unknown kind", "kabur", "", platform.MsgBadRequest, "issue"},
		{"note too long", contracts.IssueKindWrongAddress, strings.Repeat("x", maxNoteRunes+1), platform.MsgBadRequest, "note"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := reportIssue(r, stop1, c.kind, c.note, t0)
			wantRefusal(t, err, platform.CodeInvalid, c.key, c.field)
		})
	}
	if _, err := reportIssue(r, stop1, contracts.IssueKindOther, strings.Repeat("é", maxNoteRunes), t0); err != nil {
		t.Errorf("the limit counts characters, not bytes: %v", err)
	}
}

func TestReportIssueRefusals(t *testing.T) {
	cases := []struct {
		name  string
		route contracts.Route
		stop  string
		code  platform.ErrorCode
		key   string
	}{
		{"route not started", assigned(), stop1, platform.CodeConflict, "srv.api.issueOnlyActive"},
		{"not the active stop", started(t), stop2, platform.CodeConflict, "srv.api.issueOnlyActive"},
		{"no such stop", started(t), "nope", platform.CodeNotFound, "srv.ops.err.stopNotFound"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := reportIssue(c.route, c.stop, contracts.IssueKindNobodyThere, "", t0)
			wantRefusal(t, err, c.code, c.key, "")
		})
	}
}

func TestReportingAgainIsAReplay(t *testing.T) {
	first, err := reportIssue(started(t), stop1, contracts.IssueKindNobodyThere, "", t0)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reportIssue(first.Route, stop1, contracts.IssueKindNobodyThere, "", t0.Add(time.Minute))
	if err != nil || !again.Replay() || !reflect.DeepEqual(again.Route, first.Route) || again.Position != nil {
		t.Errorf("step = %+v, err = %v", again, err)
	}
}

func TestAProblemOnTheLastStopFinishesTheRoute(t *testing.T) {
	step, err := reportIssue(arrivedAt(t, 3), stop3, contracts.IssueKindRecipientRefused, "", t0.Add(40*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !step.Finished || !reflect.DeepEqual(factTypes(step), []string{contracts.TypeStopIssue, contracts.TypeRouteCompleted}) {
		t.Errorf("finished = %v, facts = %v", step.Finished, factTypes(step))
	}
}

// Every transition error of routeplan has an answer with the mock backend's key; none ends up as a 500.
func TestRefuseMapsEveryTransitionError(t *testing.T) {
	cases := []struct {
		err  error
		code platform.ErrorCode
		key  string
	}{
		{routeplan.ErrStopNotFound, platform.CodeNotFound, "srv.ops.err.stopNotFound"},
		{routeplan.ErrNotAssigned, platform.CodeConflict, "srv.api.routeCannotStart"},
		{routeplan.ErrNotRunning, platform.CodeConflict, "when-not-running"},
		{routeplan.ErrNotActiveStop, platform.CodeConflict, "when-not-active"},
		{routeplan.ErrNotEnRoute, platform.CodeConflict, "srv.api.stopAlreadyArrived"},
		{routeplan.ErrNotArrived, platform.CodeConflict, "srv.api.markArrivedFirst"},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			wantRefusal(t, refuse(c.err, "when-not-running", "when-not-active"), c.code, c.key, "")
		})
	}
	unknown := errors.New("disk on fire")
	if got := refuse(unknown, "", ""); got != unknown {
		t.Errorf("an unknown error must pass through to become a 500, got %v", got)
	}
}

func TestProofPhotoRules(t *testing.T) {
	body := strings.Repeat("A", 100)
	cases := []struct {
		name  string
		photo string
		ok    bool
	}{
		{"jpeg", "data:image/jpeg;base64," + body, true},
		{"png with padding", "data:image/png;base64," + body + "==", true},
		{"webp", "data:image/webp;base64," + body, true},
		{"alphabet with plus and slash", "data:image/jpeg;base64,ab+/Z9", true},
		{"empty", "", false},
		{"gif", "data:image/gif;base64," + body, false},
		{"not base64", "data:image/jpeg;base64,not base64!", false},
		{"no body", "data:image/jpeg;base64,", false},
		{"only padding", "data:image/jpeg;base64,==", false},
		{"padding in the middle", "data:image/jpeg;base64,ab=cd", false},
		{"plain url", "https://example.com/bukti.jpg", false},
		{"at the size limit", "data:image/jpeg;base64," + strings.Repeat("A", maxPhotoChars-len("data:image/jpeg;base64,")), true},
		{"over the size limit", "data:image/jpeg;base64," + strings.Repeat("A", maxPhotoChars), false},
	}
	for _, c := range cases {
		if got := isProofPhoto(c.photo); got != c.ok {
			t.Errorf("%s: isProofPhoto = %v, want %v", c.name, got, c.ok)
		}
	}
}
