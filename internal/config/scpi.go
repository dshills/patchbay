package config

import (
	"fmt"

	"patchbay/internal/parameter"
)

func (v *validator) scpiDevices(c *Config) error {
	if len(c.Devices) > 16 {
		return v.fail("devices", "at most 16 instruments are supported")
	}
	addresses := map[string]bool{}
	for _, id := range keys(c.Devices) {
		d := c.Devices[id]
		if err := d.Normalize(); err != nil {
			return v.fail("devices."+id, err.Error())
		}
		if addresses[d.Address] {
			return v.fail("devices."+id, "instrument endpoints must be unique")
		}
		addresses[d.Address] = true
		c.Devices[id] = d
	}
	return nil
}
func (v *validator) scpiParameters(c *Config) error {
	seen := map[string]bool{}
	for _, name := range keys(c.Parameters) {
		p := c.Parameters[name]
		b := p.Instrument
		if b == nil {
			continue
		}
		d, ok := c.Devices[b.Device]
		if b.Channel == 0 {
			b.Channel = 1
		}
		kind, unit, lo, hi := d.ValueSpec(b.Operation)
		key := fmt.Sprintf("%s/%d/%s", b.Device, b.Channel, b.Operation)
		if !ok || !d.Supports(b.Operation, b.Channel) || kind == "" || string(p.Type) != kind || p.Unit != unit || seen[key] {
			return v.fail("parameters."+name, "instrument binding must uniquely match a device operation, type and unit")
		}
		if kind == "float" && (p.Min == nil || p.Max == nil || p.Min.(float64) < lo || p.Max.(float64) > hi) {
			return v.fail("parameters."+name, "instrument parameter bounds must remain within device limits")
		}
		if b.Operation == "generator.output" && (p.Persistent || p.Value != false) {
			return v.fail("parameters."+name, "output desired state must start false and cannot persist")
		}
		seen[key] = true
		c.Parameters[name] = p
	}
	return nil
}
func (v *validator) scpiAction(path string, a *Action, c *Config) error {
	if a.Command != "" || len(a.Args) != 0 || a.Cwd != "" || len(a.Environment) != 0 || a.Target != "" || a.Workflow != "" || a.Provider != "" || a.Prompt != "" || len(a.Files) != 0 || !v.composed && len(a.Inputs) != 0 {
		return v.fail(path, "SCPI accepts only device, operation, channel or a bound parameter, plus safety and timeout; value inputs are generated")
	}
	if a.Parameter != "" {
		p, ok := c.Parameters[a.Parameter]
		if !ok || p.Instrument == nil || a.Device != "" || a.Operation != "" || a.Channel != 0 {
			return v.fail(path, "parameter actions require a bound instrument parameter and no separate device, operation or channel")
		}
		a.Device, a.Operation, a.Channel = p.Instrument.Device, p.Instrument.Operation, p.Instrument.Channel
	}
	if a.Channel == 0 {
		a.Channel = 1
	}
	d, ok := c.Devices[a.Device]
	if !ok || !d.Supports(a.Operation, a.Channel) {
		return v.fail(path, "unsupported instrument, semantic operation or channel")
	}
	kind, _, lo, hi := d.ValueSpec(a.Operation)
	if v.composed && len(a.Inputs) > 0 {
		input, exists := a.Inputs["value"]
		if a.Parameter != "" || !exists || len(a.Inputs) != 1 || kind == "" || string(input.Type) != kind {
			return v.fail(path, "composed SCPI input must match the device operation")
		}
		schema := input.definition()
		if err := schema.Normalize(); err != nil {
			return v.fail(path, err.Error())
		}
		if kind == "float" && (schema.Min == nil || schema.Max == nil || schema.Min.(float64) < lo || schema.Max.(float64) > hi) {
			return v.fail(path, "composed SCPI input exceeds device limits")
		}
		return nil
	}
	if kind != "" && a.Parameter == "" {
		input := Input{Type: parameter.Type(kind), Required: true}
		if kind == "float" {
			input.Min, input.Max = lo, hi
		}
		a.Inputs = map[string]Input{"value": input}
	}
	return nil
}
