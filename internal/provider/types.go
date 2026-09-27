// Package provider defines small integration capabilities, not device-specific code.
package provider

import "context"

type Health struct {
	Available bool   `json:"available"`
	Code      string `json:"code,omitempty"`
}

type Provider interface {
	Name() string
	Health(context.Context) Health
}

type Lifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type Device struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Capabilities []string `json:"capabilities,omitempty"`
}
