// Package supervisor contains the bounded, durable human-reviewed agent contracts.
package supervisor

import (
	"encoding/json"
	"time"
	"unicode/utf8"

	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/jsonstrict"
	"patchbay/pkg/protocol"
)

const MaxContext = 256 << 10
const MaxOutput = 64 << 10
const MaxSessions = 100
const MaxStorage = 50 << 20

type Selection struct {
	Files     []string            `json:"files,omitempty"`
	Artifacts []ArtifactSelection `json:"artifacts,omitempty"`
	Prompt    string              `json:"prompt"`
	RequestID string              `json:"request_id"`
	Retain    bool                `json:"retain_snapshots,omitempty"`
}
type ArtifactSelection struct {
	Run      string `json:"run"`
	Artifact string `json:"artifact"`
}
type Item struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Path     string `json:"path,omitempty"`
	Run      string `json:"run,omitempty"`
	Artifact string `json:"artifact,omitempty"`
	SHA256   string `json:"sha256"`
	Bytes    int    `json:"bytes"`
	Text     string `json:"text,omitempty"`
}
type ContextPreview struct {
	ID              string                      `json:"id"`
	Digest          string                      `json:"digest"`
	RequestID       string                      `json:"request_id"`
	ExpiresAt       time.Time                   `json:"expires_at"`
	Project         string                      `json:"project"`
	Destination     string                      `json:"destination"`
	Model           string                      `json:"model"`
	MaxOutputTokens int                         `json:"max_output_tokens"`
	Input           string                      `json:"input"`
	InputBytes      int                         `json:"input_bytes"`
	Items           []Item                      `json:"items"`
	Source          *protocol.SourceObservation `json:"source"`
	Retain          bool                        `json:"retain_snapshots"`
	Warnings        []string                    `json:"warnings"`
}
type Start struct {
	Preparation string `json:"preparation"`
	Digest      string `json:"digest"`
	RequestID   string `json:"request_id"`
	Confirmed   bool   `json:"confirmed"`
}
type Suggestion struct {
	Kind      string         `json:"kind"`
	Target    string         `json:"target"`
	Inputs    map[string]any `json:"inputs,omitempty"`
	Rationale string         `json:"rationale"`
	Expected  string         `json:"expected_outcome"`
	Baseline  string         `json:"baseline_run_id,omitempty"`
}
type Output struct {
	SchemaVersion int          `json:"schema_version"`
	Summary       string       `json:"summary"`
	ContextRefs   []string     `json:"context_refs"`
	Proposals     []Suggestion `json:"proposals"`
}
type Proposal struct {
	ID         string     `json:"id"`
	Suggestion Suggestion `json:"suggestion"`
	State      string     `json:"state"`
	Digest     string     `json:"digest"`
	ExpiresAt  time.Time  `json:"expires_at"`
	JobID      string     `json:"job_id,omitempty"`
	RunID      string     `json:"run_id,omitempty"`
	Message    string     `json:"message,omitempty"`
}
type Session struct {
	GenerationJobID string                      `json:"generation_job_id,omitempty"`
	ID              string                      `json:"id"`
	Project         string                      `json:"project"`
	State           string                      `json:"state"`
	CreatedAt       time.Time                   `json:"created_at"`
	RequestID       string                      `json:"request_id"`
	RequestDigest   string                      `json:"request_digest"`
	ContextDigest   string                      `json:"context_digest"`
	Instance        string                      `json:"instance"`
	Generation      uint64                      `json:"generation"`
	Revision        uint64                      `json:"revision"`
	Preconditions   string                      `json:"preconditions"`
	Model           string                      `json:"model"`
	Destination     string                      `json:"destination"`
	InputBytes      int                         `json:"input_bytes"`
	JobID           string                      `json:"job_id,omitempty"`
	Items           []Item                      `json:"items"`
	Source          *protocol.SourceObservation `json:"source"`
	Snapshot        string                      `json:"snapshot,omitempty"`
	Text            string                      `json:"text,omitempty"`
	Output          *Output                     `json:"output,omitempty"`
	UnsupportedRefs []string                    `json:"unsupported_refs,omitempty"`
	Proposals       []Proposal                  `json:"proposals,omitempty"`
	Usage           map[string]int64            `json:"usage,omitempty"`
	Error           *protocol.Error             `json:"error,omitempty"`
	Parent          string                      `json:"parent,omitempty"`
}
type List struct {
	Sessions   []Session `json:"sessions"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
type Catalog struct {
	Enabled     bool     `json:"enabled"`
	Available   bool     `json:"available"`
	Message     string   `json:"message,omitempty"`
	Project     string   `json:"project"`
	Model       string   `json:"model"`
	Destination string   `json:"destination"`
	Targets     []Target `json:"targets"`
}
type Target struct {
	Inputs map[string]protocol.Input `json:"inputs,omitempty"`
	Kind   string                    `json:"kind"`
	ID     string                    `json:"id"`
	Digest string                    `json:"digest"`
}

func Hash(value any) string { b, _ := json.Marshal(value); return evidence.Digest(b) }
func ParseOutput(text string) (Output, error) {
	var out Output
	bad := func() (Output, error) {
		return Output{}, fault.New(protocol.InvalidProposal, "Expected one complete schema-1 JSON response with at most eight suggestions; no retry was sent.")
	}
	if len(text) > MaxOutput || !utf8.ValidString(text) || jsonstrict.Decode([]byte(text), &out) != nil || out.SchemaVersion != 1 || out.ContextRefs == nil || out.Proposals == nil || len(out.Summary) > 16384 || len(out.ContextRefs) > 20 || len(out.Proposals) > 8 {
		return bad()
	}
	for _, ref := range out.ContextRefs {
		if len(ref) > 128 {
			return bad()
		}
	}
	for _, p := range out.Proposals {
		if (p.Kind != "action" && p.Kind != "workflow" && p.Kind != "experiment") || len(p.Target) == 0 || len(p.Target) > 128 || len(p.Rationale) > 4096 || len(p.Expected) > 4096 || len(p.Inputs) > 32 || len(p.Baseline) > 128 {
			return bad()
		}
		for k, v := range p.Inputs {
			if len(k) > 128 {
				return bad()
			}
			switch v.(type) {
			case string, bool, json.Number, float64:
			default:
				return bad()
			}
		}
	}
	return out, nil
}

type ProposalPreview struct {
	ID        string                  `json:"id"`
	Session   string                  `json:"session"`
	Proposal  string                  `json:"proposal"`
	Digest    string                  `json:"digest"`
	ExpiresAt time.Time               `json:"expires_at"`
	Target    Target                  `json:"target"`
	Capture   protocol.CapturePreview `json:"capture"`
	Context   []Item                  `json:"context"`
	Rationale string                  `json:"rationale"`
	Expected  string                  `json:"expected_outcome"`
}
type Approve struct {
	Preparation string `json:"preparation"`
	Digest      string `json:"digest"`
	RequestID   string `json:"request_id"`
	Confirmed   bool   `json:"confirmed"`
}
type Admission struct {
	Session  string `json:"session"`
	Proposal string `json:"proposal"`
	RunID    string `json:"run_id"`
	JobID    string `json:"job_id"`
}
type GrantReview struct {
	Target     Target               `json:"target"`
	Project    string               `json:"project"`
	Actions    map[string]any       `json:"actions"`
	Workflows  map[string]any       `json:"workflows"`
	Experiment *protocol.Experiment `json:"experiment,omitempty"`
	Warning    string               `json:"warning"`
}
