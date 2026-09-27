package runtime

import (
	"slices"
	"time"

	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/internal/parameter"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
)

func (r *Runtime) parameterSnapshot(name string, p parameter.Definition) parameter.Parameter {
	p.Enum = slices.Clone(p.Enum)
	result := parameter.Parameter{Name: name, Definition: p}
	if p.Instrument == nil {
		return result
	}
	b := *p.Instrument
	result.Instrument = &b
	s := r.synchronization[name]
	if s.Status == "" {
		s.Status = "unobserved"
	}
	s.Desired = p.Value
	if s.ObservedAt != nil {
		at := *s.ObservedAt
		s.ObservedAt = &at
	}
	result.Synchronization = &s
	return result
}
func (r *Runtime) observeInstrument(generation uint64, o provider.SCPIObservation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	for name, p := range r.parameters {
		b := p.Instrument
		if b == nil || b.Device != o.Device || b.Channel != o.Channel {
			continue
		}
		if r.generation != generation {
			// An admitted old job can finish after a newer generation's readback.
			// Its hardware effect invalidates that readback, but cannot publish
			// values under the new parameter binding.
			s := parameter.Synchronization{Status: "unobserved", Desired: p.Value}
			r.synchronization[name] = s
			r.bus.Emit(event.ParameterChanged, map[string]any{"name": name, "value": p.Value, "synchronization": s})
			continue
		}
		value, exists := o.Values[b.Operation]
		if !exists && o.Err == nil {
			continue
		}
		s := r.synchronization[name]
		s.Desired = p.Value
		if exists {
			s.Observed = value
			s.ObservedAt = &now
		}
		s.ErrorCode = ""
		if o.Err != nil {
			s.Status = "error"
			s.ErrorCode = string(fault.Safe(o.Err).Code)
		} else if value == p.Value {
			s.Status = "matched"
		} else {
			s.Status = "different"
		}
		r.synchronization[name] = s
		r.bus.Emit(event.ParameterChanged, map[string]any{"name": name, "value": p.Value, "synchronization": s})
	}
}
func scpiCapabilities(d provider.SCPIDevice) []string {
	if d.Profile == "rigol-dg800" {
		return []string{"generator.inspect", "generator.set_frequency", "generator.set_amplitude", "generator.output", "generator.disable"}
	}
	return []string{"scope.capture"}
}
func synchronizationWire(s *parameter.Synchronization) *protocol.ParameterSynchronization {
	if s == nil {
		return nil
	}
	return &protocol.ParameterSynchronization{Status: s.Status, Desired: s.Desired, Observed: s.Observed, ObservedAt: s.ObservedAt, ErrorCode: s.ErrorCode}
}
