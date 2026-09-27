package provider

import (
	"context"
	"maps"
	"slices"
	"sync"

	"patchbay/internal/action"
	"patchbay/internal/fault"
	"patchbay/internal/permission"
	wire "patchbay/pkg/plugin"
	"patchbay/pkg/protocol"
)

type pluginState struct {
	config   PluginConfig
	slot     chan struct{}
	mu       sync.Mutex
	snapshot PluginSnapshot
}
type Plugins struct {
	states  map[string]*pluginState
	mu      sync.Mutex
	closed  bool
	stopped chan struct{}
	active  map[*int]context.CancelFunc
	wg      sync.WaitGroup
	drained chan struct{}
}

func NewPlugins(configs map[string]PluginConfig) *Plugins {
	h := &Plugins{states: map[string]*pluginState{}, stopped: make(chan struct{}), active: map[*int]context.CancelFunc{}, drained: make(chan struct{})}
	for id, c := range configs {
		h.states[id] = &pluginState{config: clonePluginConfig(c), slot: make(chan struct{}, 1), snapshot: PluginSnapshot{Health: Health{Code: "not_checked"}}}
	}
	return h
}
func (h *Plugins) Snapshot(id string) PluginSnapshot {
	s := h.states[id]
	if s == nil {
		return PluginSnapshot{Health: Health{Code: "not_configured"}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.snapshot
	result.Operations = slices.Clone(result.Operations)
	return result
}
func (h *Plugins) Risk(id, operation string) permission.Permission {
	s := h.states[id]
	if s == nil {
		return permission.Dangerous
	}
	risk := s.config.Operations[operation]
	for _, op := range h.Snapshot(id).Operations {
		if op.Name == operation {
			risk = permission.Strongest(risk, permission.Permission(op.Safety))
		}
	}
	return permission.Strongest(risk, permission.Confirm)
}
func (h *Plugins) Close(ctx context.Context) error {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		close(h.stopped)
		for _, cancel := range h.active {
			cancel()
		}
		go func() { h.wg.Wait(); close(h.drained) }()
	}
	h.mu.Unlock()
	select {
	case <-h.drained:
		return nil
	case <-ctx.Done():
		return fault.Safe(ctx.Err())
	}
}
func (h *Plugins) Run(ctx context.Context, r PluginRequest, budget *Budget) (result action.Result, err error) {
	result = action.Result{Status: action.Failed, Message: "Plugin operation failed.", Data: map[string]any{"plugin": r.Plugin, "operation": r.Operation}}
	s := h.states[r.Plugin]
	if s == nil {
		return result, fault.New(protocol.ProviderUnavailable, "Plugin is not configured.")
	}
	floor, ok := s.config.Operations[r.Operation]
	if !ok {
		return result, fault.New(protocol.InvalidRequest, "Plugin operation is not allowed.")
	}
	if err = permission.Check(permission.Strongest(floor, permission.Confirm), r.AllowDangerous, r.Confirmed); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, pluginDuration(s.config.ExecutionTimeout))
	defer cancel()
	select {
	case s.slot <- struct{}{}:
	case <-ctx.Done():
		return result, fault.Safe(ctx.Err())
	case <-h.stopped:
		return result, fault.New(protocol.ShuttingDown, "Plugin host is shutting down.")
	}
	defer func() { <-s.slot }()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return result, fault.New(protocol.ShuttingDown, "Plugin host is shutting down.")
	}
	token := new(int)
	h.active[token] = cancel
	h.wg.Add(1)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.active, token)
		h.mu.Unlock()
		h.wg.Done()
	}()
	defer func() {
		if ctx.Err() != nil {
			err = fault.Safe(ctx.Err())
		}
		s.mu.Lock()
		s.snapshot.Health = Health{Available: err == nil}
		if err != nil {
			s.snapshot.Health.Code = string(fault.Safe(err).Code)
		}
		s.mu.Unlock()
		if err == nil {
			result.Status, result.Message = action.Success, "Plugin operation completed."
		} else if fault.Safe(err).Code == protocol.Cancelled {
			result.Status = action.Cancelled
		}
	}()
	if ctx.Err() != nil {
		return result, fault.Safe(ctx.Err())
	}
	args := maps.Clone(r.Args)
	if args == nil {
		args = map[string]any{}
	}
	execute := wire.Execute{Operation: r.Operation, Args: args, Context: r.Context}
	// Reject oversized input before creating a child.
	frame, e := wire.NewFrame("3", "execute", execute)
	if e != nil {
		return result, fault.New(protocol.InvalidRequest, "Invalid plugin input.")
	}
	if _, e = wire.Encode(frame); e != nil {
		return result, fault.New(protocol.InvalidRequest, "Plugin input exceeds the protocol frame limit.")
	}
	p, e := startPlugin(s.config)
	if e != nil {
		return result, fault.New(protocol.ProviderUnavailable, "Plugin executable or working directory is unavailable.")
	}
	defer func() { p.cleanup(); result.Data["stderr_bytes"] = p.stderrBytes.Load() }()
	startup, stop := context.WithTimeout(ctx, pluginDuration(s.config.StartupTimeout))
	var hello wire.HelloResponse
	e = p.call(startup, "hello", wire.HelloRequest{Versions: []int{wire.Version}}, &hello)
	if e == nil {
		e = validatePluginHello(s.config, hello)
	}
	if e == nil {
		s.mu.Lock()
		s.snapshot.Operations = slices.Clone(hello.Operations)
		s.mu.Unlock()
		var health wire.Health
		e = p.call(startup, "health", struct{}{}, &health)
		if e == nil && (health.Available == nil || !*health.Available) {
			e = fault.New(protocol.ProviderUnavailable, "Plugin did not report healthy.")
		}
	}
	stop()
	if e != nil {
		return result, e
	}
	if e = permission.Check(h.Risk(r.Plugin, r.Operation), r.AllowDangerous, r.Confirmed); e != nil {
		return result, e
	}
	if e = p.begin(ctx, "execute", execute); e != nil {
		return result, e
	}
	if r.Started != nil {
		r.Started()
	}
	reply, e := p.receive(ctx)
	if ctx.Err() != nil {
		grace, end := context.WithTimeout(context.Background(), pluginDuration(s.config.CancelTimeout))
		defer end()
		cooperative := p.cancel(grace)
		result.Data["cooperative_cancel"] = cooperative
		if cooperative {
			result.Data["shutdown_clean"] = p.shutdown(grace) == nil
		}
		return result, fault.Safe(ctx.Err())
	}
	if e != nil {
		return result, e
	}
	var output wire.Result
	if reply.Type != "result" || wire.Decode(reply.Payload, &output) != nil || output.Text == nil || output.Status != "success" && output.Status != "failed" {
		return result, pluginFailure()
	}
	capture := &capture{budget: budget}
	_, _ = capture.Write([]byte(*output.Text))
	result.Data["stdout"], result.Data["truncated"] = capture.buffer.String(), capture.truncated
	shutdown, end := context.WithTimeout(ctx, pluginDuration(s.config.ShutdownTimeout))
	defer end()
	e = p.shutdown(shutdown)
	result.Data["shutdown_clean"] = e == nil
	if e != nil {
		return result, e
	}
	if output.Status == "failed" {
		return result, fault.New(protocol.ExecutionFailed, "Plugin reported an unsuccessful operation.")
	}
	return result, nil
}
func validatePluginHello(c PluginConfig, h wire.HelloResponse) error {
	if h.SelectedVersion != wire.Version || len(h.Operations) != len(c.Operations) || len(h.Operations) > wire.MaxOperations {
		return pluginFailure()
	}
	seen := map[string]bool{}
	for _, op := range h.Operations {
		if _, ok := c.Operations[op.Name]; !ok || seen[op.Name] || !permission.Permission(op.Safety).Valid() {
			return pluginFailure()
		}
		seen[op.Name] = true
	}
	return nil
}
