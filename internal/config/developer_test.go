package config

import (
	"patchbay/internal/permission"
	"strings"
	"testing"
)

const developerFixture = `version: 1
agents: {codex: {model: gpt-5.3-codex}}
prompts:
  review: 'Review {{ .project.name }}: {{ .args.focus }}'
conventions:
  go:
    test: {command: go, args: [test, -race, ./...]}
projects:
  go: {name: Go, path: ., language: go, conventions: [validate, test, build]}
  js: {name: JS, path: ., language: typescript, conventions: [test]}
  off: {name: Off, path: ., language: go}
actions:
  review:
    type: agent
    provider: codex
    prompt: review
    safety: safe
    files: [README.md]
    inputs: {focus: {type: string, default: correctness}}
workflows:
  check: {steps: [{action: project.test}]}
bindings:
  - control: test
    when: {project: go}
    press: {action: project.test}
`

func TestDeveloperNormalizationAndOptIn(t *testing.T) {
	c, err := parseTest(t, developerFixture)
	if err != nil {
		t.Fatal(err)
	}
	a := c.Actions["review"]
	if a.Safety != permission.Confirm || a.Timeout != "2m" || a.Cwd != "{{ .project.path }}" {
		t.Fatal(a)
	}
	if c.Agents.Codex.MaxOutputTokens != 4096 {
		t.Fatal(c.Agents)
	}
	for id, command := range map[string]string{"go": "go", "js": "npm"} {
		a := c.EffectiveActions(id)["project.test"]
		if a.Command != command || a.Safety != permission.Confirm || a.Origin == "" {
			t.Fatal(id, a)
		}
	}
	if _, ok := c.EffectiveActions("off")["project.test"]; ok {
		t.Fatal("conventions leaked into non-opted-in project")
	}
	if got := c.EffectiveActions("go")["project.test"].Args; strings.Join(got, " ") != "test -race ./..." {
		t.Fatal(got)
	}
	body := strings.Replace(developerFixture, "language: go, conventions: [validate, test, build]", "language: go, conventions: [test], actions: {project.test: {type: exec, safety: safe, command: /bin/echo, args: [custom]}}", 1)
	c, err = parseTest(t, body)
	if err != nil {
		t.Fatal(err)
	}
	a = c.EffectiveActions("go")["project.test"]
	if a.Command != "/bin/echo" || a.Safety != permission.Confirm {
		t.Fatal(a)
	}
}

func TestDeveloperInvalidConfiguration(t *testing.T) {
	cases := []struct{ old, next string }{
		{"provider: codex", "provider: other"}, {"prompt: review", "prompt: missing"},
		{"model: gpt-5.3-codex", "model: ''"}, {"model: gpt-5.3-codex", "model: gpt-5.3-codex, max_output_tokens: 1"},
		{"{{ .args.focus }}", "{{ .args.unknown }}"}, {"{{ .project.name }}", "{{ .env.SECRET }}"},
		{"files: [README.md]", "files: [../secret]"}, {"files: [README.md]", "files: [/secret]"},
		{"files: [README.md]", "files: [README.md, README.md]"},
		{"files: [README.md]", "files: ['{{ .args.focus }}']"},
		{"files: [README.md]", "environment: {SECRET: value}"},
		{"files: [README.md]", "command: /bin/echo"},
		{"files: [README.md]", "tools: [hardware]"},
		{"language: go, conventions: [validate, test, build]", "language: rust, conventions: [test]"},
		{"conventions: [validate, test, build]", "conventions: [test, test]"},
		{"conventions: [validate, test, build]", "conventions: [deploy]"},
	}
	for _, tc := range cases {
		t.Run(tc.next, func(t *testing.T) {
			if _, err := parseTest(t, strings.Replace(developerFixture, tc.old, tc.next, 1)); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	for _, body := range []string{
		"version: 1\nprompts: {unused: '{{ shell }}'}",
		"version: 1\nprompts: {unused: ''}",
		"version: 1\nconventions: {rust: {test: {command: cargo}}}",
		"version: 1\nactions: {a: {type: exec, command: go, prompt: x}}",
		"version: 1\nactions: {a: {type: exec, command: go, origin: spoof}}",
	} {
		if _, err := parseTest(t, body); err == nil {
			t.Fatal(body)
		}
	}
}
