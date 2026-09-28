// Package recipe verifies portable declarative packages. Inspection has no
// runtime/provider dependencies and never executes or installs package content.
package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/parameter"
	"patchbay/internal/permission"
	"patchbay/pkg/protocol"
)

const (
	MaxPackage          = 20 << 20
	MaxFile             = 4 << 20
	MaxManifest         = 256 << 10
	MaxFiles            = 64
	MaxDirectoryEntries = MaxFiles * 8 // At most eight path segments per file.
)

type Manifest struct {
	SchemaVersion int                             `yaml:"schema_version" json:"schema_version"`
	ID            string                          `yaml:"id" json:"id"`
	Name          string                          `yaml:"name" json:"name"`
	Description   string                          `yaml:"description" json:"description"`
	Author        string                          `yaml:"author" json:"author"`
	License       string                          `yaml:"license" json:"license"`
	Version       string                          `yaml:"version" json:"version"`
	Digest        string                          `yaml:"digest" json:"digest"`
	Features      map[string]int                  `yaml:"features,omitempty" json:"features,omitempty"`
	Schemas       map[string]int                  `yaml:"schemas,omitempty" json:"schemas,omitempty"`
	Requirements  map[string]Requirement          `yaml:"requirements" json:"requirements"`
	Actions       map[string]Action               `yaml:"actions" json:"actions"`
	Parameters    map[string]parameter.Definition `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	Workflows     map[string]config.Workflow      `yaml:"workflows,omitempty" json:"workflows,omitempty"`
	Experiments   map[string]protocol.Experiment  `yaml:"experiments,omitempty" json:"experiments,omitempty"`
	Controls      map[string]Control              `yaml:"controls,omitempty" json:"controls,omitempty"`
	Inventory     []File                          `yaml:"inventory" json:"inventory"`
}
type Requirement struct {
	Kind        string   `yaml:"kind" json:"kind"` // project, tool, action
	Description string   `yaml:"description" json:"description"`
	Optional    bool     `yaml:"optional,omitempty" json:"optional,omitempty"`
	Provider    string   `yaml:"provider,omitempty" json:"provider,omitempty"`
	Operation   string   `yaml:"operation,omitempty" json:"operation,omitempty"`
	Model       string   `yaml:"model,omitempty" json:"model,omitempty"`
	Channel     int      `yaml:"channel,omitempty" json:"channel,omitempty"`
	Unit        string   `yaml:"unit,omitempty" json:"unit,omitempty"`
	Min         *float64 `yaml:"min,omitempty" json:"min,omitempty"`
	Max         *float64 `yaml:"max,omitempty" json:"max,omitempty"`
}
type Action struct {
	Type      string                  `yaml:"type" json:"type"`
	Tool      string                  `yaml:"tool,omitempty" json:"tool,omitempty"`
	Project   string                  `yaml:"project,omitempty" json:"project,omitempty"`
	Reference string                  `yaml:"reference,omitempty" json:"reference,omitempty"`
	Workflow  string                  `yaml:"workflow,omitempty" json:"workflow,omitempty"`
	Target    string                  `yaml:"target,omitempty" json:"target,omitempty"`
	Operation string                  `yaml:"operation,omitempty" json:"operation,omitempty"`
	Args      []string                `yaml:"args,omitempty" json:"args,omitempty"`
	Inputs    map[string]config.Input `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Safety    permission.Permission   `yaml:"safety,omitempty" json:"safety,omitempty"`
	Timeout   string                  `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}
type Control struct {
	Action    string `yaml:"action,omitempty" json:"action,omitempty"`
	Parameter string `yaml:"parameter,omitempty" json:"parameter,omitempty"`
	Capture   string `yaml:"capture,omitempty" json:"capture,omitempty"`
	Baseline  string `yaml:"baseline,omitempty" json:"baseline,omitempty"`
	Result    string `yaml:"result,omitempty" json:"result,omitempty"`
}
type File struct {
	Path      string `yaml:"path" json:"path"`
	MediaType string `yaml:"media_type" json:"media_type"`
	Size      int64  `yaml:"size" json:"size"`
	SHA256    string `yaml:"sha256" json:"sha256"`
}
type Package struct {
	Manifest Manifest          `json:"manifest"`
	Digest   string            `json:"package_digest"`
	Files    map[string][]byte `json:"-"`
	Samples  []protocol.Sample `json:"samples"`
}

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`)
var windowsDrive = regexp.MustCompile(`(?i)(^|=)[a-z]:`)
var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var pathPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func validPath(name string) bool {
	if len(name) > 256 || name != path.Clean(name) || strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) > 8 {
		return false
	}
	for _, p := range parts {
		if !pathPart.MatchString(p) || p == "." || p == ".." {
			return false
		}
	}
	return true
}
func fail(field, message string) error { return fmt.Errorf("invalid_recipe: %s: %s", field, message) }
func CanonicalDigest(m Manifest) string {
	m.Digest = ""
	m.Inventory = append([]File{}, m.Inventory...)
	slices.SortFunc(m.Inventory, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	data, _ := json.Marshal(m)
	return evidence.Digest(data)
}
func parseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := config.DecodeDocument(data, &m, MaxManifest); err != nil {
		return m, err
	}
	if err := validate(m); err != nil {
		return m, err
	}
	if !hexDigest.MatchString(m.Digest) || m.Digest != CanonicalDigest(m) {
		return m, fail("digest", "manifest digest mismatch")
	}
	return m, nil
}
func validate(m Manifest) error {
	if m.SchemaVersion != 1 || !config.ValidName(m.ID) || strings.TrimSpace(m.Name) == "" || len(m.Name) > 128 || len(m.Description) > 4096 || m.Author == "" || len(m.Author) > 256 || m.License == "" || len(m.License) > 128 || !semver.MatchString(m.Version) || len(m.Version) > 128 {
		return fail("metadata", "requires schema 1, valid identity, name, description, author, license and SemVer")
	}
	core, _, _ := strings.Cut(m.Version, "+")
	if _, pre, ok := strings.Cut(core, "-"); ok {
		for _, part := range strings.Split(pre, ".") {
			numeric := true
			for _, c := range part {
				if c < '0' || c > '9' {
					numeric = false
				}
			}
			if numeric && len(part) > 1 && part[0] == '0' {
				return fail("version", "numeric prerelease identifiers cannot have leading zeroes")
			}
		}
	}
	for feature, v := range m.Features {
		if !slices.Contains([]string{"capture", "comparison", "export", "recipes", "run_store", "experiment_preparation"}, feature) || v != 1 {
			return fail("features", "unsupported required capability")
		}
	}
	for schema, v := range m.Schemas {
		if !slices.Contains([]string{"experiment", "run", "series", "artifact", "recipe"}, schema) || v != 1 {
			return fail("schemas", "unsupported required schema")
		}
	}
	if len(m.Actions) > 128 || len(m.Workflows) > 32 || len(m.Experiments) > 32 || len(m.Controls) > 32 || len(m.Parameters) > 128 || len(m.Requirements) > 64 || len(m.Inventory) < 2 || len(m.Inventory) >= MaxFiles {
		return fail("$", "definition or file limit exceeded")
	}
	projects := 0
	for id, r := range m.Requirements {
		if !config.ValidName(id) || len(r.Description) > 1024 {
			return fail("requirements", "invalid role name or description")
		}
		switch r.Kind {
		case "project":
			projects++
		case "tool":
		case "action":
			if !slices.Contains([]string{"exec", "open", "git", "scpi", "plugin", "agent"}, r.Provider) {
				return fail("requirements", "action role needs an explicit provider")
			}
		default:
			return fail("requirements", "role kind must be project, tool or action")
		}
		if r.Kind != "action" && (r.Provider != "" || r.Operation != "" || r.Model != "" || r.Channel != 0 || r.Unit != "" || r.Min != nil || r.Max != nil) {
			return fail("requirements", "instrument constraints require an action role")
		}
		if r.Provider == "scpi" && (r.Model == "" || r.Operation == "" || r.Channel < 1 || r.Channel > 4) {
			return fail("requirements", "SCPI role requires model, operation and channel")
		}
		if (r.Min == nil) != (r.Max == nil) || r.Min != nil && (!finite(*r.Min) || !finite(*r.Max) || *r.Min > *r.Max || r.Unit == "") {
			return fail("requirements", "invalid unit/limit interval")
		}
	}
	if projects > 1 {
		return fail("requirements", "one explicit project role is supported per installation")
	}
	c := config.Config{Version: 1, Actions: map[string]config.Action{}, Parameters: m.Parameters, Workflows: m.Workflows, Experiments: m.Experiments, Projects: map[string]config.Project{}}
	for id, r := range m.Requirements {
		if r.Kind == "project" {
			c.Projects[id] = config.Project{Name: id, Path: "/portable/project"}
		}
	}
	for id, p := range m.Parameters {
		if !config.ValidName(id) || p.Instrument != nil || p.Sensitive {
			return fail("parameters", "portable values cannot embed instrument bindings or sensitive defaults")
		}
		if v, ok := p.Value.(string); ok && !portable(v) {
			return fail("parameters", "machine-specific defaults must become local mappings")
		}
	}
	for id, a := range m.Actions {
		if !config.ValidName(id) || a.Safety == permission.Dangerous {
			return fail("actions", "invalid name or dangerous portable action")
		}
		if a.Project != "" && m.Requirements[a.Project].Kind != "project" {
			return fail("actions", "unresolved project role")
		}
		for _, arg := range a.Args {
			if !portable(arg) {
				return fail("actions", "absolute paths and home paths require mappings")
			}
		}
		d := config.Action{Type: a.Type, Args: a.Args, Inputs: a.Inputs, Safety: a.Safety, Timeout: a.Timeout, Workflow: a.Workflow, Operation: a.Operation, Target: a.Target}
		if a.Project != "" && (a.Type == "exec" || a.Type == "git" || a.Type == "reference") {
			d.Cwd = "/portable/project"
		}
		switch a.Type {
		case "exec":
			if m.Requirements[a.Tool].Kind != "tool" || a.Reference != "" {
				return fail("actions", "exec requires a tool role")
			}
			d.Command = "/portable/tool/" + a.Tool
		case "reference":
			if m.Requirements[a.Reference].Kind != "action" || a.Tool != "" || a.Operation != "" || a.Target != "" || a.Workflow != "" || len(a.Args) > 0 {
				return fail("actions", "reference requires only an action role, input schema, project, safety and timeout")
			}
			d.Type = "exec"
			d.Command = "/portable/referenced-action"
		case "git", "open", "workflow":
			if a.Tool != "" || a.Reference != "" {
				return fail("actions", "unexpected tool or action role")
			}
		default:
			return fail("actions", "unsupported portable action type")
		}
		if a.Type == "open" && !strings.HasPrefix(a.Target, "{{ .project.path }}") && !strings.HasPrefix(a.Target, "https://") {
			return fail("actions", "open target must use the project role or an explicit HTTPS destination")
		}
		if a.Project == "" && (strings.Contains(a.Target, ".project.") || strings.Contains(strings.Join(a.Args, " "), ".project.")) {
			return fail("actions", "project templates require a project role")
		}
		c.Actions[id] = d
	}
	// Core normalization checks input schemas, templates, expansion limits, workflow
	// references, collectors and permission floors, without probing the filesystem.
	data, err := yaml.Marshal(c)
	if err != nil {
		return fail("definitions", "cannot encode definitions")
	}
	if _, err = config.Parse(data, "/portable", "/portable/home"); err != nil {
		return err
	}
	for id, e := range m.Experiments {
		if e.ID != "" && e.ID != id {
			return fail("experiments", "mismatched identity")
		}
		for _, project := range e.Projects {
			if m.Requirements[project].Kind != "project" {
				return fail("experiments", "unresolved project role")
			}
		}
	}
	for id, control := range m.Controls {
		if !config.ValidName(id) {
			return fail("controls", "invalid name")
		}
		count := 0
		for _, v := range []string{control.Action, control.Parameter, control.Capture, control.Baseline, control.Result} {
			if v != "" {
				count++
			}
		}
		if count != 1 {
			return fail("controls", "requires exactly one semantic target")
		}
		if control.Action != "" {
			if _, ok := m.Actions[control.Action]; !ok {
				return fail("controls", "unknown action")
			}
		}
		if control.Parameter != "" {
			if _, ok := m.Parameters[control.Parameter]; !ok {
				return fail("controls", "unknown parameter")
			}
		}
		for _, id := range []string{control.Capture, control.Baseline, control.Result} {
			if id != "" {
				if _, ok := m.Experiments[id]; !ok {
					return fail("controls", "unknown experiment")
				}
			}
		}
	}
	seen := map[string]bool{}
	total := int64(0)
	for _, f := range m.Inventory {
		key := strings.ToLower(f.Path)
		if !validPath(f.Path) || key == "recipe.yaml" || seen[key] || f.Size < 0 || f.Size > MaxFile || !hexDigest.MatchString(f.SHA256) {
			return fail("inventory", "invalid, colliding, or oversized file")
		}
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return fail("inventory", "file/directory collision")
			}
		}
		for prior := range seen {
			if strings.HasPrefix(prior, key+"/") {
				return fail("inventory", "file/directory collision")
			}
		}
		document := (f.Path == "LICENSE" || f.Path == "README.md" || strings.HasPrefix(f.Path, "docs/") && (strings.HasSuffix(f.Path, ".md") || strings.HasSuffix(f.Path, ".txt"))) && (f.MediaType == "text/plain" || f.MediaType == "text/markdown")
		sample := strings.HasPrefix(f.Path, "samples/") && strings.HasSuffix(f.Path, ".json") && f.MediaType == "application/json"
		if !document && !sample {
			return fail("inventory", "only text documentation/licenses and JSON samples are supported")
		}
		seen[key] = true
		total += f.Size
	}
	if !seen["license"] || !seen["readme.md"] || total > MaxPackage-MaxManifest {
		return fail("inventory", "LICENSE and README.md are required within the expanded limit")
	}
	return nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func portable(s string) bool {
	return !strings.Contains(s, "\\") && !windowsDrive.MatchString(s) && !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "~") && !strings.Contains(s, "=/") && !strings.Contains(s, "=~") && !strings.ContainsRune(s, 0)
}
func verifyFiles(m Manifest, files map[string][]byte) ([]protocol.Sample, error) {
	samples := []protocol.Sample{}
	for _, f := range m.Inventory {
		data, ok := files[f.Path]
		if !ok || int64(len(data)) != f.Size || evidence.Digest(data) != f.SHA256 {
			return nil, fail(f.Path, "payload size/hash mismatch")
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return nil, fail(f.Path, "payload must be UTF-8 text")
		}
		if f.MediaType != "application/json" {
			continue
		}
		var sample protocol.Sample
		if jsonstrict.Decode(data, &sample) != nil || sample.SchemaVersion != 1 || sample.Origin != "sample" || !config.ValidName(sample.ID) {
			return nil, fail(f.Path, "invalid sample schema or origin")
		}
		if len(sample.SourceLabel) > 256 || len(sample.Measurements) > 32 || len(sample.Series) > 32 || len(sample.Parameters) > 128 {
			return nil, fail(f.Path, "sample exceeds collection limits")
		}
		expected, ok := m.Experiments[sample.Experiment.ID]
		expected.ID = sample.Experiment.ID
		one, _ := json.Marshal(expected)
		two, _ := json.Marshal(sample.Experiment)
		if !ok || string(one) != string(two) {
			return nil, fail(f.Path, "sample experiment differs from package definition")
		}
		for _, s := range samples {
			if s.ID == sample.ID {
				return nil, fail(f.Path, "duplicate sample identity")
			}
		}
		for name, value := range sample.Parameters {
			p, ok := m.Parameters[name]
			if !ok {
				return nil, fail(f.Path, "unknown sample parameter")
			}
			if p.Normalize() != nil {
				return nil, fail(f.Path, "invalid parameter schema")
			}
			if _, err := p.ValidateValue(value); err != nil {
				return nil, fail(f.Path, "sample parameter is outside declared bounds")
			}
		}
		collectors := map[string]protocol.Collector{}
		seen := map[string]bool{}
		for _, collector := range expected.Collectors {
			collectors[collector.Name] = collector
		}
		for _, series := range sample.Series {
			collector, exists := collectors[series.Name]
			if evidence.ValidSeries(series) != nil || !exists || collector.Kind != "series" || seen[series.Name] || collector.Unit != "" && collector.Unit != series.YUnit || collector.Quantity != "" && collector.Quantity != series.Quantity {
				return nil, fail(f.Path, "invalid sample series")
			}
			seen[series.Name] = true
		}
		for _, metric := range sample.Measurements {
			collector, exists := collectors[metric.Name]
			if !exists || collector.Kind != "measurement" || seen[metric.Name] || metric.Unit != collector.Unit || metric.Quantity != collector.Quantity || metric.SourceStep != collector.Step || !slices.Contains([]string{"valid", "invalid", "unavailable"}, metric.Status) || metric.Status == "valid" && metric.Value == nil {
				return nil, fail(f.Path, "sample measurement differs from collector")
			}
			seen[metric.Name] = true
			if metric.Value != nil && !finite(*metric.Value) || metric.Spread != nil && (!finite(*metric.Spread) || *metric.Spread < 0) || metric.Repeats < 0 {
				return nil, fail(f.Path, "invalid sample measurement")
			}
		}
		samples = append(samples, sample)
	}
	return samples, nil
}
