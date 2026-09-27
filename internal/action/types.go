// Package action defines semantic execution contracts. Scheduling is owned by job.
package action

import (
	"context"
	runtimecontext "patchbay/internal/context"
	"patchbay/internal/feedback"
)

type Action interface {
	Name() string
	Execute(context.Context, ActionRequest) (Result, error)
}

type ActionRequest struct {
	Action  string                        `json:"action"`
	Args    map[string]any                `json:"args,omitempty"`
	Context runtimecontext.RuntimeContext `json:"context"`
}

type Status string

const (
	Success   Status = "success"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

type Result struct {
	Status  Status                  `json:"status"`
	Message string                  `json:"message,omitempty"`
	Data    map[string]any          `json:"data,omitempty"`
	Display *feedback.DisplayResult `json:"display,omitempty"`
}
