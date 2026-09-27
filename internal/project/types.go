// Package project defines project identity and metadata.
package project

type Project struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Path     string            `json:"path"`
	GitHub   string            `json:"github,omitempty"`
	Language string            `json:"language,omitempty"`
	Engine   string            `json:"engine,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}
