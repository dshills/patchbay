// Package feedback defines device-independent presentation hints.
package feedback

type State string

const (
	Idle     State = "idle"
	Running  State = "running"
	Success  State = "success"
	Warning  State = "warning"
	Error    State = "error"
	Disabled State = "disabled"
)

type DisplayResult struct {
	Title    string `json:"title,omitempty"`
	Value    string `json:"value,omitempty"`
	State    State  `json:"state,omitempty"`
	Progress *int   `json:"progress,omitempty"`
}

type Feedback struct {
	JobID   string        `json:"job_id,omitempty"`
	Control string        `json:"control,omitempty"`
	Display DisplayResult `json:"display"`
}
