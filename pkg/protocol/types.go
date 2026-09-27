// Package protocol defines v1 JSON contracts. It has no dependency on daemon
// implementation packages.
package protocol

import (
	"encoding/json"
	"net/http"
	"time"
)

type Code string

const (
	InvalidConfig        Code = "invalid_config"
	InvalidRequest       Code = "invalid_request"
	NotFound             Code = "not_found"
	ActionNotFound       Code = "action_not_found"
	ProjectNotFound      Code = "project_not_found"
	PermissionDenied     Code = "permission_denied"
	ConfirmationRequired Code = "confirmation_required"
	ProviderUnavailable  Code = "provider_unavailable"
	ExecutionFailed      Code = "execution_failed"
	Cancelled            Code = "cancelled"
	Timeout              Code = "timeout"
	Internal             Code = "internal"
	Busy                 Code = "busy"
	ShuttingDown         Code = "shutting_down"
)

func (c Code) HTTPStatus() int {
	switch c {
	case InvalidConfig, InvalidRequest:
		return http.StatusBadRequest
	case NotFound, ActionNotFound, ProjectNotFound:
		return http.StatusNotFound
	case PermissionDenied:
		return http.StatusForbidden
	case ConfirmationRequired, Cancelled:
		return http.StatusConflict
	case ProviderUnavailable, Busy, ShuttingDown:
		return http.StatusServiceUnavailable
	case ExecutionFailed:
		return http.StatusUnprocessableEntity
	case Timeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

type Error struct {
	Code         Code                 `json:"code"`
	Message      string               `json:"message"`
	Confirmation *ControlConfirmation `json:"confirmation,omitempty"`
}

func (c Code) Valid() bool {
	switch c {
	case InvalidConfig, InvalidRequest, NotFound, ActionNotFound, ProjectNotFound,
		PermissionDenied, ConfirmationRequired, ProviderUnavailable, ExecutionFailed,
		Cancelled, Timeout, Internal, Busy, ShuttingDown:
		return true
	default:
		return false
	}
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

type ErrorResponse struct {
	Error Error `json:"error"`
}

type Context struct {
	Project string            `json:"project,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	Values  map[string]string `json:"values,omitempty"`
}

// Pointer fields distinguish an omitted patch member from an explicit empty value.
type ContextPatch struct {
	Project *string            `json:"project,omitempty"`
	Mode    *string            `json:"mode,omitempty"`
	Values  *map[string]string `json:"values,omitempty"`
}

type ProjectSelection struct {
	Project string `json:"project"`
}
type Project struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Path     string            `json:"path"`
	GitHub   string            `json:"github,omitempty"`
	Language string            `json:"language,omitempty"`
	Engine   string            `json:"engine,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}
type ProjectList struct {
	Projects []Project `json:"projects"`
}

type ExecutionMode string

const (
	Sync  ExecutionMode = "sync"
	Async ExecutionMode = "async"
)

// Invocation never accepts a client-supplied runtime context, safety label,
// provider, executable, or environment. Args are checked against declared inputs.
type Invocation struct {
	Args      map[string]any `json:"args,omitempty"`
	Mode      ExecutionMode  `json:"mode,omitempty"`
	Confirmed bool           `json:"confirmed,omitempty"`
	TimeoutMS int64          `json:"timeout_ms,omitempty"`
}

type Input struct {
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Default  any      `json:"default,omitempty"`
	Min      any      `json:"min,omitempty"`
	Max      any      `json:"max,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}
type Action struct {
	Name   string           `json:"name"`
	Type   string           `json:"type"`
	Safety string           `json:"safety"`
	Inputs map[string]Input `json:"inputs,omitempty"`
}
type ActionList struct {
	Actions []Action `json:"actions"`
}
type Workflow struct {
	Name        string         `json:"name"`
	StopOnError bool           `json:"stop_on_error"`
	Steps       []WorkflowStep `json:"steps"`
}
type WorkflowStep struct {
	Action string `json:"action"`
}
type WorkflowList struct {
	Workflows []Workflow `json:"workflows"`
}

type DisplayResult struct {
	Title    string `json:"title,omitempty"`
	Value    string `json:"value,omitempty"`
	State    string `json:"state,omitempty"`
	Progress *int   `json:"progress,omitempty"`
}
type Result struct {
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Display *DisplayResult `json:"display,omitempty"`
}
type Job struct {
	ID         string     `json:"id"`
	Action     string     `json:"action"`
	State      string     `json:"state"`
	Generation uint64     `json:"generation"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Result     *Result    `json:"result,omitempty"`
	Error      *Error     `json:"error,omitempty"`
}
type JobList struct {
	Jobs []Job `json:"jobs"`
}
type InvocationResponse struct {
	JobID  string  `json:"job_id"`
	Result *Result `json:"result,omitempty"`
	Error  *Error  `json:"error,omitempty"`
}

type Parameter struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Value      any      `json:"value"`
	Min        any      `json:"min,omitempty"`
	Max        any      `json:"max,omitempty"`
	Step       any      `json:"step,omitempty"`
	Enum       []string `json:"enum,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	Persistent bool     `json:"persistent"`
}
type ParameterList struct {
	Parameters []Parameter `json:"parameters"`
}
type ParameterSet struct {
	Value any `json:"value"`
}

type EventRequest struct {
	Type    string          `json:"type"`
	Source  string          `json:"source"`
	Payload json.RawMessage `json:"payload"`
}
type ControlPayload struct {
	Device       string        `json:"device,omitempty"`
	Control      string        `json:"control"`
	Delta        int64         `json:"delta,omitempty"`
	Confirmed    bool          `json:"confirmed,omitempty"`
	Guard        *ControlGuard `json:"guard,omitempty"`
	Confirmation string        `json:"confirmation,omitempty"`
}
type EventResponse struct {
	EventID string `json:"event_id"`
	Matched bool   `json:"matched"`
	JobID   string `json:"job_id,omitempty"`
}
type ReloadResponse struct {
	Generation uint64 `json:"generation"`
}
type ProviderHealth struct {
	Available bool   `json:"available"`
	Code      string `json:"code,omitempty"`
}
type Status struct {
	Version     string                    `json:"version"`
	UptimeMS    int64                     `json:"uptime_ms"`
	ConfigPath  string                    `json:"config_path"`
	Project     string                    `json:"project,omitempty"`
	Mode        string                    `json:"mode,omitempty"`
	RunningJobs int                       `json:"running_jobs"`
	Generation  uint64                    `json:"generation"`
	Providers   map[string]ProviderHealth `json:"providers"`
}
