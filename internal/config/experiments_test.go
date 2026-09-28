package config

import (
	"strings"
	"testing"
)

const experimentFixture = `version: 1
parameters:
  count: {type: integer, value: 2, min: 1, max: 10}
actions:
  measure:
    type: exec
    command: /bin/echo
    args: ['{{ .args.count }}']
    inputs: {count: {type: integer, required: true, min: 1, max: 10}}
  capture: {type: experiment, experiment: bench}
experiments:
  bench:
    schema_version: 1
    title: Benchmark
    action: measure
    inputs: [{step: 0, input: count, parameter: count}]
    collectors: [{name: duration, step: 0, action: measure, kind: measurement, source: json_stdout, path: [duration], unit: ms, direction: lower}]
    layout: [{id: duration, title: Duration, kind: measurement, reference: duration}]
`

func TestExperimentSchemaAndSensitivity(t *testing.T) {
	c, err := parseTest(t, experimentFixture)
	if err != nil {
		t.Fatal(err)
	}
	if c.Experiments["bench"].ID != "bench" || c.Runs.Path != "/tmp/home/.deckd/runs" {
		t.Fatal("missing normalized identity or path")
	}
	for _, change := range []struct{ from, to string }{
		{"schema_version: 1", "schema_version: 2"},
		{"title: Benchmark", "title: Benchmark\n    invented: true"},
		{"step: 0, input", "step: 1, input"},
		{"parameter: count", "parameter: absent"},
		{"action: measure, kind", "action: capture, kind"},
		{"reference: duration", "reference: absent"},
		{"direction: lower", "direction: guessed"},
		{"value: 2", "value: 20"},
		{"required: true", "required: true, sensitive: true"},
		{"value: 2", "sensitive: true, value: 2"},
		{"action: measure\n", "action: capture\n"},
		{"source: json_stdout", "source: inferred"},
		{"inputs: [{step:", "projects: [absent]\n    inputs: [{step:"},
	} {
		t.Run(change.to, func(t *testing.T) {
			_, err := parseTest(t, strings.Replace(experimentFixture, change.from, change.to, 1))
			if err == nil {
				t.Fatal("invalid experiment accepted")
			}
		})
	}
}
func TestExperimentEffectiveOverridesAndNestedCycles(t *testing.T) {
	for _, extra := range []string{
		`projects:
  p: {name: P, path: ., actions: {measure: {type: exec, command: /bin/echo}}}
`,
		`projects:
  p: {name: P, path: ., actions: {measure: {type: experiment, experiment: bench}}}
`,
	} {
		if _, err := parseTest(t, experimentFixture+extra); err == nil {
			t.Fatal("incompatible effective override accepted")
		}
	}
	for _, path := range []string{"~/.deckd", "~/.deckd/state.json", "~/.deckd/deckd.sock"} {
		if _, err := parseTest(t, experimentFixture+"runs: {path: "+path+"}\n"); err == nil {
			t.Fatal("overlapping evidence path accepted")
		}
	}
}

func TestOutcomeCollectorVocabulary(t *testing.T) {
	valid := strings.Replace(experimentFixture, "source: json_stdout, path: [duration], unit: ms", "source: outcome, path: [duration_ms], unit: ms, quantity: duration", 1)
	if _, err := parseTest(t, valid); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []struct{ from, to string }{{"path: [duration_ms]", "path: []"}, {"path: [duration_ms]", "path: [state]"}, {"path: [duration_ms]", "path: [stdout]"}, {"unit: ms", "unit: s"}, {"quantity: duration", "quantity: count"}} {
		if _, err := parseTest(t, strings.Replace(valid, replacement.from, replacement.to, 1)); err == nil {
			t.Fatal("invalid outcome collector accepted", replacement.to)
		}
	}
}
