package event

import (
	"context"
	"encoding/json"
	"patchbay/internal/identity"
	"sync"
	"sync/atomic"
	"time"
)

// Bus owns subscriptions. Publishers never wait for consumers. Subscription
// cancellation uses context.AfterFunc; Close stops all callbacks and channels.
type Bus struct {
	mu       sync.Mutex
	subs     map[*Subscription]struct{}
	capacity int
	closed   bool
}

type Subscription struct {
	C       <-chan Event
	queue   chan Event
	bus     *Bus
	stop    func() bool
	dropped atomic.Uint64
}

func New(capacity int) *Bus {
	return &Bus{subs: make(map[*Subscription]struct{}), capacity: max(1, capacity)}
}

func (b *Bus) Subscribe(ctx context.Context) *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	q := make(chan Event, b.capacity)
	s := &Subscription{C: q, queue: q, bus: b}
	if b.closed || ctx.Err() != nil {
		close(q)
		return s
	}
	b.subs[s] = struct{}{}
	s.stop = context.AfterFunc(ctx, s.Close)
	return s
}

func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }
func (s *Subscription) Close() {
	b := s.bus
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.subs[s]; !exists {
		return
	}
	delete(b.subs, s)
	if s.stop != nil {
		s.stop()
	}
	close(s.queue)
}

func (b *Bus) Publish(ctx context.Context, e Event) {
	if ctx.Err() != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for s := range b.subs {
		copy := e
		copy.Payload = append(json.RawMessage(nil), e.Payload...)
		select {
		case s.queue <- copy:
		default:
			select {
			case <-s.queue:
				s.dropped.Add(1)
			default:
			}
			s.queue <- copy // publisher serialization guarantees a free slot
		}
	}
}

func (b *Bus) Emit(kind string, payload any) Event {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}
	}
	e := Event{ID: identity.New(), Type: kind, Source: "deckd", Timestamp: time.Now().UTC(), Payload: data}
	b.Publish(context.Background(), e)
	return e
}

func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for s := range b.subs {
		if s.stop != nil {
			s.stop()
		}
		close(s.queue)
		delete(b.subs, s)
	}
}
