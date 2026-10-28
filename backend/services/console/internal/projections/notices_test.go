package projections

import (
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus/eventbustest"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func (s *shop) notes() []contracts.AppNotification {
	s.t.Helper()
	list, err := notices.List(s.t.Context(), s.pool, notices.Customer(s.customer))
	if err != nil {
		s.t.Fatal(err)
	}
	return list
}

// titles counts the notifications by title key.
func titles(list []contracts.AppNotification) map[string]int {
	counts := map[string]int{}
	for _, n := range list {
		counts[n.Title.Key]++
	}
	return counts
}

func (s *shop) delayed(version int64, minutes float64, when time.Time) contracts.Envelope {
	return envelope(s.t, contracts.TypeRouteDelayed, s.routeID, version, when, contracts.RouteDelayed{PlanID: s.planID, RouteID: s.routeID, DelayMin: minutes})
}

func TestFollowingARouteTellsTheShipperEachThingThatMattersToThem(t *testing.T) {
	s := newShop(t)
	other := testkit.Customer(t, s.pool, "Raka")

	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	s.mustApply(arriveHandler, s.arrived(2, 1, at(10)))
	s.mustApply(completeHandler, s.completed(3, 1, at(14))) // picking up is no news
	s.mustApply(arriveHandler, s.arrived(4, 2, at(20)))
	s.mustApply(completeHandler, s.completed(5, 2, at(23)))
	s.mustApply(arriveHandler, s.arrived(6, 3, at(30)))
	s.mustApply(completeHandler, s.completed(7, 3, at(33))) // a delivery is
	s.mustApply(arriveHandler, s.arrived(8, 4, at(40)))
	if got := titles(s.notes()); got["srv.ops.ntf.orderDoneTitle"] != 0 {
		t.Fatalf("told the order is done before it is: %v", got)
	}
	s.mustApply(issueHandler, s.issue(9, 4, at(43))) // the last stop fails: that closes it and finishes the plan

	list := s.notes()
	want := map[string]int{
		"srv.ops.ntf.driverAssignedTitle":   1,
		"srv.api.notif.driverEnRoute.title": 1,
		"srv.ops.ntf.arrivedPickupTitle":    2,
		"srv.ops.ntf.arrivedDropoffTitle":   2,
		"srv.ops.ntf.deliveredTitle":        1,
		"srv.ops.ntf.problemCustomerTitle":  1,
		"srv.ops.ntf.orderDoneTitle":        1,
	}
	if got := titles(list); !equalCounts(got, want) {
		t.Fatalf("notifications by title\n got %v\nwant %v", got, want)
	}
	for _, n := range list {
		if n.PlanID != s.planID || n.Read || n.Audience != notices.Customer(s.customer) || n.Body.Key == "" {
			t.Errorf("notification %+v", n)
		}
	}

	byTitle := func(key string) []contracts.AppNotification {
		var out []contracts.AppNotification
		for _, n := range list {
			if n.Title.Key == key {
				out = append(out, n)
			}
		}
		return out
	}
	driver := map[string]any{"key": "srv.ops.driver"} // a nested message, as stored
	assigned := byTitle("srv.ops.ntf.driverAssignedTitle")[0]
	if assigned.Body.Key != "srv.ops.ntf.driverAssignedBody" || assigned.Body.Params["code"] != "PL-1" || !mapsEqual(assigned.Body.Params["driver"], driver) || !assigned.At.Equal(at(1)) {
		t.Errorf("assigned = %+v at %v", assigned.Body, assigned.At)
	}
	enRoute := byTitle("srv.api.notif.driverEnRoute.title")[0]
	if enRoute.Body.Key != "srv.api.notif.driverEnRoute.body" || enRoute.Body.Params["place"] != "Kemang" || enRoute.Body.Params["code"] != "PL-1" || !mapsEqual(enRoute.Body.Params["name"], driver) {
		t.Errorf("en route = %+v", enRoute.Body)
	}
	pickups := byTitle("srv.ops.ntf.arrivedPickupTitle")
	if pickups[1].Body.Key != "srv.ops.ntf.arrivedBody" || pickups[1].Body.Params["place"] != "Kemang" || !pickups[1].At.Equal(at(10)) { // newest first: the second pickup is at [0]
		t.Errorf("pickup arrivals = %+v / %+v", pickups[0].Body, pickups[1].Body)
	}
	delivered := byTitle("srv.ops.ntf.deliveredTitle")[0]
	if delivered.Body.Key != "srv.ops.ntf.deliveredBody" || delivered.Body.Params["place"] != "Senayan" || delivered.Body.Params["code"] != "PL-1" {
		t.Errorf("delivered = %+v", delivered.Body)
	}
	problem := byTitle("srv.ops.ntf.problemCustomerTitle")[0]
	if issue, ok := problem.Body.Params["issue"].(map[string]any); problem.Body.Key != "srv.ops.ntf.problemCustomerBody" || !ok || issue["key"] != "srv.ops.issue.withNote" || problem.Body.Params["place"] != "Pondok Indah" {
		t.Errorf("problem = %+v", problem.Body)
	}
	done := byTitle("srv.ops.ntf.orderDoneTitle")[0]
	if done.Body.Key != "srv.ops.ntf.orderDoneBody" || done.Body.Params["code"] != "PL-1" || !done.At.Equal(at(43)) {
		t.Errorf("done = %+v at %v", done.Body, done.At)
	}

	if others, err := notices.List(t.Context(), s.pool, notices.Customer(other)); err != nil || len(others) != 0 {
		t.Errorf("another customer has %d notifications (%v)", len(others), err)
	}
}

func equalCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func mapsEqual(got any, want map[string]any) bool {
	m, ok := got.(map[string]any)
	if !ok || len(m) != len(want) {
		return false
	}
	for k, v := range want {
		if m[k] != v {
			return false
		}
	}
	return true
}

func TestAnEventNotifiesTheShipperOnceHoweverOftenItIsDelivered(t *testing.T) {
	s := newShop(t)
	assigned := s.assigned(1, "drv-1", "veh-aaa", at(1))
	for range 3 {
		s.mustApply(assignHandler, assigned)
	}
	if got := titles(s.notes()); got["srv.ops.ntf.driverAssignedTitle"] != 1 || len(s.notes()) != 1 {
		t.Errorf("notifications after three deliveries: %v", got)
	}
	if n := testkit.Count(t, s.pool, `SELECT count(*) FROM notifications WHERE dedupe_key = $1`, assigned.ID+":event"); n != 1 {
		t.Errorf("%d rows under the event's key", n)
	}

	// A re-assignment is a new event: it is news again.
	s.mustApply(unassignHandler, s.unassigned(2, at(2)))
	s.mustApply(assignHandler, s.assigned(3, "drv-2", "veh-bbb", at(3)))
	if got := titles(s.notes()); got["srv.ops.ntf.driverAssignedTitle"] != 2 {
		t.Errorf("notifications = %v, want the second driver announced too", got)
	}
}

func TestAnEventThatFailsLeavesNoNotificationBehind(t *testing.T) {
	s := newShop(t)
	if err := s.apply(startHandler, s.started(1, at(5))); err == nil { // nothing assigned yet: retried later
		t.Fatal("the start was accepted")
	}
	if n := len(s.notes()); n != 0 {
		t.Errorf("%d notifications from an event that was rolled back", n)
	}
}

func TestADelayIsRecordedOnlyWhenItGrowsAndTheShipperIsToldTheNewTotal(t *testing.T) {
	s := newShop(t)
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	base := len(s.record().Plan.Events)

	s.mustApply(routeDelayedHandler, s.delayed(2, 5, at(12)))
	rec := s.record()
	if got := rec.Plan.Routes[0].DelayMin; got != 5 {
		t.Fatalf("delayMin = %v, want 5", got)
	}
	last := rec.Plan.Events[len(rec.Plan.Events)-1]
	if len(rec.Plan.Events) != base+1 || last.Text.Key != "srv.api.event.delayInjected" || last.Text.Params["minutes"] != float64(5) || !last.At.Equal(at(12)) {
		t.Errorf("timeline ends with %+v (%d events, was %d)", last, len(rec.Plan.Events), base)
	}
	delay := func() []contracts.AppNotification {
		var out []contracts.AppNotification
		for _, n := range s.notes() {
			if n.Title.Key == "srv.api.notif.delayCustomer.title" {
				out = append(out, n)
			}
		}
		return out
	}
	if got := delay(); len(got) != 1 || got[0].Body.Key != "srv.api.notif.delayCustomer.body" || got[0].Body.Params["count"] != float64(5) || got[0].Body.Params["code"] != "PL-1" || got[0].PlanID != s.planID {
		t.Fatalf("delay notifications = %+v", got)
	}

	s.mustApply(routeDelayedHandler, s.delayed(3, 10, at(17)))
	rec = s.record()
	last = rec.Plan.Events[len(rec.Plan.Events)-1]
	if rec.Plan.Routes[0].DelayMin != 10 || last.Text.Params["minutes"] != float64(5) {
		t.Errorf("delayMin %v, last event %+v: a delay of 10 after 5 is 5 more", rec.Plan.Routes[0].DelayMin, last.Text)
	}
	if got := delay(); len(got) != 2 || got[0].Body.Params["count"] != float64(10) {
		t.Fatalf("delay notifications = %+v", got)
	}

	// The same total again, a smaller one (back on time), and an old report: nothing new to say.
	notes := len(s.notes())
	s.mustApply(routeDelayedHandler, s.delayed(4, 10, at(22)))
	s.mustApply(routeDelayedHandler, s.delayed(5, 7, at(27)))
	s.mustApply(routeDelayedHandler, s.delayed(3, 20, at(28)))
	after := s.record()
	if after.Plan.Routes[0].DelayMin != 10 || len(after.Plan.Events) != len(rec.Plan.Events) || len(s.notes()) != notes {
		t.Errorf("delayMin %v, %d events, %d notifications: a report that is no worse than the known delay changed something", after.Plan.Routes[0].DelayMin, len(after.Plan.Events), len(s.notes()))
	}
	if after.RouteVersions[s.routeID].Exec != 5 {
		t.Errorf("exec version = %d, want 5: the newest report is the one applied, even if it says nothing new", after.RouteVersions[s.routeID].Exec)
	}
}

func TestADelayOnARouteThatIsNotRunningChangesNothing(t *testing.T) {
	s := newShop(t)
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	before := len(s.notes())
	s.mustApply(routeDelayedHandler, s.delayed(1, 15, at(3)))
	if got := s.route().DelayMin; got != 0 || len(s.notes()) != before {
		t.Errorf("delayMin %v, %d notifications: an assigned route cannot be late", got, len(s.notes())-before)
	}
}

func TestADelayThroughTheConsumerReachesThePlan(t *testing.T) {
	s := newShop(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicDispatch, contracts.TopicExecution)
	produce(t, brokers, contracts.TopicDispatch, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	produce(t, brokers, contracts.TopicExecution, s.started(1, at(5)), s.delayed(2, 5, at(12)), s.delayed(3, 15, at(22)))

	runConsumers(t, s, brokers, 200)
	waitFor(t, "the delay to be applied", func() bool { return s.route().DelayMin == 15 })
	if got := titles(s.notes())["srv.api.notif.delayCustomer.title"]; got != 2 {
		t.Errorf("%d delay notifications, want one per worsening", got)
	}
	if dead := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicExecution), 0); len(dead) != 0 {
		t.Errorf("%d dead letters", len(dead))
	}
}
