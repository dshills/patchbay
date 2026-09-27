// Package context defines runtime context snapshots, independently of clients.
package context

// RuntimeContext is copied at admission; maps must not be shared with mutable state.
type RuntimeContext struct {
	Project string            `json:"project,omitempty" yaml:"project,omitempty"`
	Mode    string            `json:"mode,omitempty" yaml:"mode,omitempty"`
	Values  map[string]string `json:"values,omitempty" yaml:"values,omitempty"`
}
