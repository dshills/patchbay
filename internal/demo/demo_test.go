package demo

import "testing"

func TestBoundedMeasurement(t *testing.T) {
	result, err := Run(100, 3)
	if err != nil {
		t.Fatal(err)
	}
	m := result.Data["duration"].(Measurement)
	if m.Value <= 0 || m.Repeats != 3 || m.Spread < 0 || m.Unit != "ms" {
		t.Fatalf("measurement %#v", m)
	}
	for _, pair := range [][2]int{{0, 3}, {100001, 3}, {1, 1}, {1, 21}} {
		if _, err := Run(pair[0], pair[1]); err == nil {
			t.Fatal("unbounded workload accepted")
		}
	}
}
