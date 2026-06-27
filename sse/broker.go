// Package sse implements a lightweight Server-Sent Events broker with
// per-room (per-user) fan-out. Publishers send events to a room key and
// every subscriber currently joined to that room receives them. Rooms are
// created lazily on first subscribe and reclaimed when their last
// subscriber leaves, so an idle broker holds no state.
package sse

import (
	"sync"
)

// defaultBuffer is the per-subscriber channel depth. A slow consumer that
// fills its buffer has further events dropped rather than blocking the
// publisher or stalling other subscribers in the same room.
const defaultBuffer = 16

// Subscriber is a single SSE connection joined to one room.
type Subscriber struct {
	room string
	ch   chan []byte
}

// Events returns the channel of serialized event payloads delivered to this
// subscriber. The channel is closed when the subscriber is removed.
func (s *Subscriber) Events() <-chan []byte { return s.ch }

// Broker fans messages out to subscribers grouped by room key.
type Broker struct {
	mu    sync.RWMutex
	rooms map[string]map[*Subscriber]struct{}
}

// NewBroker returns an empty, ready-to-use Broker.
func NewBroker() *Broker {
	return &Broker{rooms: make(map[string]map[*Subscriber]struct{})}
}

// Subscribe joins the given room and returns a Subscriber whose Events
// channel receives every message published to that room. Call Unsubscribe
// when the connection ends.
func (b *Broker) Subscribe(room string) *Subscriber {
	sub := &Subscriber{room: room, ch: make(chan []byte, defaultBuffer)}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rooms[room] == nil {
		b.rooms[room] = make(map[*Subscriber]struct{})
	}
	b.rooms[room][sub] = struct{}{}
	return sub
}

// Unsubscribe removes the subscriber from its room and closes its channel.
// It is safe to call more than once. Empty rooms are deleted.
func (b *Broker) Unsubscribe(sub *Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()

	members, ok := b.rooms[sub.room]
	if !ok {
		return
	}
	if _, joined := members[sub]; !joined {
		return
	}
	delete(members, sub)
	close(sub.ch)
	if len(members) == 0 {
		delete(b.rooms, sub.room)
	}
}

// Publish delivers data to every subscriber in room. Delivery is
// non-blocking: a subscriber whose buffer is full has this message dropped.
// It returns the number of subscribers the message reached.
func (b *Broker) Publish(room string, data []byte) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	delivered := 0
	for sub := range b.rooms[room] {
		select {
		case sub.ch <- data:
			delivered++
		default:
			// Subscriber too slow; drop this event for it.
		}
	}
	return delivered
}

// Subscribers returns the number of active subscribers in a room.
func (b *Broker) Subscribers(room string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.rooms[room])
}
