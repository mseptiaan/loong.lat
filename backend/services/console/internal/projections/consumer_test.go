package projections

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus/eventbustest"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// runConsumers runs the console's two consumer groups for routes against a fake Kafka. Retries are fast and capped by
// attempts, so a record that keeps failing is dead-lettered within a moment.
func runConsumers(t *testing.T, s *shop, brokers []string, attempts int) {
	t.Helper()
	for _, g := range []struct {
		group, topic string
		register     func(*eventbus.Consumer)
	}{
		{"console.dispatch", contracts.TopicDispatch, RegisterDispatch},
		{"console.execution", contracts.TopicExecution, RegisterExecution},
	} {
		c, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
			Brokers: brokers, Group: g.group, Topics: []string{g.topic}, DB: s.pool, Log: testkit.Log,
			MaxAttempts: attempts, BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		g.register(c)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { defer close(done); _ = c.Run(ctx) }()
		t.Cleanup(func() { cancel(); <-done })
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func produce(t *testing.T, brokers []string, topic string, envs ...contracts.Envelope) {
	t.Helper()
	for _, env := range envs {
		eventbustest.Produce(t, brokers, eventbustest.EnvelopeRecord(t, topic, env))
	}
}

func TestAnEventForAPlanNotKnownYetIsRetriedUntilThePlanArrives(t *testing.T) {
	s := newShop(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicDispatch, contracts.TopicExecution)

	late := &shop{t: t, pool: s.pool, customer: s.customer}
	late.insertPlan("pln-late")
	event := late.assigned(1, "drv-1", "veh-aaa", at(1))
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM plans WHERE id = 'pln-late'`); err != nil {
		t.Fatal(err)
	}
	produce(t, brokers, contracts.TopicDispatch, event)

	// Plenty of attempts: the consumer must keep trying while the plan is missing, not give the event up.
	runConsumers(t, s, brokers, 10_000)
	time.Sleep(300 * time.Millisecond)
	if dead := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicDispatch), 0); len(dead) != 0 {
		t.Fatalf("%d records were dead-lettered while the plan was only late", len(dead))
	}

	late.insertPlan("pln-late") // the plan reaches the console
	waitFor(t, "the waiting event to be applied", func() bool {
		rec, err := plansLoad(t, s, "pln-late")
		return err == nil && rec.Plan.Routes[0].DriverID == "drv-1"
	})
	if dead := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicDispatch), 0); len(dead) != 0 {
		t.Errorf("%d records were dead-lettered", len(dead))
	}
}

func TestEventsThatCanNeverApplyAreDeadLetteredAtOnceAndOrderingProblemsAfterRetries(t *testing.T) {
	s := newShop(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicDispatch, contracts.TopicExecution)
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))

	noStop := s.arrived(2, 1, at(10))
	noStop.Data = envelopeData(t, contracts.StopArrived{PlanID: s.planID, RouteID: s.routeID, StopID: s.routeID + ":s99", ArrivedAt: contracts.At(at(10))})
	otherRoute := s.arrived(3, 1, at(11))
	otherRoute.Data = envelopeData(t, contracts.StopArrived{PlanID: s.planID, RouteID: s.planID + ":r9", StopID: s.planID + ":r9:s1", ArrivedAt: contracts.At(at(11))})
	tooEarly := s.completed(4, 1, at(12)) // the driver never arrived
	produce(t, brokers, contracts.TopicExecution, noStop, otherRoute, tooEarly)

	const attempts = 3
	runConsumers(t, s, brokers, attempts)
	dead := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicExecution), 3)
	if len(dead) != 3 {
		t.Fatalf("%d dead letters, want 3", len(dead))
	}
	tried := map[string]string{} // event id -> attempts
	for _, rec := range dead {
		for _, h := range rec.Headers {
			if h.Key == eventbus.HeaderDLQAttempts {
				env, err := contracts.Decode(rec.Value)
				if err != nil {
					t.Fatal(err)
				}
				tried[env.ID] = string(h.Value)
			}
		}
	}
	// An unknown stop or route never becomes valid: no retries. A stop closed before arrival might, once the
	// arrival it waits for turns up: it is retried first.
	if tried[noStop.ID] != "1" || tried[otherRoute.ID] != "1" || tried[tooEarly.ID] != "3" {
		t.Errorf("attempts before dead-lettering = %v, want 1 for the unknown stop, 1 for the unknown route, 3 for the early completion", tried)
	}
}

func TestRedeliveredAndOlderEventsHaveOneEffectThroughTheConsumers(t *testing.T) {
	s := newShop(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicDispatch, contracts.TopicExecution)
	arrival := s.arrived(2, 1, at(10))
	produce(t, brokers, contracts.TopicDispatch, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	produce(t, brokers, contracts.TopicExecution,
		s.started(1, at(5)),
		arrival,
		arrival,                 // the very same event delivered again
		s.started(1, at(30)),    // an older state sent as a new event
		s.arrived(2, 1, at(31)), // and an arrival with a version that was already applied
	)

	runConsumers(t, s, brokers, 200) // the execution topic may get ahead of dispatch: that is retried
	waitFor(t, "the arrival to be applied", func() bool {
		return s.route().Stops[0].Status == contracts.StopStatusArrived
	})
	waitFor(t, "every record to be consumed", func() bool {
		return testkit.Count(t, s.pool, `SELECT count(*) FROM inbox WHERE consumer = 'console.execution'`) == 4
	})

	rec := s.record()
	want := []string{"srv.api.event.confirmed", "srv.ops.event.assigned", "srv.ops.seed.departed", "srv.ops.event.driverArrived"}
	if got := eventKeys(rec.Plan); !slices.Equal(got, want) {
		t.Errorf("timeline = %v, want %v: every fact once", got, want)
	}
	if !rec.Plan.Routes[0].Stops[0].ArrivedAt.Equal(at(10)) || !rec.Plan.Routes[0].StartedAt.Equal(at(5)) {
		t.Errorf("a later duplicate overwrote the facts: started %v, arrived %v", rec.Plan.Routes[0].StartedAt, rec.Plan.Routes[0].Stops[0].ArrivedAt)
	}
	if dead := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicExecution), 0); len(dead) != 0 {
		t.Errorf("%d records dead-lettered", len(dead))
	}
}
