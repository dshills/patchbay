package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"patchbay/internal/permission"
)

func parseTest(t *testing.T, source string) (*Config, error) {
	t.Helper()
	return Parse([]byte(source), "/tmp/config", "/tmp/home")
}

func TestDefaultsAndPaths(t *testing.T) {
	c, err := parseTest(t, `version: 1
projects:
  demo: {name: Demo, path: ../project}
actions:
  test: {type: exec, command: ./tools/test, args: ["$HOME", "a; b", "$(echo literal)"]}
workflows:
  validate: {steps: [{action: test}]}
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Socket != "/tmp/home/.deckd/deckd.sock" || c.State.Path != "/tmp/home/.deckd/state.json" {
		t.Fatalf("bad defaults: %+v %+v", c.Server, c.State)
	}
	if c.Jobs.Concurrency != 4 || c.Jobs.QueueCapacity != 64 || c.Events.SubscriberCapacity != 64 {
		t.Fatal("missing bounded defaults")
	}
	if c.Projects["demo"].ID != "demo" || c.Projects["demo"].Path != "/tmp/project" {
		t.Fatalf("project = %+v", c.Projects["demo"])
	}
	a := c.Actions["test"]
	if a.Command != "/tmp/config/tools/test" || a.Cwd != "/tmp/config" || a.Safety != permission.Confirm {
		t.Fatalf("action = %+v", a)
	}
	if a.Args[0] != "$HOME" || a.Args[2] != "$(echo literal)" {
		t.Fatal("configuration expanded shell content")
	}
	if !*c.Workflows["validate"].StopOnError {
		t.Fatal("stop_on_error should default to true")
	}
}

func TestExampleAndIndependentLoads(t *testing.T) {
	c, err := Load("../../configs/example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Context.Defaults.Project != "patchbay" || len(c.Bindings) != 2 {
		t.Fatal("example incomplete")
	}
	c.Actions["project.test"].Args[0] = "changed"
	again, err := Load("../../configs/example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if again.Actions["project.test"].Args[0] != "test" {
		t.Fatal("loads share mutable state")
	}
}

func TestInvalidFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/invalid/*.yaml")
	if err != nil || len(files) < 10 {
		t.Fatalf("fixtures: %v %d", err, len(files))
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = parseTest(t, string(data))
			var invalid *Error
			if !errors.As(err, &invalid) {
				t.Fatalf("expected config error, got %v", err)
			}
			if strings.Contains(err.Error(), "SECRET_SENTINEL") {
				t.Fatalf("secret leaked: %v", err)
			}
		})
	}
}

func TestInvalidConfiguration(t *testing.T) {
	cases := []struct{ name, body, field string }{
		{"empty", "", "$"},
		{"scalar", "hello", "$"},
		{"missing version", "{}", "version"},
		{"unknown version", "version: 2", "version"},
		{"quoted version", "version: '1'", "version"},
		{"coerced name", "version: 1\nprojects: {p: {name: 123, path: .}}", "projects.p.name"},
		{"legacy boolean", "version: 1\nsecurity: {allow_dangerous_actions: yes}", "security.allow_dangerous_actions"},
		{"null", "version: 1\nactions: null", "actions"},
		{"duplicate nested", "version: 1\nsecurity: {allow_dangerous_actions: false, allow_dangerous_actions: true}", "security.allow_dangerous_actions"},
		{"merge", "version: 1\nactions: {<<: {}}", "actions"},
		{"anchor", "version: 1\nactions: &a {}", "actions"},
		{"second document", "version: 1\n---\nversion: 1", "$"},
		{"zero limit", "version: 1\njobs: {concurrency: 0}", "jobs.concurrency"},
		{"negative limit", "version: 1\njobs: {history_limit: -1}", "jobs.history_limit"},
		{"large limit", "version: 1\njobs: {concurrency: 2048}", "jobs.concurrency"},
		{"duration", "version: 1\nserver: {shutdown_grace: 0s}", "server.shutdown_grace"},
		{"named home", "version: 1\nserver: {socket: '~other/socket'}", "server.socket"},
		{"colliding paths", "version: 1\nserver: {socket: same}\nstate: {path: same}", "state.path"},
		{"bad name", "version: 1\nactions: {'bad/name': {type: exec, command: go}}", "actions"},
		{"project id", "version: 1\nprojects: {p: {id: q, name: P, path: .}}", "projects.p.id"},
		{"project name", "version: 1\nprojects: {p: {path: .}}", "projects.p.name"},
		{"project path", "version: 1\nprojects: {p: {name: P}}", "projects.p.path"},
		{"project default", "version: 1\ncontext: {defaults: {project: missing}}", "context.defaults.project"},
		{"github", "version: 1\nprojects: {p: {name: P, path: ., github: 'http://example.com'}}", "projects.p.github"},
		{"unknown override", "version: 1\nprojects: {p: {name: P, path: ., actions: {x: {type: exec, command: go}}}}", "projects.p.actions.x"},
		{"env key", "version: 1\nactions: {a: {type: exec, command: go, environment: {'bad-key': value}}}", "actions.a.environment"},
		{"missing command", "version: 1\nactions: {a: {type: exec}}", "actions.a.command"},
		{"templated executable", "version: 1\nactions: {a: {type: exec, command: '{{ .project.path }}'}}", "actions.a.command"},
		{"provider field", "version: 1\nactions: {a: {type: exec, command: go, target: x}}", "actions.a"},
		{"unknown safety", "version: 1\nactions: {a: {type: exec, command: go, safety: foo}}", "actions.a.safety"},
		{"unknown provider", "version: 1\nactions: {a: {type: unknown}}", "actions.a.type"},
		{"open target", "version: 1\nactions: {a: {type: open}}", "actions.a.target"},
		{"open scheme", "version: 1\nactions: {a: {type: open, target: 'javascript:SECRET_SENTINEL'}}", "actions.a.target"},
		{"open host", "version: 1\nactions: {a: {type: open, target: 'https:///x'}}", "actions.a.target"},
		{"git command", "version: 1\nactions: {a: {type: git, operation: status, command: git}}", "actions.a"},
		{"git operation", "version: 1\nactions: {a: {type: git, operation: arbitrary}}", "actions.a.operation"},
		{"workflow target", "version: 1\nactions: {a: {type: workflow, workflow: missing}}", "actions.a.workflow"},
		{"empty workflow", "version: 1\nworkflows: {w: {steps: []}}", "workflows.w.steps"},
		{"unknown step", "version: 1\nworkflows: {w: {steps: [{action: missing}]}}", "workflows.w.steps[0].action"},
		{"required input", "version: 1\nactions: {a: {type: exec, command: go, inputs: {x: {type: integer, required: true}}}}\nworkflows: {w: {steps: [{action: a}]}}", "workflows.w.steps[0].args"},
		{"unknown argument", "version: 1\nactions: {a: {type: exec, command: go}}\nworkflows: {w: {steps: [{action: a, args: {x: SECRET_SENTINEL}}]}}", "workflows.w.steps[0].args"},
		{"input range", "version: 1\nactions: {a: {type: exec, command: go, inputs: {x: {type: integer, min: 5, max: 1}}}}", "actions.a.inputs.x"},
		{"required default", "version: 1\nactions: {a: {type: exec, command: go, inputs: {x: {type: string, required: true, default: SECRET_SENTINEL}}}}", "actions.a.inputs.x"},
		{"bad condition", "version: 1\nbindings: [{control: key, when: {arbitrary: x}, press: {action: x}}]", "bindings[0].when"},
		{"empty binding", "version: 1\nbindings: [{control: key}]", "bindings[0]"},
		{"unknown bound action", "version: 1\nbindings: [{control: key, press: {action: missing}}]", "bindings[0].press"},
		{"non-numeric rotation", "version: 1\nparameters: {p: {type: string, value: x}}\nbindings: [{control: dial, rotate: {parameter: p}}]", "bindings[0].rotate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseTest(t, tc.body)
			var invalid *Error
			if !errors.As(err, &invalid) {
				t.Fatalf("expected error; got %v", err)
			}
			if invalid.Path != tc.field {
				t.Fatalf("path %q; want %q: %v", invalid.Path, tc.field, err)
			}
			if strings.Contains(err.Error(), "SECRET_SENTINEL") {
				t.Fatalf("secret leak: %v", err)
			}
		})
	}
}

func TestLocationsAndSecretSafeErrors(t *testing.T) {
	_, err := parseTest(t, "version: 1\nserver:\n  misspelled: SECRET_SENTINEL\n")
	var invalid *Error
	if !errors.As(err, &invalid) || invalid.Line != 3 || invalid.Column != 3 {
		t.Fatalf("location: %v", err)
	}
	for _, source := range []string{
		"version: 1\nserver: {max_request_bytes: SECRET_SENTINEL}",
		"version: 1\nactions: [SECRET_SENTINEL",
		"version: 1\nparameters: {p: {type: integer, value: SECRET_SENTINEL}}",
	} {
		_, err := parseTest(t, source)
		if err == nil || strings.Contains(err.Error(), "SECRET_SENTINEL") {
			t.Fatalf("unsafe diagnostic: %v", err)
		}
	}
}

func TestLimitsAndFileFailures(t *testing.T) {
	if _, err := parseTest(t, strings.Repeat(" ", MaxConfigBytes+1)); err == nil {
		t.Fatal("oversized file accepted")
	}
	if _, err := Parse([]byte("version: 1"), ".", "/tmp/home"); err == nil {
		t.Fatal("relative base accepted")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	deep := "version: 1\nparameters: {p: {type: string, value: " + strings.Repeat("[", 70) + "x" + strings.Repeat("]", 70) + "}}"
	if _, err := parseTest(t, deep); err == nil {
		t.Fatal("deep nesting accepted")
	}
}

func TestProviderNormalizationAndInputs(t *testing.T) {
	c, err := parseTest(t, `version: 1
actions:
  push: {type: git, operation: push, safety: safe}
  open: {type: open, target: ./README.md, safety: safe}
  test:
    type: exec
    command: go
    safety: confirm
    args: [test, "{{ .args.count }}"]
    inputs: {count: {type: integer, required: true, min: 1, max: 5}}
projects:
  p:
    name: P
    path: .
    actions:
      test:
        type: exec
        command: go
        safety: safe
        inputs: {count: {type: integer, required: true, min: 1, max: 5}}
workflows:
  w: {stop_on_error: false, steps: [{action: test, args: {count: 3}}]}
bindings:
  - {control: key, press: {action: test, args: {count: 2}}}
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Actions["push"].Safety != permission.Confirm || c.Projects["p"].Actions["test"].Safety != permission.Confirm {
		t.Fatal("risk floor weakened")
	}
	if c.Actions["open"].Target != "/tmp/config/README.md" || *c.Workflows["w"].StopOnError {
		t.Fatal("normalization incorrect")
	}
	if c.Actions["test"].Inputs["count"].Min != int64(1) {
		t.Fatal("input bound not normalized")
	}
}

func TestOverrideCycleAndArguments(t *testing.T) {
	base := `version: 1
actions:
  task: {type: exec, command: go}
workflows:
  w: {steps: [{action: task}]}
projects:
  p:
    name: P
    path: .
    actions:
      task: `
	for _, replacement := range []string{
		"{type: workflow, workflow: w}",
		"{type: exec, command: go, inputs: {x: {type: string, required: true}}}",
	} {
		if _, err := parseTest(t, base+replacement); err == nil {
			t.Fatal("invalid project-specific graph accepted")
		}
	}
}

func TestTemplateGrammar(t *testing.T) {
	inputs := map[string]Input{"count": {}}
	for _, value := range []string{"literal", "{{ .project.path }}/subdir", "{{.project.id}} {{ .project.name }}", "{{ .context.mode }}", "{{ .context.values.branch }}", "{{ .args.count }}", "$(literal)"} {
		if err := ValidateTemplate(value, inputs); err != nil {
			t.Errorf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"{{", "}}", "{{ .project.path", "{{ env \"SECRET_SENTINEL\" }}", "{{ .project.path | printf }}", "{{ .args.missing }}", "{{ .context.values.a.b }}", "{{if .project}}", "{{ {{ .project.path }}"} {
		if err := ValidateTemplate(value, inputs); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}
