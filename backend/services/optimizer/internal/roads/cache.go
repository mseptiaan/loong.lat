package roads

import (
	"math"
	"sync"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// MemoryCache is a PairCache in process memory. Entries expire after TTL, which
// keeps measured distances from outliving the road data they were taken from.
type MemoryCache struct {
	TTL time.Duration
	// Max bounds the entries kept; when full, the cache starts over rather than
	// tracking recency. The working set of a courier city is far below it.
	Max int

	mu  sync.Mutex
	m   map[pairKey]entry
	now func() time.Time
}

type pairKey struct{ a, b [2]int32 }

type entry struct {
	meters  int32
	expires time.Time
}

// key rounds a place to 1e-5 degrees, about a metre, so that the same spot found twice is one place.
func key(p contracts.LatLng) [2]int32 {
	return [2]int32{int32(math.Round(p.Lat * 1e5)), int32(math.Round(p.Lng * 1e5))}
}

// NewMemoryCache creates a cache holding up to max pairs for ttl.
func NewMemoryCache(max int, ttl time.Duration) *MemoryCache {
	return &MemoryCache{TTL: ttl, Max: max, m: map[pairKey]entry{}, now: time.Now}
}

// Get implements PairCache.
func (c *MemoryCache) Get(from, to contracts.LatLng) (int32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[pairKey{key(from), key(to)}]
	if !ok || c.now().After(e.expires) {
		return 0, false
	}
	return e.meters, true
}

// Put implements PairCache.
func (c *MemoryCache) Put(from, to contracts.LatLng, meters int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Max > 0 && len(c.m) >= c.Max {
		clear(c.m)
	}
	c.m[pairKey{key(from), key(to)}] = entry{meters, c.now().Add(c.TTL)}
}
