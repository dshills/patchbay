package runtime

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"

	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/permission"
	"patchbay/internal/supervisor"
	"patchbay/internal/workflow"
	"patchbay/pkg/protocol"
)

type agentDefinition struct {
	InheritedEnvironment []string
	Kind, Target         string
	Project              config.Project
	Actions              map[string]config.Action
	Workflows            map[string]config.Workflow
	Experiment           *protocol.Experiment
	Parameters           map[string]any
}

func (r *Runtime) agentDefinition(kind, target string) (agentDefinition, error) {
	bad := func() (agentDefinition, error) {
		return agentDefinition{}, fault.New(protocol.PermissionDenied, "Agent targets may contain only explicitly granted exec or read-only Git actions; protected providers and dangerous operations are excluded.")
	}
	d := agentDefinition{Kind: kind, Target: target, Project: r.cfg.Projects[r.context.Project], Actions: map[string]config.Action{}, Workflows: map[string]config.Workflow{}, Parameters: map[string]any{}}
	d.InheritedEnvironment = os.Environ()
	slices.Sort(d.InheritedEnvironment)
	d.Project.AgentGrants = nil
	d.Project.Actions = nil // only effective reachable actions enter the digest
	budget := 1024
	visiting := map[string]bool{}
	var action func(string) error
	workflow := func(name string) error {
		if visiting[name] {
			return fault.New(protocol.PermissionDenied, "Recursive workflow is excluded.")
		}
		w, ok := r.cfg.Workflows[name]
		if !ok {
			return fault.New(protocol.NotFound, "Workflow not found.")
		}
		if !r.recipeComposition.Allows(name, r.context.Project) {
			return fault.New(protocol.PermissionDenied, "Recipe belongs to another project.")
		}
		visiting[name] = true
		defer delete(visiting, name)
		d.Workflows[name] = w
		for _, step := range w.Steps {
			if err := action(step.Action); err != nil {
				return err
			}
		}
		return nil
	}
	action = func(name string) error {
		budget--
		if budget < 0 {
			return fault.New(protocol.PermissionDenied, "Target expansion exceeds 1024 actions.")
		}
		a, ok := r.registries[r.context.Project].Get(name)
		if !ok {
			return fault.New(protocol.NotFound, "Action not found.")
		}
		if !r.recipeComposition.Allows(name, r.context.Project) {
			return fault.New(protocol.PermissionDenied, "Recipe belongs to another project.")
		}
		if a.Safety == permission.Dangerous {
			_, err := bad()
			return err
		}
		d.Actions[name] = a
		switch a.Type {
		case "exec":
			return nil
		case "git":
			if slices.Contains([]string{"status", "diff", "log"}, a.Operation) {
				return nil
			}
		case "workflow":
			return workflow(a.Workflow)
		}
		_, err := bad()
		return err
	}
	var err error
	switch kind {
	case "patch":
		if !r.cfg.Agents.Proposals.Patches || (r.patches == nil || !r.patches.Writable()) || target != "workspace.apply_patch" || len(d.Project.AgentPatchPaths) == 0 {
			return bad()
		}
		for _, name := range d.Project.AgentPatchPaths {
			if r.protectedContextPath(d.Project.Path, name) {
				return bad()
			}
		}
		d.Parameters["patch_format"] = "exact-unified-v1;10-files;256KiB-file;1MiB-content;64KiB-diff"
	case "action":
		err = action(target)
	case "workflow":
		err = workflow(target)
	case "experiment":
		e, ok := r.cfg.Experiments[target]
		if !ok {
			return bad()
		}
		if len(e.Projects) > 0 && !slices.Contains(e.Projects, r.context.Project) {
			return bad()
		}
		d.Experiment = &e
		for _, m := range e.Inputs {
			d.Parameters[m.Parameter] = r.cfg.Parameters[m.Parameter]
		}
		for _, p := range e.Parameters {
			d.Parameters[p] = r.cfg.Parameters[p]
		}
		if e.Action != "" {
			err = action(e.Action)
		} else {
			err = workflow(e.Workflow)
		}
	default:
		return bad()
	}
	return d, err
}
func publicInput(input config.Input) protocol.Input {
	return protocol.Input{Type: string(input.Type), Sensitive: input.Sensitive, Required: input.Required, Min: input.Min, Max: input.Max, Enum: slices.Clone(input.Enum), Default: func() any {
		if input.Sensitive {
			return nil
		}
		return input.Default
	}()}
}
func (r *Runtime) definitionInputs(d agentDefinition) map[string]config.Input {
	out := map[string]config.Input{}
	if d.Kind == "action" {
		for k, v := range d.Actions[d.Target].Inputs {
			out[k] = v
		}
	}
	if d.Kind == "experiment" {
		for _, mapping := range d.Experiment.Inputs {
			p := r.parameters[mapping.Parameter]
			out[mapping.Parameter] = config.Input{Type: p.Type, Default: p.Value, Min: p.Min, Max: p.Max, Enum: p.Enum, Sensitive: p.Sensitive}
		}
	}
	return out
}
func (r *Runtime) inspectAgentGrant(kind, target string) (supervisor.GrantReview, error) {
	d, err := r.agentDefinition(kind, target)
	if err != nil {
		return supervisor.GrantReview{}, err
	}
	out := supervisor.GrantReview{Project: r.context.Project, Target: supervisor.Target{Paths: slices.Clone(d.Project.AgentPatchPaths), Kind: kind, ID: target, Digest: r.sessions.Sign(d), Inputs: map[string]protocol.Input{}}, Actions: map[string]any{}, Workflows: map[string]any{}, Experiment: d.Experiment, Warning: "Configured exec commands run as your user and may write files or use the network. Review the full definitions before adding this digest to local project grants."}
	if kind == "patch" {
		out.Warning = "Grants exact reviewed edits to these tracked files only. Each patch still needs approval. Private preimages are retained; validation, commits and pushes require separate actions."
	}
	for key, input := range r.definitionInputs(d) {
		out.Target.Inputs[key] = publicInput(input)
	}
	for name, a := range d.Actions {
		inputs := map[string]protocol.Input{}
		for k, v := range a.Inputs {
			inputs[k] = publicInput(v)
		}
		env := slices.Sorted(maps.Keys(a.Environment))
		out.Actions[name] = map[string]any{"type": a.Type, "safety": a.Safety, "command": a.Command, "arguments": a.Args, "directory": a.Cwd, "environment_names": env, "inputs": inputs, "workflow": a.Workflow, "operation": a.Operation, "timeout": a.Timeout}
	}
	for name, w := range d.Workflows {
		copy := w
		copy.Steps = make([]workflow.WorkflowStep, len(w.Steps))
		for i, step := range w.Steps {
			copy.Steps[i] = step
			copy.Steps[i].Args = maps.Clone(step.Args)
			definition := d.Actions[step.Action]
			for key, input := range definition.Inputs {
				if input.Sensitive {
					if _, ok := copy.Steps[i].Args[key]; ok {
						copy.Steps[i].Args[key] = "<redacted: input." + key + ">"
					}
				}
			}
		}
		out.Workflows[name] = copy
	}
	return out, nil
}
func (r *Runtime) InspectAgentGrant(ctx context.Context, kind, target string) (supervisor.GrantReview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.agentReady(ctx); err != nil {
		return supervisor.GrantReview{}, err
	}
	return r.inspectAgentGrant(kind, target)
}
func (r *Runtime) grantedTarget(kind, target string) (supervisor.Target, config.AgentGrant, agentDefinition, error) {
	d, err := r.agentDefinition(kind, target)
	if err != nil {
		return supervisor.Target{}, config.AgentGrant{}, d, err
	}
	digest := r.sessions.Sign(d)
	for _, grant := range r.cfg.Projects[r.context.Project].AgentGrants {
		if grant.Kind != kind || grant.Target != target {
			continue
		}
		if grant.Digest != digest {
			return supervisor.Target{}, grant, d, fault.New(protocol.PermissionDenied, "Effective target changed; review and replace its local grant digest.")
		}
		inputs := r.definitionInputs(d)
		public := supervisor.Target{Paths: slices.Clone(d.Project.AgentPatchPaths), Kind: kind, ID: target, Digest: digest, Inputs: map[string]protocol.Input{}}
		for key, bound := range grant.Inputs {
			original, ok := inputs[key]
			if !ok || original.Sensitive || original.Type != bound.Type || reservedAgentInput(key) {
				return supervisor.Target{}, grant, d, fault.New(protocol.PermissionDenied, "Grant input must match a declared non-sensitive input.")
			}
			input := publicInput(bound)
			input.Required = original.Required
			input.Default = original.Default
			public.Inputs[key] = input
		}
		return public, grant, d, nil
	}
	return supervisor.Target{}, config.AgentGrant{}, d, fault.New(protocol.PermissionDenied, "Target has no current explicit project grant.")
}
func (r *Runtime) agentTargets() []supervisor.Target {
	out := []supervisor.Target{}
	for _, grant := range r.cfg.Projects[r.context.Project].AgentGrants {
		target, _, _, err := r.grantedTarget(grant.Kind, grant.Target)
		if err == nil {
			out = append(out, target)
		}
	}
	return out
}
func (r *Runtime) validateSuggestion(s supervisor.Suggestion) (supervisor.Target, map[string]any, error) {
	target, grant, d, err := r.grantedTarget(s.Kind, s.Target)
	if err != nil {
		return target, nil, err
	}
	if s.Kind == "patch" {
		if s.Target != "workspace.apply_patch" || len(s.Inputs) != 0 || s.Diff == "" || len(s.Diff) > supervisor.MaxOutput || s.Baseline != "" {
			return target, nil, fault.New(protocol.InvalidProposal, "Invalid bounded patch suggestion.")
		}
		return target, nil, nil
	}
	original := r.definitionInputs(d)
	values := map[string]any{}
	for key, value := range s.Inputs {
		bound, ok := grant.Inputs[key]
		input, declared := original[key]
		if !ok || !declared || input.Sensitive {
			return target, nil, fault.New(protocol.PermissionDenied, "Suggestion supplies an undeclared, ungranted or sensitive input.")
		}
		// Checking both schemas implements intersection even if local bounds are wider.
		for _, schema := range []config.Input{input, bound} {
			schema.Required = true
			schema.Default = nil
			out, err := (config.Action{Inputs: map[string]config.Input{key: schema}}).Arguments(map[string]any{key: value})
			if err != nil {
				return target, nil, fault.New(protocol.InvalidProposal, "Suggestion input is outside its declared and granted bounds.")
			}
			values[key] = out[key]
		}
	}
	if s.Kind == "workflow" && len(s.Inputs) != 0 {
		return target, nil, fault.New(protocol.InvalidProposal, "Workflows do not accept top-level model inputs.")
	}
	if s.Kind == "action" {
		if _, err := d.Actions[d.Target].Arguments(values); err != nil {
			return target, nil, fault.New(protocol.InvalidProposal, "Suggestion omits a required action input.")
		}
	}
	// Grant bounds also constrain inherited defaults/current parameter values.
	for key, bound := range grant.Inputs {
		value, ok := values[key]
		if !ok {
			value = original[key].Default
		}
		if value == nil && bound.Required {
			return target, nil, fault.New(protocol.InvalidProposal, "A grant-required input is missing.")
		}
		if value != nil {
			bound.Default = nil
			bound.Required = true
			if _, err := (config.Action{Inputs: map[string]config.Input{key: bound}}).Arguments(map[string]any{key: value}); err != nil {
				return target, nil, fault.New(protocol.InvalidProposal, "Effective input is outside its local grant bounds.")
			}
		}
	}
	if s.Baseline != "" {
		run, err := r.runs.Get(s.Baseline)
		if err != nil || run.Project != r.context.Project {
			return target, nil, fault.New(protocol.InvalidProposal, "Expected baseline is unavailable in this project.")
		}
	}
	return target, values, nil
}
func (r *Runtime) agentSourceCurrent(ctx context.Context, s supervisor.Session) error {
	if err := r.agentReady(ctx); err != nil {
		return err
	}
	if s.Instance != r.instance || s.Generation != r.generation || s.Revision != r.controlRevision || s.Project != r.context.Project || s.Preconditions != r.recipeInputDigest() {
		return fault.New(protocol.ContextChanged, "Session project, configuration or input revisions changed.")
	}
	// An un-reloaded on-disk policy edit is still an invalidation, not permission.
	fresh, err := config.Load(r.path)
	if err != nil {
		return fault.New(protocol.ContextChanged, "Local configuration is unavailable.")
	}
	before, _ := json.Marshal(r.baseConfig)
	after, _ := json.Marshal(fresh)
	if string(before) != string(after) {
		return fault.New(protocol.ContextChanged, "Local configuration changed; reload and review again.")
	}
	return r.checkAgentItems(ctx, r.cfg.Projects[s.Project].Path, s.Items, s.Source)
}
func (r *Runtime) maskedAgentSteps(plan *prepared) []protocol.PreparedStep {
	out := []protocol.PreparedStep{}
	var visit func(*prepared)
	visit = func(p *prepared) {
		if p.steps != nil {
			for _, child := range p.steps {
				visit(child)
			}
			return
		}
		a, _ := r.registries[r.context.Project].Get(p.name)
		inputs := evidence.Clone(p.inputs)
		for k, def := range a.Inputs {
			if def.Sensitive {
				inputs[k] = "<redacted: input." + k + ">"
			}
		}
		step := protocol.PreparedStep{Index: len(out), Action: p.name, Type: p.kind, Safety: string(permission.Strongest(p.risk, permission.Confirm)), Inputs: inputs}
		if p.command != nil {
			step.Executable = p.command.Path
			step.Directory = p.command.Dir
			step.Arguments = slices.Clone(p.command.Args)
			vars := r.variables(inputs)
			if a.Type == "exec" {
				for i, arg := range a.Args {
					if value, err := config.Render(arg, vars); err == nil {
						step.Arguments[i] = value
					}
				}
			}
			if a.Type == "git" {
				maskGitInputs(&step, a)
			}
			if a.Cwd != "" {
				for key, def := range a.Inputs {
					if def.Sensitive && strings.Contains(a.Cwd, ".args."+key) {
						step.Directory = "<redacted: directory uses input." + key + ">"
					}
				}
			}
			for _, entry := range p.command.Env {
				key, _, _ := strings.Cut(entry, "=")
				step.Environment = append(step.Environment, key)
			}
		}
		out = append(out, step)
	}
	visit(plan)
	return out
}

func reservedAgentInput(name string) bool {
	return slices.Contains([]string{"confirmed", "safety", "command", "executable", "argv", "shell", "cwd", "directory", "env", "environment", "provider", "model", "api_key", "project", "project_id"}, strings.ToLower(name))
}

// Git has a closed semantic option surface. Mask only the argument belonging to
// that input, preserving unrelated flags and every original argument position.
func maskGitInputs(step *protocol.PreparedStep, a config.Action) {
	marker := func(key string) string { return "<redacted: input." + key + ">" }
	if a.Operation == "log" && a.Inputs["limit"].Sensitive {
		for i, arg := range step.Arguments {
			if strings.HasPrefix(arg, "--max-count=") {
				step.Arguments[i] = "--max-count=" + marker("limit")
			}
		}
	}
	if a.Operation == "diff" {
		separator := -1
		for i, arg := range step.Arguments {
			if arg == "--" {
				separator = i
				break
			}
		}
		if separator >= 0 && a.Inputs["path"].Sensitive && len(step.Arguments) > separator+1 {
			step.Arguments[separator+1] = marker("path")
		}
		if separator >= 0 && a.Inputs["staged"].Sensitive {
			for i := 0; i < separator; i++ {
				if step.Arguments[i] == "--cached" {
					step.Arguments[i] = marker("staged")
				}
			}
		}
	}
}
