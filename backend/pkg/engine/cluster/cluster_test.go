package cluster

import (
	"math/rand/v2"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
)

// blobs builds `per` units around each of three far apart centres and returns
// the input plus the true blob of every unit.
func blobs(per int) (Input, []int) {
	rng := rand.New(rand.NewPCG(7, 7))
	centres := []geo.XY{{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 10000, Y: 18000}}
	var units []Unit
	var truth []int
	for c, ctr := range centres {
		for range per {
			p := geo.XY{X: ctr.X + rng.NormFloat64()*800, Y: ctr.Y + rng.NormFloat64()*800}
			units = append(units, Unit{Orders: []int{len(units)}, Pts: []geo.XY{p}})
			truth = append(truth, c)
		}
	}
	in := Input{Units: units, Time: func(a, b int) float64 {
		return unitPoint(units[a]).Dist(unitPoint(units[b])) / 10 // 10 m/s
	}}
	return in, truth
}

// samePartition reports whether two labelings group items identically.
func samePartition(a, b []int) bool {
	m := map[int]int{}
	back := map[int]int{}
	for i := range a {
		if v, ok := m[a[i]]; ok && v != b[i] {
			return false
		}
		if v, ok := back[b[i]]; ok && v != a[i] {
			return false
		}
		m[a[i]], back[b[i]] = b[i], a[i]
	}
	return true
}

func TestAlgorithmsRecoverSeparatedBlobs(t *testing.T) {
	in, truth := blobs(8)
	for _, alg := range []string{KMeans, KMedoids, Agglomerative} {
		r, err := Run(in, Params{Algorithm: alg, K: 3, Seed: 1})
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		if !samePartition(r.Assign, truth) {
			t.Errorf("%s did not recover the three blobs: %v", alg, r.Assign)
		}
		if r.Silhouette < 0.7 {
			t.Errorf("%s silhouette = %.2f, expected well separated clusters", alg, r.Silhouette)
		}
	}
}

func TestAutoKFindsThreeClusters(t *testing.T) {
	in, _ := blobs(8)
	for _, alg := range []string{KMeans, KMedoids, Agglomerative} {
		r, err := Run(in, Params{Algorithm: alg, Seed: 3})
		if err != nil {
			t.Fatal(err)
		}
		if r.K != 3 {
			t.Errorf("%s chose k=%d, want 3", alg, r.K)
		}
	}
}

func TestDBSCANSeparatesBlobsAndIsolatesOutlier(t *testing.T) {
	in, truth := blobs(8)
	// Add a far outlier.
	in.Units = append(in.Units, Unit{Orders: []int{99}, Pts: []geo.XY{{X: -60000, Y: -60000}}})
	units := in.Units
	in.Time = func(a, b int) float64 { return unitPoint(units[a]).Dist(unitPoint(units[b])) / 10 }

	r, err := Run(in, Params{Algorithm: DBSCAN, Eps: 400, MinPts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if r.K != 4 {
		t.Fatalf("want 3 blobs + 1 outlier cluster, got K=%d (%v)", r.K, r.Assign)
	}
	if !samePartition(r.Assign[:len(truth)], truth) {
		t.Errorf("blobs not separated: %v", r.Assign)
	}
	outlier := r.Assign[len(truth)]
	for i, c := range r.Assign[:len(truth)] {
		if c == outlier {
			t.Errorf("unit %d shares the outlier's cluster", i)
		}
	}
}

func TestNoneAndSingleUnit(t *testing.T) {
	in, _ := blobs(3)
	r, err := Run(in, Params{Algorithm: None})
	if err != nil || r.K != 1 {
		t.Errorf("none: %+v, %v", r, err)
	}
	one := Input{Units: in.Units[:1], Time: in.Time}
	if r, err = Run(one, Params{Algorithm: KMeans, K: 4}); err != nil || r.K != 1 {
		t.Errorf("single unit: %+v, %v", r, err)
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	in, _ := blobs(3)
	if _, err := Run(in, Params{Algorithm: "magic", K: 2}); err == nil {
		t.Error("unknown algorithm must fail")
	}
	if _, err := Run(Input{Time: in.Time}, Params{Algorithm: KMeans, K: 2}); err == nil {
		t.Error("empty input must fail")
	}
	if _, err := Run(in, Params{Algorithm: KMeans, K: -1}); err == nil {
		t.Error("negative k must fail")
	}
}

func TestKClampedToNumberOfUnits(t *testing.T) {
	in, _ := blobs(1) // 3 units
	r, err := Run(in, Params{Algorithm: KMedoids, K: 10, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if r.K != 3 {
		t.Errorf("k should be clamped to 3, got %d", r.K)
	}
}

func TestResultsAreReproducible(t *testing.T) {
	in, _ := blobs(8)
	a, _ := Run(in, Params{Algorithm: KMeans, K: 3, Seed: 42})
	b, _ := Run(in, Params{Algorithm: KMeans, K: 3, Seed: 42})
	for i := range a.Assign {
		if a.Assign[i] != b.Assign[i] {
			t.Fatal("same seed must give the same clustering")
		}
	}
}

func TestMedoidsAreMembersOfTheirCluster(t *testing.T) {
	in, _ := blobs(6)
	r, _ := Run(in, Params{Algorithm: KMedoids, K: 3, Seed: 5})
	if len(r.Centers) != r.K {
		t.Fatalf("centers = %d, K = %d", len(r.Centers), r.K)
	}
	for c, m := range r.Centers {
		if r.Assign[m] != c {
			t.Errorf("medoid %d of cluster %d belongs to cluster %d", m, c, r.Assign[m])
		}
	}
}
