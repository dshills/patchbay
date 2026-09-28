package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/permission"
	"patchbay/pkg/protocol"
)

type capturePreparation struct {
	preview    protocol.CapturePreview
	experiment protocol.Experiment
	plan       *prepared
	bytes      int
}

func (r *Runtime) Capabilities() protocol.Capabilities {
	return protocol.Capabilities{Features: map[string]int{"agent_patches": 1, "agent_supervision": 1, "agent_context": 1, "agent_proposals": 1, "outcome_collectors": 1, "recipes": 1, "experiment_preparation": 1, "run_store": 1, "capture": 1, "comparison": 1, "export": 1}, Schemas: map[string]int{"recipe": 1, "experiment": 1, "run": 1, "series": 1, "artifact": 1}}
}
func (r *Runtime) Experiments() protocol.ExperimentList {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := protocol.ExperimentList{Experiments: []protocol.Experiment{}}
	for _, id := range slices.Sorted(maps.Keys(r.cfg.Experiments)) {
		e := r.cfg.Experiments[id]
		if len(e.Projects) == 0 || slices.Contains(e.Projects, r.context.Project) {
			out.Experiments = append(out.Experiments, evidence.Clone(e))
		}
	}
	return out
}
func (r *Runtime) Evidence() (*evidence.Store, error) {
	r.mu.Lock()
	r.expireExports()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fault.New(protocol.ShuttingDown, "Daemon is shutting down.")
	}
	if r.runs == nil {
		return nil, fault.New(protocol.RecordingFailed, "Run storage is unavailable; inspect storage diagnostics.")
	}
	return r.runs, nil
}
func (r *Runtime) Storage() protocol.StoreStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs != nil {
		return r.runs.Status()
	}
	message := "Run storage could not be opened."
	if r.storageError != nil {
		message += " " + r.storageError.Error()
	}
	return protocol.StoreStatus{ReadOnly: true, Diagnostics: []string{message}}
}
func (r *Runtime) PrepareCapture(ctx context.Context, request protocol.CapturePrepare) (protocol.CapturePreview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prepareCapture(ctx, request, nil)
}
func (r *Runtime) prepareCapture(ctx context.Context, request protocol.CapturePrepare, wrapper *config.Action) (protocol.CapturePreview, error) {
	if err := r.writable(ctx); err != nil {
		return protocol.CapturePreview{}, err
	}
	e, ok := r.cfg.Experiments[request.Experiment]
	if !ok {
		return protocol.CapturePreview{}, fault.New(protocol.NotFound, "Experiment not found.")
	}
	if len(e.Projects) > 0 && !slices.Contains(e.Projects, r.context.Project) {
		return protocol.CapturePreview{}, fault.New(protocol.InvalidRequest, "Experiment is not available in this project.")
	}
	return r.prepareExperiment(ctx, e, wrapper, nil, nil)
}
func (r *Runtime) prepareExperiment(ctx context.Context, e protocol.Experiment, wrapper *config.Action, overrides, parameterOverrides map[string]any) (protocol.CapturePreview, error) {
	now := time.Now()
	for id, p := range r.captures {
		if !now.Before(p.preview.ExpiresAt) {
			delete(r.captures, id)
		}
	}
	if len(r.captures) >= 64 {
		return protocol.CapturePreview{}, fault.New(protocol.Busy, "Too many live preparation previews; wait for one to expire.")
	}
	bindings := &inputBindings{values: map[int]map[string]any{}}
	parameters := map[string]any{}
	for _, name := range e.Parameters {
		parameters[name] = r.parameters[name].Value
	}
	for _, mapping := range e.Inputs {
		p := r.parameters[mapping.Parameter]
		if p.Sensitive {
			return protocol.CapturePreview{}, fault.New(protocol.InvalidRequest, "Sensitive inputs cannot be captured.")
		}
		if value, ok := parameterOverrides[mapping.Parameter]; ok {
			p.Value = value
		}
		parameters[mapping.Parameter] = p.Value
		if bindings.values[mapping.Step] == nil {
			bindings.values[mapping.Step] = map[string]any{}
		}
		bindings.values[mapping.Step][mapping.Input] = p.Value
	}
	remaining := 1024
	var plan *prepared
	var err error
	if e.Action != "" {
		plan, err = r.prepareActionBound(ctx, e.Action, overrides, &remaining, bindings)
	} else {
		plan, err = r.prepareWorkflowBound(ctx, e.Workflow, &remaining, bindings)
	}
	if err != nil {
		return protocol.CapturePreview{}, err
	}
	if wrapper != nil {
		plan.risk = permission.Strongest(plan.risk, wrapper.Safety)
		if wrapper.Timeout != "" {
			timeout, _ := time.ParseDuration(wrapper.Timeout)
			if plan.timeout == 0 || timeout < plan.timeout {
				plan.timeout = timeout
			}
		}
	}
	if plan.risk == permission.Dangerous && !r.cfg.Security.AllowDangerousActions {
		return protocol.CapturePreview{}, fault.New(protocol.PermissionDenied, "Dangerous actions are disabled.")
	}
	steps := []protocol.PreparedStep{}
	var visit func(*prepared)
	visit = func(p *prepared) {
		if p.steps != nil {
			for _, child := range p.steps {
				visit(child)
			}
			return
		}
		step := protocol.PreparedStep{Index: len(steps), Action: p.name, Type: p.kind, Safety: string(p.risk), Inputs: evidence.Clone(p.inputs)}
		if p.command != nil {
			step.Executable, step.Arguments, step.Directory = p.command.Path, slices.Clone(p.command.Args), p.command.Dir
			for _, entry := range p.command.Env {
				key, _, _ := strings.Cut(entry, "=")
				step.Environment = append(step.Environment, key)
			}
		}
		if p.scpi != nil {
			definition, _ := r.registries[r.context.Project].Get(p.name)
			if definition.Parameter != "" {
				parameters[definition.Parameter] = p.scpi.Value
			}
			if p.scpi.Value != nil {
				if step.Inputs == nil {
					step.Inputs = map[string]any{}
				}
				step.Inputs["value"] = p.scpi.Value
			}
			step.Device, step.Operation = p.scpi.Device, p.scpi.Operation
		}
		steps = append(steps, step)
	}
	visit(plan)
	rawExperiment, _ := json.Marshal(e)
	preview := protocol.CapturePreview{ID: identity.New(), Experiment: e.ID, ExperimentDigest: evidence.Digest(rawExperiment), Instance: r.instance, Generation: r.generation, Revision: r.controlRevision, Context: protocol.Context{Project: r.context.Project, Mode: r.context.Mode, Values: maps.Clone(r.context.Values)}, Parameters: parameters, Steps: steps, Safety: string(plan.risk), ExpiresAt: now.Add(time.Minute).UTC()}
	// HMAC includes private effective command environments, prompt metadata, and
	// provider requests without turning low-entropy secrets into a public hash oracle.
	private, err := json.Marshal(struct {
		Preview protocol.CapturePreview
		Plan    any
	}{preview, privatePlan(plan)})
	bytes := len(private)
	for _, existing := range r.captures {
		bytes += existing.bytes
	}
	if err != nil || len(private) > 1<<20 || bytes > 16<<20 {
		return protocol.CapturePreview{}, fault.New(protocol.Busy, "Prepared plans exceed the private memory limit.")
	}
	mac := hmac.New(sha256.New, r.captureKey)
	_, _ = mac.Write(private)
	preview.Digest = hex.EncodeToString(mac.Sum(nil))
	r.captures[preview.ID] = capturePreparation{preview: preview, experiment: evidence.Clone(e), plan: plan, bytes: len(private)}
	return evidence.Clone(preview), nil
}
func privatePlan(p *prepared) any {
	children := []any{}
	for _, child := range p.steps {
		children = append(children, privatePlan(child))
	}
	return map[string]any{"name": p.name, "kind": p.kind, "risk": p.risk, "timeout": p.timeout, "command": p.command, "agent": p.agent, "scpi": p.scpi, "plugin": p.plugin, "stop_on_error": p.stopOnError, "children": children}
}

func (r *Runtime) SetBaseline(project, experiment string, update protocol.BaselineUpdate) (protocol.Baseline, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.setBaseline(project, experiment, update)
}
func (r *Runtime) setBaseline(project, experiment string, update protocol.BaselineUpdate) (protocol.Baseline, error) {
	if r.closed || r.runs == nil {
		return protocol.Baseline{}, fault.New(protocol.RecordingFailed, "Run storage is unavailable.")
	}
	e, ok := r.cfg.Experiments[experiment]
	if !ok || len(e.Projects) > 0 && !slices.Contains(e.Projects, project) {
		return protocol.Baseline{}, fault.New(protocol.NotFound, "Experiment not available in this project.")
	}
	if update.RunID != "" {
		run, err := r.runs.Get(update.RunID)
		if err != nil {
			return protocol.Baseline{}, err
		}
		raw, _ := json.Marshal(e)
		if run.ExperimentDigest != evidence.Digest(raw) {
			return protocol.Baseline{}, fault.New(protocol.IncompatibleResults, "Baseline experiment definition has changed.")
		}
	}
	value, err := r.runs.SetBaseline(project, experiment, update)
	if err == nil {
		r.invalidateControls()
	}
	return value, err
}
