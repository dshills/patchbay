package runtime

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/recipe"
	"patchbay/pkg/protocol"
)

var possibleSecret = regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret)\s*[:=]`)

type recipeExportPreparation struct {
	preview    recipe.ExportPreview
	data       []byte
	generation uint64
	inputs     string
	bytes      int
}

func (r *Runtime) expireRecipeExports() {
	for id, p := range r.recipeExports {
		if !time.Now().Before(p.preview.ExpiresAt) {
			delete(r.recipeExports, id)
		}
	}
}
func selectDefinitions[V any](items map[string]V, names []string) (map[string]V, error) {
	if names == nil {
		return items, nil
	}
	out := map[string]V{}
	for _, name := range names {
		item, ok := items[name]
		if _, duplicate := out[name]; !ok || duplicate {
			return nil, fmt.Errorf("unknown or repeated definition: %s", name)
		}
		out[name] = item
	}
	return out, nil
}
func (r *Runtime) PrepareRecipeExport(ctx context.Context, name string, request recipe.ExportPrepare) (recipe.ExportPreview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireRecipeExports()
	if err := r.writable(ctx); err != nil {
		return recipe.ExportPreview{}, err
	}
	store, err := r.recipeStore()
	if err != nil {
		return recipe.ExportPreview{}, err
	}
	if len(r.recipeExports) >= 16 || len(request.Runs) > 8 {
		return recipe.ExportPreview{}, fault.New(protocol.Busy, "At most sixteen export previews and eight selected sample runs are supported.")
	}
	entry, err := recipe.Resolve(store.Snapshot(), name)
	if err != nil {
		return recipe.ExportPreview{}, recipeError(err)
	}
	content := request.Content
	if content == "" {
		content = entry.Content
	}
	if content != entry.Content && content != entry.Previous && content != entry.Candidate {
		return recipe.ExportPreview{}, fault.New(protocol.InvalidRequest, "Select a stored version of this installation.")
	}
	p, err := store.Package(ctx, content)
	if err != nil {
		return recipe.ExportPreview{}, recipeError(err)
	}
	m := p.Manifest
	if m.Actions, err = selectDefinitions(m.Actions, request.Actions); err != nil {
		return recipe.ExportPreview{}, recipeError(err)
	}
	if m.Workflows, err = selectDefinitions(m.Workflows, request.Workflows); err != nil {
		return recipe.ExportPreview{}, recipeError(err)
	}
	if m.Experiments, err = selectDefinitions(m.Experiments, request.Experiments); err != nil {
		return recipe.ExportPreview{}, recipeError(err)
	}
	if m.Parameters, err = selectDefinitions(m.Parameters, request.Parameters); err != nil {
		return recipe.ExportPreview{}, recipeError(err)
	}
	if m.Controls, err = selectDefinitions(m.Controls, request.Controls); err != nil {
		return recipe.ExportPreview{}, recipeError(err)
	}
	prefix := recipe.Namespace(entry.ID)
	for _, name := range request.Defaults {
		param, exists := m.Parameters[name]
		current, available := r.parameters[prefix+name]
		if !exists || !available || content != entry.Content || !entry.Active || current.Sensitive {
			return recipe.ExportPreview{}, fault.New(protocol.InvalidRequest, "Selected portable default is unavailable.")
		}
		param.Value = current.Value
		m.Parameters[name] = param
	}
	files := map[string][]byte{"LICENSE": p.Files["LICENSE"], "README.md": p.Files["README.md"]}
	for _, name := range request.Documentation {
		if !strings.HasPrefix(name, "docs/") || p.Files[name] == nil {
			return recipe.ExportPreview{}, fault.New(protocol.InvalidRequest, "Select inventoried documentation.")
		}
		files[name] = p.Files[name]
	}
	for _, id := range request.Samples {
		found := false
		for _, file := range p.Manifest.Inventory {
			if !strings.HasPrefix(file.Path, "samples/") {
				continue
			}
			var sample protocol.Sample
			if jsonstrict.Decode(p.Files[file.Path], &sample) == nil && sample.ID == id {
				files[file.Path] = p.Files[file.Path]
				found = true
			}
		}
		if !found {
			return recipe.ExportPreview{}, fault.New(protocol.NotFound, "Selected sample is unavailable.")
		}
	}
	if len(request.Runs) > 0 {
		if r.runs == nil {
			return recipe.ExportPreview{}, fault.New(protocol.RecordingFailed, "Run storage is unavailable.")
		}
		release, err := r.runs.Hold(request.Runs...)
		if err != nil {
			return recipe.ExportPreview{}, err
		}
		defer release()
		for i, id := range request.Runs {
			run, err := r.runs.Get(id)
			if err != nil {
				return recipe.ExportPreview{}, err
			}
			if run.State != "success" || run.Recipe == nil || run.Recipe.Installation != entry.ID || run.Recipe.Content != content {
				return recipe.ExportPreview{}, fault.New(protocol.InvalidRequest, "Samples require successful runs from the selected installation/version.")
			}
			portableID := strings.TrimPrefix(run.Experiment.ID, prefix)
			experiment, ok := m.Experiments[portableID]
			if !ok {
				return recipe.ExportPreview{}, fault.New(protocol.InvalidRequest, "Include the run's portable experiment definition.")
			}
			experiment.ID = portableID
			sample := protocol.Sample{SourceLabel: "Converted from a local measured run; publisher-supplied sample", SchemaVersion: 1, ID: fmt.Sprintf("saved-%d", i+1), Origin: "sample", Experiment: experiment, Parameters: map[string]any{}, Measurements: run.Measurements, Series: []protocol.Series{}}
			for key, value := range run.Parameters {
				local := strings.TrimPrefix(key, prefix)
				if _, ok := m.Parameters[local]; ok {
					sample.Parameters[local] = value
				}
			}
			for _, artifact := range run.Artifacts {
				if artifact.MediaType != "application/json" {
					continue
				}
				data, _, err := r.runs.Artifact(id, artifact.ID)
				if err != nil {
					return recipe.ExportPreview{}, err
				}
				var series protocol.Series
				if jsonstrict.Decode(data, &series) == nil && evidence.ValidSeries(series) == nil {
					sample.Series = append(sample.Series, series)
				}
			}
			// Exclude execution IDs, annotations, text logs, source/device observations and
			// local mappings. The exact portable measurements/series remain visible below.
			data, err := json.MarshalIndent(sample, "", "  ")
			if err != nil {
				return recipe.ExportPreview{}, recipeError(err)
			}
			path := "samples/" + sample.ID + ".json"
			if files[path] != nil {
				return recipe.ExportPreview{}, fault.New(protocol.InvalidRequest, "Selected sample names collide; remove the earlier saved sample selection.")
			}
			files[path] = data
		}
	}
	exported, err := recipe.Build(m, files)
	if err != nil {
		return recipe.ExportPreview{}, recipeError(err)
	}
	data, err := recipe.ZIP(ctx, exported)
	if err != nil {
		return recipe.ExportPreview{}, recipeError(err)
	}
	view := recipe.ExportPreview{ID: identity.New(), Digest: evidence.Digest(data), ExpiresAt: time.Now().Add(time.Minute).UTC(), Installation: entry.ID, PackageDigest: exported.Digest, Files: map[string]string{}, Inventory: exported.Manifest.Inventory, Warnings: []string{"Review every included file. Literal commands, defaults, documentation and result text can contain private information; this preview is not a secret-free guarantee."}}
	for name, data := range exported.Files {
		view.Files[name] = string(data)
		if possibleSecret.Match(data) {
			view.Warnings = append(view.Warnings, "Possible private field in "+name+". Inspect its full content before sharing.")
		}
	}
	body, err := json.Marshal(view)
	size := len(body) + len(data)
	total := size
	for _, v := range r.recipeExports {
		total += v.bytes
	}
	if err != nil || len(body) > 8<<20 || total > 16<<20 {
		return recipe.ExportPreview{}, fault.New(protocol.StorageFull, "Recipe export preview exceeds its memory allowance; select fewer samples or documents.")
	}
	if r.recipeExports == nil {
		r.recipeExports = map[string]recipeExportPreparation{}
	}
	r.recipeExports[view.ID] = recipeExportPreparation{preview: view, data: data, generation: r.generation, inputs: r.recipeInputDigest(), bytes: size}
	return evidence.Clone(view), nil
}
func (r *Runtime) ExportRecipe(ctx context.Context, name string, request recipe.ExportRequest) (protocol.ExportFile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireRecipeExports()
	if err := r.writable(ctx); err != nil {
		return protocol.ExportFile{}, err
	}
	p, ok := r.recipeExports[request.Preparation]
	if !ok || p.preview.Installation != name || !hmac.Equal([]byte(p.preview.Digest), []byte(request.Digest)) || p.generation != r.generation || p.inputs != r.recipeInputDigest() {
		return protocol.ExportFile{}, fault.New(protocol.StalePreparation, "Recipe export changed or expired; review again.")
	}
	if !request.Confirmed {
		return protocol.ExportFile{}, fault.New(protocol.ConfirmationRequired, "Confirm the exact portable files before export.")
	}
	delete(r.recipeExports, request.Preparation)
	return protocol.ExportFile{MediaType: "application/zip", SHA256: p.preview.Digest, Data: p.data}, nil
}

// RecipeSample keeps the publisher's original sample untouched. Only this owned
// comparison view receives local reference names; no sample is written to runs.
func (r *Runtime) RecipeSample(ctx context.Context, ref protocol.ResultReference) (protocol.Sample, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	store, err := r.recipeStore()
	if err != nil {
		return protocol.Sample{}, err
	}
	entry, err := recipe.Resolve(store.Snapshot(), ref.Installation)
	if err != nil {
		return protocol.Sample{}, recipeError(err)
	}
	if ref.Content == "" || ref.Content != entry.Content && ref.Content != entry.Previous && ref.Content != entry.Candidate {
		return protocol.Sample{}, fault.New(protocol.InvalidRequest, "A selected package content digest is required.")
	}
	p, err := store.Package(ctx, ref.Content)
	if err != nil {
		return protocol.Sample{}, recipeError(err)
	}
	for _, sample := range p.Samples {
		if sample.ID == ref.ID {
			prefix := recipe.Namespace(entry.ID)
			sample = evidence.Clone(sample)
			sample.ID = prefix + "sample." + sample.ID
			sample.Experiment = recipe.LocalExperiment(sample.Experiment, prefix, recipe.Project(entry, p.Manifest))
			params := map[string]any{}
			for name, value := range sample.Parameters {
				params[prefix+name] = value
			}
			sample.Parameters = params
			return sample, nil
		}
	}
	return protocol.Sample{}, fault.New(protocol.NotFound, "Recipe sample not found.")
}
