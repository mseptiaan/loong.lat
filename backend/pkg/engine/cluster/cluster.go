// Package cluster groups routing units (orders) into geographic clusters.
//
// Four algorithms are offered, each with its own strength:
//
//   - kmeans: fast, compact, geometric clusters (k-means++ with restarts).
//   - kmedoids: like k-means but on real road travel times, so rivers and
//     highways shape the clusters; every cluster has an actual stop as centre.
//   - agglomerative: deterministic average-linkage hierarchy on road times.
//   - dbscan: density based, finds natural groups and isolates outliers.
//
// All of them work on Units. A unit may carry several orders that must stay
// together (for example orders bound to one driver by a global constraint),
// which the algorithms treat as a single indivisible item.
package cluster

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
)

// Algorithm names accepted by Run.
const (
	KMeans        = "kmeans"
	KMedoids      = "kmedoids"
	Agglomerative = "agglomerative"
	DBSCAN        = "dbscan"
	None          = "none" // a single cluster: the "no clustering" baseline
)

// Algorithms lists the selectable algorithms.
var Algorithms = []string{KMeans, KMedoids, Agglomerative, DBSCAN, None}

// Unit is an indivisible item to cluster.
type Unit struct {
	Orders []int    // order indices contained in this unit
	Pts    []geo.XY // planar anchor points (meters); empty means "at the depot"
}

// Input describes what to cluster.
type Input struct {
	Units []Unit
	// Time returns the symmetric road travel time in seconds between two
	// units. It is used by every algorithm except kmeans for assignment, and
	// by all of them for quality scores.
	Time func(a, b int) float64
}

// Params control a clustering run.
type Params struct {
	Algorithm string
	K         int     // number of clusters; 0 lets silhouette analysis choose (not for dbscan)
	Seed      uint64  // makes runs reproducible
	Eps       float64 // dbscan radius in seconds of travel; 0 picks one automatically
	MinPts    int     // dbscan core threshold; defaults to 2
}

// Result is a partition of the units.
type Result struct {
	Algorithm  string
	K          int
	Assign     []int   // unit -> cluster id in [0, K)
	Silhouette float64 // mean silhouette in [-1, 1]; 0 when K < 2
	Centers    []int   // representative unit per cluster for kmedoids, else nil
	Notes      []string
}

// Run clusters the input.
func Run(in Input, p Params) (*Result, error) {
	n := len(in.Units)
	if n == 0 {
		return nil, errors.New("nothing to cluster")
	}
	if in.Time == nil {
		return nil, errors.New("cluster input needs a time function")
	}
	if p.Algorithm == "" {
		p.Algorithm = KMedoids
	}
	if !slices.Contains(Algorithms, p.Algorithm) {
		return nil, fmt.Errorf("unknown clustering algorithm %q (use one of %v)", p.Algorithm, Algorithms)
	}
	if p.Algorithm == None || n == 1 {
		return &Result{Algorithm: p.Algorithm, K: 1, Assign: make([]int, n)}, nil
	}
	if p.K < 0 {
		return nil, fmt.Errorf("k must not be negative, got %d", p.K)
	}
	p.K = min(p.K, n)
	if p.Algorithm == DBSCAN {
		return runDBSCAN(in, p), nil
	}

	if p.K >= 1 {
		return runFixed(in, p, p.K), nil
	}

	// Choose k by silhouette over a sensible range. Silhouette drifts upward as
	// clusters shrink, so among all k within 5% of the best score the smallest
	// wins: the simplest partition that is nearly as good.
	kmax := min(8, n-1)
	var all []*Result
	for k := 2; k <= kmax; k++ {
		all = append(all, runFixed(in, p, k))
	}
	if len(all) == 0 {
		return runFixed(in, p, 1), nil
	}
	top := all[0].Silhouette
	for _, r := range all {
		top = max(top, r.Silhouette)
	}
	best := all[len(all)-1]
	for _, r := range all {
		if r.Silhouette >= top-0.05*math.Abs(top) {
			best = r
			break
		}
	}
	best.Notes = append(best.Notes, fmt.Sprintf("k=%d chosen by silhouette %.2f (best %.2f over k=2..%d, smallest k within 5%%)", best.K, best.Silhouette, top, kmax))
	return best, nil
}

func runFixed(in Input, p Params, k int) *Result {
	var r *Result
	switch p.Algorithm {
	case KMeans:
		r = kmeans(in, k, p.Seed)
	case Agglomerative:
		r = agglomerative(in, k)
	default:
		r = kmedoids(in, k, p.Seed)
	}
	r.Algorithm = p.Algorithm
	r.K = k
	r.Silhouette = Silhouette(in, r.Assign, k)
	return r
}

// Silhouette is the mean silhouette coefficient of a partition using the
// input's travel times: values near 1 mean tight, well separated clusters.
func Silhouette(in Input, assign []int, k int) float64 {
	n := len(assign)
	if k < 2 || n < 3 {
		return 0
	}
	size := make([]int, k)
	for _, c := range assign {
		size[c]++
	}
	var total float64
	counted := 0
	sum := make([]float64, k)
	for i := range n {
		if size[assign[i]] < 2 {
			continue // a singleton scores 0 by convention
		}
		clear(sum)
		for j := range n {
			if j != i {
				sum[assign[j]] += in.Time(i, j)
			}
		}
		a := sum[assign[i]] / float64(size[assign[i]]-1)
		b := math.Inf(1)
		for c := range k {
			if c != assign[i] && size[c] > 0 {
				b = min(b, sum[c]/float64(size[c]))
			}
		}
		if m := max(a, b); m > 0 && !math.IsInf(b, 1) {
			total += (b - a) / m
		}
		counted++
	}
	if counted == 0 {
		return 0
	}
	return total / float64(counted)
}

// unitPoint is the representative planar point of a unit: the mean of its
// anchors, or the origin when it has none.
func unitPoint(u Unit) geo.XY { return geo.Centroid(u.Pts) }

func kmeans(in Input, k int, seed uint64) *Result {
	n := len(in.Units)
	pts := make([]geo.XY, n)
	for i, u := range in.Units {
		pts[i] = unitPoint(u)
	}
	var best *Result
	bestCost := math.Inf(1)
	const restarts = 12
	for r := range restarts {
		rng := rand.New(rand.NewPCG(seed, uint64(r)+1))
		centers := kmeansPlusPlus(pts, k, rng)
		assign := make([]int, n)
		var cost float64
		for range 100 {
			changed := false
			cost = 0
			for i, p := range pts {
				bc, bd := 0, math.Inf(1)
				for c, ctr := range centers {
					if d := p.Dist(ctr); d < bd {
						bc, bd = c, d
					}
				}
				cost += bd * bd
				if assign[i] != bc {
					assign[i], changed = bc, true
				}
			}
			sums := make([]geo.XY, k)
			cnt := make([]int, k)
			for i, p := range pts {
				sums[assign[i]].X += p.X
				sums[assign[i]].Y += p.Y
				cnt[assign[i]]++
			}
			for c := range k {
				if cnt[c] > 0 {
					centers[c] = geo.XY{X: sums[c].X / float64(cnt[c]), Y: sums[c].Y / float64(cnt[c])}
				}
			}
			if !changed {
				break
			}
		}
		if cost < bestCost {
			bestCost = cost
			best = &Result{Assign: slices.Clone(assign)}
		}
	}
	return compact(best)
}

// kmeansPlusPlus seeds centers with D² weighting.
func kmeansPlusPlus(pts []geo.XY, k int, rng *rand.Rand) []geo.XY {
	centers := []geo.XY{pts[rng.IntN(len(pts))]}
	d2 := make([]float64, len(pts))
	for len(centers) < k {
		var total float64
		for i, p := range pts {
			d := math.Inf(1)
			for _, c := range centers {
				d = min(d, p.Dist(c))
			}
			d2[i] = d * d
			total += d2[i]
		}
		if total == 0 {
			centers = append(centers, pts[rng.IntN(len(pts))])
			continue
		}
		target := rng.Float64() * total
		idx := len(pts) - 1
		for i, v := range d2 {
			if target -= v; target <= 0 {
				idx = i
				break
			}
		}
		centers = append(centers, pts[idx])
	}
	return centers
}

// kmedoids runs Voronoi-iteration k-medoids on road travel times with
// k-medoids++ seeding and several restarts.
func kmedoids(in Input, k int, seed uint64) *Result {
	n := len(in.Units)
	d := distMatrix(in)
	var best *Result
	bestCost := math.Inf(1)
	const restarts = 12
	for r := range restarts {
		rng := rand.New(rand.NewPCG(seed, uint64(r)+101))
		medoids := []int{rng.IntN(n)}
		for len(medoids) < k {
			w := make([]float64, n)
			var total float64
			for i := range n {
				w[i] = math.Inf(1)
				for _, m := range medoids {
					w[i] = min(w[i], d[i*n+m])
				}
				total += w[i] * w[i]
			}
			if total == 0 {
				for i := range n {
					if !slices.Contains(medoids, i) {
						medoids = append(medoids, i)
						break
					}
				}
				continue
			}
			target := rng.Float64() * total
			pick := n - 1
			for i, v := range w {
				if target -= v * v; target <= 0 {
					pick = i
					break
				}
			}
			if slices.Contains(medoids, pick) {
				for i := range n {
					if !slices.Contains(medoids, i) {
						pick = i
						break
					}
				}
			}
			medoids = append(medoids, pick)
		}

		assign := make([]int, n)
		var cost float64
		for range 100 {
			cost = 0
			for i := range n {
				bc, bd := 0, math.Inf(1)
				for c, m := range medoids {
					if v := d[i*n+m]; v < bd {
						bc, bd = c, v
					}
				}
				assign[i] = bc
				cost += bd
			}
			moved := false
			for c := range medoids {
				bestM, bestSum := medoids[c], math.Inf(1)
				for i := range n {
					if assign[i] != c {
						continue
					}
					var s float64
					for j := range n {
						if assign[j] == c {
							s += d[i*n+j]
						}
					}
					if s < bestSum-1e-9 {
						bestM, bestSum = i, s
					}
				}
				if bestM != medoids[c] {
					medoids[c], moved = bestM, true
				}
			}
			if !moved {
				break
			}
		}
		if cost < bestCost {
			bestCost = cost
			res := &Result{Assign: slices.Clone(assign), Centers: slices.Clone(medoids)}
			best = res
		}
	}
	return compactWithCenters(best)
}

// agglomerative merges the two closest clusters (average linkage) until k remain.
func agglomerative(in Input, k int) *Result {
	n := len(in.Units)
	d := distMatrix(in)
	active := make([]bool, n)
	size := make([]int, n)
	for i := range n {
		active[i], size[i] = true, 1
	}
	label := make([]int, n)
	for i := range label {
		label[i] = i
	}
	for count := n; count > k; count-- {
		bi, bj, bd := -1, -1, math.Inf(1)
		for i := range n {
			if !active[i] {
				continue
			}
			for j := i + 1; j < n; j++ {
				if active[j] && d[i*n+j] < bd {
					bi, bj, bd = i, j, d[i*n+j]
				}
			}
		}
		// Lance-Williams update for average linkage.
		for x := range n {
			if !active[x] || x == bi || x == bj {
				continue
			}
			v := (float64(size[bi])*d[bi*n+x] + float64(size[bj])*d[bj*n+x]) / float64(size[bi]+size[bj])
			d[bi*n+x], d[x*n+bi] = v, v
		}
		size[bi] += size[bj]
		active[bj] = false
		for u := range label {
			if label[u] == bj {
				label[u] = bi
			}
		}
	}
	return compact(&Result{Assign: label})
}

func runDBSCAN(in Input, p Params) *Result {
	n := len(in.Units)
	d := distMatrix(in)
	minPts := p.MinPts
	if minPts <= 0 {
		minPts = 2
	}
	eps := p.Eps
	r := &Result{Algorithm: DBSCAN}
	if eps <= 0 {
		eps = autoEps(d, n, minPts)
		r.Notes = append(r.Notes, fmt.Sprintf("eps chosen automatically: %.0f s of travel", eps))
	}

	const unvisited, noise = -2, -1
	label := make([]int, n)
	for i := range label {
		label[i] = unvisited
	}
	neighbors := func(i int) []int {
		var out []int
		for j := range n {
			if d[i*n+j] <= eps {
				out = append(out, j)
			}
		}
		return out
	}
	k := 0
	for i := range n {
		if label[i] != unvisited {
			continue
		}
		nb := neighbors(i)
		if len(nb) < minPts {
			label[i] = noise
			continue
		}
		label[i] = k
		queue := slices.Clone(nb)
		for len(queue) > 0 {
			j := queue[0]
			queue = queue[1:]
			if label[j] == noise {
				label[j] = k
			}
			if label[j] != unvisited {
				continue
			}
			label[j] = k
			if nbj := neighbors(j); len(nbj) >= minPts {
				queue = append(queue, nbj...)
			}
		}
		k++
	}
	outliers := 0
	for i := range label {
		if label[i] == noise {
			label[i] = k // each outlier becomes its own cluster
			k++
			outliers++
		}
	}
	if outliers > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("%d outlier unit(s) form singleton clusters", outliers))
	}
	r.Assign, r.K = label, k
	r.Silhouette = Silhouette(in, label, k)
	return r
}

// autoEps picks the knee of the sorted k-distance curve's upper half, a common
// rule of thumb, using the median of the minPts-th neighbour distance.
func autoEps(d []float64, n, minPts int) float64 {
	kd := make([]float64, 0, n)
	for i := range n {
		row := make([]float64, 0, n-1)
		for j := range n {
			if j != i {
				row = append(row, d[i*n+j])
			}
		}
		slices.Sort(row)
		kd = append(kd, row[min(minPts-1, len(row)-1)])
	}
	slices.Sort(kd)
	return kd[len(kd)*3/4]
}

func distMatrix(in Input) []float64 {
	n := len(in.Units)
	d := make([]float64, n*n)
	for i := range n {
		for j := i + 1; j < n; j++ {
			v := in.Time(i, j)
			d[i*n+j], d[j*n+i] = v, v
		}
	}
	return d
}

// compact renumbers clusters to 0..K-1 ordered by their first member so
// results are stable and free of empty clusters.
func compact(r *Result) *Result {
	remap := map[int]int{}
	for i, c := range r.Assign {
		id, ok := remap[c]
		if !ok {
			id = len(remap)
			remap[c] = id
		}
		r.Assign[i] = id
	}
	return r
}

func compactWithCenters(r *Result) *Result {
	remap := map[int]int{}
	old := r.Assign
	assign := make([]int, len(old))
	var centers []int
	for i, c := range old {
		id, ok := remap[c]
		if !ok {
			id = len(remap)
			remap[c] = id
			centers = append(centers, r.Centers[c])
		}
		assign[i] = id
	}
	r.Assign, r.Centers = assign, centers
	return r
}
