package realtime

import (
	"encoding/json"
	"sync"
	"time"
)

// PresenceState is the user-supplied metadata for a presence entry.
type PresenceState map[string]interface{}

// PresenceEntry represents one client's presence in a channel.
type PresenceEntry struct {
	Key       string        `json:"key"`
	State     PresenceState `json:"state"`
	JoinedAt  time.Time     `json:"joined_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// PresenceEvent is the payload broadcast when the presence set changes.
type PresenceEvent struct {
	Event   string          `json:"event"` // "presence" always
	Topic   string          `json:"topic"`
	Payload PresencePayload `json:"payload"`
}

// PresencePayload contains joins and leaves for a single presence delta.
type PresencePayload struct {
	Joins  map[string]PresenceEntry `json:"joins,omitempty"`
	Leaves map[string]PresenceEntry `json:"leaves,omitempty"`
}

// PresenceTracker maintains the presence set for a channel and broadcasts
// join/leave diffs to the Hub.
type PresenceTracker struct {
	mu      sync.RWMutex
	channel string
	hub     *Hub
	entries map[string]*PresenceEntry // key → entry
}

// NewPresenceTracker creates a tracker for channel, broadcasting via hub.
func NewPresenceTracker(channel string, hub *Hub) *PresenceTracker {
	return &PresenceTracker{
		channel: channel,
		hub:     hub,
		entries: make(map[string]*PresenceEntry),
	}
}

// Join adds or updates a presence entry for key with state.
// A "presence" event containing the join diff is broadcast to all subscribers.
func (pt *PresenceTracker) Join(key string, state PresenceState) {
	now := time.Now().UTC()

	pt.mu.Lock()
	existing, update := pt.entries[key]
	entry := &PresenceEntry{
		Key:      key,
		State:    state,
		JoinedAt: now,
	}
	if update {
		entry.JoinedAt = existing.JoinedAt
	}
	entry.UpdatedAt = now
	pt.entries[key] = entry
	pt.mu.Unlock()

	evt := PresenceEvent{
		Event: "presence",
		Topic: pt.channel,
		Payload: PresencePayload{
			Joins: map[string]PresenceEntry{key: *entry},
		},
	}
	pt.broadcast(evt)
}

// Leave removes the presence entry for key and broadcasts the leave diff.
func (pt *PresenceTracker) Leave(key string) {
	pt.mu.Lock()
	entry, ok := pt.entries[key]
	if ok {
		delete(pt.entries, key)
	}
	pt.mu.Unlock()

	if !ok {
		return
	}

	evt := PresenceEvent{
		Event: "presence",
		Topic: pt.channel,
		Payload: PresencePayload{
			Leaves: map[string]PresenceEntry{key: *entry},
		},
	}
	pt.broadcast(evt)
}

// State returns a snapshot of all current presence entries.
func (pt *PresenceTracker) State() map[string]PresenceEntry {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	snapshot := make(map[string]PresenceEntry, len(pt.entries))
	for k, v := range pt.entries {
		snapshot[k] = *v
	}
	return snapshot
}

func (pt *PresenceTracker) broadcast(evt PresenceEvent) {
	b, err := json.Marshal(evt)
	if err != nil {
		return
	}
	pt.hub.Broadcast(pt.channel, b)
}
