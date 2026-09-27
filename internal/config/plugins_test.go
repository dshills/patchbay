package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"patchbay/internal/permission"
)

func pluginSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../configs/plugins.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestPluginConfigOfflineAndPermissionFloors(t *testing.T) {
	c, err := parseTest(t, strings.Replace(pluginSource(t), "operations: {echo: confirm, wait: confirm}", "operations: {echo: safe, wait: dangerous}", 1))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(c.Plugins["example"].Command) || c.Plugins["example"].Operations["echo"] != permission.Confirm || c.Actions["plugin.echo"].Safety != permission.Confirm || c.Actions["plugin.wait"].Safety != permission.Dangerous {
		t.Fatal(c)
	}
}
func TestPluginConfigRejectsImplicitAuthority(t *testing.T) {
	for _, tc := range []struct{ old, new string }{
		{"command: ../bin/deckplugin-example", "command: deckplugin-example"},
		{"command: ../bin/deckplugin-example", "command: '{{ .args.executable }}'"},
		{"cwd: ..", "cwd: '{{ .project.path }}'"},
		{"environment: {}", "environment: {BAD-KEY: value}"},
		{"environment: {}", "environment: {KEY: '{{ .args.secret }}'}"},
		{"operations: {echo: confirm, wait: confirm}", "operations: {}"},
		{"operations: {echo: confirm, wait: confirm}", "operations: {echo: trusted, wait: confirm}"},
		{"startup_timeout: 2s", "startup_timeout: 0s"},
		{"cancel_timeout: 500ms", "cancel_timeout: 1m"},
		{"plugin: example", "plugin: missing"},
		{"operation: echo", "operation: undeclared"},
		{"operation: echo", "operation: echo\n    command: /bin/echo"},
		{"operation: echo", "operation: echo\n    cwd: /tmp"},
		{"operation: echo", "operation: echo\n    device: generator"},
		{"operation: echo", "operation: echo\n    provider: codex"},
		{"type: plugin", "type: exec"},
	} {
		t.Run(tc.new, func(t *testing.T) {
			if _, err := parseTest(t, strings.Replace(pluginSource(t), tc.old, tc.new, 1)); err == nil {
				t.Fatal("accepted invalid plugin configuration")
			}
		})
	}
}
