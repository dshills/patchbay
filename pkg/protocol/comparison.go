package protocol

type ResultReference struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type ComparisonRequest struct {
	Baseline  ResultReference `json:"baseline"`
	Candidate ResultReference `json:"candidate"`
}
type Sample struct {
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"id"`
	Origin        string         `json:"origin"`
	Experiment    Experiment     `json:"experiment"`
	Parameters    map[string]any `json:"parameters"`
	Measurements  []Measurement  `json:"measurements"`
	Series        []Series       `json:"series"`
}
type SampleList struct {
	Samples []Sample `json:"samples"`
}
type MetricDelta struct {
	Name      string   `json:"name"`
	Unit      string   `json:"unit"`
	Direction string   `json:"direction,omitempty"`
	Baseline  *float64 `json:"baseline,omitempty"`
	Candidate *float64 `json:"candidate,omitempty"`
	Delta     *float64 `json:"delta,omitempty"`
	Percent   *float64 `json:"percent,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}
type SeriesDelta struct {
	Name      string    `json:"name"`
	Baseline  Series    `json:"baseline"`
	Candidate Series    `json:"candidate"`
	Overlay   bool      `json:"overlay"`
	Delta     []float64 `json:"delta,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}
type Comparison struct {
	SchemaVersion int           `json:"schema_version"`
	Baseline      string        `json:"baseline"`
	Candidate     string        `json:"candidate"`
	Compatible    bool          `json:"compatible"`
	Partial       bool          `json:"partial"`
	Illustrative  bool          `json:"illustrative,omitempty"`
	Reasons       []string      `json:"reasons"`
	Metrics       []MetricDelta `json:"metrics"`
	Series        []SeriesDelta `json:"series"`
}
