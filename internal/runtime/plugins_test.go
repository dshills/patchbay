package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"patchbay/internal/fault"
	"patchbay/internal/job"
	"patchbay/internal/plugintest"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
)

func TestPluginChild(t *testing.T) { plugintest.Child() }
func pluginRuntime(t *testing.T, mode string) (*Runtime, string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "trace")
	text := fmt.Sprintf(`version: 1
server: {socket: private/sock}
state: {path: private/state, flush_interval: 1h}
plugins:
  example:
    command: %q
    args: [-test.run=^TestPluginChild$]
    environment: {PATCHBAY_PLUGIN_FIXTURE: %q, PATCHBAY_PLUGIN_TRACE: %q, GORACE: 'atexit_sleep_ms=0'}
    operations: {echo: safe, wait: confirm}
context: {defaults: {mode: initial}}
actions:
  plugin.echo: {type: plugin, plugin: example, operation: echo, safety: safe, inputs: {text: {type: string, default: initial}}}
  plugin.wait: {type: plugin, plugin: example, operation: wait}
  alive: {type: exec, command: /bin/echo, safety: safe}
workflows:
  plugin.demo: {steps: [{action: plugin.echo}, {action: plugin.echo, args: {text: second}}]}
parameters:
  value: {type: integer, value: 3}
bindings:
  - control: plugin
    press: {action: plugin.echo}
`, executable, mode, trace)
	r, path := setup(t, text, nil)
	return r, path, trace
}
func TestPluginsRuntimePolicyStateAndReload(t *testing.T) {
	r, path, trace := pluginRuntime(t, "good")
	if r.Status().Plugins["example"].Discovered {
		t.Fatal("startup ran discovery")
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatal("startup executed child")
	}
	a, err := r.Action("plugin.echo")
	if err != nil || a.Plugin == nil || a.Plugin.Operation != "echo" || a.Safety != "confirm" {
		t.Fatal(a, err)
	}
	if _, err := r.Invoke(context.Background(), "plugin.echo", false, protocol.Invocation{}); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal(err)
	}
	if _, err := r.Invoke(context.Background(), "plugin.echo", false, protocol.Invocation{Confirmed: true, Args: map[string]any{"unknown": "x"}}); fault.Safe(err).Code != protocol.InvalidRequest {
		t.Fatal(err)
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatal("denied invocation executed child")
	}
	j := benchInvoke(t, r, "plugin.demo", true)
	if j.State != job.Success {
		t.Fatal(j)
	}
	if !r.Status().Plugins["example"].Discovered {
		t.Fatal("missing discovered capabilities")
	}
	text, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(text), "default: initial", "default: replacement", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	j = benchInvoke(t, r, "plugin.echo", false)
	if j.State != job.Success || j.Result.Data["stdout"] != "replacement" {
		t.Fatal(j)
	}
	changed, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(changed), "operations: {echo: safe, wait: confirm}", "operations: {echo: dangerous, wait: confirm}", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reload(context.Background()); fault.Safe(err).Code != protocol.InvalidConfig {
		t.Fatal(err)
	}
}
func TestPluginCannotForgeRuntimeState(t *testing.T) {
	r, _, _ := pluginRuntime(t, "forge")
	j := benchInvoke(t, r, "plugin.echo", false)
	if j.State != job.Failed || r.Context().Mode != "initial" {
		t.Fatal(j, r.Context())
	}
	p, _ := r.Parameter("value")
	if p.Value != int64(3) {
		t.Fatal(p)
	}
	if j := benchInvoke(t, r, "alive", false); j.State != job.Success {
		t.Fatal(j)
	}
}
func TestPluginPreparedContextAndArgumentsPinned(t *testing.T) {
	r, _, _ := pluginRuntime(t, "context")
	r.mu.Lock()
	remaining := 1024
	plan, err := r.prepareAction(context.Background(), "plugin.echo", map[string]any{"text": "pinned"}, &remaining)
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	mode := "changed"
	if _, err := r.PatchContext(context.Background(), protocol.ContextPatch{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Exercise the admitted immutable plan after the generation changed.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := r.execute(ctx, "test", plan, provider.NewBudget(4096), false, true)
	if err != nil || !strings.Contains(result.Data["stdout"].(string), `"mode":"initial"`) || plan.plugin.Args["text"] != "pinned" {
		t.Fatal(result, err)
	}
}
