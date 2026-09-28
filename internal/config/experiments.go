package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"patchbay/pkg/protocol"
)

func (v *validator) runSettings(c *Config, base, home string) error {
	if c.Runs.Path == "" {
		c.Runs.Path = filepath.Join(filepath.Dir(c.State.Path), "runs")
	}
	path, err := resolvePath(c.Runs.Path, base, home)
	if err != nil {
		return v.fail("runs.path", "expected a filesystem path")
	}
	c.Runs.Path = path
	for _, occupied := range []string{c.State.Path, c.State.Path + ".lock", c.Server.Socket, c.Server.Socket + ".lock"} {
		relative, _ := filepath.Rel(path, occupied)
		if relative == "." || filepath.IsLocal(relative) {
			return v.fail("runs.path", "run storage must not contain state, socket, or their locks")
		}
	}
	l := c.Runs
	if l.MaxRuns < 1 || l.MaxRuns > 1000 || l.MaxBytes < (16<<20)+l.MaxRunBytes || l.MaxBytes > 1<<30 || l.MaxRunBytes < 1024 || l.MaxRunBytes > 16<<20 || l.MaxRunBytes+1<<20 > l.MaxBytes || l.MaxArtifactBytes < 1 || l.MaxArtifactBytes > 4<<20 || l.MaxArtifactBytes > l.MaxRunBytes || l.MaxReceipts < 1 || l.MaxReceipts > 10000 {
		return v.fail("runs", "invalid storage limits; maximums are 1000 runs, 1 GiB total, 16 MiB per run, 4 MiB per artifact, and 10000 receipts")
	}
	return nil
}

// ExperimentSteps identifies the flattened leaves without changing the workflow's
// grouping, deadlines, or error policy. Configuration validation bounds recursion.
func (c *Config) ExperimentSteps(e protocol.Experiment, project string) ([]struct {
	Name       string
	Definition Action
	Args       map[string]any
}, error) {
	actions := c.EffectiveActions(project)
	leaves := []struct {
		Name       string
		Definition Action
		Args       map[string]any
	}{}
	remaining := 1024
	var action func(string, map[string]any, int) error
	var workflow func(string, int) error
	action = func(name string, args map[string]any, depth int) error {
		remaining--
		if remaining < 0 || depth > 32 {
			return fmt.Errorf("experiment exceeds workflow expansion limits")
		}
		a, exists := actions[name]
		if !exists {
			return fmt.Errorf("experiment action does not exist")
		}
		if a.Type == "experiment" {
			return fmt.Errorf("experiment targets cannot contain experiment actions")
		}
		if a.Type == "workflow" {
			return workflow(a.Workflow, depth+1)
		}
		leaves = append(leaves, struct {
			Name       string
			Definition Action
			Args       map[string]any
		}{name, a, maps.Clone(args)})
		return nil
	}
	workflow = func(name string, depth int) error {
		w, exists := c.Workflows[name]
		if !exists {
			return fmt.Errorf("experiment workflow does not exist")
		}
		for _, step := range w.Steps {
			if err := action(step.Action, step.Args, depth); err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	if e.Action != "" {
		err = action(e.Action, nil, 0)
	} else {
		err = workflow(e.Workflow, 0)
	}
	return leaves, err
}

func (v *validator) experiments(c *Config) error {
	if len(c.Experiments) > 128 {
		return v.fail("experiments", "at most 128 experiments are supported")
	}
	for _, id := range keys(c.Experiments) {
		e := c.Experiments[id]
		path := "experiments." + id
		if !ValidName(id) || e.SchemaVersion != 1 || e.ID != "" && e.ID != id || strings.TrimSpace(e.Title) == "" || len(e.Title) > 256 || len(e.Description) > 4096 || (e.Action == "") == (e.Workflow == "") {
			return v.fail(path, "requires schema_version 1, matching ID, title, and exactly one action or workflow")
		}
		e.ID = id
		seenParameters := map[string]bool{}
		for _, name := range e.Parameters {
			p, ok := c.Parameters[name]
			if !ok || p.Sensitive || seenParameters[name] || len(e.Parameters) > 128 {
				return v.fail(path+".parameters", "requires at most 128 unique non-sensitive parameters")
			}
			seenParameters[name] = true
		}
		if len(e.Collectors) == 0 || len(e.Collectors) > 32 || len(e.Layout) > 32 || len(e.Inputs) > 128 {
			return v.fail(path, "requires 1–32 collectors, at most 32 widgets, and at most 128 input mappings")
		}
		projects := e.Projects
		if len(projects) == 0 {
			projects = append([]string{""}, keys(c.Projects)...)
		}
		seenProjects := map[string]bool{}
		for _, project := range projects {
			if _, ok := c.Projects[project]; project != "" && !ok || seenProjects[project] {
				return v.fail(path+".projects", "project must exist and occur only once")
			}
			seenProjects[project] = true
			leaves, err := c.ExperimentSteps(e, project)
			if err != nil {
				return v.fail(path, err.Error())
			}
			mapped := map[string]bool{}
			for _, input := range e.Inputs {
				if input.Step < 0 || input.Step >= len(leaves) {
					return v.fail(path+".inputs", "input step is outside the flattened target")
				}
				a := &leaves[input.Step]
				declared, ok := a.Definition.Inputs[input.Input]
				p, exists := c.Parameters[input.Parameter]
				key := fmt.Sprintf("%d:%s", input.Step, input.Input)
				if !ok || !exists || p.Type != declared.Type || p.Sensitive || declared.Sensitive || mapped[key] {
					return v.fail(path+".inputs", "mapping requires unique, non-sensitive input and parameter with matching types")
				}
				if a.Args == nil {
					a.Args = map[string]any{}
				}
				a.Args[input.Input] = p.Value
				mapped[key] = true
			}
			for _, leaf := range leaves {
				for _, input := range leaf.Definition.Inputs {
					if input.Sensitive {
						return v.fail(path, "captures cannot retain actions with sensitive input declarations")
					}
				}
				if leaf.Definition.Parameter != "" && c.Parameters[leaf.Definition.Parameter].Sensitive {
					return v.fail(path, "captures cannot bind sensitive parameters")
				}
				if _, err := leaf.Definition.Arguments(leaf.Args); err != nil {
					return v.fail(path, "experiment inputs do not satisfy effective action arguments")
				}
			}
			names := map[string]protocol.Collector{}
			for _, collector := range e.Collectors {
				if !ValidName(collector.Name) || collector.Step < 0 || collector.Step >= len(leaves) || collector.Action != leaves[collector.Step].Name {
					return v.fail(path+".collectors", "collector requires a name and the exact flattened step/action identity")
				}
				if _, exists := names[collector.Name]; exists {
					return v.fail(path+".collectors", "collector names must be unique")
				}
				names[collector.Name] = collector
				if !slices.Contains([]string{"measurement", "series", "text"}, collector.Kind) || !slices.Contains([]string{"native", "json_stdout"}, collector.Source) || len(collector.Path) > 16 || len(collector.Unit) > 32 || len(collector.Quantity) > 128 || !slices.Contains([]string{"", "higher", "lower", "neutral"}, collector.Direction) {
					return v.fail(path+".collectors", "invalid collector kind, source, path, units, or direction")
				}
				if collector.Source == "json_stdout" && leaves[collector.Step].Definition.Type != "exec" {
					return v.fail(path+".collectors", "JSON stdout collectors require an exec action")
				}
				for _, segment := range collector.Path {
					if segment == "" || len(segment) > 128 {
						return v.fail(path+".collectors", "collector path segments must be bounded literal field names")
					}
				}
			}
			widgets := map[string]bool{}
			for _, widget := range e.Layout {
				if !ValidName(widget.ID) || widgets[widget.ID] || widget.Title == "" || len(widget.Title) > 256 || !slices.Contains([]string{"measurement", "series", "text"}, widget.Kind) {
					return v.fail(path+".layout", "invalid or duplicate widget")
				}
				collector, ok := names[widget.Reference]
				if !ok || collector.Kind != widget.Kind {
					return v.fail(path+".layout", "widget requires a collector of the same kind")
				}
				widgets[widget.ID] = true
			}
		}
		c.Experiments[id] = e
	}
	return nil
}
