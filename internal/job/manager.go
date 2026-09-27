package job

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/internal/feedback"
	"patchbay/internal/identity"
	"patchbay/internal/logging"
	"patchbay/pkg/protocol"
)

type Work func(context.Context, string) (action.Result, error)
type Limits struct{ Concurrency, Queue, History int }

type entry struct {
	job    Job
	ctx    context.Context
	cancel context.CancelFunc
	stop   func() bool
	work   Work
	done   chan struct{}
}

// Handle pins a job for a submitting synchronous waiter even if terminal history
// is trimmed before the HTTP handler starts waiting.
type Handle struct {
	ID      string
	manager *Manager
	entry   *entry
}

func (h *Handle) Wait(ctx context.Context) (Job, error) {
	select {
	case <-ctx.Done():
		return Job{}, fault.Safe(ctx.Err())
	case <-h.entry.done:
	}
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	return clone(h.entry.job)
}

// Manager owns one goroutine per running job and context cancellation callbacks.
// Shutdown closes admission, cancels all work and joins these workers. Work must
// obey cancellation; provider subprocesses have their own bounded cleanup.
type Manager struct {
	mu        sync.Mutex
	limits    Limits
	entries   map[string]*entry
	queue     []*entry
	history   []string
	running   int
	closed    bool
	drained   chan struct{}
	drainOnce sync.Once
	bus       *event.Bus
	log       *logging.Logger
}

func NewManager(limits Limits, bus *event.Bus, log *logging.Logger) *Manager {
	return &Manager{limits: limits, entries: map[string]*entry{}, drained: make(chan struct{}), bus: bus, log: log}
}

func (m *Manager) Submit(parent context.Context, name string, generation uint64, timeout time.Duration, work Work) (*Handle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fault.New(protocol.ShuttingDown, "Daemon is shutting down.")
	}
	if err := parent.Err(); err != nil {
		return nil, fault.Safe(err)
	}
	if m.running >= m.limits.Concurrency && len(m.queue) >= m.limits.Queue {
		return nil, fault.New(protocol.Busy, "The job queue is full.")
	}
	ctx, cancel := context.WithCancel(parent)
	if timeout > 0 {
		deadlineCtx, deadlineCancel := context.WithTimeout(ctx, timeout)
		baseCancel := cancel
		ctx, cancel = deadlineCtx, func() { deadlineCancel(); baseCancel() }
	}
	id := identity.New()
	e := &entry{job: Job{ID: id, Action: name, Generation: generation, State: Queued, CreatedAt: time.Now().UTC()}, ctx: ctx, cancel: cancel, work: work, done: make(chan struct{})}
	m.entries[id] = e
	m.queue = append(m.queue, e)
	e.stop = context.AfterFunc(ctx, func() { m.cancelQueued(id) })
	m.emit(event.JobQueued, e)
	m.kick()
	return &Handle{ID: id, manager: m, entry: e}, nil
}

func (m *Manager) kick() {
	for !m.closed && m.running < m.limits.Concurrency && len(m.queue) > 0 {
		e := m.queue[0]
		m.queue[0] = nil
		m.queue = m.queue[1:]
		if e.ctx.Err() != nil {
			m.finish(e, action.Result{}, e.ctx.Err())
			continue
		}
		now := time.Now().UTC()
		e.job.State, e.job.StartedAt = Running, &now
		m.running++
		m.emit(event.JobRunning, e)
		go m.run(e)
	}
}

func safeRun(ctx context.Context, id string, work Work) (result action.Result, err error) {
	defer func() {
		if recover() != nil {
			result = action.Result{}
			err = fault.New(protocol.Internal, "Action panicked.")
		}
	}()
	return work(ctx, id)
}

func (m *Manager) run(e *entry) {
	result, err := safeRun(e.ctx, e.job.ID, e.work)
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ctx.Err() != nil {
		err = e.ctx.Err()
	}
	m.running--
	m.finish(e, result, err)
	m.kick()
	m.checkDrained()
}

// finish is called under mu and only once per entry. Stored results are copied
// before publication so callers and provider-owned maps cannot mutate history.
func (m *Manager) finish(e *entry, result action.Result, err error) {
	if e.stop != nil {
		e.stop()
	}
	e.cancel()
	now := time.Now().UTC()
	e.job.FinishedAt = &now
	e.job.Error = fault.Safe(err)
	if e.job.Error == nil && result.Status == action.Cancelled {
		e.job.Error = fault.Safe(context.Canceled)
	}
	e.job.State = Success
	if err != nil || result.Status == action.Failed || result.Status == action.Cancelled {
		e.job.State = Failed
		if e.job.Error == nil {
			e.job.Error = fault.New(protocol.ExecutionFailed, "Action failed.")
		}
		if e.job.Error.Code == protocol.Cancelled {
			e.job.State = Cancelled
		}
	}
	result.Status = action.Status(e.job.State)
	result.Display = feedback.ForOutcome(e.job.Action, string(e.job.State))
	e.job.Result = &result
	copy, copyErr := clone(e.job)
	if copyErr != nil {
		e.job.State = Failed
		e.job.Error = fault.New(protocol.Internal, "Action returned an invalid result.")
		e.job.Result = &action.Result{Status: action.Failed, Message: "Invalid result."}
	} else {
		e.job = copy
	}
	e.work = nil // release pinned generation, environment, and prepared workflow
	m.history = append(m.history, e.job.ID)
	m.trim()
	m.emit(event.JobFinished, e)
	if m.log != nil {
		m.log.Operation(context.Background(), logging.Record{Component: "job", JobID: e.job.ID, ActionID: e.job.Action, Duration: now.Sub(e.job.CreatedAt), Outcome: string(e.job.State), Err: err})
	}
	close(e.done)
}

func clone(value Job) (Job, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return Job{}, err
	}
	var copy Job
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	err = d.Decode(&copy)
	return copy, err
}

func (m *Manager) emit(kind string, e *entry) {
	if m.bus != nil {
		m.bus.Emit(kind, map[string]any{"job_id": e.job.ID, "action": e.job.Action, "state": e.job.State, "generation": e.job.Generation})
	}
}
func (m *Manager) trim() {
	for len(m.history) > m.limits.History {
		id := m.history[0]
		m.history = m.history[1:]
		delete(m.entries, id)
	}
}

func (m *Manager) cancelQueued(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, exists := m.entries[id]
	if !exists || e.job.State != Queued {
		return
	}
	m.removeQueued(e)
	m.finish(e, action.Result{}, e.ctx.Err())
	m.checkDrained()
}
func (m *Manager) removeQueued(e *entry) {
	for i, queued := range m.queue {
		if queued == e {
			copy(m.queue[i:], m.queue[i+1:])
			m.queue[len(m.queue)-1] = nil
			m.queue = m.queue[:len(m.queue)-1]
			return
		}
	}
}

func (m *Manager) Get(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, exists := m.entries[id]
	if !exists {
		return Job{}, fault.New(protocol.NotFound, "Job not found.")
	}
	return clone(e.job)
}
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := make([]Job, 0, len(m.entries))
	for _, e := range m.entries {
		copy, _ := clone(e.job)
		jobs = append(jobs, copy)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs
}
func (m *Manager) Running() int { m.mu.Lock(); defer m.mu.Unlock(); return m.running }

func (m *Manager) Cancel(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, exists := m.entries[id]
	if !exists {
		return Job{}, fault.New(protocol.NotFound, "Job not found.")
	}
	switch e.job.State {
	case Queued:
		e.cancel()
		m.removeQueued(e)
		m.finish(e, action.Result{}, context.Canceled)
	case Running:
		e.cancel()
	}
	return clone(e.job)
}

func (m *Manager) Wait(ctx context.Context, id string) (Job, error) {
	m.mu.Lock()
	e, exists := m.entries[id]
	m.mu.Unlock()
	if !exists {
		return Job{}, fault.New(protocol.NotFound, "Job not found.")
	}
	select {
	case <-ctx.Done():
		return Job{}, fault.Safe(ctx.Err())
	case <-e.done:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return clone(e.job) // works even if history evicted it while this waiter held e
}

func (m *Manager) Reconfigure(limits Limits) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limits = limits
	m.trim()
	m.kick()
}
func (m *Manager) checkDrained() {
	if m.closed && m.running == 0 {
		m.drainOnce.Do(func() { close(m.drained) })
	}
}
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		queued := m.queue
		m.queue = nil
		for _, e := range queued {
			e.cancel()
			m.finish(e, action.Result{}, context.Canceled)
		}
		for _, e := range m.entries {
			if e.job.State == Running {
				e.cancel()
			}
		}
		m.checkDrained()
	}
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.drained:
		return nil
	}
}
