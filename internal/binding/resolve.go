package binding

import (
	runtimecontext "patchbay/internal/context"
	"patchbay/internal/event"
	"strings"
)

// Resolve uses validated bindings and returns the highest matching tier for one
// gesture. The caller must hold its configuration/context snapshot stable.
func Resolve(bindings []Binding, device, control, gesture string, state runtimecontext.RuntimeContext) *Target {
	rank := -1
	var selected *Target
	for _, b := range bindings {
		if b.Control != control || b.Device != "" && b.Device != device {
			continue
		}
		matches := true
		for key, expected := range b.When {
			var actual string
			exists := true
			switch key {
			case "project":
				actual = state.Project
			case "mode":
				actual = state.Mode
			default:
				actual, exists = state.Values[strings.TrimPrefix(key, "values.")]
			}
			if !exists || actual != expected {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		var target *Target
		switch gesture {
		case event.ControlPressed:
			target = b.Press
		case event.ControlReleased:
			target = b.Release
		case event.ControlRotated:
			target = b.Rotate
		}
		if target != nil && b.Rank() > rank {
			selected, rank = target, b.Rank()
		}
	}
	return selected
}
