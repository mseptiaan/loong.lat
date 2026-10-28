package platform

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// Audience says who may receive an event; its strings match the frontend's NotificationAudience.
type Audience string

const AudienceAdmin Audience = "admin"

func CustomerAudience(id string) Audience { return Audience("customer:" + id) }
func DriverAudience(id string) Audience   { return Audience("driver:" + id) }

// AudienceOf is the audience of a signed-in principal.
func AudienceOf(p Principal) Audience {
	switch p.Role {
	case contracts.RoleAdmin:
		return AudienceAdmin
	case contracts.RoleDriver:
		return DriverAudience(p.UserID)
	default:
		return CustomerAudience(p.UserID)
	}
}

// Event tells a client that something changed; it carries names, not data: the client refetches. It mirrors
// the frontend's AppEvent (`{type: 'plans.changed', planId?}`).
type Event struct {
	Type   string `json:"type"`
	PlanID string `json:"planId,omitempty"`
}

const (
	subscriberBuffer  = 32
	defaultKeepAlive  = 15 * time.Second
	sseReconnectDelay = 3 * time.Second
)

type subscriber struct {
	audience Audience
	events   chan Event
}

// Hub fans events out to the open SSE connections of one process, each filtered by its audience.
type Hub struct {
	mu        sync.Mutex
	subs      map[*subscriber]struct{}
	closed    bool
	done      chan struct{}
	closeOnce sync.Once
	keepAlive time.Duration
}

func NewHub() *Hub {
	return &Hub{subs: map[*subscriber]struct{}{}, done: make(chan struct{}), keepAlive: defaultKeepAlive}
}

// Publish delivers ev to every subscriber whose audience is listed, or to everyone when none is. A subscriber
// that stops reading loses its oldest queued event rather than blocking the publisher: events are only
// "refetch" hints, so the newest matters most.
func (h *Hub) Publish(ev Event, to ...Audience) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if len(to) > 0 && !slices.Contains(to, sub.audience) {
			continue
		}
		select {
		case sub.events <- ev:
		default:
			select {
			case <-sub.events:
			default:
			}
			select {
			case sub.events <- ev:
			default:
			}
		}
	}
}

// Close ends every open stream and refuses new ones. Register it with Server.OnShutdown so a deploy does not
// wait on connections that never end by themselves.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		h.mu.Unlock()
		close(h.done)
	})
}

func (h *Hub) subscribe(audience Audience) (*subscriber, func(), bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, false
	}
	sub := &subscriber{audience: audience, events: make(chan Event, subscriberBuffer)}
	h.subs[sub] = struct{}{}
	return sub, func() {
		h.mu.Lock()
		delete(h.subs, sub)
		h.mu.Unlock()
	}, true
}

// Handler serves `GET /v1/events` as Server-Sent Events for the signed-in principal. Browsers' EventSource
// cannot send an Authorization header, so clients read this with fetch() streaming, like the NDJSON methods.
func (h *Hub) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFrom(r.Context())
		if !ok {
			writeError(w, Unauthenticated())
			return
		}
		sub, unsubscribe, ok := h.subscribe(AudienceOf(principal))
		if !ok {
			writeError(w, Unavailable(contracts.Msg{Key: MsgServiceError}))
			return
		}
		defer unsubscribe()

		header := w.Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		header.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		rc := http.NewResponseController(w)
		if _, err := fmt.Fprintf(w, "retry: %d\n\n", sseReconnectDelay.Milliseconds()); err != nil || rc.Flush() != nil {
			return
		}

		ticker := time.NewTicker(h.keepAlive)
		defer ticker.Stop()
		for {
			var frame string
			select {
			case <-r.Context().Done():
				return
			case <-h.done:
				return
			case <-ticker.C:
				frame = ": ping\n\n"
			case ev := <-sub.events:
				data, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				frame = "data: " + string(data) + "\n\n"
			}
			if _, err := w.Write([]byte(frame)); err != nil || rc.Flush() != nil {
				return
			}
		}
	})
}
