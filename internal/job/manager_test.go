package job

import (
	"context"
	"encoding/json"
	"patchbay/internal/action"
	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func manager(t *testing.T, limits Limits) *Manager {
	t.Helper()
	m := NewManager(limits, event.New(100), nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := m.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		m.bus.Close()
	})
	return m
}
func submit(t *testing.T, m *Manager, ctx context.Context, d time.Duration, work Work) *Handle {
	t.Helper()
	h, err := m.Submit(ctx, "action", 1, d, work)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func wait(t *testing.T, h *Handle) Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	j, err := h.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func succeeds(context.Context, string) (action.Result, error) {
	return action.Result{Status: action.Success, Data: map[string]any{"n": int64(9007199254740993)}}, nil
}
func blocks(ctx context.Context, _ string) (action.Result, error) {
	<-ctx.Done()
	return action.Result{}, ctx.Err()
}

func TestQueueCancellationHistoryAndCopies(t *testing.T) {
	m := manager(t, Limits{1, 1, 1})
	sub := m.bus.Subscribe(context.Background())
	defer sub.Close()
	running := submit(t, m, context.Background(), 0, blocks)
	queued := submit(t, m, context.Background(), 0, succeeds)
	if _, err := m.Submit(context.Background(), "x", 1, 0, succeeds); err == nil || fault.Safe(err).Code != protocol.Busy {
		t.Fatal(err)
	}
	if j, err := m.Cancel(queued.ID); err != nil || j.State != Cancelled || j.StartedAt != nil {
		t.Fatal(j, err)
	}
	next := submit(t, m, context.Background(), 0, succeeds)
	if _, err := m.Cancel(running.ID); err != nil {
		t.Fatal(err)
	}
	if j := wait(t, running); j.State != Cancelled {
		t.Fatal(j)
	}
	j := wait(t, next)
	if j.State != Success || j.Result.Data["n"] != json.Number("9007199254740993") {
		t.Fatal(j)
	}
	j.Result.Data["n"] = "changed"
	copy, err := m.Get(next.ID)
	if err != nil || copy.Result.Data["n"] == "changed" {
		t.Fatal(copy, err)
	}
	if j := wait(t, queued); j.State != Cancelled {
		t.Fatal("handle lost evicted job", j)
	}
	if len(m.List()) != 1 {
		t.Fatal(m.List())
	}
	if _, err := m.Get(queued.ID); err == nil {
		t.Fatal("history not evicted")
	}
	if j, err := m.Cancel(next.ID); err != nil || j.State != Success {
		t.Fatal(j, err)
	}
	events := map[string][]string{}
	for len(sub.C) > 0 {
		e := <-sub.C
		var p struct {
			ID string `json:"job_id"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		events[p.ID] = append(events[p.ID], e.Type)
	}
	if got := events[next.ID]; len(got) != 3 || got[0] != event.JobQueued || got[1] != event.JobRunning || got[2] != event.JobFinished {
		t.Fatal(events)
	}
}

func TestTimeoutPanicAndInvalidResults(t *testing.T) {
	for _, c := range []struct {
		name    string
		work    Work
		timeout time.Duration
		code    protocol.Code
	}{
		{"deadline", func(ctx context.Context, _ string) (action.Result, error) {
			<-ctx.Done()
			return action.Result{Status: action.Cancelled}, nil
		}, 5 * time.Millisecond, protocol.Timeout},
		{"panic", func(context.Context, string) (action.Result, error) { panic("secret") }, 0, protocol.Internal},
		{"invalid", func(context.Context, string) (action.Result, error) {
			return action.Result{Data: map[string]any{"bad": make(chan int)}}, nil
		}, 0, protocol.Internal},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := manager(t, Limits{1, 1, 10})
			j := wait(t, submit(t, m, context.Background(), c.timeout, c.work))
			if j.State != Failed || j.Error == nil || j.Error.Code != c.code {
				t.Fatal(j)
			}
			if wait(t, submit(t, m, context.Background(), 0, succeeds)).State != Success {
				t.Fatal("capacity leaked")
			}
		})
	}
	m := manager(t, Limits{1, 1, 10})
	submit(t, m, context.Background(), 0, blocks)
	j := wait(t, submit(t, m, context.Background(), 5*time.Millisecond, succeeds))
	if j.State != Failed || j.Error.Code != protocol.Timeout || j.StartedAt != nil {
		t.Fatal(j)
	}
	// Expiry must free the queue without waiting for the running job.
	submit(t, m, context.Background(), 0, succeeds)
}

func TestConcurrencyReconfigurationAndShutdown(t *testing.T) {
	m := manager(t, Limits{2, 100, 10})
	var active, peak atomic.Int64
	release := make(chan struct{})
	started := make(chan struct{}, 100)
	work := func(ctx context.Context, _ string) (action.Result, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return action.Result{Status: action.Success}, nil
	}
	handles := make([]*Handle, 40)
	for i := range handles {
		handles[i] = submit(t, m, context.Background(), 0, work)
	}
	for range 2 {
		<-started
	}
	if m.Running() != 2 || peak.Load() != 2 {
		t.Fatal(m.Running(), peak.Load())
	}
	m.Reconfigure(Limits{4, 100, 10})
	for range 2 {
		<-started
	}
	if m.Running() != 4 {
		t.Fatal(m.Running())
	}
	close(release)
	var wg sync.WaitGroup
	for _, h := range handles {
		wg.Go(func() { _, _ = m.Cancel(h.ID); _, _ = m.Get(h.ID); _ = m.List() })
	}
	wg.Wait()
	for _, h := range handles {
		j := wait(t, h)
		if j.FinishedAt == nil {
			t.Fatal(j)
		}
	}
	if peak.Load() > 4 {
		t.Fatal(peak.Load())
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(context.Background(), "x", 1, 0, succeeds); err == nil || fault.Safe(err).Code != protocol.ShuttingDown {
		t.Fatal(err)
	}
}
