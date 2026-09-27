package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimePathsAndGitSchemas(t *testing.T) {
	for _, source := range []string{
		"version: 1\nserver: {socket: local}\nstate: {path: local.lock}\n",
		"version: 1\nserver: {socket: local.lock}\nstate: {path: local}\n",
		"version: 1\nactions: {git: {type: git, operation: status, inputs: {flags: {type: string}}}}\n",
		"version: 1\nactions: {git: {type: git, operation: log, inputs: {limit: {type: string}}}}\n",
	} {
		if _, err := parseTest(t, source); err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.lock")
	if err := os.WriteFile(path, []byte("version: 1\nstate: {path: state}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("config collides with state lock")
	}
}

func TestWorkflowExpansionBound(t *testing.T) {
	source := "version: 1\nactions:\n  leaf: {type: exec, command: /bin/echo}\n  inner: {type: workflow, workflow: inner}\nworkflows:\n  inner:\n    steps:\n" + strings.Repeat("      - {action: leaf}\n", 256) + "  outer:\n    steps:\n" + strings.Repeat("      - {action: inner}\n", 4)
	if _, err := parseTest(t, source); err == nil || !strings.Contains(err.Error(), "1024") {
		t.Fatal(err)
	}
}
