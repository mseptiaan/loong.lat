package projections

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/proofs"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

const photoURL = "data:image/jpeg;base64,/9j/4AAQSkZJRg=="

// toStop runs the route up to the driver standing at the given stop (arrived, not yet completed) and returns the
// next execution version.
func (s *shop) toStop(stop int) int64 {
	s.t.Helper()
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	version := int64(1)
	for i := 1; i < stop; i++ {
		version++
		s.mustApply(arriveHandler, s.arrived(version, i, at(10*i)))
		version++
		s.mustApply(completeHandler, s.completed(version, i, at(10*i+3)))
	}
	version++
	s.mustApply(arriveHandler, s.arrived(version, stop, at(10*stop)))
	return version + 1
}

// completedWith is a stop.completed that carries this proof.
func (s *shop) completedWith(version int64, stop int, when time.Time, proof *contracts.DeliveryProof) contracts.Envelope {
	return envelope(s.t, contracts.TypeStopCompleted, s.routeID, version, when, contracts.StopCompleted{
		PlanID: s.planID, RouteID: s.routeID, StopID: s.stops[stop-1], CompletedAt: contracts.At(when), Proof: proof,
	})
}

func (s *shop) jobs() int {
	s.t.Helper()
	return testkit.Count(s.t, s.pool, `SELECT count(*) FROM proof_jobs`)
}

func photoOf(when time.Time) *contracts.DeliveryProof {
	return &contracts.DeliveryProof{Kind: contracts.ProofKindPhoto, At: contracts.At(when), Photo: photoURL}
}

func TestADropoffClosedWithAPhotoQueuesExactlyOneAnalysis(t *testing.T) {
	s := newShop(t)
	next := s.toStop(3)
	if s.jobs() != 0 {
		t.Fatalf("%d jobs before any photo", s.jobs())
	}

	env := s.completedWith(next, 3, at(33), photoOf(at(33)))
	s.mustApply(completeHandler, env)

	var planID, routeID, stopID, state string
	var attempts int
	err := s.pool.QueryRow(t.Context(), `SELECT plan_id, route_id, stop_id, state, attempts FROM proof_jobs`).Scan(&planID, &routeID, &stopID, &state, &attempts)
	if err != nil || planID != s.planID || routeID != s.routeID || stopID != s.stops[2] || state != "pending" || attempts != 0 {
		t.Fatalf("job = %s %s %s %s %d, %v", planID, routeID, stopID, state, attempts, err)
	}
	if p := s.route().Stops[2].Proof; p == nil || p.Photo != photoURL || p.Analysis != nil {
		t.Errorf("the stored proof = %+v: the photo is kept and there is no analysis yet", p)
	}

	// A redelivery changes nothing, and neither does a different event that carries the same photo for the stop.
	before := snapshot(t, s.record())
	s.mustApply(completeHandler, env)
	if s.jobs() != 1 || snapshot(t, s.record()) != before {
		t.Errorf("after a redelivery: %d jobs", s.jobs())
	}
	if err := platform.WithTx(t.Context(), s.pool, func(tx pgx.Tx) error { return proofs.Enqueue(t.Context(), tx, s.planID, s.routeID, s.stops[2]) }); err != nil || s.jobs() != 1 {
		t.Errorf("enqueuing the same stop again: %v, %d jobs", err, s.jobs())
	}
}

func TestOtherProofsAndOtherStopsQueueNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		stop  int
		proof *contracts.DeliveryProof
	}{
		"a drop-off proved with the OTP":    {3, &contracts.DeliveryProof{Kind: contracts.ProofKindOTP, At: contracts.At(at(33))}},
		"a drop-off with no proof":          {3, nil},
		"a photo proof that holds no photo": {3, &contracts.DeliveryProof{Kind: contracts.ProofKindPhoto, At: contracts.At(at(33))}},
		"a photo with the kind of an OTP":   {3, &contracts.DeliveryProof{Kind: contracts.ProofKindOTP, At: contracts.At(at(33)), Photo: photoURL}},
		"a pickup, which is not a delivery": {1, photoOf(at(13))},
		"a pickup with no proof":            {1, nil},
	} {
		t.Run(name, func(t *testing.T) {
			s := newShop(t)
			next := s.toStop(tc.stop)
			s.mustApply(completeHandler, s.completedWith(next, tc.stop, at(10*tc.stop+3), tc.proof))
			if s.jobs() != 0 {
				t.Errorf("%d jobs", s.jobs())
			}
			if got := s.route().Stops[tc.stop-1].Status; got != contracts.StopStatusDone {
				t.Errorf("the stop is %s: the event was not applied", got)
			}
		})
	}
}

func TestAPhotoThatWasNotStoredQueuesNothing(t *testing.T) {
	s := newShop(t)
	next := s.toStop(3)

	// An older event is dropped, a plan that is over takes no more events: no proof is stored, so there is nothing for
	// the AI to read.
	s.mustApply(completeHandler, s.completedWith(next-1, 3, at(33), photoOf(at(33))))
	if s.jobs() != 0 || s.route().Stops[2].Status == contracts.StopStatusDone {
		t.Fatalf("an old event: %d jobs, stop %s", s.jobs(), s.route().Stops[2].Status)
	}
	err := platform.WithTx(t.Context(), s.pool, func(tx pgx.Tx) error {
		rec, err := plans.Lock(t.Context(), tx, s.planID)
		if err != nil {
			return err
		}
		rec.Plan.Status = contracts.PlanStatusCancelled
		return plans.Save(t.Context(), tx, rec)
	})
	if err != nil {
		t.Fatal(err)
	}
	s.mustApply(completeHandler, s.completedWith(next, 3, at(33), photoOf(at(33))))
	if s.jobs() != 0 || s.route().Stops[2].Proof != nil {
		t.Errorf("a cancelled plan: %d jobs, proof %+v", s.jobs(), s.route().Stops[2].Proof)
	}
}

func TestTheJobCommitsOrRollsBackWithTheProof(t *testing.T) {
	s := newShop(t)
	next := s.toStop(3)
	env := s.completedWith(next, 3, at(33), photoOf(at(33)))

	boom := errors.New("the transaction fails after the handler")
	err := platform.WithTx(t.Context(), s.pool, func(tx pgx.Tx) error {
		if err := completeHandler(t.Context(), tx, env); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || s.jobs() != 0 || s.route().Stops[2].Proof != nil {
		t.Fatalf("after a rollback: %v, %d jobs, proof %+v", err, s.jobs(), s.route().Stops[2].Proof)
	}

	s.mustApply(completeHandler, env)
	if s.jobs() != 1 || s.route().Stops[2].Proof == nil {
		t.Errorf("after the retry: %d jobs, proof %+v", s.jobs(), s.route().Stops[2].Proof)
	}
}
