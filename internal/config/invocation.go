package config

import (
	"errors"
	"strings"
)

// Arguments validates supplied values and inserts explicit defaults into a new
// map. The returned scalar values are canonical and independent of input maps.
func (a Action) Arguments(supplied map[string]any) (map[string]any, error) {
	values := make(map[string]any, len(a.Inputs))
	for key := range supplied {
		if _, exists := a.Inputs[key]; !exists {
			return nil, errors.New("undeclared action argument")
		}
	}
	for _, key := range keys(a.Inputs) {
		input := a.Inputs[key]
		value, present := supplied[key]
		if !present {
			value = input.Default
		}
		if value == nil {
			if input.Required || present {
				return nil, errors.New("required or non-null action argument is missing")
			}
			continue
		}
		d := input.definition()
		if err := d.Normalize(); err != nil {
			return nil, err
		}
		normalized, err := d.ValidateValue(value)
		if err != nil {
			return nil, err
		}
		values[key] = normalized
	}
	return values, nil
}

// Render substitutes only already-validated placeholders and never reparses
// substituted data. Missing context/input values fail before execution.
func Render(template string, values map[string]string) (string, error) {
	var result strings.Builder
	for {
		start := strings.Index(template, "{{")
		if start < 0 {
			result.WriteString(template)
			return result.String(), nil
		}
		end := strings.Index(template[start+2:], "}}")
		if end < 0 {
			return "", errors.New("invalid template")
		}
		end += start + 2
		key := strings.TrimSpace(template[start+2 : end])
		value, exists := values[key]
		if !exists {
			return "", errors.New("required template variable is missing")
		}
		result.WriteString(template[:start])
		result.WriteString(value)
		template = template[end+2:]
	}
}

func ValidName(name string) bool     { return namePattern.MatchString(name) }
func ValidValueKey(name string) bool { return valueKeyPattern.MatchString(name) }
