package protocol

import "time"

type ExportOptions struct {
	Inputs bool `json:"inputs"`
	Notes  bool `json:"notes"`
	Logs   bool `json:"logs"`
	Source bool `json:"source"`
}
type ExportPrepare struct {
	Runs    []string      `json:"runs"`
	Options ExportOptions `json:"options"`
}
type ExportRun struct {
	Instruments   []InstrumentObservation `json:"instruments,omitempty"`
	ID            string                  `json:"id"`
	Origin        string                  `json:"origin"`
	Experiment    string                  `json:"experiment"`
	Title         string                  `json:"title"`
	State         string                  `json:"state"`
	CreatedAt     time.Time               `json:"created_at"`
	Measurements  []Measurement           `json:"measurements"`
	Series        []Series                `json:"series"`
	Parameters    map[string]any          `json:"parameters,omitempty"`
	Note          string                  `json:"note,omitempty"`
	Logs          map[string]string       `json:"logs,omitempty"`
	SourceStart   *SourceObservation      `json:"source_start,omitempty"`
	SourceEnd     *SourceObservation      `json:"source_end,omitempty"`
	SourceChanged bool                    `json:"source_changed,omitempty"`
}
type ExportDocument struct {
	SchemaVersion int         `json:"schema_version"`
	Title         string      `json:"title"`
	Runs          []ExportRun `json:"runs"`
	Comparison    *Comparison `json:"comparison,omitempty"`
}
type ExportPreview struct {
	ID        string         `json:"id"`
	Digest    string         `json:"digest"`
	ExpiresAt time.Time      `json:"expires_at"`
	Document  ExportDocument `json:"document"`
}
type ExportRequest struct {
	Preparation string `json:"preparation"`
	Digest      string `json:"digest"`
	Format      string `json:"format"`
}
type ExportFile struct {
	MediaType string `json:"media_type"`
	SHA256    string `json:"sha256"`
	Data      []byte `json:"data_base64"`
}
