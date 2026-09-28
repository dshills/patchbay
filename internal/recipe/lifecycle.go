package recipe

import (
	"encoding/json"
	"patchbay/internal/config"
	"patchbay/pkg/protocol"
	"time"
)

type Prepare struct {
	Operation   string                `json:"operation"`
	Content     string                `json:"content,omitempty"`
	Alias       string                `json:"alias,omitempty"`
	Mappings    map[string]Mapping    `json:"mappings,omitempty"`
	Assignments map[string]Assignment `json:"assignments,omitempty"`
	Reset       bool                  `json:"reset_parameters,omitempty"`
}
type Commit struct {
	Preparation string `json:"preparation"`
	Digest      string `json:"digest"`
	RequestID   string `json:"request_id"`
	Confirmed   bool   `json:"confirmed"`
}
type DefinitionPreview struct {
	Name             string         `json:"name"`
	Type             string         `json:"type"`
	Safety           string         `json:"safety"`
	Executable       string         `json:"executable,omitempty"`
	Arguments        []string       `json:"arguments,omitempty"`
	Directory        string         `json:"directory,omitempty"`
	Target           string         `json:"target,omitempty"`
	Workflow         string         `json:"workflow,omitempty"`
	Inputs           map[string]any `json:"inputs,omitempty"`
	EnvironmentNames []string       `json:"environment_names,omitempty"`
	Device           string         `json:"device,omitempty"`
	Operation        string         `json:"operation,omitempty"`
	Channel          int            `json:"channel,omitempty"`
	Parameter        string         `json:"parameter,omitempty"`
	Plugin           string         `json:"plugin,omitempty"`
	Prompt           string         `json:"prompt,omitempty"`
	Files            []string       `json:"files,omitempty"`
}
type Preview struct {
	ExperimentSteps map[string][]DefinitionPreview `json:"experiment_steps,omitempty"`
	Cleanup         []File                         `json:"cleanup,omitempty"`
	Workflows       map[string]config.Workflow     `json:"effective_workflows,omitempty"`
	Experiments     map[string]protocol.Experiment `json:"effective_experiments,omitempty"`
	ID              string                         `json:"id"`
	Digest          string                         `json:"digest"`
	ExpiresAt       time.Time                      `json:"expires_at"`
	Instance        string                         `json:"instance"`
	Generation      uint64                         `json:"generation"`
	StoreRevision   uint64                         `json:"store_revision"`
	Operation       string                         `json:"operation"`
	Installation    Installation                   `json:"installation"`
	Project         string                         `json:"project"`
	Definitions     []DefinitionPreview            `json:"definitions"`
	Changes         []string                       `json:"changes"`
	Disabled        []string                       `json:"disabled"`
	Resets          []string                       `json:"parameter_resets"`
	Before          *Manifest                      `json:"before,omitempty"`
	After           *Manifest                      `json:"after,omitempty"`
	Inventory       []File                         `json:"inventory"`
}
type Document struct {
	Path      string `json:"path"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}
type View struct {
	Documents    []Document   `json:"documents,omitempty"`
	Installation Installation `json:"installation"`
	Status       string       `json:"status"`
	Package      *Package     `json:"package,omitempty"`
	Candidate    *Package     `json:"candidate,omitempty"`
	Diagnostics  []string     `json:"diagnostics"`
}
type List struct {
	Installations []View   `json:"installations"`
	Diagnostics   []string `json:"diagnostics"`
	Revision      uint64   `json:"revision"`
	Bytes         int64    `json:"bytes"`
	Unused        []File   `json:"unused"`
}
type ImportResult struct {
	Installation Installation `json:"installation"`
	Digest       string       `json:"package_digest"`
}
type SampleView struct {
	Recipe protocol.RecipeOrigin `json:"recipe"`
	Sample protocol.Sample       `json:"sample"`
}

// An empty assignment object explicitly clears controls; nil retains them.
func (r Prepare) MarshalJSON() ([]byte, error) {
	fields := map[string]any{"operation": r.Operation}
	if r.Content != "" {
		fields["content"] = r.Content
	}
	if r.Alias != "" {
		fields["alias"] = r.Alias
	}
	if r.Mappings != nil {
		fields["mappings"] = r.Mappings
	}
	if r.Assignments != nil {
		fields["assignments"] = r.Assignments
	}
	if r.Reset {
		fields["reset_parameters"] = true
	}
	return json.Marshal(fields)
}
