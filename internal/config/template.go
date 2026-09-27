package config

import (
	"errors"
	"regexp"
	"strings"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var valueKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateTemplate accepts substitutions only, never Go template expressions.
// inputs is the allowlist of declared invocation arguments.
func ValidateTemplate(text string, inputs map[string]Input) error {
	for {
		start := strings.Index(text, "{{")
		end := strings.Index(text, "}}")
		if start < 0 && end < 0 {
			return nil
		}
		if start < 0 || end < start {
			return errors.New("unbalanced template delimiters")
		}
		token := strings.TrimSpace(text[start+2 : end])
		allowed := token == ".project.path" || token == ".project.id" || token == ".project.name" || token == ".context.mode"
		if key, ok := strings.CutPrefix(token, ".context.values."); ok {
			allowed = valueKeyPattern.MatchString(key)
		}
		if key, ok := strings.CutPrefix(token, ".args."); ok {
			_, exists := inputs[key]
			allowed = valueKeyPattern.MatchString(key) && exists
		}
		if !allowed {
			return errors.New("template must use an allowed project/context variable or declared input")
		}
		text = text[end+2:]
	}
}
