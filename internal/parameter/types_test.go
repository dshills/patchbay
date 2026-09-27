package parameter

import (
	"encoding/json"
	"math"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name        string
		definition  Definition
		value, step any
	}{
		{"integer", Definition{Type: Integer, Value: 3, Min: 0, Max: 5}, int64(3), int64(1)},
		{"large integer", Definition{Type: Integer, Value: json.Number("9223372036854775807")}, int64(math.MaxInt64), int64(1)},
		{"negative integer", Definition{Type: Integer, Value: json.Number("-9223372036854775808")}, int64(math.MinInt64), int64(1)},
		{"uint integer", Definition{Type: Integer, Value: uint64(42)}, int64(42), int64(1)},
		{"float", Definition{Type: Float, Value: 1, Min: 0.0, Max: 3, Step: 0.5}, 1.0, 0.5},
		{"json float", Definition{Type: Float, Value: json.Number("1.25")}, 1.25, 1.0},
		{"int64 float", Definition{Type: Float, Value: int64(1)}, 1.0, 1.0},
		{"uint float", Definition{Type: Float, Value: uint64(1)}, 1.0, 1.0},
		{"bool", Definition{Type: Boolean, Value: false}, false, nil},
		{"enum", Definition{Type: Enum, Value: "a", Enum: []string{"a", "b"}}, "a", nil},
		{"string", Definition{Type: String, Value: ""}, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.definition
			if err := d.Normalize(); err != nil {
				t.Fatal(err)
			}
			if d.Value != tc.value || d.Step != tc.step {
				t.Fatalf("value/step: %v %v; expected %v %v", d.Value, d.Step, tc.value, tc.step)
			}
			if err := d.Normalize(); err != nil {
				t.Fatalf("not idempotent: %v", err)
			}
		})
	}
}

func TestRejectInvalidDefinitions(t *testing.T) {
	cases := []Definition{
		{Type: "unknown", Value: 1},
		{Type: Integer, Value: 1.0},
		{Type: Integer, Value: uint64(math.MaxUint64)},
		{Type: Integer, Value: json.Number("1e3")},
		{Type: Integer, Value: json.Number("9223372036854775808")},
		{Type: Integer, Value: 1, Min: 1.1},
		{Type: Integer, Value: 1, Step: 0},
		{Type: Integer, Value: 1, Min: 2, Max: 1},
		{Type: Integer, Value: 0, Min: 1},
		{Type: Integer, Value: 2, Max: 1},
		{Type: Float, Value: math.NaN()},
		{Type: Float, Value: math.Inf(1)},
		{Type: Float, Value: json.Number("1e1000")},
		{Type: Float, Value: "1"},
		{Type: Float, Value: 1.0, Step: -1},
		{Type: Float, Value: 1.0, Min: 2, Max: 1},
		{Type: Float, Value: 0.0, Min: 1},
		{Type: Float, Value: 2.0, Max: 1},
		{Type: Boolean, Value: "false"},
		{Type: String, Value: 1},
		{Type: String, Value: "hello", Min: 1},
		{Type: String, Value: "hello", Enum: []string{"hello"}},
		{Type: Enum, Value: "a"},
		{Type: Enum, Value: "a", Enum: []string{"a", "a"}},
		{Type: Enum, Value: "b", Enum: []string{"a"}},
		{Type: Integer},
	}
	for i, d := range cases {
		if err := d.Normalize(); err == nil {
			t.Errorf("accepted case %d: %+v", i, d)
		}
	}
}
