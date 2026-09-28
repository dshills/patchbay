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
	Agent     *AgentSelection `json:"agent,omitempty"`
	Result    *ControlResult  `json:"result,omitempty"`
	Action    string          `json:"action,omitempty"`
	Safety    string          `json:"safety,omitempty"`
	Enabled   bool            `json:"enabled"`
	Parameter *Parameter      `json:"parameter,omitempty"`
}
type ControlResult struct {
	Experiment  string       `json:"experiment"`
	RunID       string       `json:"run_id,omitempty"`
	State       string       `json:"state"`
	Baseline    bool         `json:"baseline"`
	Measurement *Measurement `json:"measurement,omitempty"`
}

type ControlView struct {
	ControlRef
	Targets map[string]ControlTarget `json:"targets"`
}

type ControlSnapshot struct {
	AgentRevision uint64        `json:"agent_revision"`
	Guard         ControlGuard  `json:"guard"`
	Generation    uint64        `json:"generation"`
	Context       Context       `json:"context"`
	Controls      []ControlView `json:"controls"`
}

type ControlConfirmation struct {
	Capture   *CapturePreview `json:"capture,omitempty"`
	Token     string          `json:"token"`
	Action    string          `json:"action"`
	ExpiresAt time.Time       `json:"expires_at"`
}

// AgentSelection identifies one user-selected proposal or job in one daemon life.
type AgentSelection struct {
	Revision        uint64 `json:"revision"`
	Project         string `json:"project"`
	Session         string `json:"session,omitempty"`
	Proposal        string `json:"proposal,omitempty"`
	Job             string `json:"job,omitempty"`
	State           string `json:"state,omitempty"`
	ReviewRequested uint64 `json:"review_requested"`
	ReviewActive    bool   `json:"review_active"`
}
type AgentSelect struct {
	Revision uint64 `json:"revision"`
	Proposal string `json:"proposal,omitempty"`
	Session  string `json:"session,omitempty"`
	Job      string `json:"job,omitempty"`
}
type AgentReviewLink struct {
	Revision    uint64 `json:"revision"`
	Preparation string `json:"preparation"`
	Digest      string `json:"digest"`
	Renew       bool   `json:"renew,omitempty"`
}
