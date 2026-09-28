package evidence

import (
	"math"
	"testing"

	"patchbay/pkg/protocol"
)

func TestComparisonExactSemantics(t *testing.T) {
	number := func(n float64) *float64 { return &n }
	a := draft()
	a.SchemaVersion = 1
	a.ID = "base"
	a.State = "success"
	a.Measurements = []protocol.Measurement{{Name: "time", Value: number(-5), Unit: "ms", Status: "valid", Quantity: "duration", Direction: "lower"}}
	b := Clone(a)
	b.ID = "candidate"
	b.Measurements[0].Value = number(-3)
	c := Compare(a, b)
	if !c.Compatible || c.Partial || *c.Metrics[0].Delta != 2 || *c.Metrics[0].Percent != 40 {
		t.Fatalf("comparison %#v", c)
	}
	a.Measurements[0].Value = number(0)
	c = Compare(a, b)
	if c.Metrics[0].Delta == nil || c.Metrics[0].Percent != nil {
		t.Fatal("zero baseline invented percent")
	}
	b.Measurements[0].Unit = "s"
	if Compare(a, b).Metrics[0].Delta != nil {
		t.Fatal("implicit unit conversion")
	}
	b.Measurements[0].Unit = "ms"
	b.Measurements[0].Status = "missing"
	if Compare(a, b).Metrics[0].Delta != nil {
		t.Fatal("missing treated as zero")
	}
	b.State = "failed"
	if !Compare(a, b).Partial {
		t.Fatal("failed run presented as full success")
	}
	b.ExperimentDigest = "changed"
	if Compare(a, b).Compatible {
		t.Fatal("changed experiment considered compatible")
	}
	a.Measurements[0].Value = number(-math.MaxFloat64)
	b = Clone(a)
	b.Measurements[0].Value = number(math.MaxFloat64)
	if Compare(a, b).Metrics[0].Delta != nil {
		t.Fatal("nonfinite delta emitted")
	}
}
func TestSeriesGridAndSuspectData(t *testing.T) {
	a := protocol.Series{SchemaVersion: 1, Name: "trace", X: []float64{0, 1}, Y: []float64{1, 2}, XUnit: "s", YUnit: "V", Quality: "valid"}
	b := Clone(a)
	b.Y[1] = 3
	c := CompareSeries(a, b)
	if !c.Overlay || len(c.Delta) != 2 || c.Delta[1] != 1 {
		t.Fatalf("same grid %#v", c)
	}
	b.X[1] = 2
	c = CompareSeries(a, b)
	if !c.Overlay || c.Delta != nil || c.Reason == "" {
		t.Fatal("mismatched grid interpolated")
	}
	b.Quality = "suspect"
	c = CompareSeries(a, b)
	if c.Overlay || c.Delta != nil {
		t.Fatal("suspect trace compared")
	}
}
