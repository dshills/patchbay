package config

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"patchbay/internal/binding"
	"patchbay/internal/parameter"
	"patchbay/internal/permission"
	"patchbay/internal/provider"
)

func keys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

func (v *validator) normalize(c *Config, baseDir, home string) error {
	if c.Version != 1 {
		return v.fail("version", "only version 1 is supported")
	}
	for _, entry := range []struct {
		path  string
		value *string
	}{
		{"server.socket", &c.Server.Socket}, {"state.path", &c.State.Path},
	} {
		resolved, err := resolvePath(*entry.value, baseDir, home)
		if err != nil {
			return v.fail(entry.path, "expected a filesystem path without named-user expansion")
		}
		*entry.value = resolved
	}
	paths := map[string]bool{}
	for _, path := range []string{c.Server.Socket, c.State.Path, c.Server.Socket + ".lock", c.State.Path + ".lock"} {
		if paths[path] {
			return v.fail("state.path", "state, socket, and lock paths must differ")
		}
		paths[path] = true
	}
	for _, entry := range []struct {
		path    string
		value   int64
		maximum int64
	}{
		{"server.max_request_bytes", c.Server.MaxRequestBytes, 64 << 20},
		{"jobs.concurrency", int64(c.Jobs.Concurrency), 1024},
		{"jobs.queue_capacity", int64(c.Jobs.QueueCapacity), 65536},
		{"jobs.history_limit", int64(c.Jobs.HistoryLimit), 65536},
		{"jobs.output_limit_bytes", c.Jobs.OutputLimitBytes, 64 << 20},
		{"events.subscriber_capacity", int64(c.Events.SubscriberCapacity), 65536},
	} {
		if entry.value <= 0 || entry.value > entry.maximum {
			return v.fail(entry.path, fmt.Sprintf("must be between 1 and %d", entry.maximum))
		}
	}
	for _, entry := range []struct{ path, value string }{
		{"server.shutdown_grace", c.Server.ShutdownGrace}, {"state.flush_interval", c.State.FlushInterval},
	} {
		if err := v.duration(entry.path, entry.value); err != nil {
			return err
		}
	}
	for _, group := range []struct {
		path  string
		names []string
	}{
		{"projects", keys(c.Projects)}, {"actions", keys(c.Actions)}, {"workflows", keys(c.Workflows)}, {"parameters", keys(c.Parameters)},
	} {
		for _, name := range group.names {
			if !namePattern.MatchString(name) {
				return v.fail(group.path, "names must start with an alphanumeric and contain only letters, digits, dot, underscore, or hyphen (128 characters maximum)")
			}
		}
	}
	for _, name := range keys(c.Parameters) {
		p := c.Parameters[name]
		if err := p.Normalize(); err != nil {
			return v.fail("parameters."+name, err.Error())
		}
		c.Parameters[name] = p
	}
	for _, name := range keys(c.Actions) {
		a := c.Actions[name]
		if err := v.action("actions."+name, &a, c, baseDir, home); err != nil {
			return err
		}
		c.Actions[name] = a
	}
	for _, id := range keys(c.Projects) {
		p := c.Projects[id]
		path := "projects." + id
		if p.ID != "" && p.ID != id {
			return v.fail(path+".id", "must match the project map key")
		}
		p.ID = id
		if strings.TrimSpace(p.Name) == "" {
			return v.fail(path+".name", "name is required")
		}
		resolved, err := resolvePath(p.Path, baseDir, home)
		if err != nil || strings.Contains(p.Path, "{{") {
			return v.fail(path+".path", "a literal filesystem path is required")
		}
		p.Path = resolved
		if p.GitHub != "" {
			u, err := url.Parse(p.GitHub)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return v.fail(path+".github", "expected an HTTPS URL without credentials")
			}
		}
		if err := v.environment(path+".environment", p.Environment, nil); err != nil {
			return err
		}
		for _, name := range keys(p.Actions) {
			base, exists := c.Actions[name]
			if !exists {
				return v.fail(path+".actions."+name, "override must name a global action")
			}
			a := p.Actions[name]
			if err := v.action(path+".actions."+name, &a, c, baseDir, home); err != nil {
				return err
			}
			a.Safety = permission.Strongest(base.Safety, a.Safety)
			p.Actions[name] = a
		}
		c.Projects[id] = p
	}
	if id := c.Context.Defaults.Project; id != "" {
		if _, ok := c.Projects[id]; !ok {
			return v.fail("context.defaults.project", "project does not exist")
		}
	}
	if mode := c.Context.Defaults.Mode; mode != "" && !namePattern.MatchString(mode) {
		return v.fail("context.defaults.mode", "invalid mode name")
	}
	for key := range c.Context.Defaults.Values {
		if !valueKeyPattern.MatchString(key) {
			return v.fail("context.defaults.values", "context keys must be identifiers")
		}
	}
	for _, name := range keys(c.Workflows) {
		w := c.Workflows[name]
		if len(w.Steps) == 0 || len(w.Steps) > 256 {
			return v.fail("workflows."+name+".steps", "workflows require between 1 and 256 steps")
		}
		if w.StopOnError == nil {
			enabled := true
			w.StopOnError = &enabled
		}
		c.Workflows[name] = w
	}
	if err := v.workflowGraph(c, c.Actions); err != nil {
		return err
	}
	for _, id := range keys(c.Projects) {
		if err := v.workflowGraph(c, c.EffectiveActions(id)); err != nil {
			return err
		}
	}
	return v.bindings(c)
}

func (v *validator) duration(path, value string) error {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return v.fail(path, "expected a positive Go duration such as 5s")
	}
	return nil
}

func (v *validator) environment(path string, environment map[string]string, inputs map[string]Input) error {
	for _, key := range keys(environment) {
		value := environment[key]
		if !valueKeyPattern.MatchString(key) {
			return v.fail(path, "environment keys must be identifiers")
		}
		if strings.ContainsRune(value, 0) {
			return v.fail(path+"."+key, "environment values cannot contain NUL")
		}
		if err := ValidateTemplate(value, inputs); err != nil {
			return v.fail(path+"."+key, err.Error())
		}
	}
	return nil
}

func (v *validator) action(path string, a *Action, c *Config, baseDir, home string) error {
	if a.Safety == "" {
		a.Safety = permission.Confirm
	}
	if !a.Safety.Valid() {
		return v.fail(path+".safety", "expected safe, confirm, or dangerous")
	}
	if a.Timeout != "" {
		if err := v.duration(path+".timeout", a.Timeout); err != nil {
			return err
		}
	}
	for _, key := range keys(a.Inputs) {
		if !valueKeyPattern.MatchString(key) {
			return v.fail(path+".inputs", "input names must be identifiers")
		}
		input := a.Inputs[key]
		if input.Required && input.Default != nil {
			return v.fail(path+".inputs."+key, "a required input cannot also have a default")
		}
		d := input.definition()
		if err := d.Normalize(); err != nil {
			return v.fail(path+".inputs."+key, err.Error())
		}
		input.Min, input.Max = d.Min, d.Max
		if input.Default != nil {
			input.Default = d.Value
		}
		a.Inputs[key] = input
	}
	switch a.Type {
	case "exec":
		if strings.TrimSpace(a.Command) == "" || strings.ContainsAny(a.Command, "\x00\r\n") || strings.Contains(a.Command, "{{") || strings.Contains(a.Command, "}}") {
			return v.fail(path+".command", "a literal executable is required")
		}
		if a.Target != "" || a.Operation != "" || a.Workflow != "" {
			return v.fail(path, "exec does not accept target, operation, or workflow")
		}
		if strings.Contains(a.Command, "/") || strings.HasPrefix(a.Command, "~") {
			resolved, err := resolvePath(a.Command, baseDir, home)
			if err != nil {
				return v.fail(path+".command", "invalid executable path")
			}
			a.Command = resolved
		}
	case "open":
		if a.Target == "" {
			return v.fail(path+".target", "target is required")
		}
		if a.Command != "" || len(a.Args) != 0 || a.Operation != "" || a.Workflow != "" || a.Cwd != "" || len(a.Environment) != 0 {
			return v.fail(path, "open accepts target, inputs, safety, and timeout only")
		}
		if !strings.Contains(a.Target, "{{") {
			u, err := url.Parse(a.Target)
			if err != nil {
				return v.fail(path+".target", "invalid open target")
			}
			if u.Scheme != "" {
				if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "file" {
					return v.fail(path+".target", "only file, http, and https URL schemes are supported")
				}
				if (u.Scheme == "http" || u.Scheme == "https") && u.Host == "" {
					return v.fail(path+".target", "URL requires a host")
				}
			} else {
				a.Target, err = resolvePath(a.Target, baseDir, home)
				if err != nil {
					return v.fail(path+".target", "invalid target path")
				}
			}
		}
	case "git":
		schema := provider.GitInputs(a.Operation)
		for key, input := range a.Inputs {
			if string(input.Type) != schema[key] {
				return v.fail(path+".inputs", "inputs must match the Git operation schema")
			}
		}
		if a.Command != "" || len(a.Args) != 0 || a.Target != "" || a.Workflow != "" {
			return v.fail(path, "git does not accept command, args, target, or workflow")
		}
		switch a.Operation {
		case "status", "diff", "log", "branch":
		case "pull", "push", "stash", "stash-pop":
			a.Safety = permission.Strongest(a.Safety, permission.Confirm)
		default:
			return v.fail(path+".operation", "unsupported Git operation")
		}
	case "workflow":
		if _, exists := c.Workflows[a.Workflow]; !exists {
			return v.fail(path+".workflow", "workflow does not exist")
		}
		if a.Command != "" || len(a.Args) != 0 || a.Target != "" || a.Operation != "" || a.Cwd != "" || len(a.Environment) != 0 || len(a.Inputs) != 0 {
			return v.fail(path, "workflow accepts workflow, safety, and timeout only")
		}
	default:
		return v.fail(path+".type", "supported providers are exec, open, git, and workflow")
	}
	if a.Cwd == "" && (a.Type == "exec" || a.Type == "git") {
		a.Cwd = baseDir
	}
	if a.Cwd != "" && !strings.Contains(a.Cwd, "{{") {
		resolved, err := resolvePath(a.Cwd, baseDir, home)
		if err != nil {
			return v.fail(path+".cwd", "invalid working directory")
		}
		a.Cwd = resolved
	}
	for _, field := range []struct{ name, value string }{{"cwd", a.Cwd}, {"target", a.Target}} {
		if strings.ContainsRune(field.value, 0) {
			return v.fail(path+"."+field.name, "NUL is unsupported")
		}
		if err := ValidateTemplate(field.value, a.Inputs); err != nil {
			return v.fail(path+"."+field.name, err.Error())
		}
	}
	for i, arg := range a.Args {
		p := fmt.Sprintf("%s.args[%d]", path, i)
		if strings.ContainsRune(arg, 0) {
			return v.fail(p, "NUL is unsupported")
		}
		if err := ValidateTemplate(arg, a.Inputs); err != nil {
			return v.fail(p, err.Error())
		}
	}
	return v.environment(path+".environment", a.Environment, a.Inputs)
}

// definition supplies a representative value when validating metadata for an
// input without a default. It does not create an invocation default.
func (i Input) definition() parameter.Definition {
	value := i.Default
	if value == nil {
		switch i.Type {
		case parameter.Integer, parameter.Float:
			value = 0
			if i.Min != nil {
				value = i.Min
			} else if i.Max != nil {
				value = i.Max
			}
		case parameter.Boolean:
			value = false
		case parameter.String:
			value = ""
		case parameter.Enum:
			if len(i.Enum) > 0 {
				value = i.Enum[0]
			}
		}
	}
	return parameter.Definition{Type: i.Type, Value: value, Min: i.Min, Max: i.Max, Enum: i.Enum}
}

// EffectiveActions returns a new map of complete project replacements. Callers
// must not mutate nested maps/slices in its definitions after config publication.
func (c *Config) EffectiveActions(projectID string) map[string]Action {
	result := maps.Clone(c.Actions)
	if result == nil {
		result = map[string]Action{}
	}
	for name, a := range c.Projects[projectID].Actions {
		result[name] = a
	}
	return result
}

func (v *validator) arguments(path string, args map[string]any, a Action) error {
	for _, key := range keys(args) {
		input, exists := a.Inputs[key]
		if !exists {
			return v.fail(path, "argument is not declared by the action")
		}
		d := input.definition()
		if err := d.Normalize(); err != nil {
			return v.fail(path, "invalid input metadata")
		}
		if _, err := d.ValidateValue(args[key]); err != nil {
			return v.fail(path+"."+key, err.Error())
		}
	}
	for _, key := range keys(a.Inputs) {
		input := a.Inputs[key]
		if _, exists := args[key]; input.Required && !exists {
			return v.fail(path, "required action argument is missing")
		}
	}
	return nil
}

func (v *validator) workflowGraph(c *Config, actions map[string]Action) error {
	for _, name := range keys(c.Workflows) {
		for i, step := range c.Workflows[name].Steps {
			path := fmt.Sprintf("workflows.%s.steps[%d]", name, i)
			a, exists := actions[step.Action]
			if !exists {
				return v.fail(path+".action", "action does not exist")
			}
			if err := v.arguments(path+".args", step.Args, a); err != nil {
				return err
			}
		}
	}
	visiting := map[string]bool{}
	depths := map[string]int{}
	sizes := map[string]int{}
	var visit func(string) (int, error)
	visit = func(name string) (int, error) {
		if visiting[name] {
			return 0, v.fail("workflows."+name, "recursive workflow cycle detected")
		}
		if depth := depths[name]; depth > 0 {
			return depth, nil
		}
		visiting[name] = true
		depth := 1
		size := 1
		for _, step := range c.Workflows[name].Steps {
			if a := actions[step.Action]; a.Type == "workflow" {
				childDepth, err := visit(a.Workflow)
				if err != nil {
					return 0, err
				}
				depth = max(depth, childDepth+1)
				size += sizes[a.Workflow]
			} else {
				size++
			}
			if size > 1024 {
				return 0, v.fail("workflows."+name, "workflow expands beyond 1024 actions")
			}
		}
		if depth > 32 {
			return 0, v.fail("workflows."+name, "workflow nesting exceeds 32 levels")
		}
		visiting[name] = false
		depths[name] = depth
		sizes[name] = size
		return depth, nil
	}
	for _, name := range keys(c.Workflows) {
		if _, err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func (v *validator) bindings(c *Config) error {
	for i, b := range c.Bindings {
		path := fmt.Sprintf("bindings[%d]", i)
		if !namePattern.MatchString(b.Control) {
			return v.fail(path+".control", "valid control name is required")
		}
		if b.Device != "" && !namePattern.MatchString(b.Device) {
			return v.fail(path+".device", "invalid device name")
		}
		if b.Press == nil && b.Release == nil && b.Rotate == nil && b.LongPress == nil && b.Touch == nil && b.LongTouch == nil {
			return v.fail(path, "at least one input gesture is required")
		}
		for _, key := range keys(b.When) {
			value := b.When[key]
			contextKey, isValue := strings.CutPrefix(key, "values.")
			validKey := key == "mode" || key == "project" || isValue && valueKeyPattern.MatchString(contextKey)
			if !validKey {
				return v.fail(path+".when", "conditions support mode, project, and values identifiers only")
			}
			if key == "mode" && !namePattern.MatchString(value) {
				return v.fail(path+".when.mode", "invalid mode name")
			}
			if key == "project" {
				if _, exists := c.Projects[value]; !exists {
					return v.fail(path+".when.project", "project does not exist")
				}
			}
		}
		for _, gesture := range []struct {
			name   string
			target *binding.Target
		}{{"press", b.Press}, {"release", b.Release}, {"rotate", b.Rotate}, {"long_press", b.LongPress}, {"touch", b.Touch}, {"long_touch", b.LongTouch}} {
			target := gesture.target
			if target == nil {
				continue
			}
			p := path + "." + gesture.name
			if gesture.name == "rotate" {
				param, exists := c.Parameters[target.Parameter]
				if target.Action != "" || len(target.Args) != 0 || !exists || (param.Type != parameter.Integer && param.Type != parameter.Float) {
					return v.fail(p, "rotation requires only a known numeric parameter")
				}
			} else {
				a, exists := c.Actions[target.Action]
				if !exists || target.Parameter != "" {
					return v.fail(p, "action gestures require a known action")
				}
				if projectID := b.When["project"]; projectID != "" {
					a = c.EffectiveActions(projectID)[target.Action]
					if err := v.arguments(p+".args", target.Args, a); err != nil {
						return err
					}
				} else {
					if err := v.arguments(p+".args", target.Args, a); err != nil {
						return err
					}
					for _, id := range keys(c.Projects) {
						if err := v.arguments(p+".args", target.Args, c.EffectiveActions(id)[target.Action]); err != nil {
							return err
						}
					}
				}
			}
		}
		for j := 0; j < i; j++ {
			if binding.Overlaps(c.Bindings[j], b) {
				return v.fail(path, fmt.Sprintf("ambiguous with bindings[%d] at the same precedence", j))
			}
		}
	}
	return nil
}
