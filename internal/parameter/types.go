// Package parameter defines typed parameter metadata and pure value validation.
// Synchronized mutation and persistence are coordinated by internal/runtime.
package parameter

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

type Type string

const (
	Integer Type = "integer"
	Float   Type = "float"
	Boolean Type = "boolean"
	Enum    Type = "enum"
	String  Type = "string"
)

type Parameter struct {
	Name string `json:"name"`
	Definition
}

type Definition struct {
	Type       Type     `json:"type" yaml:"type"`
	Value      any      `json:"value" yaml:"value"`
	Min        any      `json:"min,omitempty" yaml:"min,omitempty"`
	Max        any      `json:"max,omitempty" yaml:"max,omitempty"`
	Step       any      `json:"step,omitempty" yaml:"step,omitempty"`
	Enum       []string `json:"enum,omitempty" yaml:"enum,omitempty"`
	Unit       string   `json:"unit,omitempty" yaml:"unit,omitempty"`
	Persistent bool     `json:"persistent" yaml:"persistent,omitempty"`
}

// Normalize validates metadata and returns canonical int64/float64 scalar values.
// Errors describe the constraint without echoing potentially sensitive values.
func (d *Definition) Normalize() error {
	if d.Type != Integer && d.Type != Float && d.Type != Boolean && d.Type != Enum && d.Type != String {
		return errors.New("type must be integer, float, boolean, enum, or string")
	}
	if d.Type == Enum {
		if len(d.Enum) == 0 {
			return errors.New("enum must contain at least one option")
		}
		seen := map[string]bool{}
		for _, option := range d.Enum {
			if seen[option] {
				return errors.New("enum options must be unique")
			}
			seen[option] = true
		}
	} else if len(d.Enum) != 0 {
		return errors.New("enum is only valid for enum parameters")
	}
	if d.Type == Integer || d.Type == Float {
		for _, bound := range []*any{&d.Min, &d.Max, &d.Step} {
			if *bound == nil {
				continue
			}
			v, err := scalar(d.Type, *bound)
			if err != nil {
				return errors.New("min, max, and step must match the numeric type")
			}
			*bound = v
		}
		if d.Step == nil {
			if d.Type == Integer {
				d.Step = int64(1)
			} else {
				d.Step = float64(1)
			}
		}
		if d.Type == Integer {
			if d.Step.(int64) <= 0 {
				return errors.New("step must be positive")
			}
			if d.Min != nil && d.Max != nil && d.Min.(int64) > d.Max.(int64) {
				return errors.New("min must not exceed max")
			}
		} else {
			if d.Step.(float64) <= 0 {
				return errors.New("step must be positive")
			}
			if d.Min != nil && d.Max != nil && d.Min.(float64) > d.Max.(float64) {
				return errors.New("min must not exceed max")
			}
		}
	} else if d.Min != nil || d.Max != nil || d.Step != nil {
		return errors.New("min, max, and step require a numeric type")
	}
	v, err := d.ValidateValue(d.Value)
	if err != nil {
		return err
	}
	d.Value = v
	return nil
}

// ValidateValue requires normalized metadata, as produced by Normalize.
func (d Definition) ValidateValue(value any) (any, error) {
	v, err := scalar(d.Type, value)
	if err != nil {
		return nil, err
	}
	switch d.Type {
	case Integer:
		if d.Min != nil && v.(int64) < d.Min.(int64) || d.Max != nil && v.(int64) > d.Max.(int64) {
			return nil, errors.New("value is outside bounds")
		}
	case Float:
		if d.Min != nil && v.(float64) < d.Min.(float64) || d.Max != nil && v.(float64) > d.Max.(float64) {
			return nil, errors.New("value is outside bounds")
		}
	case Enum:
		for _, option := range d.Enum {
			if option == v.(string) {
				return v, nil
			}
		}
		return nil, errors.New("value is not an enum option")
	}
	return v, nil
}

func scalar(t Type, value any) (any, error) {
	switch t {
	case Integer:
		switch v := value.(type) {
		case int:
			return int64(v), nil
		case int64:
			return v, nil
		case uint64:
			if v <= math.MaxInt64 {
				return int64(v), nil
			}
		case json.Number:
			if n, err := strconv.ParseInt(string(v), 10, 64); err == nil {
				return n, nil
			}
		}
	case Float:
		var n float64
		switch v := value.(type) {
		case float64:
			n = v
		case int:
			n = float64(v)
		case int64:
			n = float64(v)
		case uint64:
			n = float64(v)
		case json.Number:
			var err error
			n, err = strconv.ParseFloat(string(v), 64)
			if err != nil {
				return nil, errors.New("value must be a finite number")
			}
		default:
			return nil, errors.New("value must be a number")
		}
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, nil
		}
	case Boolean:
		if v, ok := value.(bool); ok {
			return v, nil
		}
	case Enum, String:
		if v, ok := value.(string); ok {
			return v, nil
		}
	}
	return nil, errors.New("value does not match its declared type")
}
