package runtime

import (
	"context"
	"maps"
	"reflect"
	"slices"

	"patchbay/internal/config"
	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/internal/permission"
	"patchbay/pkg/protocol"
)

func (r *Runtime) Reload(ctx context.Context) (uint64, error) {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, fault.Safe(err)
	}
	candidate, err := config.Load(r.path)
	if err != nil {
		return 0, fault.New(protocol.InvalidConfig, "Configuration reload failed validation.")
	}
	registries, err := buildRegistries(candidate)
	if err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writable(ctx); err != nil {
		return 0, err
	}
	old := r.cfg
	if candidate.Server.Socket != old.Server.Socket || candidate.Server.ShutdownGrace != old.Server.ShutdownGrace || candidate.State != old.State || candidate.Events != old.Events {
		return 0, fault.New(protocol.InvalidConfig, "Socket, state, shutdown, or subscription settings require a restart.")
	}
	ctxState := cloneContext(r.context)
	if _, exists := candidate.Projects[ctxState.Project]; !exists {
		ctxState.Project = ""
	}
	if ctxState.Values == nil {
		ctxState.Values = map[string]string{}
	}
	for key, value := range candidate.Context.Defaults.Values {
		if _, exists := ctxState.Values[key]; !exists {
			ctxState.Values[key] = value
		}
	}
	parameters := cloneParameters(candidate.Parameters)
	for name, current := range r.parameters {
		p, exists := parameters[name]
		if !exists || p.Type != current.Type {
			continue
		}
		if value, err := p.ValidateValue(current.Value); err == nil {
			p.Value = value
			parameters[name] = p
		}
	}
	if err := r.persist(ctxState, parameters); err != nil {
		return 0, fault.New(protocol.InvalidConfig, "Reload state exceeds its size limit.")
	}
	previousContext, previousParameters := r.context, r.parameters
	r.cfg, r.registries, r.context, r.parameters = candidate, registries, ctxState, parameters
	r.generation++
	r.jobs.Reconfigure(jobLimits(candidate))
	r.bus.Emit(event.ConfigReloaded, map[string]uint64{"generation": r.generation})
	if previousContext.Project != ctxState.Project {
		r.bus.Emit(event.ProjectChanged, map[string]string{"project": ctxState.Project})
	}
	if !reflect.DeepEqual(previousContext, ctxState) {
		r.bus.Emit(event.ContextChanged, ctxState)
	}
	for _, name := range slices.Sorted(maps.Keys(parameters)) {
		if !reflect.DeepEqual(previousParameters[name], parameters[name]) {
			r.bus.Emit(event.ParameterChanged, map[string]any{"name": name, "value": parameters[name].Value})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(previousParameters)) {
		if _, exists := parameters[name]; !exists {
			r.bus.Emit(event.ParameterChanged, map[string]any{"name": name, "removed": true})
		}
	}
	return r.generation, nil
}

func (r *Runtime) actionRisk(name string, seen map[string]bool) permission.Permission {
	if seen[name] {
		return permission.Dangerous
	}
	seen[name] = true
	defer delete(seen, name)
	a, exists := r.registries[r.context.Project].Get(name)
	if !exists {
		return permission.Dangerous
	}
	risk := a.Safety
	if a.Type == "git" && a.Operation == "branch" {
		if _, exists := a.Inputs["mode"]; exists {
			risk = permission.Strongest(risk, permission.Confirm)
		}
	}
	if a.Type == "workflow" {
		for _, step := range r.cfg.Workflows[a.Workflow].Steps {
			risk = permission.Strongest(risk, r.actionRisk(step.Action, seen))
		}
	}
	return risk
}

func (r *Runtime) Actions() []protocol.Action {
	r.mu.Lock()
	defer r.mu.Unlock()
	registry := r.registries[r.context.Project]
	list := make([]protocol.Action, 0, len(registry.Names()))
	for _, name := range registry.Names() {
		a, _ := registry.Get(name)
		list = append(list, r.actionMetadata(name, a))
	}
	return list
}

func (r *Runtime) Action(name string) (protocol.Action, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, exists := r.registries[r.context.Project].Get(name)
	if !exists {
		return protocol.Action{}, fault.New(protocol.ActionNotFound, "Action not found.")
	}
	return r.actionMetadata(name, a), nil
}

// actionMetadata reads a published generation while its caller holds r.mu.
func (r *Runtime) actionMetadata(name string, a config.Action) protocol.Action {
	inputs := make(map[string]protocol.Input, len(a.Inputs))
	for key, input := range a.Inputs {
		inputs[key] = protocol.Input{Type: string(input.Type), Required: input.Required, Default: input.Default, Min: input.Min, Max: input.Max, Enum: slices.Clone(input.Enum)}
	}
	return protocol.Action{Name: name, Type: a.Type, Safety: string(r.actionRisk(name, map[string]bool{})), Inputs: inputs}
}

func (r *Runtime) Workflows() []protocol.Workflow {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := make([]protocol.Workflow, 0, len(r.cfg.Workflows))
	for _, name := range slices.Sorted(maps.Keys(r.cfg.Workflows)) {
		w := r.cfg.Workflows[name]
		steps := make([]protocol.WorkflowStep, 0, len(w.Steps))
		for _, step := range w.Steps {
			steps = append(steps, protocol.WorkflowStep{Action: step.Action})
		}
		list = append(list, protocol.Workflow{Name: name, StopOnError: *w.StopOnError, Steps: steps})
	}
	return list
}
