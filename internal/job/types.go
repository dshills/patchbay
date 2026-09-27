// Package job defines job snapshots and legal state transitions.
package job

import (
	"patchbay/internal/action"
	"time"
)

type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Success   State = "success"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

func CanTransition(from, to State) bool {
	return from == Queued && (to == Running || to == Cancelled) ||
		from == Running && (to == Success || to == Failed || to == Cancelled)
}

type Job struct {
	ID         string         `json:"id"`
	Action     string         `json:"action"`
	State      State          `json:"state"`
	Generation uint64         `json:"generation"`
	CreatedAt  time.Time      `json:"created_at"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Result     *action.Result `json:"result,omitempty"`
}
