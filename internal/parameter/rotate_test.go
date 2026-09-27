package parameter

import (
	"math"
	"testing"
)

func TestRotationWideIntermediates(t *testing.T) {
	cases := []struct {
		d     Definition
		delta int64
		want  any
	}{
		{Definition{Type: Integer, Value: int64(1), Step: int64(10)}, math.MaxInt64, int64(math.MaxInt64)},
		{Definition{Type: Integer, Value: int64(1), Step: int64(10)}, math.MinInt64, int64(math.MinInt64)},
		{Definition{Type: Integer, Value: int64(5), Step: int64(2), Min: int64(0), Max: int64(10)}, 4, int64(10)},
		{Definition{Type: Integer, Value: int64(5), Step: int64(2), Min: int64(0)}, -4, int64(0)},
		{Definition{Type: Integer, Value: int64(5), Step: int64(2)}, -1, int64(3)},
		{Definition{Type: Float, Value: 1.0, Step: math.MaxFloat64}, math.MaxInt64, math.MaxFloat64},
		{Definition{Type: Float, Value: 1.0, Step: math.MaxFloat64}, math.MinInt64, -math.MaxFloat64},
		{Definition{Type: Float, Value: 1.0, Step: 0.5, Min: 0.0, Max: 2.0}, 10, 2.0},
		{Definition{Type: Float, Value: 1.0, Step: 0.5, Min: 0.0}, -10, 0.0},
		{Definition{Type: Float, Value: 1.0, Step: 0.5}, 1, 1.5},
	}
	for _, c := range cases {
		got, err := c.d.Rotate(c.delta)
		if err != nil || got != c.want {
			t.Fatalf("%+v delta %d: %v %v; want %v", c.d, c.delta, got, err, c.want)
		}
	}
	if _, err := (Definition{Type: Boolean, Value: true}).Rotate(1); err == nil {
		t.Fatal("boolean rotation")
	}
}
