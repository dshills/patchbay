// Run from the repository root with go run ./scripts/generate_recipes.go.
// This maintainer tool regenerates the portable example; it executes no recipe.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/parameter"
	"patchbay/internal/recipe"
	"patchbay/internal/workflow"
	"patchbay/pkg/protocol"
	"path/filepath"
	"time"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func generate() error {
	if err := benchmark(); err != nil {
		return err
	}
	if err := checkup(); err != nil {
		return err
	}
	return rigol()
}
func benchmark() error {
	cfg, err := config.Load("configs/benchmark.yaml")
	if err != nil {
		return err
	}
	m := recipe.Manifest{SchemaVersion: 1, ID: "benchmark", Name: "Benchmark Playground", Description: "Try an offline repeated CPU experiment and compare saved results.", Author: "Patchbay contributors", License: "MIT", Version: "1.0.0", Features: map[string]int{"capture": 1, "comparison": 1}, Schemas: map[string]int{"experiment": 1, "series": 1}, Requirements: map[string]recipe.Requirement{"benchmark": {Kind: "project", Description: "Choose a local project for this experiment."}, "demo": {Kind: "tool", Description: "Map the deckdemo executable from a verified Patchbay bundle."}}, Actions: map[string]recipe.Action{}, Parameters: cfg.Parameters, Experiments: cfg.Experiments, Controls: map[string]recipe.Control{"capture": {Capture: "benchmark"}, "baseline": {Baseline: "benchmark"}, "result": {Result: "benchmark"}, "iterations": {Parameter: "iterations"}}}
	a := cfg.Actions["benchmark.measure"]
	m.Actions["benchmark.measure"] = recipe.Action{Type: "exec", Tool: "demo", Project: "benchmark", Args: a.Args, Inputs: a.Inputs, Safety: a.Safety, Timeout: a.Timeout}
	license, err := os.ReadFile("LICENSE")
	if err != nil {
		return err
	}
	files := map[string][]byte{"LICENSE": license, "README.md": []byte("# Benchmark Playground\n\nMap a project and the bundled deckdemo executable, then review activation. Capture a run, choose a baseline, change iterations and capture again. Sample values are illustrative, not measurements from your computer. Nothing is executed by inspection or import.\n")}
	for _, sample := range evidence.Samples().Samples {
		data, err := json.MarshalIndent(sample, "", "  ")
		if err != nil {
			return err
		}
		files["samples/"+sample.ID+".json"] = append(data, '\n')
	}
	return writeRecipe("benchmark", m, files)
}
func writeRecipe(folder string, m recipe.Manifest, files map[string][]byte) error {
	license, err := os.ReadFile("LICENSE")
	if err != nil {
		return err
	}
	files["LICENSE"] = license
	p, err := recipe.Build(m, files)
	if err != nil {
		return err
	}
	for name, data := range p.Files {
		target := filepath.Join("recipes", folder, name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			return err
		}
	}
	return nil
}
func samples(files map[string][]byte, m recipe.Manifest, id string, build func(int, *protocol.Sample)) error {
	for i, label := range []string{"baseline", "candidate"} {
		e := m.Experiments[id]
		e.ID = id
		s := protocol.Sample{SourceLabel: "Illustrative fixture; no physical or local measurement", SchemaVersion: 1, ID: id + "-" + label, Origin: "sample", Experiment: e, Parameters: map[string]any{}, Measurements: []protocol.Measurement{}, Series: []protocol.Series{}}
		for name, p := range m.Parameters {
			s.Parameters[name] = p.Value
		}
		build(i, &s)
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return err
		}
		files["samples/"+s.ID+".json"] = append(data, '\n')
	}
	return nil
}
func metric(c protocol.Collector, value float64) protocol.Measurement {
	return protocol.Measurement{Name: c.Name, Value: &value, Unit: c.Unit, Quantity: c.Quantity, Direction: c.Direction, Status: "valid", SourceStep: c.Step, ObservedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)}
}
func checkup() error {
	stop := false
	m := recipe.Manifest{SchemaVersion: 1, ID: "project-checkup", Name: "Project Checkup", Description: "Run two explicitly mapped project checks and compare their status and elapsed time.", Author: "Patchbay contributors", License: "MIT", Version: "1.0.0", Features: map[string]int{"capture": 1, "comparison": 1, "outcome_collectors": 1}, Schemas: map[string]int{"experiment": 1}, Requirements: map[string]recipe.Requirement{
		"project": {Kind: "project", Description: "Choose the project these checks operate on."},
		"check":   {Kind: "action", Provider: "exec", Description: "Map a configured lint/build/check action with no required inputs. Review its exact executable and arguments."},
		"test":    {Kind: "action", Provider: "exec", Description: "Map a configured test action with no required inputs. All commands and effects remain explicit."}},
		Actions: map[string]recipe.Action{"check": {Type: "reference", Safety: "confirm", Reference: "check", Project: "project", Timeout: "2m"}, "test": {Type: "reference", Safety: "confirm", Reference: "test", Project: "project", Timeout: "2m"}}, Workflows: map[string]config.Workflow{"checkup": {StopOnError: &stop, Steps: []workflow.WorkflowStep{{Action: "check"}, {Action: "test"}}}}, Experiments: map[string]protocol.Experiment{}, Controls: map[string]recipe.Control{"capture": {Capture: "checkup"}, "baseline": {Baseline: "checkup"}, "result": {Result: "checkup"}}}
	e := protocol.Experiment{SchemaVersion: 1, ID: "checkup", Title: "Project Checkup", Description: "Both checks run in order even if the first fails. Duration is measured by Patchbay; status records the actual step outcome. Output truncation does not erase these independent observations.", Projects: []string{"project"}, Workflow: "checkup"}
	for i, name := range []string{"check", "test"} {
		e.Collectors = append(e.Collectors, protocol.Collector{Name: name + ".duration", Step: i, Action: name, Source: "outcome", Path: []string{"duration_ms"}, Kind: "measurement", Unit: "ms", Quantity: "duration", Direction: "lower"}, protocol.Collector{Name: name + ".state", Step: i, Action: name, Source: "outcome", Path: []string{"state"}, Kind: "text"})
		e.Layout = append(e.Layout, protocol.Widget{ID: name + "-duration", Kind: "measurement", Title: name + " elapsed", Reference: name + ".duration"}, protocol.Widget{ID: name + "-state", Kind: "text", Title: name + " status", Reference: name + ".state"})
	}
	m.Experiments[e.ID] = e
	files := map[string][]byte{"README.md": []byte("# Project Checkup\n\nMap a project and two existing exec actions: a check and a test. Commands are fully configured locally; they may build files or run tests, so inspect their effective preview. Choose actions without required inputs (or with declared defaults). Nothing is probed or installed. The workflow continues to the second action after a first-action failure. Save a baseline, make a change yourself, capture again and compare elapsed time and per-step status. These times include process/provider overhead and are not a statistical benchmark. Captures are partial when any action fails.\n"), "docs/mapping.md": []byte("For a Go project, configure separate local actions for `go vet ./...` and `go test ./...` with the project working directory. For other languages, map your existing lint/test commands. Paths, environment values and local action IDs remain outside the portable package. Suggested physical controls are unassigned until explicitly mapped.\n")}
	if err := samples(files, m, e.ID, func(i int, s *protocol.Sample) {
		for _, c := range e.Collectors {
			if c.Kind == "measurement" {
				s.Measurements = append(s.Measurements, metric(c, float64(1000+c.Step*500)*(1-float64(i)*0.2)))
			}
		}
	}); err != nil {
		return err
	}
	return writeRecipe("project-checkup", m, files)
}
func rigol() error {
	c, err := config.Load("configs/bench.yaml")
	if err != nil {
		return err
	}
	params := map[string]parameter.Definition{}
	for name, p := range c.Parameters {
		p.Instrument = nil
		params[name] = p
	}
	e := c.Experiments["bench.trace"]
	e.Projects = []string{"bench"}
	e.Description = "Physical verification pending. Inputs record intended settings only and never apply them. Use separately configured local controls to disable outputs, apply safe settings and explicitly enable output. Acquire and stop manually at the MHO954, then transfer the stopped trace. Enter the acquisition assertion in the saved run note. Acquisition time remains unknown."
	m := recipe.Manifest{SchemaVersion: 1, ID: "rigol-capture", Name: "Rigol Capture & Compare", Description: "DG812 / MHO954 stopped-trace workflow. Physical verification pending; samples are illustrative.", Author: "Patchbay contributors", License: "MIT", Version: "1.0.0", Features: map[string]int{"capture": 1, "comparison": 1}, Schemas: map[string]int{"experiment": 1, "series": 1}, Requirements: map[string]recipe.Requirement{
		"bench": {Kind: "project", Description: "Select the local bench project for saved evidence."}, "generator": {Kind: "action", Provider: "scpi", Operation: "generator.inspect", Model: "DG812", Channel: 1, Description: "Map a configured DG812 channel-1 inspection action; no output write."}, "scope": {Kind: "action", Provider: "scpi", Operation: "scope.capture", Model: "MHO954", Channel: 1, Description: "Map the stopped-trace MHO954 channel-1 capture action."}},
		Actions: map[string]recipe.Action{"generator.inspect": {Type: "reference", Safety: "confirm", Reference: "generator", Project: "bench", Timeout: "15s"}, "scope.capture": {Type: "reference", Safety: "confirm", Reference: "scope", Project: "bench", Timeout: "15s"}}, Parameters: params, Workflows: map[string]config.Workflow{"bench.capture": c.Workflows["bench.capture"]}, Experiments: map[string]protocol.Experiment{"bench.trace": e}, Controls: map[string]recipe.Control{"capture": {Capture: "bench.trace"}, "baseline": {Baseline: "bench.trace"}, "result": {Result: "bench.trace"}, "intended-frequency": {Parameter: "bench.frequency"}}}
	files := map[string][]byte{"README.md": []byte("# Rigol Capture & Compare\n\n**Physical verification pending.** This package targets a Rigol DG812 and MHO954 using locally configured SCPI profiles. Addresses and limits stay in the host configuration. Imported package names do not establish verified compatibility.\n\n1. Map the bench project, DG812 channel-1 inspection, and MHO954 channel-1 stopped capture actions.\n2. Separately disable both generator outputs, apply locally validated settings and explicitly authorize output enable using your existing controls. Recipe parameters record intended values only; changing them never writes hardware.\n3. Acquire and stop manually at the scope. Review and capture the existing stopped trace. Patchbay checks STOP immediately before and after transfer; this cannot establish the original acquisition time.\n4. Record your acquisition assertion in the run note. Compare generator observations before/after, inspect suspect traces, and set a successful capture as baseline.\n\nNo generator apply/output or scope acquisition command is installed by this recipe. Rollback/removal cannot undo hardware state.\n"), "docs/verification.md": []byte("Simulator tests cover disconnected/bounded transfer, STOP before and after, changed generator observations, pinned result controls and suspect evidence. Real DG812/MHO954 and Stream Deck+ verification requires a separately authorized operator session with model/app/firmware and test results recorded. All supplied traces are illustrative sine waves, never claimed to be equipment measurements.\n")}
	if err := samples(files, m, e.ID, func(i int, s *protocol.Sample) {
		for _, col := range e.Collectors {
			if col.Kind == "measurement" {
				v := 1000.0
				if col.Quantity == "amplitude" {
					v = 0.1
				}
				s.Measurements = append(s.Measurements, metric(col, v))
			}
		}
		trace := protocol.Series{SchemaVersion: 1, Name: "waveform", XUnit: "s", YUnit: "V", Quantity: "voltage", Quality: "valid"}
		for n := range 128 {
			x := float64(n) * 1e-5
			trace.X = append(trace.X, x)
			trace.Y = append(trace.Y, (0.05+float64(i)*0.005)*math.Sin(2*math.Pi*1000*x))
		}
		s.Series = append(s.Series, trace)
	}); err != nil {
		return err
	}
	return writeRecipe("rigol-capture", m, files)
}
