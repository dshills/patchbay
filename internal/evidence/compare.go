package evidence

import (
	"fmt"
	"math"
	"slices"

	"patchbay/pkg/protocol"
)

// Compare is pure: it neither selects a baseline nor changes saved evidence.
func Compare(base, candidate protocol.Run) protocol.Comparison {
	out := protocol.Comparison{SchemaVersion: 1, Baseline: base.ID, Candidate: candidate.ID, Compatible: true, Reasons: []string{}, Metrics: []protocol.MetricDelta{}, Series: []protocol.SeriesDelta{}}
	reject := func(reason string) { out.Compatible = false; out.Reasons = append(out.Reasons, reason) }
	if base.SchemaVersion != Version || candidate.SchemaVersion != Version {
		reject("Run schemas are incompatible.")
	}
	out.Illustrative = base.Origin == "sample" || candidate.Origin == "sample"
	if !out.Illustrative && base.Project != candidate.Project || base.Experiment.ID != candidate.Experiment.ID || base.ExperimentDigest != candidate.ExperimentDigest {
		reject("Project or experiment definitions differ.")
	}
	out.Partial = base.State != "success" || candidate.State != "success"
	if out.Partial {
		out.Reasons = append(out.Reasons, "At least one run is incomplete or failed; inspect individual outcomes.")
	}
	byName := map[string]protocol.Measurement{}
	for _, m := range base.Measurements {
		byName[m.Name] = m
	}
	seen := map[string]bool{}
	for _, right := range candidate.Measurements {
		left, ok := byName[right.Name]
		seen[right.Name] = true
		delta := protocol.MetricDelta{Name: right.Name, Unit: right.Unit, Direction: right.Direction}
		switch {
		case !out.Compatible:
			delta.Reason = "Run definitions are incompatible."
		case !ok:
			delta.Reason = "Baseline measurement is missing."
		case left.Unit != right.Unit || left.Quantity != right.Quantity || left.SourceStep != right.SourceStep:
			delta.Reason = "Quantity, unit, or source step differs."
		case left.Status != "valid" || right.Status != "valid" || left.Value == nil || right.Value == nil:
			delta.Reason = "Measurement is unavailable or invalid."
		default:
			value := *right.Value - *left.Value
			if math.IsInf(value, 0) || math.IsNaN(value) {
				delta.Reason = "Delta exceeds finite numeric range."
				break
			}
			delta.Baseline, delta.Candidate, delta.Delta = left.Value, right.Value, &value
			if *left.Value != 0 {
				percent := 100 * (value / math.Abs(*left.Value))
				if !math.IsInf(percent, 0) && !math.IsNaN(percent) {
					delta.Percent = &percent
				} else {
					delta.Reason = "Percent exceeds finite numeric range."
				}
			} else {
				delta.Reason = "Percent unavailable for a zero baseline."
			}
		}
		out.Metrics = append(out.Metrics, delta)
	}
	for _, left := range base.Measurements {
		if !seen[left.Name] {
			out.Metrics = append(out.Metrics, protocol.MetricDelta{Name: left.Name, Unit: left.Unit, Direction: left.Direction, Reason: "Candidate measurement is missing."})
		}
	}
	return out
}

func CompareSeries(left, right protocol.Series) protocol.SeriesDelta {
	out := protocol.SeriesDelta{Name: right.Name, Baseline: left, Candidate: right}
	if err := ValidSeries(left); err != nil {
		out.Reason = "Invalid baseline series."
		return out
	}
	if err := ValidSeries(right); err != nil {
		out.Reason = "Invalid candidate series."
		return out
	}
	if left.Name != right.Name || left.XUnit != right.XUnit || left.YUnit != right.YUnit || left.Quantity != right.Quantity {
		out.Reason = "Series quantity, name, or units differ."
		return out
	}
	if left.Quality != "valid" || right.Quality != "valid" {
		out.Reason = "Suspect acquisition data cannot be compared."
		return out
	}
	out.Overlay = true
	if !slices.Equal(left.X, right.X) {
		out.Reason = "Different sample grids: overlay only; no interpolation or subtraction."
		return out
	}
	out.Delta = make([]float64, len(left.Y))
	for i := range left.Y {
		value := right.Y[i] - left.Y[i]
		if math.IsInf(value, 0) || math.IsNaN(value) {
			out.Delta = nil
			out.Reason = fmt.Sprintf("Nonfinite delta at sample %d.", i)
			return out
		}
		out.Delta[i] = value
	}
	return out
}
