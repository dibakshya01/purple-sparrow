// Package events is the M8 realtime spine: a tiny change-event model and an
// EventBus port. Record mutations publish change events; the realtime HTTP
// endpoint subscribes and fans them out to clients, filtered by policy so a
// subscriber only ever receives events for rows it is authorized to see.
//
// The in-process Hub is the solo/startup adapter. The port is small enough that
// a NATS/Redis adapter (enterprise tier) drops in behind the same interface.
package events

import "sync"

// Kind is a change-event type.
const (
	KindInsert = "insert"
	KindUpdate = "update"
	KindDelete = "delete"
)

// Event is a single row-change notification. Row holds the current column values
// for insert/update (used for policy filtering); for delete, only ID is set.
type Event struct {
	Type  string         `json:"type"`
	Table string         `json:"table"`
	ID    string         `json:"id"`
	Row   map[string]any `json:"row,omitempty"`
	At    string         `json:"at"`
}

// Publisher accepts change events. Record services depend only on this.
type Publisher interface {
	Publish(Event)
}

// Bus is a Publisher plus subscription.
type Bus interface {
	Publisher
	// Subscribe returns a receive channel and a cancel func. The channel is closed
	// by cancel; callers MUST call cancel to avoid leaking a subscriber slot.
	Subscribe() (<-chan Event, func())
}

// Hub is an in-process fan-out Bus. Publish never blocks on a slow subscriber:
// if a subscriber's buffer is full the event is dropped for that subscriber only
// (realtime is best-effort; durable history is a separate concern).
type Hub struct {
	mu   sync.RWMutex
	subs map[int]chan Event
	next int
	buf  int
}

// NewHub returns a Hub with a per-subscriber buffer.
func NewHub() *Hub {
	return &Hub{subs: make(map[int]chan Event), buf: 128}
}

// Subscribe registers a subscriber.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, h.buf)
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			if c, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(c)
			}
			h.mu.Unlock()
		})
	}
	return ch, cancel
}

// Publish fans out to all current subscribers. The write lock is NOT held; the
// read lock makes Publish mutually exclusive with Subscribe/cancel, so a channel
// is never sent-to after being closed.
func (h *Hub) Publish(e Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, ch := range h.subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop this event for them only
		}
	}
}

// SubscriberCount reports the number of active subscribers (for metrics/tests).
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
