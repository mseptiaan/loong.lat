package platform

import (
	"testing"
	"time"
)

func TestLoginLimiterAllowsABurstThenThrottles(t *testing.T) {
	now := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	l := NewLoginLimiter(10*time.Second, 3)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if ok, _ := l.Allow("dina@contoh.id"); !ok {
			t.Fatalf("attempt %d of the burst was refused", i+1)
		}
	}
	ok, wait := l.Allow("dina@contoh.id")
	if ok || wait <= 0 || wait > 10*time.Second {
		t.Fatalf("4th attempt: ok=%v wait=%v, want refused with a wait up to 10s", ok, wait)
	}

	// Hammering while refused must not push the next allowed attempt further out.
	for range 20 {
		l.Allow("dina@contoh.id")
	}
	now = now.Add(wait)
	if ok, _ := l.Allow("dina@contoh.id"); !ok {
		t.Error("after the advertised wait one attempt must be allowed again")
	}
	if ok, _ := l.Allow("dina@contoh.id"); ok {
		t.Error("only one attempt per refill interval")
	}

	if ok, _ := l.Allow("someone-else@contoh.id"); !ok {
		t.Error("another key has its own budget")
	}
}

func TestLoginLimiterForgetsIdleKeys(t *testing.T) {
	now := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	l := NewLoginLimiter(time.Second, 2)
	l.now = func() time.Time { return now }

	for i := range 50 {
		l.Allow("user-" + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	if len(l.entries) != 50 {
		t.Fatalf("entries = %d", len(l.entries))
	}
	now = now.Add(5 * time.Minute) // every bucket has refilled
	l.Allow("fresh")
	if len(l.entries) != 1 {
		t.Errorf("entries = %d after the sweep, want only the fresh key", len(l.entries))
	}
}

func TestLoginLimiterBurstIsAtLeastOne(t *testing.T) {
	l := NewLoginLimiter(time.Minute, 0)
	if ok, _ := l.Allow("k"); !ok {
		t.Error("a zero burst would lock everyone out forever")
	}
}
