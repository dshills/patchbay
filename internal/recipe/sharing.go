package recipe

import (
	"encoding/json"
	"time"
)

// Nil definition lists retain the portable definitions. Empty lists omit a
// category; all references must still pass the ordinary import verifier.
type ExportPrepare struct {
	Content       string   `json:"content,omitempty"`
	Actions       []string `json:"actions"`
	Workflows     []string `json:"workflows"`
	Experiments   []string `json:"experiments"`
	Parameters    []string `json:"parameters"`
	Controls      []string `json:"controls"`
	Defaults      []string `json:"defaults,omitempty"`
	Documentation []string `json:"documentation,omitempty"`
	Samples       []string `json:"samples,omitempty"`
	Runs          []string `json:"runs,omitempty"`
}
type ExportPreview struct {
	ID            string            `json:"id"`
	Digest        string            `json:"digest"`
	ExpiresAt     time.Time         `json:"expires_at"`
	Installation  string            `json:"installation"`
	PackageDigest string            `json:"package_digest"`
	Files         map[string]string `json:"files"`
	Inventory     []File            `json:"inventory"`
	Warnings      []string          `json:"warnings"`
}
type ExportRequest struct {
	Preparation string `json:"preparation"`
	Digest      string `json:"digest"`
	Confirmed   bool   `json:"confirmed"`
}

// Keep omitted and explicitly empty selection lists distinct across typed clients.
// Strict JSON rejects null, and [] means remove a category rather than retain it.
func (r ExportPrepare) MarshalJSON() ([]byte, error) {
	fields := map[string]any{}
	if r.Content != "" {
		fields["content"] = r.Content
	}
	for name, values := range map[string][]string{"actions": r.Actions, "workflows": r.Workflows, "experiments": r.Experiments, "parameters": r.Parameters, "controls": r.Controls, "defaults": r.Defaults, "documentation": r.Documentation, "samples": r.Samples, "runs": r.Runs} {
		if values != nil {
			fields[name] = values
		}
	}
	return json.Marshal(fields)
}
