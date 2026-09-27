package config

import (
	"os"
	"strings"
	"testing"

	"patchbay/internal/permission"
)

func benchSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../configs/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func TestSCPIConfigDefaultsAndFloors(t *testing.T) {
	c, err := parseTest(t, benchSource(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Devices["generator"].IOTimeout != "2s" || c.Actions["generator.output"].Safety != permission.Dangerous || c.Actions["generator.frequency"].Device != "generator" || len(c.Actions["generator.frequency"].Inputs) != 0 {
		t.Fatal(c.Actions)
	}
	if c.Actions["generator.output"].Inputs["value"].Type != "boolean" {
		t.Fatal("missing generated input")
	}
	if _, err := c.Actions["generator.output"].Arguments(map[string]any{"value": "ON"}); err == nil {
		t.Fatal("string injection accepted")
	}
}
func TestSCPIInvalidConfigurations(t *testing.T) {
	for _, tc := range []struct{ old, new string }{
		{"profile: rigol-dg800", "profile: generic"}, {"model: DG812", "model: DG822"}, {"model: MHO954", "model: MHO984"},
		{"model: MHO954", "model: MHO954\n    shutdown: output_off"},
		{"transport: tcp", "transport: usb"}, {"127.0.0.1:5501", "example.com:5555"}, {"127.0.0.1:5501", "0.0.0.0:5555"}, {"127.0.0.1:5501", "127.0.0.1:0"}, {"127.0.0.1:5501", "127.0.0.1:5502"},
		{"shutdown: preserve", "shutdown: restore"}, {"shutdown: preserve", "shutdown: preserve\n    io_timeout: 1h"},
		{"frequency_max_hz: 1000000", "frequency_max_hz: 100000000"}, {"amplitude_max_vpp: 1", "amplitude_max_vpp: .nan"},
		{"amplitude_max_vpp: 1", "amplitude_max_vpp: '1'"},
		{"unit: Vpp", "unit: Vrms"}, {"max: 1000000", "max: 1000001"}, {"channel: 1, operation: generator.set_frequency", "channel: 3, operation: generator.set_frequency"},
		{"parameter: bench.frequency}", "parameter: bench.frequency, device: generator}"},
		{"operation: generator.output}", "operation: raw}"}, {"operation: generator.output}", "operation: generator.output, inputs: {value: {type: string}}}"},
		{"type: scpi, parameter: bench.frequency", "type: exec, parameter: bench.frequency, command: echo"},
	} {
		t.Run(tc.new, func(t *testing.T) {
			if _, err := parseTest(t, strings.Replace(benchSource(t), tc.old, tc.new, 1)); err == nil {
				t.Fatal("accepted invalid SCPI config")
			}
		})
	}
	for _, output := range []string{"true", "false, persistent: true"} {
		text := benchSource(t) + "\n"
		text = strings.Replace(text, "parameters:\n", "parameters:\n  output: {type: boolean, value: "+output+", instrument: {device: generator, channel: 1, operation: generator.output}}\n", 1)
		if _, err := parseTest(t, text); err == nil {
			t.Fatal("persistent or initially enabled output accepted")
		}
	}
}
