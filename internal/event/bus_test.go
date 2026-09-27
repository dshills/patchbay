package event

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestBusOverflowCopiesAndLifecycle(t *testing.T) {
	b := New(2)
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s, other := b.Subscribe(ctx), b.Subscribe(context.Background())
	payload := json.RawMessage(`{"n":0}`)
	b.Publish(ctx, Event{ID: "0", Payload: payload})
	payload[5] = '9'
	first := <-other.C
	if string(first.Payload) != `{"n":0}` {
		t.Fatal("shared publisher payload")
	}
	first.Payload[5] = '7'
	if string((<-s.C).Payload) != `{"n":0}` {
		t.Fatal("shared subscriber payload")
	}
	for _, id := range []string{"1", "2", "3"} {
		b.Publish(ctx, Event{ID: id})
	}
	if s.Dropped() != 1 || (<-s.C).ID != "2" || (<-s.C).ID != "3" {
		t.Fatal("drop-oldest order")
	}
	cancel()
	select {
	case _, ok := <-s.C:
		if ok {
			t.Fatal("expected closed")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close subscription")
	}
	s.Close()
	b.Close()
	b.Close()
	closed := b.Subscribe(context.Background())
	if _, ok := <-closed.C; ok {
		t.Fatal("subscribe after close")
	}
	b.Emit("ignored", nil)
}

func TestConcurrentBusLifecycle(t *testing.T) {
	b := New(1)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 100 {
				ctx, cancel := context.WithCancel(context.Background())
				s := b.Subscribe(ctx)
				b.Emit("test", map[string]int{"x": 1})
				cancel()
				s.Close()
			}
		})
	}
	wg.Wait()
	b.Close()
}

func BenchmarkPublishSlowSubscriber(b *testing.B) {
	bus := New(64)
	defer bus.Close()
	bus.Subscribe(context.Background())
	e := Event{Type: ControlRotated, Payload: json.RawMessage(`{"delta":1}`)}
	b.ReportAllocs()
	for b.Loop() {
		bus.Publish(context.Background(), e)
	}
}
