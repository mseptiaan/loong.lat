package proofs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/ai"
)

const pixels = "\xff\xd8\xff\xe0 PIXELS OF THE DELIVERY"

func jpeg() string { return dataURL("image/jpeg", pixels) }

func TestEnqueueAddsOneJobPerStopWhateverHappensToIt(t *testing.T) {
	r := newRig(t)
	tg := r.plan(photoProof(jpeg()))[0]
	for range 3 {
		r.enqueue(tg)
	}
	if n := countJobs(t, r); n != 1 {
		t.Fatalf("%d jobs after enqueuing the same stop three times", n)
	}
	if j := r.job(tg); j.state != "pending" || j.attempts != 0 || j.dueIn > 0 {
		t.Errorf("a new job = %+v: want pending, untried and due", j)
	}

	// A job that is over is not brought back by a redelivered event.
	r.step(r.worker)
	r.enqueue(tg)
	if j := r.job(tg); j.state != "done" || j.attempts != 1 {
		t.Errorf("after enqueuing a done job again: %+v", j)
	}
	if got := r.model.calls(); got != 1 {
		t.Errorf("%d analyses", got)
	}
}

func countJobs(t *testing.T, r *rig) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM proof_jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestADueJobIsAnalysedAndTheAnalysisLandsOnTheRightStopOnly(t *testing.T) {
	r := newRig(t)
	// Two drop-offs with photos in one plan: only the one with a job is read.
	targets := r.plan(photoProof(dataURL("image/png", "first photo")), photoProof(jpeg()))
	first, second := targets[0], targets[1]
	before := r.record(second)
	announced := listenForChanges(t, r.customer)
	r.enqueue(second)

	r.step(r.worker)

	// The model was asked about the right photo and the right items.
	if r.model.calls() != 1 {
		t.Fatalf("%d analyses", r.model.calls())
	}
	in := r.model.inputs[0]
	if in.MIME != "image/jpeg" || !bytes.Equal(in.Data, []byte(pixels)) {
		t.Errorf("the model was shown %s of %d bytes", in.MIME, len(in.Data))
	}
	if want := []string{"2 x Box", "1 x Lamp"}; fmt.Sprint(in.Parcels) != fmt.Sprint(want) {
		t.Errorf("parcels = %q, want %q", in.Parcels, want)
	}

	// The analysis is on that stop, whole, and nowhere else.
	after := r.record(second)
	got := locate(&after.Plan, second.route, second.stop).Proof
	if got == nil || got.Analysis == nil || mustJSON(t, got.Analysis) != mustJSON(t, stubAnalysis) {
		t.Fatalf("stored proof = %+v", got)
	}
	if got.Kind != contracts.ProofKindPhoto || got.Photo != jpeg() || !got.At.Equal(t0) {
		t.Errorf("the rest of the proof changed: %+v", got)
	}
	if other := r.stop(first); other.Proof.Analysis != nil {
		t.Errorf("the other stop got an analysis: %+v", other.Proof.Analysis)
	}

	// Nothing else about the plan changed: not its version, not its route versions, not its status, timeline or
	// updatedAt (the shipper decided nothing and the driver did nothing).
	if after.Version != before.Version || after.Version != 7 || mustJSON(t, after.RouteVersions) != mustJSON(t, before.RouteVersions) || after.RouteVersions[second.route].Exec != 5 {
		t.Errorf("versions: plan %d -> %d, routes %v -> %v", before.Version, after.Version, before.RouteVersions, after.RouteVersions)
	}
	locate(&after.Plan, second.route, second.stop).Proof.Analysis = nil
	if mustJSON(t, after.Plan) != mustJSON(t, before.Plan) {
		t.Errorf("the plan changed beyond the analysis:\n before %s\n after  %s", mustJSON(t, before.Plan), mustJSON(t, after.Plan))
	}

	// The shipper's dashboards are told to fetch the plan again.
	if got := announced(); !strings.Contains(got, `"plans.changed"`) || !strings.Contains(got, `"planId":"`+second.plan+`"`) || !strings.Contains(got, r.customer) {
		t.Errorf("announcement = %s", got)
	}
	if j := r.job(second); j.state != "done" || j.attempts != 1 || j.lastError != "" {
		t.Errorf("job = %+v", j)
	}
	// The log says which job, how big the photo was and how it ended, and nothing of the photo.
	logs := r.logs.String()
	for _, want := range []string{`"planId":"` + second.plan + `"`, `"stopId":"` + second.stop + `"`, `"result":"done"`, `"photoBytes":` + fmt.Sprint(len(pixels)), `"latencyMs"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log lacks %s: %s", want, logs)
		}
	}
	if strings.Contains(logs, "PIXELS") || strings.Contains(logs, "base64") {
		t.Errorf("the log holds the photo: %s", logs)
	}
	r.idle(r.worker)
}

func TestNothingDueMeansNothingHappens(t *testing.T) {
	r := newRig(t)
	r.idle(r.worker)

	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	if _, err := r.pool.Exec(t.Context(), `UPDATE proof_jobs SET next_at = now() + interval '5 minutes'`); err != nil {
		t.Fatal(err)
	}
	r.idle(r.worker)
	if r.model.calls() != 0 || r.job(tg).attempts != 0 || r.stop(tg).Proof.Analysis != nil {
		t.Errorf("a job that is not due was worked: %+v after %d analyses", r.job(tg), r.model.calls())
	}

	r.makeDue(tg)
	r.step(r.worker)
	if r.stop(tg).Proof.Analysis == nil {
		t.Error("the job was not worked once due")
	}
}

func TestAJobIsClaimedWithALeaseThatBringsItBackOnlyWhenItRunsOut(t *testing.T) {
	r := newRig(t)
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)

	// A worker claims the job and dies: it is counted, and it is not due for as long as the lease.
	j, ok, err := r.worker.claim(t.Context())
	if err != nil || !ok || j.attempts != 1 || j.stopID != tg.stop || j.routeID != tg.route {
		t.Fatalf("claim = %+v, %v, %v", j, ok, err)
	}
	if row := r.job(tg); row.state != "pending" || row.attempts != 1 || row.dueIn < (DefaultLease-10*time.Second).Seconds() || row.dueIn > DefaultLease.Seconds() {
		t.Fatalf("a claimed job = %+v, want pending, one attempt, due in about %v", row, DefaultLease)
	}
	r.idle(r.worker)
	r.idle(r.newWorker())

	// The lease runs out: another worker takes it over and finishes it.
	r.makeDue(tg)
	r.step(r.newWorker())
	if row := r.job(tg); row.state != "done" || row.attempts != 2 {
		t.Errorf("after the takeover: %+v", row)
	}
	if r.stop(tg).Proof.Analysis == nil {
		t.Error("the taken-over job stored nothing")
	}
}

func TestTheLeaseFollowsTheOptions(t *testing.T) {
	r := newRig(t)
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	w := New(Options{Pool: r.pool, Analyzer: r.model, Flags: r.worker.flags, Lease: 7 * time.Minute})
	if _, ok, err := w.claim(t.Context()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if row := r.job(tg); row.dueIn < 6*60+50 || row.dueIn > 7*60 {
		t.Errorf("due in %.0fs, want about 7 minutes", row.dueIn)
	}
	if d := New(Options{}); d.poll != DefaultPoll || d.lease != DefaultLease || DefaultPoll != 2*time.Second || DefaultLease != 2*time.Minute {
		t.Errorf("defaults: poll %v, lease %v", d.poll, d.lease)
	}
}

func TestTwoWorkersNeverAnalyseOneJobTwice(t *testing.T) {
	r := newRig(t)
	r.model.setFn(func(ctx context.Context, in ai.ProofInput) (contracts.ProofAnalysis, error) {
		time.Sleep(30 * time.Millisecond) // long enough for the other worker to look at the same job
		return stubAnalysis, nil
	})
	var proofs []*contracts.DeliveryProof
	for i := range 6 {
		proofs = append(proofs, photoProof(dataURL("image/jpeg", fmt.Sprintf("photo %d", i))))
	}
	targets := r.plan(proofs...)
	for _, tg := range targets {
		r.enqueue(tg)
	}

	var wg sync.WaitGroup
	for range 4 {
		w := r.newWorker()
		wg.Go(func() {
			for {
				worked, err := w.Step(context.WithoutCancel(t.Context()))
				if err != nil {
					t.Error(err)
					return
				}
				if !worked {
					return
				}
			}
		})
	}
	wg.Wait()

	if got := r.model.calls(); got != len(targets) {
		t.Errorf("%d analyses for %d jobs", got, len(targets))
	}
	seen := map[string]int{}
	for _, in := range r.model.inputs {
		seen[string(in.Data)]++
	}
	for photo, n := range seen {
		if n != 1 {
			t.Errorf("%q was analysed %d times", photo, n)
		}
	}
	for _, tg := range targets {
		if j := r.job(tg); j.state != "done" || j.attempts != 1 {
			t.Errorf("%s: %+v", tg.stop, j)
		}
		if r.stop(tg).Proof.Analysis == nil {
			t.Errorf("%s has no analysis", tg.stop)
		}
	}
}

func TestOneJobClaimedByManyWorkersAtOnceIsWorkedOnce(t *testing.T) {
	r := newRig(t)
	r.model.setFn(func(ctx context.Context, in ai.ProofInput) (contracts.ProofAnalysis, error) {
		time.Sleep(50 * time.Millisecond)
		return stubAnalysis, nil
	})
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	var wg sync.WaitGroup
	start := make(chan struct{})
	workers := make([]*Worker, 8)
	for i := range workers {
		workers[i] = r.newWorker()
		wg.Go(func() {
			<-start
			if _, err := workers[i].Step(context.WithoutCancel(t.Context())); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	if got := r.model.calls(); got != 1 {
		t.Errorf("%d analyses of one photo", got)
	}
}

func TestWhileTheVisionSwitchIsOffPhotosAreSkippedForGood(t *testing.T) {
	r := newRig(t)
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	r.vision.Store(false)

	r.step(r.worker)
	if j := r.job(tg); j.state != "skipped" || j.attempts != 1 || !strings.Contains(j.lastError, "switched off") {
		t.Errorf("job = %+v", j)
	}
	if r.model.calls() != 0 || r.stop(tg).Proof.Analysis != nil {
		t.Errorf("the switch is off, yet %d analyses and %+v stored", r.model.calls(), r.stop(tg).Proof.Analysis)
	}

	// Off means off: turning it on again does not go back for the photos taken meanwhile.
	r.vision.Store(true)
	r.idle(r.worker)
	if r.model.calls() != 0 {
		t.Errorf("an old photo was analysed after the switch came on")
	}
}

func TestAJobWhoseSettingsCannotBeReadIsTriedAgain(t *testing.T) {
	r := newRig(t)
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	w := New(Options{Pool: r.pool, Analyzer: r.model, Flags: func(context.Context) (contracts.AIFeatures, error) {
		return contracts.AIFeatures{}, errors.New("settings are down")
	}})
	r.step(w)
	if j := r.job(tg); j.state != "pending" || j.dueIn < 25 || !strings.Contains(j.lastError, "settings are down") {
		t.Errorf("job = %+v", j)
	}
	if r.model.calls() != 0 {
		t.Error("the model was called without knowing whether it may be")
	}
}

func TestStopsThatNeedNoAnalysisAreSkipped(t *testing.T) {
	analysed := photoProof(jpeg())
	analysed.Analysis = &contracts.ProofAnalysis{Summary: contracts.Msg{Key: "kept"}, Model: "earlier"}
	otp := &contracts.DeliveryProof{Kind: contracts.ProofKindOTP, At: contracts.At(t0)}
	noPhoto := &contracts.DeliveryProof{Kind: contracts.ProofKindPhoto, At: contracts.At(t0)}
	for name, tc := range map[string]struct {
		proof  *contracts.DeliveryProof
		reason string
	}{
		"an OTP proof":         {otp, "no photo proof"},
		"no proof":             {nil, "no photo proof"},
		"a photo kind without": {noPhoto, "no photo proof"},
		"already analysed":     {analysed, "analysed already"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			tg := r.plan(tc.proof)[0]
			r.enqueue(tg)
			r.step(r.worker)
			if j := r.job(tg); j.state != "skipped" || !strings.Contains(j.lastError, tc.reason) {
				t.Errorf("job = %+v, want skipped (%s)", j, tc.reason)
			}
			if r.model.calls() != 0 {
				t.Error("the model was called")
			}
			if tc.proof != nil && tc.proof.Analysis != nil {
				if got := r.stop(tg).Proof.Analysis; got == nil || got.Model != "earlier" {
					t.Errorf("the earlier analysis became %+v", got)
				}
			}
		})
	}

	t.Run("a stop that is gone", func(t *testing.T) {
		r := newRig(t)
		tg := r.plan(photoProof(jpeg()))[0]
		ghost := target{tg.plan, tg.route, tg.route + ":s9"}
		r.enqueue(ghost)
		r.step(r.worker)
		if j := r.job(ghost); j.state != "skipped" || !strings.Contains(j.lastError, "stop is gone") {
			t.Errorf("job = %+v", j)
		}
	})

	t.Run("a route the stop is not in", func(t *testing.T) {
		r := newRig(t)
		tg := r.plan(photoProof(jpeg()))[0]
		wrong := target{tg.plan, "elsewhere:r1", tg.stop}
		r.enqueue(wrong)
		r.step(r.worker)
		if j := r.job(wrong); j.state != "skipped" {
			t.Errorf("job = %+v", j)
		}
	})

	t.Run("a plan that is gone", func(t *testing.T) {
		r := newRig(t)
		tg := r.plan(photoProof(jpeg()))[0]
		r.enqueue(tg)
		for _, q := range []string{`ALTER TABLE proof_jobs DROP CONSTRAINT proof_jobs_plan_id_fkey`, `DELETE FROM plans`} {
			if _, err := r.pool.Exec(t.Context(), q); err != nil {
				t.Fatal(err)
			}
		}
		r.step(r.worker)
		if j := r.job(tg); j.state != "skipped" || !strings.Contains(j.lastError, "plan is gone") {
			t.Errorf("job = %+v", j)
		}
	})
}

func TestAPhotoThatCannotBeAnalysedFailsAtOnceWithoutCallingTheModel(t *testing.T) {
	huge := "data:image/jpeg;base64," + strings.Repeat("QUJD", (ai.MaxImageBytes+3)/3+4)
	for name, photo := range map[string]string{
		"a gif":                 dataURL("image/gif", "GIF89a"),
		"a document":            dataURL("application/pdf", "%PDF"),
		"not a data URL":        "https://example.test/photo.jpg",
		"not base64":            "data:image/jpeg;utf8,hello",
		"broken base64":         "data:image/jpeg;base64,@@@@",
		"no data":               "data:image/jpeg;base64,",
		"no comma":              "data:image/jpeg;base64",
		"over the gateway size": huge,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			tg := r.plan(photoProof(photo))[0]
			r.enqueue(tg)
			r.step(r.worker)
			j := r.job(tg)
			if j.state != "failed" || j.attempts != 1 || j.lastError == "" {
				t.Errorf("job = %+v, want failed at once with a reason", j)
			}
			if len(j.lastError) > maxErrorChars || strings.Contains(j.lastError, "QUJD") {
				t.Errorf("last_error holds the photo: %.100s", j.lastError)
			}
			if r.model.calls() != 0 {
				t.Errorf("the model was called %d times", r.model.calls())
			}
			r.idle(r.worker)
		})
	}
}

func TestThePhotoAtTheLimitIsAnalysed(t *testing.T) {
	r := newRig(t)
	photo := dataURL("image/png", strings.Repeat("x", ai.MaxImageBytes))
	tg := r.plan(photoProof(photo))[0]
	r.enqueue(tg)
	r.step(r.worker)
	if j := r.job(tg); j.state != "done" {
		t.Errorf("job = %+v", j)
	}
	if got := len(r.model.inputs[0].Data); got != ai.MaxImageBytes {
		t.Errorf("the model got %d bytes", got)
	}
}

func TestTheModelsFinalRefusalsFailTheJobAtOnce(t *testing.T) {
	for name, refusal := range map[string]error{
		"the gateway refuses the input":  &aiapi.Error{Kind: aiapi.KindInvalid},
		"the analyzer refuses the image": fmt.Errorf("wrapped: %w", ai.ErrProofImage),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.model.setFn(func(context.Context, ai.ProofInput) (contracts.ProofAnalysis, error) {
				return contracts.ProofAnalysis{}, refusal
			})
			tg := r.plan(photoProof(jpeg()))[0]
			r.enqueue(tg)
			r.step(r.worker)
			if j := r.job(tg); j.state != "failed" || j.attempts != 1 || j.lastError == "" {
				t.Errorf("job = %+v", j)
			}
			if r.model.calls() != 1 || r.stop(tg).Proof.Analysis != nil {
				t.Errorf("%d analyses, stored %+v", r.model.calls(), r.stop(tg).Proof.Analysis)
			}
			r.idle(r.worker)
		})
	}
}

// within checks that a delay is the wanted one, a little elapsed.
func within(t *testing.T, what string, got, want float64) {
	t.Helper()
	if got > want || got < want-10 {
		t.Errorf("%s is due in %.1fs, want about %.0fs", what, got, want)
	}
}

func TestATransientFailureIsRetriedLaterWithGrowingDelaysAndGivenUpAfterTheFourthTry(t *testing.T) {
	r := newRig(t)
	r.model.setFn(func(context.Context, ai.ProofInput) (contracts.ProofAnalysis, error) {
		return contracts.ProofAnalysis{}, &aiapi.Error{Kind: aiapi.KindUnavailable, Status: 503}
	})
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)

	for attempt, delay := range []float64{30, 120, 600} {
		r.step(r.worker)
		j := r.job(tg)
		if j.state != "pending" || j.attempts != attempt+1 || !strings.Contains(j.lastError, "unavailable") {
			t.Fatalf("after try %d: %+v", attempt+1, j)
		}
		within(t, fmt.Sprintf("after try %d the job", attempt+1), j.dueIn, delay)
		// Not due until then: nothing happens, however often the worker looks.
		r.idle(r.worker)
		r.idle(r.newWorker())
		r.makeDue(tg)
	}

	r.step(r.worker)
	j := r.job(tg)
	if j.state != "failed" || j.attempts != 4 || r.model.calls() != 4 || !strings.Contains(j.lastError, "unavailable") {
		t.Fatalf("after the fourth try: %+v, %d analyses", j, r.model.calls())
	}
	r.idle(r.worker)
	if r.stop(tg).Proof.Analysis != nil {
		t.Error("an analysis was stored without an answer")
	}
}

func TestEveryKindOfTransientFailureIsRetried(t *testing.T) {
	for name, failure := range map[string]error{
		"timeout":                     &aiapi.Error{Kind: aiapi.KindTimeout},
		"rate limited":                &aiapi.Error{Kind: aiapi.KindRateLimited, RetryAfter: 5 * time.Second},
		"no gateway":                  ai.ErrNoGateway,
		"a reply that is no analysis": fmt.Errorf("%w: it is not the requested document", ai.ErrProofReply),
		"a plain error":               errors.New("connection reset"),
		"the call's own deadline":     context.DeadlineExceeded,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.model.setFn(func(context.Context, ai.ProofInput) (contracts.ProofAnalysis, error) {
				return contracts.ProofAnalysis{}, failure
			})
			tg := r.plan(photoProof(jpeg()))[0]
			r.enqueue(tg)
			r.step(r.worker)
			if j := r.job(tg); j.state != "pending" || j.attempts != 1 || j.dueIn < 20 {
				t.Errorf("job = %+v, want pending and due later", j)
			}
		})
	}
}

func TestAJobThatRecoversStoresItsAnalysis(t *testing.T) {
	r := newRig(t)
	var calls int
	r.model.setFn(func(context.Context, ai.ProofInput) (contracts.ProofAnalysis, error) {
		calls++
		if calls == 1 {
			return contracts.ProofAnalysis{}, &aiapi.Error{Kind: aiapi.KindUnavailable}
		}
		return stubAnalysis, nil
	})
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	r.step(r.worker)
	r.makeDue(tg)
	r.step(r.worker)
	if j := r.job(tg); j.state != "done" || j.attempts != 2 {
		t.Errorf("job = %+v", j)
	}
	if r.stop(tg).Proof.Analysis == nil {
		t.Error("nothing stored")
	}
}

func TestAJobsErrorTextNeverHoldsThePhoto(t *testing.T) {
	r := newRig(t)
	photo := jpeg()
	r.model.setFn(func(_ context.Context, in ai.ProofInput) (contracts.ProofAnalysis, error) {
		// A badly behaved error that quotes the photo, and a NUL that Postgres text would refuse.
		return contracts.ProofAnalysis{}, fmt.Errorf("gateway said: bad input %s\x00\n%s", photo, strings.Repeat("y", 1000))
	})
	tg := r.plan(photoProof(photo))[0]
	r.enqueue(tg)
	r.step(r.worker)
	j := r.job(tg)
	if j.state != "pending" || !strings.HasPrefix(j.lastError, "gateway said: bad input") {
		t.Fatalf("job = %+v", j)
	}
	if strings.Contains(j.lastError, "base64") || strings.Contains(j.lastError, photo[:40]) || len(j.lastError) > maxErrorChars {
		t.Errorf("last_error = %q", j.lastError)
	}
	if logs := r.logs.String(); strings.Contains(logs, "base64") || strings.Contains(logs, "PIXELS") || strings.Contains(logs, "/9j") {
		t.Errorf("the log holds the photo: %.300s", logs)
	}
}

func TestAnalysisOfAPhotoThatWasReplacedMeanwhileIsNotStored(t *testing.T) {
	r := newRig(t)
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	replacement := dataURL("image/jpeg", "a newer photo")
	r.model.setFn(func(context.Context, ai.ProofInput) (contracts.ProofAnalysis, error) {
		r.rewrite(tg, func(s *contracts.RouteStop) { s.Proof.Photo = replacement })
		return stubAnalysis, nil
	})
	r.step(r.worker)

	stop := r.stop(tg)
	if stop.Proof.Analysis != nil || stop.Proof.Photo != replacement {
		t.Errorf("proof = %+v: the analysis of the old photo was stored on the new one", stop.Proof)
	}
	if j := r.job(tg); j.state != "skipped" || !strings.Contains(j.lastError, "changed") {
		t.Errorf("job = %+v", j)
	}
}

func TestAnalysisThatArrivedFirstFromAnotherWorkerIsKept(t *testing.T) {
	r := newRig(t)
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	earlier := contracts.ProofAnalysis{Summary: contracts.Msg{Key: "first"}, Model: "first-worker"}
	r.model.setFn(func(context.Context, ai.ProofInput) (contracts.ProofAnalysis, error) {
		r.rewrite(tg, func(s *contracts.RouteStop) { s.Proof.Analysis = &earlier })
		return stubAnalysis, nil
	})
	r.step(r.worker)
	if got := r.stop(tg).Proof.Analysis; got == nil || got.Model != "first-worker" {
		t.Errorf("analysis = %+v, want the first one kept", got)
	}
	if j := r.job(tg); j.state != "skipped" {
		t.Errorf("job = %+v", j)
	}
}

func TestAWorkerWhoseLeaseWasTakenOverCannotChangeTheNewOwnersJob(t *testing.T) {
	for name, late := range map[string]error{
		"a late failure worth a retry": errors.New("late and slow"),
		"a late final refusal":         &aiapi.Error{Kind: aiapi.KindInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			tg := r.plan(photoProof(jpeg()))[0]
			r.enqueue(tg)
			r.model.setFn(func(context.Context, ai.ProofInput) (contracts.ProofAnalysis, error) {
				// While this worker is busy its lease runs out and a second worker claims the job, and is still on it.
				r.makeDue(tg)
				if _, ok, err := r.newWorker().claim(t.Context()); err != nil || !ok {
					t.Errorf("the takeover failed: %v, %v", ok, err)
				}
				return contracts.ProofAnalysis{}, late
			})
			r.step(r.worker)
			if j := r.job(tg); j.state != "pending" || j.attempts != 2 || j.lastError != "" || j.dueIn < 100 {
				t.Errorf("job = %+v: the late worker's word must not count (pending, second attempt, still leased)", j)
			}
		})
	}
}

func TestJobsThatAreOverAreNeverClaimedAgainEvenWhenTheirTimeComes(t *testing.T) {
	r := newRig(t)
	targets := r.plan(
		photoProof(jpeg()),                         // done
		photoProof(dataURL("image/gif", "GIF89a")), // failed: not a photo the AI reads
		&contracts.DeliveryProof{Kind: contracts.ProofKindOTP, At: contracts.At(t0)}, // skipped
	)
	for _, tg := range targets {
		r.enqueue(tg)
		r.step(r.worker)
	}
	for i, want := range []string{"done", "failed", "skipped"} {
		if got := r.job(targets[i]).state; got != want {
			t.Fatalf("job %d is %s, want %s", i, got, want)
		}
	}
	if _, err := r.pool.Exec(t.Context(), `UPDATE proof_jobs SET next_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	r.idle(r.worker)
	if r.model.calls() != 1 || r.job(targets[0]).attempts != 1 {
		t.Errorf("a job that was over was worked again: %d analyses", r.model.calls())
	}
}

func TestAJobAnotherTransactionHoldsIsSkippedNotWaitedFor(t *testing.T) {
	r := newRig(t)
	targets := r.plan(photoProof(jpeg()), photoProof(dataURL("image/png", "second")))
	r.enqueue(targets[0]) // the more overdue of the two
	r.enqueue(targets[1])

	tx, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(t.Context()))
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM proof_jobs WHERE stop_id = $1 FOR UPDATE`, targets[0].stop); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if worked, err := r.worker.Step(ctx); err != nil || !worked {
		t.Fatalf("Step = %v, %v: a worker must go on to the next job instead of waiting for a locked one", worked, err)
	}
	if first, second := r.job(targets[0]), r.job(targets[1]); first.attempts != 0 || second.state != "done" {
		t.Errorf("the locked job %+v, the other %+v", first, second)
	}
}

func TestAWorkerThatIsStoppedGivesTheJobBackWithoutAStrike(t *testing.T) {
	r := newRig(t)
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.model.setFn(func(ctx context.Context, _ ai.ProofInput) (contracts.ProofAnalysis, error) {
		cancel() // the service is shutting down while the model is thinking
		<-ctx.Done()
		return contracts.ProofAnalysis{}, ctx.Err()
	})
	if worked, err := r.worker.Step(ctx); !worked || err != nil {
		t.Fatalf("Step = %v, %v", worked, err)
	}
	if j := r.job(tg); j.state != "pending" || j.attempts != 0 || j.dueIn > 0 || j.lastError != "" {
		t.Errorf("job = %+v: want it pending, untried and due again at once", j)
	}

	// After the restart the job is worked normally.
	r.model.setFn(nil)
	r.step(r.newWorker())
	if j := r.job(tg); j.state != "done" || j.attempts != 1 {
		t.Errorf("job = %+v", j)
	}
}

func TestTheModelCallIsCutOffBeforeTheLeaseEnds(t *testing.T) {
	r := newRig(t)
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	var left time.Duration
	r.model.setFn(func(ctx context.Context, _ ai.ProofInput) (contracts.ProofAnalysis, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("the call has no deadline")
		}
		left = time.Until(deadline)
		return stubAnalysis, nil
	})
	r.step(r.worker)
	if left > DefaultLease*3/4 || left < DefaultLease/2 {
		t.Errorf("the call had %v, want under the %v lease", left, DefaultLease)
	}
}

func TestRunWorksJobsAsTheyArriveAndStopsWhenTheContextIsDone(t *testing.T) {
	r := newRig(t)
	w := New(Options{Pool: r.pool, Analyzer: r.model, Flags: r.worker.flags, Poll: 10 * time.Millisecond, Log: r.worker.log})
	targets := r.plan(photoProof(jpeg()), photoProof(dataURL("image/png", "second")))
	r.enqueue(targets[0])

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- w.Run(ctx) }()

	waitFor(t, "the first job", func() bool { return r.job(targets[0]).state == "done" })
	// A job that comes later is found by the next look.
	r.enqueue(targets[1])
	waitFor(t, "the second job", func() bool { return r.job(targets[1]).state == "done" })

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when its context was done")
	}
	if r.model.calls() != 2 {
		t.Errorf("%d analyses", r.model.calls())
	}
}

func TestRunKeepsGoingWhenTheDatabaseFailsAndNeverReturnsAnError(t *testing.T) {
	r := newRig(t)
	w := New(Options{Pool: r.pool, Analyzer: r.model, Flags: r.worker.flags, Poll: 10 * time.Millisecond, Log: r.worker.log})
	if _, err := r.pool.Exec(t.Context(), `ALTER TABLE proof_jobs RENAME TO proof_jobs_away`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- w.Run(ctx) }()
	waitFor(t, "the failure to be logged", func() bool { return strings.Contains(r.logs.String(), "could not claim") })
	// The table is back: the worker is still there.
	if _, err := r.pool.Exec(t.Context(), `ALTER TABLE proof_jobs_away RENAME TO proof_jobs`); err != nil {
		t.Fatal(err)
	}
	tg := r.plan(photoProof(jpeg()))[0]
	r.enqueue(tg)
	waitFor(t, "the job after the outage", func() bool { return r.job(tg).state == "done" })
	cancel()
	if err := <-stopped; err != nil {
		t.Errorf("Run returned %v", err)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
