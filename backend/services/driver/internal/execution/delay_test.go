package execution_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/execution"
)

// The sweep for route.delayed. The clock is the fixture's, moved by hand; the sweeper is called directly.

func (f fixture) sweeper() *execution.Sweeper {
	return execution.NewSweeper(f.pool, drivertest.Publisher(f.pool), f.clock.Now, drivertest.Logger(), time.Second)
}

// sweep runs one sweep at the given offset from the ETA of the first stop and returns what it published.
func (f fixture) sweepAt(s *execution.Sweeper, eta time.Time, late time.Duration) int {
	f.t.Helper()
	f.clock.Set(eta.Add(late))
	n, err := s.Sweep(f.t.Context())
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// delays are the route.delayed events published so far, with their versions.
type delayEvent struct {
	contracts.RouteDelayed
	Key     string
	Version int64
}

func (f fixture) delays() []delayEvent {
	f.t.Helper()
	var out []delayEvent
	for _, e := range f.outbox() {
		if e.Type == contracts.TypeRouteDelayed {
			out = append(out, delayEvent{drivertest.Payload[contracts.RouteDelayed](f.t, e), e.Key, e.Version})
		}
	}
	return out
}

// started has the route running and returns the ETA of its first stop.
func (f fixture) started() time.Time {
	f.t.Helper()
	f.start()
	eta := f.task().Route.Stops[0].ArriveAt.Time
	if eta.IsZero() {
		f.t.Fatal("the first stop has no ETA")
	}
	return eta
}

func TestARouteIsReportedLateWhenItsDelayReachesFiveMinutesAndAtEachFurtherStep(t *testing.T) {
	f := newFixture(t)
	s := f.sweeper()
	eta := f.started() // execution event 1

	for _, early := range []time.Duration{-10 * time.Minute, -time.Second, 0, time.Minute, 4*time.Minute + 59*time.Second} {
		if n := f.sweepAt(s, eta, early); n != 0 {
			t.Fatalf("%v after the ETA: published %d, want nothing below 5 minutes", early, n)
		}
	}
	if got := f.delays(); len(got) != 0 {
		t.Fatalf("delays = %+v", got)
	}

	if n := f.sweepAt(s, eta, 5*time.Minute); n != 1 {
		t.Fatalf("at 5 minutes: published %d, want 1", n)
	}
	want := delayEvent{contracts.RouteDelayed{PlanID: plan, RouteID: route, DelayMin: 5}, route, 2}
	if got := f.delays(); len(got) != 1 || got[0] != want {
		t.Fatalf("delays = %+v, want %+v (keyed by route, the next execution version)", got, want)
	}

	// the same step is never published twice, however often the sweep looks
	for _, late := range []time.Duration{5 * time.Minute, 6 * time.Minute, 9*time.Minute + 59*time.Second} {
		if n := f.sweepAt(s, eta, late); n != 0 {
			t.Fatalf("%v late: published %d, want nothing within the step already reported", late, n)
		}
	}
	if n := f.sweepAt(s, eta, 10*time.Minute); n != 1 {
		t.Fatalf("at 10 minutes: published %d, want 1", n)
	}
	// a sweep that was late skips the steps in between and reports the one reached, with the real delay
	if n := f.sweepAt(s, eta, 23*time.Minute+30*time.Second); n != 1 {
		t.Fatalf("at 23.5 minutes: published %d, want 1", n)
	}
	got := f.delays()
	if len(got) != 3 || got[1].DelayMin != 10 || got[2].DelayMin != 23 || got[1].Version != 3 || got[2].Version != 4 {
		t.Errorf("delays = %+v, want 5, 10 and 23 minutes as execution versions 2, 3 and 4", got)
	}
}

func TestRecoveringOrWaitingAtTheStopPublishesNothing(t *testing.T) {
	f := newFixture(t)
	s := f.sweeper()
	eta := f.started()

	f.sweepAt(s, eta, 7*time.Minute) // reported: 7 minutes
	f.clock.Set(eta.Add(7 * time.Minute))
	f.arrive(stop1) // arrives 8 minutes late (the step advances the clock by a minute)

	// the driver is at the stop now: waiting there, even for an hour, is not a delay of arriving
	if n := f.sweepAt(s, eta, time.Hour); n != 0 {
		t.Fatalf("a driver who has arrived and waits: published %d, want nothing", n)
	}
	f.complete(stop1, nil)
	next := f.task().Route.Stops[1].ArriveAt.Time
	if !next.After(f.clock.Now()) {
		t.Fatalf("the next stop's ETA %v is not after the completion at %v: ETAs must re-anchor", next, f.clock.Now())
	}
	// the clock is the new leg's: on time again, nothing is published, and the earlier lateness does not carry over
	if n := f.sweepAt(s, next, -time.Minute); n != 0 {
		t.Fatalf("on time again: published %d, want nothing", n)
	}
	if got := f.delays(); len(got) != 1 {
		t.Errorf("delays = %+v, want only the one reported before", got)
	}

	// being late on the new leg is reported again, for that stop
	if n := f.sweepAt(s, next, 6*time.Minute); n != 1 {
		t.Fatalf("late on the next stop: published %d, want 1", n)
	}
	if got := f.delays(); len(got) != 2 || got[1].DelayMin != 6 {
		t.Errorf("delays = %+v, want the second of 6 minutes", got)
	}
}

func TestADriverWhoArrivedLateBetweenSweepsIsReportedForTheLatenessOfTheArrivalOnly(t *testing.T) {
	f := newFixture(t)
	s := f.sweeper()
	eta := f.started()

	f.clock.Set(eta.Add(11 * time.Minute)) // arrives 12 minutes late (the arrive step adds one); no sweep yet
	f.arrive(stop1)
	if n := f.sweepAt(s, eta, 3*time.Hour); n != 1 {
		t.Fatalf("published %d, want the arrival's lateness reported once", n)
	}
	if got := f.delays(); len(got) != 1 || got[0].DelayMin != 12 {
		t.Errorf("delays = %+v, want 12 minutes (the arrival), not the 3 hours spent at the stop", got)
	}
}

func TestARestartOrAnotherReplicaDoesNotRepeatAStep(t *testing.T) {
	f := newFixture(t)
	eta := f.started()
	if n := f.sweepAt(f.sweeper(), eta, 12*time.Minute); n != 1 {
		t.Fatalf("published %d, want 1", n)
	}
	for range 3 { // a new sweeper each time: the last step lives in the database, not in the sweeper
		if n := f.sweepAt(f.sweeper(), eta, 12*time.Minute); n != 0 {
			t.Fatalf("a restarted sweeper repeated the step (published %d)", n)
		}
	}

	// two sweeps at the very same moment on a new step: exactly one wins
	f.clock.Set(eta.Add(16 * time.Minute))
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range 4 {
		wg.Go(func() {
			n, err := f.sweeper().Sweep(t.Context())
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += n
			mu.Unlock()
		})
	}
	wg.Wait()
	if total != 1 || len(f.delays()) != 2 {
		t.Errorf("concurrent sweeps published %d, total delays %d; want one for the step", total, len(f.delays()))
	}
}

func TestOnlyRunningRoutesAreSweptAndTheVersionsStayInStepWithTheDriversOwnEvents(t *testing.T) {
	f := newFixture(t)
	s := f.sweeper()
	far := drivertest.Epoch.Add(24 * time.Hour)

	// assigned, not started: its start time having passed is not a delay of this kind
	if n := f.sweepAt(s, far, 0); n != 0 {
		t.Fatalf("an unstarted route was reported late (%d)", n)
	}

	eta := f.started()
	f.sweepAt(s, eta, 6*time.Minute) // v2
	f.clock.Set(eta.Add(6 * time.Minute))
	f.arrive(stop1) // v3: the driver's own events carry on from the delay's version
	f.complete(stop1, nil)
	var versions []int64
	for _, e := range f.outbox() {
		versions = append(versions, e.Version)
	}
	for i, v := range versions {
		if v != int64(i+1) {
			t.Fatalf("execution versions = %v: one rising version per event, whoever publishes it", versions)
		}
	}
	if f.task().ExecVersion != int64(len(versions)) {
		t.Errorf("exec version %d after %d events", f.task().ExecVersion, len(versions))
	}

	// a finished route is not swept
	f.arrive(stop2)
	f.complete(stop2, otp(drivertest.CodeA))
	f.arrive(stop3)
	f.complete(stop3, otp(drivertest.CodeB))
	before := len(f.outbox())
	if n := f.sweepAt(s, far, 0); n != 0 || len(f.outbox()) != before {
		t.Errorf("a finished route was swept (%d)", n)
	}
}

func TestTheSweeperRunsOnItsOwnUntilItIsStopped(t *testing.T) {
	f := newFixture(t)
	eta := f.started()
	f.clock.Set(eta.Add(8 * time.Minute))
	s := execution.NewSweeper(f.pool, drivertest.Publisher(f.pool), f.clock.Now, drivertest.Logger(), 10*time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for len(f.delays()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the running sweeper published nothing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(60 * time.Millisecond) // several more ticks: still one
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v, want nil after a stop", err)
	}
	if got := f.delays(); len(got) != 1 || got[0].DelayMin != 8 {
		t.Errorf("delays = %+v, want one of 8 minutes", got)
	}
}
