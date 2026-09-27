// Package event defines runtime notifications and external control events.
package event

import (
	"encoding/json"
	"time"
)

const (
	ControlPressed     = "control.pressed"
	ControlReleased    = "control.released"
	ControlRotated     = "control.rotated"
	ControlLongPressed = "control.long_pressed"
	ControlTouched     = "control.touched"
	ControlLongTouched = "control.long_touched"
	ContextChanged     = "context.changed"
	ProjectChanged     = "project.changed"
	ParameterChanged   = "parameter.changed"
	ActionStarted      = "action.started"
	ActionFinished     = "action.finished"
	JobQueued          = "job.queued"
	JobRunning         = "job.running"
	JobFinished        = "job.finished"
	ConfigReloaded     = "config.reloaded"
)

type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type ControlPayload struct {
	Device    string `json:"device,omitempty"`
	Control   string `json:"control"`
	Delta     int64  `json:"delta,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
}
