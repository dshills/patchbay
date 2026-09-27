package action

import (
	"errors"
	"maps"
	"slices"
)

// Registry holds a generation's definitions. Build it before publication, then
// use it read-only. T is normally config.Action; this avoids a config import cycle.
type Registry[T any] struct{ entries map[string]T }

func NewRegistry[T any]() *Registry[T] { return &Registry[T]{entries: map[string]T{}} }
func (r *Registry[T]) Register(name string, value T) error {
	if name == "" {
		return errors.New("action name is required")
	}
	if _, exists := r.entries[name]; exists {
		return errors.New("duplicate action name")
	}
	r.entries[name] = value
	return nil
}
func (r *Registry[T]) Get(name string) (T, bool) {
	value, exists := r.entries[name]
	return value, exists
}
func (r *Registry[T]) Names() []string { return slices.Sorted(maps.Keys(r.entries)) }
