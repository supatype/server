package realtime

import (
	"sync"
)

// Subscriber represents a connected WebSocket client listening on one channel.
type Subscriber struct {
	channel string
	send    chan []byte // buffered; Hub writes encoded messages here
}

// Hub routes notifications from Postgres to subscribed WebSocket clients.
// It is safe for concurrent use.
type Hub struct {
	mu   sync.RWMutex
	subs map[string][]*Subscriber // channel → subscribers
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{
		subs: make(map[string][]*Subscriber),
	}
}

// Subscribe registers s as a subscriber for channel.
func (h *Hub) Subscribe(s *Subscriber, channel string) {
	s.channel = channel
	h.mu.Lock()
	h.subs[channel] = append(h.subs[channel], s)
	h.mu.Unlock()
}

// Unsubscribe removes s from its channel's subscriber list.
func (h *Hub) Unsubscribe(s *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()

	list := h.subs[s.channel]
	for i, sub := range list {
		if sub == s {
			h.subs[s.channel] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(h.subs[s.channel]) == 0 {
		delete(h.subs, s.channel)
	}
}

// Broadcast sends payload to all subscribers of channel.
// Non-blocking: if a subscriber's send buffer is full, the message is dropped
// with no error (slow consumers should be disconnected by the handler's write
// timeout).
func (h *Hub) Broadcast(channel string, payload []byte) {
	h.mu.RLock()
	subs := make([]*Subscriber, len(h.subs[channel]))
	copy(subs, h.subs[channel])
	h.mu.RUnlock()

	for _, s := range subs {
		select {
		case s.send <- payload:
		default:
			// Subscriber is too slow — drop.
		}
	}
}

// ChannelCount returns the number of active channels with at least one subscriber.
func (h *Hub) ChannelCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
