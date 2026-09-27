package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"patchbay/internal/scpitest"
)

func TestBenchCLIAPIWorkflowAndFailureIsolation(t *testing.T) {
	var fail atomic.Bool
	g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.1}
	generator := scpitest.New(t, func(c string) string {
		if fail.Load() {
			return "!disconnect"
		}
		return g.Handle(c)
	})
	scope := scpitest.New(t, scpitest.Scope)
	dir, err := os.MkdirTemp("/tmp", "pb-bench-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	data, err := os.ReadFile("../../configs/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.NewReplacer("~/.deckd/", dir+"/", "127.0.0.1:5501", generator.Address, "127.0.0.1:5502", scope.Address).Replace(string(data))
	text = strings.Replace(text, "actions:\n", "actions:\n  alive: {type: exec, command: /bin/echo, args: [alive], safety: safe}\n", 1)
	d := &cliDaemon{path: filepath.Join(dir, "config.yaml"), socket: filepath.Join(dir, "bench.sock"), text: text}
	d.write(t, text)
	d.start(t)
	t.Cleanup(func() { d.stop(t) })
	out, _ := ctl(t, d, false, 0, "status")
	if !strings.Contains(out, "DG812") || !strings.Contains(out, "MHO954") {
		t.Fatal(out)
	}
	out, _ = ctl(t, d, false, 0, "action", "list")
	if !strings.Contains(out, "generator.set_frequency") || !strings.Contains(out, "dangerous") {
		t.Fatal(out)
	}
	ctl(t, d, true, 4, "action", "run", "generator.frequency")
	ctl(t, d, true, 4, "action", "run", "generator.output", "--arg", "value=true", "--confirm")
	ctl(t, d, true, 0, "param", "set", "bench.frequency", "2000")
	out, _ = ctl(t, d, false, 0, "param", "get", "bench.frequency")
	if !strings.Contains(out, "desired=2000") || !strings.Contains(out, "synchronization=pending") || len(generator.Commands()) != 0 {
		t.Fatal(out)
	}
	ctl(t, d, true, 0, "workflow", "run", "bench.prepare", "--confirm")
	out, _ = ctl(t, d, true, 0, "param", "get", "bench.frequency")
	if !strings.Contains(out, `"observed":2000`) || !strings.Contains(out, `"status":"matched"`) {
		t.Fatal(out)
	}
	out, _ = ctl(t, d, false, 0, "workflow", "run", "bench.capture", "--confirm")
	if !strings.Contains(out, "Waveform: channel=1 points=3") {
		t.Fatal(out)
	}
	out, _ = ctl(t, d, true, 0, "action", "run", "scope.capture", "--confirm")
	if !strings.Contains(out, `"samples_v":[-0.01,0,0.01]`) {
		t.Fatal(out)
	}
	fail.Store(true)
	ctl(t, d, true, 3, "action", "run", "generator.inspect", "--confirm")
	out, _ = ctl(t, d, false, 0, "param", "get", "bench.frequency")
	if !strings.Contains(out, "synchronization=error") || !strings.Contains(out, "observed=2000") {
		t.Fatal(out)
	}
	ctl(t, d, true, 0, "action", "run", "alive")
	ctl(t, d, true, 0, "action", "run", "scope.capture", "--confirm")
}
