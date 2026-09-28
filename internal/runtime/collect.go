package runtime

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"time"
	"unicode/utf8"

	"patchbay/internal/action"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/jsonstrict"
	"patchbay/pkg/protocol"
)

type outputEnvelope struct {
	SchemaVersion int            `json:"schema_version"`
	Data          map[string]any `json:"data"`
}
type metricValue struct {
	Value    *float64 `json:"value"`
	Unit     string   `json:"unit"`
	Quantity string   `json:"quantity"`
	Repeats  int      `json:"repeats,omitempty"`
	Spread   *float64 `json:"spread,omitempty"`
}

// Each execution owns this collector and run snapshot. Workflow leaves execute
// sequentially; the local lock also makes callback ownership explicit.
func captureCollector(store *evidence.Store, run *protocol.Run, plan *prepared, experiment protocol.Experiment, cancel context.CancelFunc) resultCollector {
	indices := map[*prepared]int{}
	var visit func(*prepared)
	visit = func(p *prepared) {
		if p.steps != nil {
			for _, child := range p.steps {
				visit(child)
			}
			return
		}
		indices[p] = len(indices)
	}
	visit(plan)
	var mu sync.Mutex
	return func(p *prepared, result action.Result, executeErr error, started, finished time.Time) error {
		mu.Lock()
		defer mu.Unlock()
		leaf := indices[p]

		outcome := protocol.StepOutcome{Index: leaf, Action: p.name, State: string(result.Status), StartedAt: &started, FinishedAt: &finished, Truncated: result.Data["truncated"] == true, Error: fault.Safe(executeErr)}
		if p.kind == "scpi" {
			if observation, ok := result.Data["instrument"].(*protocol.InstrumentObservation); ok {
				outcome.Instrument = evidence.Clone(observation)
			}
		}
		if executeErr != nil {
			outcome.State = "failed"
			if fault.Safe(executeErr).Code == protocol.Cancelled {
				outcome.State = "cancelled"
			}
		}
		if outcome.State == "" {
			outcome.State = "success"
		}
		run.Outcomes = append(run.Outcomes, outcome)
		var envelope outputEnvelope
		var envelopeErr error
		parsed := false
		requiredFailed := false
		for _, collector := range experiment.Collectors {
			if collector.Step != leaf {
				continue
			}
			source := result.Data
			valid := !outcome.Truncated
			if collector.Source == "json_stdout" {
				if !parsed {
					parsed = true
					stdout, ok := result.Data["stdout"].(string)
					if !ok || !utf8.ValidString(stdout) {
						envelopeErr = fault.New(protocol.InvalidRequest, "Invalid JSON stdout.")
					} else {
						envelopeErr = jsonstrict.Decode([]byte(stdout), &envelope)
					}
					if envelope.SchemaVersion != 1 || envelope.Data == nil {
						envelopeErr = fault.New(protocol.InvalidRequest, "Unsupported measurement envelope.")
					}
				}
				source = envelope.Data
				valid = valid && envelopeErr == nil
			}
			value, found := field(source, collector.Path)
			valid = valid && found
			measurement := protocol.Measurement{Name: collector.Name, Unit: collector.Unit, Quantity: collector.Quantity, Direction: collector.Direction, Status: "valid", SourceStep: leaf, ObservedAt: finished}
			reason := "Result is missing, malformed, or truncated."
			if valid {
				switch collector.Kind {
				case "measurement":
					metric, ok := decodeMetric(value, collector)
					valid = ok
					if ok {
						measurement.Value = metric.Value
						measurement.Repeats = metric.Repeats
						measurement.Spread = metric.Spread
					}
				case "series":
					encoded, err := json.Marshal(value)
					var series protocol.Series
					valid = err == nil && jsonstrict.Decode(encoded, &series) == nil && evidence.ValidSeries(series) == nil && series.Name == collector.Name && (collector.Unit == "" || series.YUnit == collector.Unit) && (collector.Quantity == "" || series.Quantity == collector.Quantity)
					if valid {
						if _, err := store.AddArtifact(run.ID, collector.Name, "application/json", encoded); err != nil {
							cancel()
							return err
						}
						if series.Quality != "valid" {
							valid = false
							reason = "Acquisition quality is suspect; trace retained for diagnosis."
						}
					}
				case "text":
					text, ok := value.(string)
					valid = ok && utf8.ValidString(text)
					if valid {
						if _, err := store.AddArtifact(run.ID, collector.Name, "text/plain", []byte(text)); err != nil {
							cancel()
							return err
						}
					}
				}
			}
			if !valid {
				measurement.Status = "invalid"
				measurement.Reason = reason
				if collector.Optional {
					measurement.Status = "unavailable"
				} else {
					requiredFailed = true
				}
			}
			if collector.Kind == "measurement" || !valid {
				run.Measurements = append(run.Measurements, measurement)
			}
		}
		current, err := store.Get(run.ID)
		if err != nil {
			cancel()
			return err
		}
		run.Artifacts = current.Artifacts
		if err := store.Update(*run); err != nil {
			cancel()
			return err
		}
		if requiredFailed {
			return fault.New(protocol.ExecutionFailed, "Required evidence is missing, malformed, or truncated; inspect the recorded step outcome.")
		}
		return nil
	}
}

func field(data map[string]any, path []string) (any, bool) {
	var current any = data
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return current, current != nil
}
func decodeMetric(value any, collector protocol.Collector) (metricValue, bool) {
	var metric metricValue
	switch v := value.(type) {
	case json.Number:
		n, err := v.Float64()
		if err != nil {
			return metric, false
		}
		metric.Value = &n
		metric.Unit = collector.Unit
		metric.Quantity = collector.Quantity
	case float64:
		metric.Value = &v
		metric.Unit = collector.Unit
		metric.Quantity = collector.Quantity
	case int:
		n := float64(v)
		metric.Value = &n
		metric.Unit = collector.Unit
		metric.Quantity = collector.Quantity
	case int64:
		n := float64(v)
		metric.Value = &n
		metric.Unit = collector.Unit
		metric.Quantity = collector.Quantity
	default:
		data, err := json.Marshal(value)
		if err != nil || jsonstrict.Decode(data, &metric) != nil {
			return metric, false
		}
	}
	valid := metric.Value != nil && metric.Unit == collector.Unit && metric.Quantity == collector.Quantity && metric.Repeats >= 0 && metric.Repeats <= 1000000
	if metric.Value != nil && (math.IsNaN(*metric.Value) || math.IsInf(*metric.Value, 0)) {
		valid = false
	}
	if metric.Spread != nil && (math.IsNaN(*metric.Spread) || math.IsInf(*metric.Spread, 0) || *metric.Spread < 0) {
		valid = false
	}
	return metric, valid
}
