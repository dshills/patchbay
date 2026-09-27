package protocol

import "time"

// ControlGuard identifies a binding/context generation within one daemon life.
type ControlGuard struct {
	Instance string `json:"instance"`
	Revision uint64 `json:"revision"`
}

type ControlRef struct {
	Device  string `json:"device"`
	Control string `json:"control"`
}

type ControlSnapshotRequest struct {
	Controls []ControlRef `json:"controls"`
}

type ControlTarget struct {
	Action    string     `json:"action,omitempty"`
	Safety    string     `json:"safety,omitempty"`
	Enabled   bool       `json:"enabled"`
	Parameter *Parameter `json:"parameter,omitempty"`
}

type ControlView struct {
	ControlRef
	Targets map[string]ControlTarget `json:"targets"`
}

type ControlSnapshot struct {
	Guard      ControlGuard  `json:"guard"`
	Generation uint64        `json:"generation"`
	Context    Context       `json:"context"`
	Controls   []ControlView `json:"controls"`
}

type ControlConfirmation struct {
	Token     string    `json:"token"`
	Action    string    `json:"action"`
	ExpiresAt time.Time `json:"expires_at"`
}
