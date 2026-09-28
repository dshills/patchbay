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
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/job"
	"patchbay/internal/localfs"
	"patchbay/internal/logging"
	"patchbay/internal/parameter"
	"patchbay/internal/provider"
	"patchbay/internal/recipe"
	"patchbay/internal/state"
	"patchbay/internal/supervisor"
	"patchbay/internal/version"
	"patchbay/pkg/protocol"
)

type Options struct {
	Supervisor supervisor.Options
	Recipes    recipe.StoreOptions
	Evidence   evidence.Options
	Log        io.Writer
	Runner     provider.Runner
	Opener     string
	Agent      provider.Agent
}
type Runtime struct {
	agentSelection       protocol.AgentSelection
	agentReview          string
	agentReviewUntil     time.Time
	proposalPreviews     map[string]agentProposalPreparation
	sessions             *supervisor.Store
	sessionError         error
	contexts             map[string]agentContextPreparation
	baseConfig           *config.Config
	recipes              *recipe.Store
	recipeError          error
	recipeComposition    recipe.Composition
	recipePreviews       map[string]recipePreparation
	recipeExports        map[string]recipeExportPreparation
	compositionUncertain bool
	exports              map[string]exportPreparation
	runs                 *evidence.Store
	storageError         error
	captureKey           []byte
	captures             map[string]capturePreparation
	mu                   sync.Mutex
	reloadMu             sync.Mutex
	cfg                  *config.Config
	registries           map[string]*action.Registry[config.Action]
	context              runtimecontext.RuntimeContext
	parameters           map[string]parameter.Definition
	generation           uint64
	instance             string
	controlRevision      uint64
	confirmations        map[string]controlConfirmation
	closed               bool
	path                 string
	started              time.Time
	bus                  *event.Bus
	jobs                 *job.Manager
	state                *state.Writer
	stateLock            *localfs.Lock
	runner               provider.Runner
	opener               string
	agent                provider.Agent
	scpi                 *provider.SCPI
	plugins              *provider.Plugins
	synchronization      map[string]parameter.Synchronization
	log                  *logging.Logger
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
	if options.Agent == nil {
		options.Agent = provider.NewCodex()
	}
	r := &Runtime{baseConfig: c, cfg: c, registries: registries, path: path, started: time.Now(), generation: 1, instance: identity.New(), controlRevision: 1, runner: options.Runner, opener: options.Opener, log: logging.New(options.Log)}
	r.agent = options.Agent
	r.scpi = provider.NewSCPI(c.Devices)
	r.plugins = provider.NewPlugins(c.Plugins)
	r.synchronization = map[string]parameter.Synchronization{}
	r.context = cloneContext(c.Context.Defaults)
	r.parameters = cloneParameters(c.Parameters)
	r.stateLock, err = localfs.Acquire(c.State.Path + ".lock")
	if err != nil {
		return nil, err
	}
	r.initRecipes(c, options.Recipes)
	c = r.cfg
	r.parameters = cloneParameters(c.Parameters)
	saved, err := state.Read(c.State.Path)
	if err != nil && !errors.Is(err, state.ErrRecovered) {
		_ = r.stateLock.Close()
		if r.recipes != nil {
			_ = r.recipes.Close()
		}
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
				r.captures = map[string]capturePreparation{}
			}
		}
	}
	r.runs, r.storageError = evidence.Open(c.Runs.Path, evidence.Limits{MaxRuns: c.Runs.MaxRuns, MaxBytes: c.Runs.MaxBytes, MaxRunBytes: c.Runs.MaxRunBytes, MaxArtifactBytes: c.Runs.MaxArtifactBytes, MaxReceipts: c.Runs.MaxReceipts}, options.Evidence)
	r.sessions, r.sessionError = supervisor.Open(c.State.Path+".agents", options.Supervisor)
	r.reconcileAgentRuns()
	r.contexts = map[string]agentContextPreparation{}
	r.proposalPreviews = map[string]agentProposalPreparation{}
	r.captureKey = []byte(identity.New() + identity.New())
	r.captures = map[string]capturePreparation{}
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
		if value.Instrument != nil {
			binding := *value.Instrument
			value.Instrument = &binding
		}
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
	if r.compositionUncertain {
		return fault.New(protocol.RecordingFailed, "Recipe selection durability is uncertain; restart before new work.")
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
	r.invalidateControls()
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
		list = append(list, r.parameterSnapshot(name, p))
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
	return r.parameterSnapshot(name, p), nil
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
		r.captures = map[string]capturePreparation{}
		if p.Persistent {
			if err := r.persist(r.context, r.parameters); err != nil {
				r.parameters[name] = previous
				return parameter.Parameter{}, fault.New(protocol.InvalidRequest, "Persistent state is too large.")
			}
		}
		change := map[string]any{"name": name, "value": value}
		if p.Instrument != nil {
			sync := r.synchronization[name]
			sync.Status = "pending"
			sync.Desired = value
			sync.ErrorCode = ""
			r.synchronization[name] = sync
			change["synchronization"] = sync
			r.invalidateControls()
		}
		r.bus.Emit(event.ParameterChanged, change)
	}
	p.Enum = slices.Clone(p.Enum)
	return r.parameterSnapshot(name, p), nil
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
	agentHealth := r.agent.Health(context.Background())
	if r.cfg.Agents.Codex.Model == "" {
		agentHealth = provider.Health{Code: "not_configured"}
	}
	providers["codex"] = protocol.ProviderHealth{Available: agentHealth.Available, Code: agentHealth.Code}
	devices := map[string]protocol.InstrumentStatus{}
	for id, d := range r.cfg.Devices {
		h := r.scpi.Health(id)
		providers["scpi:"+id] = protocol.ProviderHealth{Available: h.Available, Code: h.Code}
		devices[id] = protocol.InstrumentStatus{Profile: d.Profile, Model: d.Model, Firmware: d.Firmware, Address: d.Address, Shutdown: d.Shutdown, Capabilities: scpiCapabilities(d)}
	}
	plugins := map[string]protocol.PluginStatus{}
	for id, definition := range r.cfg.Plugins {
		snapshot := r.plugins.Snapshot(id)
		providers["plugin:"+id] = protocol.ProviderHealth{Available: snapshot.Health.Available, Code: snapshot.Health.Code}
		operations := map[string]string{}
		for name := range definition.Operations {
			operations[name] = string(r.plugins.Risk(id, name))
		}
		plugins[id] = protocol.PluginStatus{Protocol: 1, Operations: operations, Discovered: len(snapshot.Operations) > 0}
	}
	for name, command := range map[string]string{"git": "git", "open": r.opener} {
		_, err := provider.ResolveExecutable(command, filepath.Dir(r.path), os.Environ())
		health := protocol.ProviderHealth{Available: err == nil}
		if err != nil {
			health.Code = string(protocol.ProviderUnavailable)
		}
		providers[name] = health
	}
	return protocol.Status{Instance: r.instance, Version: version.Current().Version, UptimeMS: time.Since(r.started).Milliseconds(), ConfigPath: r.path, Project: r.context.Project, Mode: r.context.Mode, RunningJobs: r.jobs.Running(), Generation: r.generation, Providers: providers, Devices: devices, Plugins: plugins}
}

func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	for id, p := range r.exports {
		p.release()
		delete(r.exports, id)
	}
	r.mu.Unlock()
	jobErr := r.jobs.Shutdown(ctx)
	if jobErr == nil && r.runs != nil {
		_ = r.runs.Close()
	}
	if jobErr == nil && r.recipes != nil {
		_ = r.recipes.Close()
	}
	if jobErr == nil && r.sessions != nil {
		_ = r.sessions.Close()
	}
	pluginErr := r.plugins.Close(ctx)
	scpiErr := r.scpi.Close(ctx)
	if closer, ok := r.agent.(io.Closer); ok {
		_ = closer.Close()
	}
	stateErr := r.state.Close(ctx)
	select {
	case <-r.state.Done():
		_ = r.stateLock.Close()
	default:
	}
	r.bus.Close()
	return errors.Join(jobErr, pluginErr, scpiErr, stateErr)
}
