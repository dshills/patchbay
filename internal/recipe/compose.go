package recipe

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"patchbay/internal/binding"
	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/parameter"
	"patchbay/internal/permission"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
)

type Composition struct {
	Config   *config.Config
	Scopes   map[string]string
	Origins  map[string]protocol.RecipeOrigin
	Disabled []string
}

func BaseDigest(base *config.Config) string {
	data, _ := json.Marshal(base)
	return evidence.Digest(data)
}
func Namespace(id string) string { return "recipe." + id + "." }
func Project(entry Installation, m Manifest) string {
	for role, r := range m.Requirements {
		if r.Kind == "project" {
			return entry.Mappings[role].Project
		}
	}
	return ""
}
func Compose(base *config.Config, entries []Installation, packages map[string]*Package) (Composition, error) {
	var out Composition
	data, err := compositionYAML(base)
	if err != nil {
		return out, err
	}
	candidate, err := config.ParseComposition(data, "/", "/")
	if err != nil {
		return out, err
	}
	if candidate.Actions == nil {
		candidate.Actions = map[string]config.Action{}
	}
	if candidate.Workflows == nil {
		candidate.Workflows = map[string]config.Workflow{}
	}
	if candidate.Parameters == nil {
		candidate.Parameters = map[string]parameter.Definition{}
	}
	if candidate.Experiments == nil {
		candidate.Experiments = map[string]protocol.Experiment{}
	}
	out.Scopes = map[string]string{}
	out.Origins = map[string]protocol.RecipeOrigin{}
	for _, entry := range entries {
		if !entry.Active {
			continue
		}
		p := packages[entry.Content]
		if p == nil {
			return out, fail(entry.ID, "missing immutable package")
		}
		m := p.Manifest
		prefix := Namespace(entry.ID)
		project := Project(entry, m)
		if entry.BaseDigest != BaseDigest(base) {
			out.Disabled = append(out.Disabled, entry.ID+": host configuration changed; review activation again")
			continue
		}
		for _, name := range []map[string]config.Action{candidate.Actions} {
			for key := range name {
				if strings.HasPrefix(key, prefix) {
					return out, fail(entry.ID, "host or installation namespace collision")
				}
			}
		}
		for key := range candidate.Workflows {
			if strings.HasPrefix(key, prefix) {
				return out, fail(entry.ID, "workflow namespace collision")
			}
		}
		for key := range candidate.Parameters {
			if strings.HasPrefix(key, prefix) {
				return out, fail(entry.ID, "parameter namespace collision")
			}
		}
		for key := range candidate.Experiments {
			if strings.HasPrefix(key, prefix) {
				return out, fail(entry.ID, "experiment namespace collision")
			}
		}
		missing := map[string]bool{}
		for role, requirement := range m.Requirements {
			mapping := entry.Mappings[role]
			count := 0
			for _, v := range []string{mapping.Project, mapping.Tool, mapping.Action} {
				if v != "" {
					count++
				}
			}
			if count == 0 && requirement.Optional {
				missing[role] = true
				out.Disabled = append(out.Disabled, prefix+role+": optional role unmapped")
				continue
			}
			if count != 1 {
				return out, fail(role, "exactly one local mapping is required")
			}
			switch requirement.Kind {
			case "project":
				if mapping.Project == "" {
					return out, fail(role, "project mapping required")
				}
				if _, ok := base.Projects[mapping.Project]; !ok {
					return out, fail(role, "local project not found")
				}
			case "tool":
				if mapping.Tool == "" || !filepath.IsAbs(mapping.Tool) {
					return out, fail(role, "tool mapping requires an absolute executable path")
				}
				resolved, err := provider.ResolveExecutable(mapping.Tool, "/", os.Environ())
				if err != nil || resolved != mapping.Tool {
					return out, fail(role, "mapped executable is unavailable")
				}
			case "action":
				if mapping.Action == "" {
					return out, fail(role, "action mapping required")
				}
				a, ok := base.EffectiveActions(project)[mapping.Action]
				if !ok || a.Type != requirement.Provider {
					return out, fail(role, "mapped action provider differs")
				}
				if requirement.Operation != "" && a.Operation != requirement.Operation {
					return out, fail(role, "mapped operation differs")
				}
				if a.Type == "scpi" {
					d := base.Devices[a.Device]
					if d.Model != requirement.Model || a.Channel != requirement.Channel {
						return out, fail(role, "mapped instrument model/channel differs")
					}
					_, unit, lo, hi := d.ValueSpec(a.Operation)
					if requirement.Unit != "" && unit != requirement.Unit {
						return out, fail(role, "mapped units differ")
					}
					if requirement.Min != nil && (max(lo, *requirement.Min) > min(hi, *requirement.Max)) {
						return out, fail(role, "instrument limits do not intersect")
					}
				}
			}
		}
		for role := range entry.Mappings {
			if _, ok := m.Requirements[role]; !ok {
				return out, fail(role, "unknown mapping role")
			}
		}
		absent := map[string]bool{}
		actions := map[string]config.Action{}
		for name, a := range m.Actions {
			if missing[a.Reference] || missing[a.Tool] || missing[a.Project] {
				absent[name] = true
				continue
			}
			d := config.Action{Type: a.Type, Safety: permission.Strongest(permission.Confirm, a.Safety), Args: slices.Clone(a.Args), Inputs: a.Inputs, Target: a.Target, Operation: a.Operation, Workflow: a.Workflow, Timeout: a.Timeout, Origin: prefix}
			if a.Project != "" && (a.Type == "exec" || a.Type == "git") {
				d.Cwd = base.Projects[project].Path
			}
			switch a.Type {
			case "exec":
				d.Command = entry.Mappings[a.Tool].Tool
			case "workflow":
				d.Workflow = prefix + a.Workflow
			case "reference":
				d = base.EffectiveActions(project)[entry.Mappings[a.Reference].Action]
				d.Safety = permission.Strongest(permission.Confirm, permission.Strongest(d.Safety, a.Safety))
				d.Origin = prefix
				if a.Timeout != "" {
					d.Timeout = shorterTimeout(d.Timeout, a.Timeout)
				}
				requirement := m.Requirements[a.Reference]
				if d.Type == "scpi" && requirement.Min != nil {
					_, unit, lo, hi := base.Devices[d.Device].ValueSpec(d.Operation)
					lo, hi = max(lo, *requirement.Min), min(hi, *requirement.Max)
					if unit == "" {
						return out, fail(name, "operation has no numeric limits")
					}
					d.Inputs = maps.Clone(d.Inputs)
					if d.Inputs == nil {
						d.Inputs = map[string]config.Input{}
					}
					input := d.Inputs["value"]
					if d.Parameter != "" {
						param := base.Parameters[d.Parameter]
						input = config.Input{Type: param.Type, Default: param.Value, Min: param.Min, Max: param.Max, Sensitive: param.Sensitive}
						d.Parameter = ""
					}
					if input.Min != nil {
						value, parseErr := strconv.ParseFloat(fmtNumber(input.Min), 64)
						if parseErr != nil {
							return out, fail(name, "invalid host lower bound")
						}
						lo = max(lo, value)
					}
					if input.Max != nil {
						value, parseErr := strconv.ParseFloat(fmtNumber(input.Max), 64)
						if parseErr != nil {
							return out, fail(name, "invalid host upper bound")
						}
						hi = min(hi, value)
					}
					if lo > hi {
						return out, fail(name, "recipe, host input and device limits do not intersect")
					}
					input.Type = parameter.Float
					input.Min, input.Max = lo, hi
					d.Inputs["value"] = input
				}
				if len(a.Inputs) > 0 {
					d.Inputs = maps.Clone(d.Inputs)
					for key, input := range a.Inputs {
						host, exists := d.Inputs[key]
						if !exists || host.Type != input.Type {
							return out, fail(name, "host input schema differs")
						}
						merged, err := intersectInput(host, input)
						if err != nil {
							return out, fail(name, err.Error())
						}
						d.Inputs[key] = merged
					}
				}
			}
			actions[name] = d
		}
		// Missing optional roles disable their complete dependent action/workflow trees.
		disabledWorkflows := map[string]bool{}
		for changed := true; changed; {
			changed = false
			for name, w := range m.Workflows {
				if disabledWorkflows[name] {
					continue
				}
				for _, step := range w.Steps {
					if absent[step.Action] {
						disabledWorkflows[name] = true
						changed = true
						break
					}
				}
			}
			for name, a := range m.Actions {
				if !absent[name] && a.Type == "workflow" && disabledWorkflows[a.Workflow] {
					absent[name] = true
					changed = true
				}
			}
		}
		for name, a := range actions {
			if !absent[name] {
				candidate.Actions[prefix+name] = a
			}
		}
		for name, p := range m.Parameters {
			candidate.Parameters[prefix+name] = p
		}
		for name, w := range m.Workflows {
			if disabledWorkflows[name] {
				continue
			}
			w.Steps = slices.Clone(w.Steps)
			for i := range w.Steps {
				w.Steps[i].Action = prefix + w.Steps[i].Action
			}
			candidate.Workflows[prefix+name] = w
		}
		experiments := map[string]bool{}
		for name, definition := range m.Experiments {
			if absent[definition.Action] || disabledWorkflows[definition.Workflow] {
				out.Disabled = append(out.Disabled, prefix+name+": optional dependency unavailable")
				continue
			}
			e := evidence.Clone(definition)
			e.ID = prefix + name
			e.Projects = []string{project}
			if e.Action != "" {
				e.Action = prefix + e.Action
			}
			if e.Workflow != "" {
				e.Workflow = prefix + e.Workflow
			}
			for i := range e.Inputs {
				e.Inputs[i].Parameter = prefix + e.Inputs[i].Parameter
			}
			for i := range e.Parameters {
				e.Parameters[i] = prefix + e.Parameters[i]
			}
			for i := range e.Collectors {
				e.Collectors[i].Action = prefix + e.Collectors[i].Action
			}
			candidate.Experiments[e.ID] = e
			experiments[name] = true
			wrapper := prefix + "capture." + name
			if _, exists := candidate.Actions[wrapper]; exists {
				return out, fail(wrapper, "capture wrapper collides with an action")
			}
			candidate.Actions[wrapper] = config.Action{Type: "experiment", Experiment: e.ID, Safety: permission.Confirm, Origin: prefix}
		}
		for name, assignment := range entry.Assignments {
			control, ok := m.Controls[name]
			if !ok {
				return out, fail(name, "unknown suggested control")
			}
			if control.Action != "" && absent[control.Action] || control.Capture != "" && !experiments[control.Capture] || control.Baseline != "" && !experiments[control.Baseline] || control.Result != "" && !experiments[control.Result] {
				continue
			}
			target := &binding.Target{}
			if control.Action != "" {
				target.Action = prefix + control.Action
			}
			if control.Parameter != "" {
				target.Parameter = prefix + control.Parameter
			}
			if control.Capture != "" {
				target.Action = prefix + "capture." + control.Capture
			}
			if control.Baseline != "" {
				target.Baseline = prefix + control.Baseline
			}
			if control.Result != "" {
				target.Result = prefix + control.Result
			}
			b := binding.Binding{Device: assignment.Device, Control: assignment.Control, When: map[string]string{"project": project}}
			// A projectless installation gets a runtime scope check; empty project cannot
			// be expressed as a core binding predicate and is not assigned to hardware.
			if project == "" {
				return out, fail(name, "physical assignments require a mapped project")
			}
			switch assignment.Gesture {
			case "press":
				b.Press = target
			case "long_press":
				b.LongPress = target
			case "touch":
				b.Touch = target
			case "long_touch":
				b.LongTouch = target
			case "rotate":
				b.Rotate = target
			default:
				return out, fail(name, "unsupported control gesture")
			}
			candidate.Bindings = append(candidate.Bindings, b)
		}
		out.Scopes[prefix] = project
		out.Origins[prefix] = protocol.RecipeOrigin{Installation: entry.ID, DeclaredID: m.ID, Version: m.Version, Content: p.Digest}
	}
	data, err = compositionYAML(candidate)
	if err != nil {
		return out, err
	}
	out.Config, err = config.ParseComposition(data, "/", "/")
	if err != nil {
		return out, err
	}
	for name, a := range out.Config.Actions {
		for prefix := range out.Scopes {
			if strings.HasPrefix(name, prefix) {
				a.Origin = prefix
				out.Config.Actions[name] = a
			}
		}
	}
	return out, nil
}
func shorterTimeout(a, b string) string {
	left, _ := time.ParseDuration(a)
	right, _ := time.ParseDuration(b)
	if left == 0 || right < left {
		return b
	}
	return a
}
func Resolve(selection Selection, id string) (Installation, error) {
	var found *Installation
	for _, entry := range selection.Installations {
		if entry.ID == id || strings.EqualFold(entry.Alias, id) {
			if found != nil {
				return Installation{}, fail("installation", "ambiguous ID or alias")
			}
			copy := entry
			found = &copy
		}
	}
	if found == nil {
		return Installation{}, fail("installation", "not found")
	}
	return *found, nil
}
func (c Composition) Allows(name, project string) bool {
	for prefix, required := range c.Scopes {
		if strings.HasPrefix(name, prefix) {
			return required == project
		}
	}
	return true
}

// The validated host schema remains a floor. Portable declarations may narrow
// bounds/enum options and choose a default, but cannot remove host restrictions.
func intersectInput(host, requested config.Input) (config.Input, error) {
	host.Required = host.Required || requested.Required
	host.Sensitive = host.Sensitive || requested.Sensitive
	if requested.Default != nil {
		host.Default = requested.Default
	}
	if requested.Min != nil && (host.Min == nil || numberGreater(requested.Min, host.Min, host.Type)) {
		host.Min = requested.Min
	}
	if requested.Max != nil && (host.Max == nil || numberGreater(host.Max, requested.Max, host.Type)) {
		host.Max = requested.Max
	}
	if host.Min != nil && host.Max != nil && numberGreater(host.Min, host.Max, host.Type) {
		return host, fail("input", "bounds do not intersect")
	}
	if host.Type == parameter.Enum && len(requested.Enum) > 0 {
		values := []string{}
		for _, v := range host.Enum {
			if slices.Contains(requested.Enum, v) {
				values = append(values, v)
			}
		}
		if len(values) == 0 {
			return host, fail("input", "enum choices do not intersect")
		}
		host.Enum = values
	}
	return host, nil
}
func fmtNumber(value any) string {
	data, _ := json.Marshal(value)
	return strings.Trim(string(data), "\"")
}
func numberGreater(a, b any, kind parameter.Type) bool {
	if kind == parameter.Integer {
		x, _ := strconv.ParseInt(fmtNumber(a), 10, 64)
		y, _ := strconv.ParseInt(fmtNumber(b), 10, 64)
		return x > y
	}
	x, _ := strconv.ParseFloat(fmtNumber(a), 64)
	y, _ := strconv.ParseFloat(fmtNumber(b), 64)
	return x > y
}

// Normalization expands an instrument parameter into its derived device fields.
// Remove those derived fields in an owned copy before reusing strict source
// validation, which rightly rejects specifying both forms in user YAML.
func compositionYAML(c *config.Config) ([]byte, error) {
	copy := *c
	sourceActions := func(actions map[string]config.Action) map[string]config.Action {
		out := maps.Clone(actions)
		for name, a := range out {
			if a.Type == "scpi" && a.Parameter != "" {
				a.Device = ""
				a.Operation = ""
				a.Channel = 0
				out[name] = a
			}
		}
		return out
	}
	copy.Actions = sourceActions(c.Actions)
	copy.Projects = maps.Clone(c.Projects)
	for name, p := range copy.Projects {
		p.Actions = sourceActions(p.Actions)
		copy.Projects[name] = p
	}
	return yaml.Marshal(&copy)
}
