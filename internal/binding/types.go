// Package binding defines device-independent input mappings.
package binding

type Target struct {
	Action    string         `yaml:"action,omitempty"`
	Parameter string         `yaml:"parameter,omitempty"`
	Args      map[string]any `yaml:"args,omitempty"`
}

type Binding struct {
	Device    string            `yaml:"device,omitempty"`
	Control   string            `yaml:"control"`
	When      map[string]string `yaml:"when,omitempty"`
	Press     *Target           `yaml:"press,omitempty"`
	Release   *Target           `yaml:"release,omitempty"`
	Rotate    *Target           `yaml:"rotate,omitempty"`
	LongPress *Target           `yaml:"long_press,omitempty"`
	Touch     *Target           `yaml:"touch,omitempty"`
	LongTouch *Target           `yaml:"long_touch,omitempty"`
}

// Rank implements the specification's four precedence tiers. Context predicate
// count is deliberately not an additional tie-breaker.
func (b Binding) Rank() int {
	rank := 0
	if b.Device != "" {
		rank++
	}
	if len(b.When) > 0 {
		rank += 2
	}
	return rank
}

// Overlaps reports ambiguity within one precedence tier and input gesture.
func Overlaps(a, b Binding) bool {
	if a.Control != b.Control || a.Device != b.Device || a.Rank() != b.Rank() {
		return false
	}
	sharedGesture := a.Press != nil && b.Press != nil || a.Release != nil && b.Release != nil || a.Rotate != nil && b.Rotate != nil
	sharedGesture = sharedGesture || a.LongPress != nil && b.LongPress != nil || a.Touch != nil && b.Touch != nil || a.LongTouch != nil && b.LongTouch != nil
	if !sharedGesture {
		return false
	}
	for k, v := range a.When {
		if other, exists := b.When[k]; exists && other != v {
			return false
		}
	}
	return true
}
