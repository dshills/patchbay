package protocol

import "time"

// Experiment describes a bounded, sequential measurement using configured actions.
// Step numbers address leaves in the prepared workflow in execution order.
type Experiment struct {
	SchemaVersion int               `json:"schema_version" yaml:"schema_version"`
	ID            string            `json:"id" yaml:"id,omitempty"`
	Title         string            `json:"title" yaml:"title"`
	Description   string            `json:"description,omitempty" yaml:"description,omitempty"`
	Projects      []string          `json:"projects,omitempty" yaml:"projects,omitempty"`
	Action        string            `json:"action,omitempty" yaml:"action,omitempty"`
	Workflow      string            `json:"workflow,omitempty" yaml:"workflow,omitempty"`
	Inputs        []ExperimentInput `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Parameters    []string          `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Collectors    []Collector       `json:"collectors" yaml:"collectors"`
	Layout        []Widget          `json:"layout,omitempty" yaml:"layout,omitempty"`
}

type ExperimentInput struct {
	Step      int    `json:"step" yaml:"step"`
	Input     string `json:"input" yaml:"input"`
	Parameter string `json:"parameter" yaml:"parameter"`
}

type Collector struct {
	Name      string   `json:"name" yaml:"name"`
	Step      int      `json:"step" yaml:"step"`
	Action    string   `json:"action" yaml:"action"`
	Kind      string   `json:"kind" yaml:"kind"`
	Source    string   `json:"source" yaml:"source"`
	Path      []string `json:"path,omitempty" yaml:"path,omitempty"`
	Unit      string   `json:"unit,omitempty" yaml:"unit,omitempty"`
	Quantity  string   `json:"quantity,omitempty" yaml:"quantity,omitempty"`
	Direction string   `json:"direction,omitempty" yaml:"direction,omitempty"`
	Optional  bool     `json:"optional,omitempty" yaml:"optional,omitempty"`
}

type Widget struct {
	ID        string `json:"id" yaml:"id"`
	Kind      string `json:"kind" yaml:"kind"`
	Title     string `json:"title" yaml:"title"`
	Reference string `json:"reference,omitempty" yaml:"reference,omitempty"`
}

type ExperimentList struct {
	Experiments []Experiment `json:"experiments"`
}

type Capabilities struct {
	Features map[string]int `json:"features"`
	Schemas  map[string]int `json:"schemas"`
}

type CapturePrepare struct {
	Experiment string `json:"experiment"`
}

type PreparedStep struct {
	Index       int            `json:"index"`
	Action      string         `json:"action"`
	Type        string         `json:"type"`
	Safety      string         `json:"safety"`
	Executable  string         `json:"executable,omitempty"`
	Arguments   []string       `json:"arguments,omitempty"`
	Directory   string         `json:"directory,omitempty"`
	Environment []string       `json:"environment_names,omitempty"`
	Inputs      map[string]any `json:"inputs,omitempty"`
	Device      string         `json:"device,omitempty"`
	Operation   string         `json:"operation,omitempty"`
}

type CapturePreview struct {
	ID               string         `json:"id"`
	Digest           string         `json:"digest"`
	Experiment       string         `json:"experiment"`
	ExperimentDigest string         `json:"experiment_digest"`
	Instance         string         `json:"instance"`
	Generation       uint64         `json:"generation"`
	Revision         uint64         `json:"revision"`
	Context          Context        `json:"context"`
	Parameters       map[string]any `json:"parameters"`
	Steps            []PreparedStep `json:"steps"`
	Safety           string         `json:"safety"`
	ExpiresAt        time.Time      `json:"expires_at"`
}

type CaptureRequest struct {
	Preparation string `json:"preparation"`
	Digest      string `json:"digest"`
	RequestID   string `json:"request_id"`
	Confirmed   bool   `json:"confirmed"`
}

type CaptureResponse struct {
	RequestID string `json:"request_id"`
	RunID     string `json:"run_id"`
	JobID     string `json:"job_id,omitempty"`
	Deleted   bool   `json:"deleted,omitempty"`
}

type Measurement struct {
	Repeats    int       `json:"repeats,omitempty"`
	Spread     *float64  `json:"spread,omitempty"`
	Name       string    `json:"name"`
	Value      *float64  `json:"value,omitempty"`
	Unit       string    `json:"unit,omitempty"`
	Quantity   string    `json:"quantity,omitempty"`
	Direction  string    `json:"direction,omitempty"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	SourceStep int       `json:"source_step"`
	ObservedAt time.Time `json:"observed_at"`
}

type Series struct {
	SchemaVersion int       `json:"schema_version"`
	Name          string    `json:"name"`
	X             []float64 `json:"x"`
	Y             []float64 `json:"y"`
	XUnit         string    `json:"x_unit"`
	YUnit         string    `json:"y_unit"`
	Quantity      string    `json:"quantity,omitempty"`
	Quality       string    `json:"quality"`
	Reason        string    `json:"reason,omitempty"`
}

type Artifact struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	MediaType     string `json:"media_type"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
}

type SourceObservation struct {
	Commit       string    `json:"commit,omitempty"`
	Dirty        bool      `json:"dirty"`
	StatusDigest string    `json:"status_digest,omitempty"`
	Status       string    `json:"status"`
	ObservedAt   time.Time `json:"observed_at"`
}

type StepOutcome struct {
	Patch      *PatchOutcome          `json:"patch,omitempty"`
	Instrument *InstrumentObservation `json:"instrument,omitempty"`
	Index      int                    `json:"index"`
	Action     string                 `json:"action"`
	State      string                 `json:"state"`
	StartedAt  *time.Time             `json:"started_at,omitempty"`
	FinishedAt *time.Time             `json:"finished_at,omitempty"`
	Truncated  bool                   `json:"truncated,omitempty"`
	Error      *Error                 `json:"error,omitempty"`
}

// Instrument observations describe a transfer, never an inferred acquisition.
type InstrumentObservation struct {
	Device     string               `json:"device"`
	Model      string               `json:"model"`
	Firmware   string               `json:"firmware"`
	Channel    int                  `json:"channel"`
	ObservedAt time.Time            `json:"observed_at"`
	Values     map[string]any       `json:"values,omitempty"`
	Waveform   *WaveformObservation `json:"waveform,omitempty"`
}
type AcquisitionState struct {
	State      string    `json:"state"`
	ObservedAt time.Time `json:"observed_at"`
}
type WaveformObservation struct {
	AcquisitionTime string           `json:"acquisition_time"`
	Before          AcquisitionState `json:"before"`
	After           AcquisitionState `json:"after"`
	Preamble        []float64        `json:"preamble,omitempty"`
}

type AgentOrigin struct {
	Session  string `json:"session"`
	Proposal string `json:"proposal"`
}
type Run struct {
	Agent            *AgentOrigin       `json:"agent,omitempty"`
	Recipe           *RecipeOrigin      `json:"recipe,omitempty"`
	Context          Context            `json:"context"`
	Outcomes         []StepOutcome      `json:"outcomes,omitempty"`
	SchemaVersion    int                `json:"schema_version"`
	ID               string             `json:"id"`
	Origin           string             `json:"origin"`
	Experiment       Experiment         `json:"experiment"`
	ExperimentDigest string             `json:"experiment_digest"`
	PlanDigest       string             `json:"plan_digest"`
	Project          string             `json:"project"`
	Instance         string             `json:"instance"`
	Generation       uint64             `json:"generation"`
	RequestID        string             `json:"request_id"`
	RequestDigest    string             `json:"request_digest"`
	JobID            string             `json:"job_id,omitempty"`
	State            string             `json:"state"`
	CreatedAt        time.Time          `json:"created_at"`
	StartedAt        *time.Time         `json:"started_at,omitempty"`
	FinishedAt       *time.Time         `json:"finished_at,omitempty"`
	Parameters       map[string]any     `json:"parameters"`
	Steps            []PreparedStep     `json:"steps"`
	Measurements     []Measurement      `json:"measurements"`
	Artifacts        []Artifact         `json:"artifacts"`
	SourceStart      *SourceObservation `json:"source_start,omitempty"`
	SourceEnd        *SourceObservation `json:"source_end,omitempty"`
	SourceChanged    bool               `json:"source_changed,omitempty"`
	Error            *Error             `json:"error,omitempty"`
	Annotation       Annotation         `json:"annotation"`
}
type RecipeOrigin struct {
	Installation string `json:"installation"`
	DeclaredID   string `json:"declared_id"`
	Version      string `json:"version"`
	Content      string `json:"content_digest"`
}

type Annotation struct {
	Revision uint64    `json:"revision"`
	Title    string    `json:"title"`
	Note     string    `json:"note"`
	Pinned   bool      `json:"pinned"`
	Updated  time.Time `json:"updated_at"`
}

type AnnotationUpdate struct {
	Revision uint64 `json:"revision"`
	Title    string `json:"title"`
	Note     string `json:"note"`
	Pinned   bool   `json:"pinned"`
}

type RunSummary struct {
	ID         string     `json:"id"`
	Experiment string     `json:"experiment"`
	Project    string     `json:"project"`
	State      string     `json:"state"`
	CreatedAt  time.Time  `json:"created_at"`
	JobID      string     `json:"job_id,omitempty"`
	Annotation Annotation `json:"annotation"`
}

type RunList struct {
	Runs       []RunSummary `json:"runs"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type Baseline struct {
	Project    string `json:"project"`
	Experiment string `json:"experiment"`
	RunID      string `json:"run_id"`
	Revision   uint64 `json:"revision"`
}

type BaselineUpdate struct {
	RunID    string `json:"run_id"`
	Revision uint64 `json:"revision"`
}

type StoreStatus struct {
	Available   bool     `json:"available"`
	ReadOnly    bool     `json:"read_only"`
	Runs        int      `json:"runs"`
	Bytes       int64    `json:"bytes"`
	Reserved    int64    `json:"reserved_bytes"`
	MaxBytes    int64    `json:"max_bytes"`
	MaxRuns     int      `json:"max_runs"`
	Diagnostics []string `json:"diagnostics"`
}

type Deletion struct {
	Deleted bool `json:"deleted"`
}

// ArtifactContent uses bounded base64 to avoid arbitrary JSON string expansion.
type ArtifactContent struct {
	Artifact Artifact `json:"artifact"`
	Data     []byte   `json:"data_base64"`
}
