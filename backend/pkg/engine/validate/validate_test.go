package validate

import (
	"slices"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/solver"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/testutil"
)

func solved(t *testing.T) (*model.Problem, *model.Solution) {
	t.Helper()
	p := testutil.RepoProblem(t)
	s, _ := solver.Solve(p, nil, solver.Options{Seed: 1, Iterations: 300, Workers: 1})
	return p, s
}

func hasViolation(r *Report, substr string) bool {
	return slices.ContainsFunc(r.Violations, func(v string) bool { return strings.Contains(v, substr) })
}

func TestSolverOutputPassesValidation(t *testing.T) {
	p, s := solved(t)
	r := Check(p, s)
	if !r.Valid {
		t.Fatalf("solver output rejected: %v", r.Violations)
	}
	if r.Recomputed != s.M {
		t.Errorf("recomputed %v != cached %v", r.Recomputed, s.M)
	}
}

func TestDetectsMissingAndDuplicatedStops(t *testing.T) {
	p, s := solved(t)
	r0 := slices.IndexFunc(s.Routes, func(r []int) bool { return len(r) > 0 })

	missing := s.Clone()
	missing.Routes[r0] = missing.Routes[r0][1:]
	if r := Check(p, missing); r.Valid || !hasViolation(r, "neither routed nor reported") {
		t.Errorf("a dropped stop must be reported: %v", r.Violations)
	}

	dup := s.Clone()
	dup.Routes[r0] = append(dup.Routes[r0], dup.Routes[r0][0])
	if r := Check(p, dup); r.Valid || !hasViolation(r, "more than once") {
		t.Errorf("a duplicated stop must be reported: %v", r.Violations)
	}
}

func TestDetectsDeliveryBeforePickup(t *testing.T) {
	p, s := solved(t)
	bad := s.Clone()
	r0 := slices.IndexFunc(bad.Routes, func(r []int) bool { return len(r) > 0 })
	route := bad.Routes[r0]
	// Move the first pickup behind the last stop, so its delivery precedes it.
	first := route[0]
	bad.Routes[r0] = append(slices.Clone(route[1:]), first)
	r := Check(p, bad)
	if r.Valid {
		t.Fatal("a pickup after its delivery must be rejected")
	}
}

func TestDetectsGoodsOnAVehicleWithoutCapacity(t *testing.T) {
	p, s := solved(t)
	bad := s.Clone()
	// Move every stop of the busiest route onto driver-1 (index 2), which has no capacity.
	busiest := 0
	for v := range bad.Routes {
		if len(bad.Routes[v]) > len(bad.Routes[busiest]) {
			busiest = v
		}
	}
	if busiest == 2 {
		t.Skip("driver-1 already busiest")
	}
	bad.Routes[2], bad.Routes[busiest] = bad.Routes[busiest], nil
	r := Check(p, bad)
	if r.Valid {
		t.Fatal("goods on a zero-capacity vehicle must be rejected")
	}
	if !hasViolation(r, "capacity") && !hasViolation(r, "infeasible") {
		t.Errorf("expected a capacity related violation: %v", r.Violations)
	}
}

func TestDetectsKeychainSplitAcrossDrivers(t *testing.T) {
	p, s := solved(t)
	// Put one keychain order on a different vehicle than the other two.
	ov := s.OrderVehicle()
	var keychain []int
	for o, ord := range p.Orders {
		if strings.HasPrefix(ord.ID, "order-keychain") {
			keychain = append(keychain, o)
		}
	}
	bad := s.Clone()
	from := ov[keychain[0]]
	to := (from + 1) % len(bad.Routes)
	node := p.Orders[keychain[0]].Nodes[0]
	bad.Routes[from] = slices.DeleteFunc(slices.Clone(bad.Routes[from]), func(n int) bool { return n == node })
	bad.Routes[to] = append(slices.Clone(bad.Routes[to]), node)

	r := Check(p, bad)
	if r.Valid {
		t.Fatal("serving the keychain set with two drivers must be rejected")
	}
	if !hasViolation(r, "one-driver-per-key-set") && !hasViolation(r, "infeasible") && !hasViolation(r, "capacity") {
		t.Errorf("expected the global constraint (or an infeasibility) to be reported: %v", r.Violations)
	}
}

func TestDetectsStaleMetrics(t *testing.T) {
	p, s := solved(t)
	bad := s.Clone()
	bad.M[model.ObjDist] -= 1000
	if r := Check(p, bad); r.Valid || !hasViolation(r, "drifted") {
		t.Errorf("tampered metrics must be detected: %v", r.Violations)
	}
}

func TestUnassignedOrdersAreAcceptedWhenReported(t *testing.T) {
	p := testutil.RepoProblem(t)
	s := model.NewSolution(p) // everything unassigned
	r := Check(p, s)
	if !r.Valid {
		t.Fatalf("an all-unassigned solution is consistent, got %v", r.Violations)
	}
	if r.Recomputed[model.ObjUnserved] != int64(len(p.Nodes)) {
		t.Errorf("unserved = %d, want %d", r.Recomputed[model.ObjUnserved], len(p.Nodes))
	}
}
