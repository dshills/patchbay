package pluginconform

import (
	"context"
	"os"
	"testing"
	"time"

	"patchbay/internal/plugintest"
	wire "patchbay/pkg/plugin"
)

func TestPluginChild(t *testing.T) { plugintest.Child() }
func TestConformanceUsesCooperativeCancellation(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"good", "refuse_cancel", "refuse_shutdown", "extra"} {
		t.Run(mode, func(t *testing.T) {
			s := Specification{Command: command, Args: []string{"-test.run=^TestPluginChild$"}, Directory: t.TempDir(), Environment: map[string]string{"PATCHBAY_PLUGIN_FIXTURE": mode, "GORACE": "atexit_sleep_ms=0"}, Operations: map[string]string{"echo": "confirm", "wait": "confirm"}, Execute: wire.Execute{Operation: "echo", Args: map[string]any{"text": "success"}}, Cancel: wire.Execute{Operation: "wait"}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			report, err := Check(ctx, s)
			if mode == "good" {
				if err != nil || !report.Execution || !report.Cancellation || !report.Shutdown {
					t.Fatal(report, err)
				}
			} else if err == nil {
				t.Fatal("nonconforming plugin passed", report)
			}
		})
	}
}
