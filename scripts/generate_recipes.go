// Run from the repository root with go run ./scripts/generate_recipes.go.
// This maintainer tool regenerates the portable example; it executes no recipe.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/recipe"
	"path/filepath"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func generate() error {
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
	p, err := recipe.Build(m, files)
	if err != nil {
		return err
	}
	for name, data := range p.Files {
		target := filepath.Join("recipes/benchmark", name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			return err
		}
	}
	return nil
}
