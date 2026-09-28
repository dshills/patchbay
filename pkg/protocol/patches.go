package protocol

// PatchPreview is complete; clients must not truncate Diff before approval.
type PatchPreview struct {
	Diff   string      `json:"diff"`
	Digest string      `json:"digest"`
	Head   string      `json:"git_head"`
	Files  []PatchFile `json:"files"`
}
type PatchFile struct {
	Path        string `json:"path"`
	Before      string `json:"before_sha256"`
	After       string `json:"after_sha256"`
	BeforeBytes int    `json:"before_bytes"`
	AfterBytes  int    `json:"after_bytes"`
	Mode        uint32 `json:"mode"`
}
type PatchFileOutcome struct {
	Path    string `json:"path"`
	State   string `json:"state"`
	Cleanup string `json:"cleanup,omitempty"`
}
type PatchOutcome struct {
	Operation string             `json:"operation"`
	Run       string             `json:"run"`
	Project   string             `json:"project"`
	State     string             `json:"state"`
	Message   string             `json:"message,omitempty"`
	Files     []PatchFileOutcome `json:"files"`
}

type PatchList struct {
	Operations []PatchOutcome `json:"operations"`
}
