package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"patchbay/internal/plugintest"
)

func TestPluginChild(t *testing.T) { plugintest.Child() }
func TestPluginCLIAPIAndCancellation(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "pb-plugin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../configs/plugins.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.NewReplacer("~/.deckd/", dir+"/", "../bin/deckplugin-example", fmt.Sprintf("%q", executable), "environment: {}", "args: [-test.run=^TestPluginChild$]\n    environment: {PATCHBAY_PLUGIN_FIXTURE: good, GORACE: 'atexit_sleep_ms=0'}").Replace(string(data))
	d := &cliDaemon{path: filepath.Join(dir, "config.yaml"), socket: filepath.Join(dir, "plugins.sock"), text: text}
	d.write(t, text)
	d.start(t)
	t.Cleanup(func() { d.stop(t) })
	out, _ := ctl(t, d, false, 0, "action", "list")
	if !strings.Contains(out, "plugin: example operation=echo protocol=1") {
		t.Fatal(out)
	}
	out, _ = ctl(t, d, false, 0, "status")
	if !strings.Contains(out, "discovered=false") {
		t.Fatal(out)
	}
	ctl(t, d, true, 4, "action", "run", "plugin.echo")
	ctl(t, d, true, 2, "action", "run", "plugin.echo", "--confirm", "--arg", "extra=x")
	out, _ = ctl(t, d, false, 0, "workflow", "run", "plugin.demo", "--confirm")
	if !strings.Contains(out, "First step") || !strings.Contains(out, "Second step") {
		t.Fatal(out)
	}
	out, _ = ctl(t, d, true, 0, "status")
	if !strings.Contains(out, `"discovered":true`) {
		t.Fatal(out)
	}
	out, _ = ctl(t, d, false, 0, "action", "run", "plugin.wait", "--confirm", "--async")
	id := strings.TrimSpace(out)
	deadline := time.Now().Add(2 * time.Second)
	for {
		out, _ = ctl(t, d, true, 0, "job", "show", id)
		if strings.Contains(out, `"state":"running"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(out)
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctl(t, d, true, 0, "job", "cancel", id)
	for {
		out, _ = ctl(t, d, true, 0, "job", "show", id)
		if strings.Contains(out, `"state":"cancelled"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(out)
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctl(t, d, true, 0, "action", "run", "plugin.echo", "--confirm")
}
