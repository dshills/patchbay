// Package workflow defines and executes ordered, prepared workflows.
package workflow

import (
	"patchbay/internal/action"
	"patchbay/pkg/protocol"
	"time"
)

type Workflow struct {
	Name        string
	StopOnError bool
	Steps       []WorkflowStep
}

type WorkflowStep struct {
	Action string         `json:"action" yaml:"action"`
	Args   map[string]any `json:"args,omitempty" yaml:"args,omitempty"`
}

type StepResult struct {
	Index      int             `json:"index"`
	Action     string          `json:"action"`
	Result     *action.Result  `json:"result,omitempty"`
	Skipped    bool            `json:"skipped,omitempty"`
	Error      *protocol.Error `json:"error,omitempty"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}
