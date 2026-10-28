package platform

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// LoginLimiter throttles sign-in attempts per key (typically the account identifier, optionally with the
// client address): a burst of attempts, then one per refill interval. It is in-memory and per process, which
// is enough for a single instance per service; a shared store would be needed to enforce it across replicas.
type LoginLimiter struct {
	mu        sync.Mutex
	limit     rate.Limit
	burst     int
	entries   map[string]*rate.Limiter
	now       func() time.Time
	lastSweep time.Time
}

const limiterSweepEvery = time.Minute

// NewLoginLimiter allows burst attempts at once and then one every `every`.
func NewLoginLimiter(every time.Duration, burst int) *LoginLimiter {
	return &LoginLimiter{limit: rate.Every(every), burst: max(1, burst), entries: map[string]*rate.Limiter{}, now: time.Now}
}

// Allow records an attempt for key. When over the limit it returns false and how long to wait.
func (l *LoginLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)

	lim, found := l.entries[key]
	if !found {
		lim = rate.NewLimiter(l.limit, l.burst)
		l.entries[key] = lim
	}
	res := lim.ReserveN(now, 1)
	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now) // a refused attempt must not push the next allowed one further out
		return false, delay
	}
	return true, 0
}

// sweep forgets keys whose bucket is full again, so the map does not grow with every account ever tried.
func (l *LoginLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < limiterSweepEvery {
		return
	}
	l.lastSweep = now
	for key, lim := range l.entries {
		if lim.TokensAt(now) >= float64(l.burst) {
			delete(l.entries, key)
		}
	}
}
