// Package runtime coordinates state, prepared invocations, and atomic reloads.
// It does not import API handlers or device-specific code.
package runtime

import (
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/config"
	runtimecontext "patchbay/internal/context"
	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/internal/job"
	"patchbay/internal/localfs"
	"patchbay/internal/logging"
	"patchbay/internal/parameter"
	"patchbay/internal/provider"
	"patchbay/internal/state"
	"patchbay/internal/version"
	"patchbay/pkg/protocol"
)

type Options struct {
	Log    io.Writer
	Runner provider.Runner
	Opener string
}
type Runtime struct {
	mu         sync.Mutex
	reloadMu   sync.Mutex
	cfg        *config.Config
	registries map[string]*action.Registry[config.Action]
	context    runtimecontext.RuntimeContext
	parameters map[string]parameter.Definition
	generation uint64
	closed     bool
	path       string
	started    time.Time
	bus        *event.Bus
	jobs       *job.Manager
	state      *state.Writer
	stateLock  *localfs.Lock
	runner     provider.Runner
	opener     string
	log        *logging.Logger
}

func New(path string, options Options) (*Runtime, error) {
	path, err := config.FilePath(path)
	if err != nil {
		return nil, err
	}
	c, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return NewConfigured(path, c, options)
}

// NewConfigured consumes a validated configuration snapshot. The caller transfers
// ownership and must not mutate it. Daemon startup uses this to acquire the socket
// lock and restore state from exactly the same file generation.
func NewConfigured(path string, c *config.Config, options Options) (*Runtime, error) {
	path, err := config.FilePath(path)
	if err != nil {
		return nil, err
	}
	registries, err := buildRegistries(c)
	if err != nil {
		return nil, err
	}
	if options.Log == nil {
		options.Log = io.Discard
	}
	if options.Runner == nil {
		options.Runner = provider.ProcessRunner{}
	}
	if options.Opener == "" {
		options.Opener = "/usr/bin/open"
	}
	r := &Runtime{cfg: c, registries: registries, path: path, started: time.Now(), generation: 1, runner: options.Runner, opener: options.Opener, log: logging.New(options.Log)}
	r.context = cloneContext(c.Context.Defaults)
	r.parameters = cloneParameters(c.Parameters)
	r.stateLock, err = localfs.Acquire(c.State.Path + ".lock")
	if err != nil {
		return nil, err
	}
	saved, err := state.Read(c.State.Path)
	if err != nil && !errors.Is(err, state.ErrRecovered) {
		_ = r.stateLock.Close()
		return nil, err
	}
	if err != nil {
		r.warn(err)
	}
	if saved != nil {
		r.context = cloneContext(saved.Context)
		values := maps.Clone(c.Context.Defaults.Values)
		if values == nil {
			values = map[string]string{}
		}
		maps.Copy(values, r.context.Values)
		r.context.Values = values
		if !config.ValidName(r.context.Mode) && r.context.Mode != "" {
			r.context.Mode = c.Context.Defaults.Mode
		}
		for key := range r.context.Values {
			if !config.ValidValueKey(key) {
				delete(r.context.Values, key)
			}
		}
		if _, exists := c.Projects[r.context.Project]; !exists {
			r.context.Project = ""
		}
		for name, value := range saved.Parameters {
			p, exists := r.parameters[name]
			if !exists || !p.Persistent {
				continue
			}
			if normalized, err := p.ValidateValue(value); err == nil {
				p.Value = normalized
				r.parameters[name] = p
			}
		}
	}
	r.bus = event.New(c.Events.SubscriberCapacity)
	r.jobs = job.NewManager(jobLimits(c), r.bus, r.log)
	interval, _ := time.ParseDuration(c.State.FlushInterval)
	r.state = state.NewWriter(c.State.Path, interval, r.warn)
	if err := r.persist(r.context, r.parameters); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = r.Close(ctx)
		return nil, err
	}
	return r, nil
}

func buildRegistries(c *config.Config) (map[string]*action.Registry[config.Action], error) {
	result := make(map[string]*action.Registry[config.Action])
	for _, id := range append([]string{""}, slices.Sorted(maps.Keys(c.Projects))...) {
		registry := action.NewRegistry[config.Action]()
		for name, definition := range c.EffectiveActions(id) {
			if err := registry.Register(name, definition); err != nil {
				return nil, fault.New(protocol.InvalidConfig, "Duplicate action definition.")
			}
		}
		result[id] = registry
	}
	return result, nil
}

func cloneContext(value runtimecontext.RuntimeContext) runtimecontext.RuntimeContext {
	value.Values = maps.Clone(value.Values)
	return value
}
func cloneParameters(values map[string]parameter.Definition) map[string]parameter.Definition {
	copy := make(map[string]parameter.Definition, len(values))
	for key, value := range values {
		value.Enum = slices.Clone(value.Enum)
		copy[key] = value
	}
	return copy
}
func jobLimits(c *config.Config) job.Limits {
	return job.Limits{Concurrency: c.Jobs.Concurrency, Queue: c.Jobs.QueueCapacity, History: c.Jobs.HistoryLimit}
}
func (r *Runtime) warn(err error) {
	outcome := "persist_failed"
	if errors.Is(err, state.ErrRecovered) {
		outcome = "recovered"
	}
	r.log.Operation(context.Background(), logging.Record{Component: "state", Outcome: outcome, Err: err})
}
func (r *Runtime) persist(ctx runtimecontext.RuntimeContext, parameters map[string]parameter.Definition) error {
	values := make(map[string]any)
	for name, p := range parameters {
		if p.Persistent {
			values[name] = p.Value
		}
	}
	return r.state.Submit(state.Snapshot{Version: 1, Context: ctx, Parameters: values})
}
func (r *Runtime) writable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fault.Safe(err)
	}
	if r.closed {
		return fault.New(protocol.ShuttingDown, "Daemon is shutting down.")
	}
	return nil
}

func (r *Runtime) Events() *event.Bus { return r.bus }
func (r *Runtime) Jobs() *job.Manager { return r.jobs }
func (r *Runtime) Context() runtimecontext.RuntimeContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneContext(r.context)
}
func (r *Runtime) Settings() config.Server { r.mu.Lock(); defer r.mu.Unlock(); return r.cfg.Server }

func (r *Runtime) PatchContext(ctx context.Context, patch protocol.ContextPatch) (runtimecontext.RuntimeContext, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writable(ctx); err != nil {
		return runtimecontext.RuntimeContext{}, err
	}
	next := cloneContext(r.context)
	if patch.Project != nil {
		if _, exists := r.cfg.Projects[*patch.Project]; *patch.Project != "" && !exists {
			return next, fault.New(protocol.ProjectNotFound, "Project not found.")
		}
		next.Project = *patch.Project
	}
	if patch.Mode != nil {
		if *patch.Mode != "" && !config.ValidName(*patch.Mode) {
			return next, fault.New(protocol.InvalidRequest, "Invalid mode name.")
		}
		next.Mode = *patch.Mode
	}
	if patch.Values != nil {
		for key := range *patch.Values {
			if !config.ValidValueKey(key) {
				return next, fault.New(protocol.InvalidRequest, "Invalid context value key.")
			}
		}
		next.Values = maps.Clone(*patch.Values)
	}
	if reflect.DeepEqual(next, r.context) {
		return cloneContext(next), nil
	}
	if err := r.persist(next, r.parameters); err != nil {
		return runtimecontext.RuntimeContext{}, fault.New(protocol.InvalidRequest, "Persistent state is too large.")
	}
	previous := r.context
	r.context = next
	if previous.Project != next.Project {
		r.bus.Emit(event.ProjectChanged, map[string]string{"project": next.Project})
	}
	r.bus.Emit(event.ContextChanged, next)
	return cloneContext(next), nil
}

func (r *Runtime) Parameters() []parameter.Parameter {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := make([]parameter.Parameter, 0, len(r.parameters))
	for _, name := range slices.Sorted(maps.Keys(r.parameters)) {
		p := r.parameters[name]
		p.Enum = slices.Clone(p.Enum)
		list = append(list, parameter.Parameter{Name: name, Definition: p})
	}
	return list
}
func (r *Runtime) Parameter(name string) (parameter.Parameter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, exists := r.parameters[name]
	if !exists {
		return parameter.Parameter{}, fault.New(protocol.NotFound, "Parameter not found.")
	}
	p.Enum = slices.Clone(p.Enum)
	return parameter.Parameter{Name: name, Definition: p}, nil
}
func (r *Runtime) SetParameter(ctx context.Context, name string, value any) (parameter.Parameter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writable(ctx); err != nil {
		return parameter.Parameter{}, err
	}
	return r.setParameter(name, value, nil)
}
func (r *Runtime) setParameter(name string, value any, delta *int64) (parameter.Parameter, error) {
	p, exists := r.parameters[name]
	if !exists {
		return parameter.Parameter{}, fault.New(protocol.NotFound, "Parameter not found.")
	}
	var err error
	if delta != nil {
		value, err = p.Rotate(*delta)
	} else {
		value, err = p.ValidateValue(value)
	}
	if err != nil {
		return parameter.Parameter{}, fault.New(protocol.InvalidRequest, "Parameter value does not satisfy its type or bounds.")
	}
	if p.Value != value {
		previous := p
		p.Value = value
		r.parameters[name] = p
		if p.Persistent {
			if err := r.persist(r.context, r.parameters); err != nil {
				r.parameters[name] = previous
				return parameter.Parameter{}, fault.New(protocol.InvalidRequest, "Persistent state is too large.")
			}
		}
		r.bus.Emit(event.ParameterChanged, map[string]any{"name": name, "value": value})
	}
	p.Enum = slices.Clone(p.Enum)
	return parameter.Parameter{Name: name, Definition: p}, nil
}

func (r *Runtime) Projects() []protocol.Project {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := make([]protocol.Project, 0, len(r.cfg.Projects))
	for _, id := range slices.Sorted(maps.Keys(r.cfg.Projects)) {
		p := r.cfg.Projects[id]
		list = append(list, protocol.Project{ID: id, Name: p.Name, Path: p.Path, GitHub: p.GitHub, Language: p.Language, Engine: p.Engine, Metadata: maps.Clone(p.Metadata)})
	}
	return list
}

func (r *Runtime) Status() protocol.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	providers := map[string]protocol.ProviderHealth{"exec": {Available: true}, "workflow": {Available: true}}
	for name, command := range map[string]string{"git": "git", "open": r.opener} {
		_, err := provider.ResolveExecutable(command, filepath.Dir(r.path), os.Environ())
		health := protocol.ProviderHealth{Available: err == nil}
		if err != nil {
			health.Code = string(protocol.ProviderUnavailable)
		}
		providers[name] = health
	}
	return protocol.Status{Version: version.Current().Version, UptimeMS: time.Since(r.started).Milliseconds(), ConfigPath: r.path, Project: r.context.Project, Mode: r.context.Mode, RunningJobs: r.jobs.Running(), Generation: r.generation, Providers: providers}
}

func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	jobErr := r.jobs.Shutdown(ctx)
	stateErr := r.state.Close(ctx)
	select {
	case <-r.state.Done():
		_ = r.stateLock.Close()
	default:
	}
	r.bus.Close()
	return errors.Join(jobErr, stateErr)
}
