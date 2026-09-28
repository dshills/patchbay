package evidence

import (
	"encoding/json"
	"time"

	"patchbay/pkg/protocol"
)

// Samples are illustrative fixtures. They never enter Store or the job manager.
func Samples() protocol.SampleList {
	experiment := protocol.Experiment{SchemaVersion: 1, ID: "benchmark", Title: "Benchmark Playground", Description: "Change the work size and compare repeated SHA-256 timings on this computer.", Action: "benchmark.measure", Projects: []string{"benchmark"}, Inputs: []protocol.ExperimentInput{{Step: 0, Input: "iterations", Parameter: "iterations"}, {Step: 0, Input: "repeats", Parameter: "repeats"}}, Collectors: []protocol.Collector{
		{Name: "duration", Step: 0, Action: "benchmark.measure", Kind: "measurement", Source: "json_stdout", Path: []string{"duration"}, Unit: "ms", Quantity: "duration", Direction: "lower"},
		{Name: "host", Step: 0, Action: "benchmark.measure", Kind: "text", Source: "json_stdout", Path: []string{"host"}},
		{Name: "work", Step: 0, Action: "benchmark.measure", Kind: "text", Source: "json_stdout", Path: []string{"work"}},
	}, Layout: []protocol.Widget{{ID: "duration", Kind: "measurement", Title: "Median duration", Reference: "duration"}, {ID: "host", Kind: "text", Title: "Measured on", Reference: "host"}}}
	samples := []protocol.Sample{}
	for i, id := range []string{"benchmark-small", "benchmark-large"} {
		value := float64(i+1) * 1.2
		spread := 0.12
		observed := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
		samples = append(samples, protocol.Sample{SchemaVersion: 1, ID: id, Origin: "sample", Experiment: experiment, Parameters: map[string]any{"iterations": 10000 * (i + 1), "repeats": 5}, Measurements: []protocol.Measurement{{Name: "duration", Value: &value, Unit: "ms", Quantity: "duration", Direction: "lower", Status: "valid", SourceStep: 0, ObservedAt: observed, Repeats: 5, Spread: &spread}}, Series: []protocol.Series{}})
	}
	return protocol.SampleList{Samples: samples}
}
func SampleRun(sample protocol.Sample) protocol.Run {
	data, _ := json.Marshal(sample.Experiment)
	return protocol.Run{SchemaVersion: Version, ID: sample.ID, Origin: "sample", Experiment: sample.Experiment, ExperimentDigest: Digest(data), Project: "benchmark", State: "success", Parameters: sample.Parameters, Measurements: sample.Measurements, Artifacts: []protocol.Artifact{}}
}
