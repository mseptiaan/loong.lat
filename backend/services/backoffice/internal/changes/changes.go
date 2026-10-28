// Package changes tells open admin consoles that data changed (the SSE events of the frontend's AppEvent).
package changes

import (
	"sync"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Feed publishes "refetch" hints to the admin audience. A hint is held for a short delay and merged with equal
// ones, for two reasons: event handlers announce a change from inside the transaction that makes it, so the
// delay lets the commit land before a console refetches; and a burst (GPS fixes, a sweep assigning many routes)
// becomes one hint per kind instead of a storm.
type Feed struct {
	hub   *platform.Hub
	delay time.Duration

	mu      sync.Mutex
	pending map[platform.Event]struct{}
}

// DefaultDelay is long enough for a commit that follows the announcing call immediately.
const DefaultDelay = 100 * time.Millisecond

func New(hub *platform.Hub, delay time.Duration) *Feed {
	return &Feed{hub: hub, delay: delay, pending: map[platform.Event]struct{}{}}
}

// Plan announces that one plan changed.
func (f *Feed) Plan(planID string) { f.add(platform.Event{Type: "plans.changed", PlanID: planID}) }

// Plans announces that plans changed in a way not tied to one of them (a customer was renamed).
func (f *Feed) Plans() { f.add(platform.Event{Type: "plans.changed"}) }

// Fleet announces a change of vehicles, drivers, or where they are.
func (f *Feed) Fleet() { f.add(platform.Event{Type: "fleet.changed"}) }

// Settings announces a change of the settings.
func (f *Feed) Settings() { f.add(platform.Event{Type: "settings.changed"}) }

// Users announces a change of the customers (a sign-up, a rename, a block).
func (f *Feed) Users() { f.add(platform.Event{Type: "users.changed"}) }

// Notifications announces that the admins' bell menu changed (a notification arrived, or some were read).
func (f *Feed) Notifications() { f.add(platform.Event{Type: "notifications.changed"}) }

// Wallet announces a change of what the finance pages show: balances, ledgers, top-ups.
func (f *Feed) Wallet() { f.add(platform.Event{Type: "wallet.changed"}) }

func (f *Feed) add(ev platform.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) == 0 {
		time.AfterFunc(f.delay, f.flush)
	}
	f.pending[ev] = struct{}{}
}

func (f *Feed) flush() {
	f.mu.Lock()
	batch := f.pending
	f.pending = map[platform.Event]struct{}{}
	f.mu.Unlock()
	for ev := range batch {
		f.hub.Publish(ev, platform.AudienceAdmin)
	}
}
