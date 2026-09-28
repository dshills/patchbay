package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"patchbay/internal/config"
	runtimecontext "patchbay/internal/context"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/parameter"
	"patchbay/internal/permission"
	"patchbay/internal/recipe"
	"patchbay/internal/state"
	"patchbay/pkg/protocol"
)

type recipePreparation struct {
	preview     recipe.Preview
	selection   recipe.Selection
	composition recipe.Composition
	inputs      string
	bytes       int
}

func (r *Runtime) initRecipes(base *config.Config, options recipe.StoreOptions) {
	r.recipes, r.recipeError = recipe.OpenStore(base.State.Path+".recipes", options)
	r.recipeComposition = r.composeRecipes(context.Background(), base)
	r.cfg = r.recipeComposition.Config
	registries, err := buildRegistries(r.cfg)
	if err != nil {
		r.recipeError = err
		r.cfg = base
		r.recipeComposition = recipe.Composition{Config: base}
		return
	}
	r.registries = registries
}
func (r *Runtime) composeRecipes(ctx context.Context, base *config.Config) recipe.Composition {
	out := recipe.Composition{Config: base}
	if r.recipes == nil {
		return out
	}
	selection := r.recipes.Snapshot()
	packages := map[string]*recipe.Package{}
	accepted := []recipe.Installation{}
	diagnostics := []string{}
	for _, entry := range selection.Installations {
		if !entry.Active {
			continue
		}
		p, err := r.recipes.Package(ctx, entry.Content)
		if err != nil {
			diagnostics = append(diagnostics, entry.ID+": package unavailable; review required")
			continue
		}
		packages[entry.Content] = p
		candidate, err := recipe.Compose(base, append(slices.Clone(accepted), entry), packages)
		if err != nil {
			diagnostics = append(diagnostics, entry.ID+": "+err.Error())
			continue
		}
		accepted = append(accepted, entry)
		out = candidate
	}
	out.Disabled = append(out.Disabled, diagnostics...)
	return out
}
func (r *Runtime) recipeStore() (*recipe.Store, error) {
	if r.recipes == nil {
		return nil, fault.New(protocol.RecordingFailed, "Recipe store is unavailable; inspect recipe diagnostics.")
	}
	return r.recipes, nil
}
func (r *Runtime) ImportRecipe(ctx context.Context, data []byte, target, alias string) (recipe.ImportResult, error) {
	p, err := recipe.InspectZIP(ctx, data)
	if err != nil {
		return recipe.ImportResult{}, fault.New(protocol.InvalidRequest, err.Error())
	}
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	r.mu.Lock()
	err = r.writable(ctx)
	store, storeErr := r.recipeStore()
	r.mu.Unlock()
	if err != nil {
		return recipe.ImportResult{}, err
	}
	if storeErr != nil {
		return recipe.ImportResult{}, storeErr
	}
	installed, err := store.Import(ctx, p, target, alias)
	if err != nil {
		return recipe.ImportResult{}, recipeError(err)
	}
	return recipe.ImportResult{Installation: installed, Digest: p.Digest}, nil
}
func (r *Runtime) Recipes(ctx context.Context) (recipe.List, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := recipe.List{Installations: []recipe.View{}, Diagnostics: slices.Clone(r.recipeComposition.Disabled)}
	if r.recipeError != nil {
		result.Diagnostics = append(result.Diagnostics, r.recipeError.Error())
	}
	if r.recipes == nil {
		return result, nil
	}
	selection := r.recipes.Snapshot()
	result.Revision = selection.Revision
	result.Unused, result.Bytes, _ = r.recipes.Unused()
	for _, entry := range selection.Installations {
		view := r.recipeView(entry)
		result.Installations = append(result.Installations, view)
	}
	if err := ctx.Err(); err != nil {
		return result, fault.Safe(err)
	}
	return result, nil
}
func (r *Runtime) recipeView(entry recipe.Installation) recipe.View {
	view := recipe.View{Installation: entry, Status: "staged", Diagnostics: []string{}}
	if entry.BaseDigest != "" {
		view.Status = "disabled"
	}
	if _, ok := r.recipeComposition.Origins[recipe.Namespace(entry.ID)]; ok && entry.Active {
		view.Status = "active"
	}
	if entry.Active && view.Status != "active" {
		view.Diagnostics = append(view.Diagnostics, "Host configuration, bindings or package changed. Review activation again.")
	}
	if entry.Candidate != "" {
		view.Diagnostics = append(view.Diagnostics, "An imported update is staged; it has not changed the active version.")
	}
	if !r.recipes.Writable() {
		view.Diagnostics = append(view.Diagnostics, "Recipe store is read-only.")
	}
	return view
}
func (r *Runtime) Recipe(ctx context.Context, name string) (recipe.View, error) {
	return r.RecipeVersion(ctx, name, "")
}
func (r *Runtime) RecipeVersion(ctx context.Context, name, content string) (recipe.View, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	store, err := r.recipeStore()
	if err != nil {
		return recipe.View{}, err
	}
	entry, err := recipe.Resolve(store.Snapshot(), name)
	if err != nil {
		return recipe.View{}, recipeError(err)
	}
	view := r.recipeView(entry)
	if content == "" {
		content = entry.Content
	}
	if content != entry.Content && content != entry.Candidate && content != entry.Previous {
		return recipe.View{}, fault.New(protocol.InvalidRequest, "Select a stored version of this installation.")
	}
	view.Package, err = store.Package(ctx, content)
	if err != nil {
		return view, recipeError(err)
	}
	if entry.Candidate != "" {
		view.Candidate, err = store.Package(ctx, entry.Candidate)
	}
	budget := 1 << 20
	for _, name := range slices.Sorted(maps.Keys(view.Package.Files)) {
		if name != "LICENSE" && name != "README.md" && !strings.HasPrefix(name, "docs/") {
			continue
		}
		data := view.Package.Files[name]
		limit := min(len(data), 256<<10, budget)
		for limit > 0 && !utf8.Valid(data[:limit]) {
			limit--
		}
		budget -= limit
		view.Documents = append(view.Documents, recipe.Document{Path: name, Text: string(data[:limit]), Truncated: limit < len(data)})
	}
	return view, recipeError(err)
}
func recipeError(err error) error {
	if err == nil {
		return nil
	}
	var known *protocol.Error
	if errors.As(err, &known) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fault.Safe(err)
	}
	return fault.New(protocol.InvalidRequest, err.Error())
}
func (r *Runtime) recipeInputDigest() string {
	data, _ := json.Marshal(struct {
		Context    runtimecontext.RuntimeContext
		Parameters map[string]parameter.Definition
	}{r.context, r.parameters})
	return evidence.Digest(data)
}
func (r *Runtime) PrepareRecipe(ctx context.Context, name string, request recipe.Prepare) (recipe.Preview, error) {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writable(ctx); err != nil {
		return recipe.Preview{}, err
	}
	store, err := r.recipeStore()
	if err != nil {
		return recipe.Preview{}, err
	}
	disk, err := config.Load(r.path)
	if err != nil || recipe.BaseDigest(disk) != recipe.BaseDigest(r.baseConfig) {
		return recipe.Preview{}, fault.New(protocol.StalePreparation, "Host configuration changed on disk; reload before reviewing a recipe.")
	}
	now := time.Now()
	for id, p := range r.recipePreviews {
		if !now.Before(p.preview.ExpiresAt) {
			delete(r.recipePreviews, id)
		}
	}
	if len(r.recipePreviews) >= 64 {
		return recipe.Preview{}, fault.New(protocol.Busy, "Too many recipe previews; wait for expiry.")
	}
	next := store.Snapshot()
	entry, err := recipe.Resolve(next, name)
	if name == "store" && request.Operation == "cleanup" {
		entry = recipe.Installation{ID: "store"}
		err = nil
	}
	if err != nil {
		return recipe.Preview{}, recipeError(err)
	}
	old := entry
	if request.Operation == "update" || request.Operation == "rollback" {
		if name != entry.ID {
			return recipe.Preview{}, fault.New(protocol.InvalidRequest, "Updates and rollback require the exact local installation ID.")
		}
	}
	switch request.Operation {
	case "activate":
		entry.Active = true
	case "update":
		if entry.Candidate == "" {
			return recipe.Preview{}, fault.New(protocol.InvalidRequest, "Import an update for this installation first.")
		}
		entry.Content, entry.Previous, entry.Candidate, entry.Active = entry.Candidate, entry.Content, "", true
	case "rollback":
		if entry.Previous == "" {
			return recipe.Preview{}, fault.New(protocol.InvalidRequest, "No previous activated version is retained.")
		}
		entry.Content, entry.Previous, entry.Active = entry.Previous, entry.Content, true
	case "deactivate", "remove":
		entry.Active = false
	case "rename", "cleanup":
	default:
		return recipe.Preview{}, fault.New(protocol.InvalidRequest, "Expected activate, update, rollback, deactivate, remove, rename or cleanup.")
	}
	if request.Content != "" && request.Content != entry.Content {
		return recipe.Preview{}, fault.New(protocol.InvalidRequest, "Content does not match this operation's selected version.")
	}
	if request.Alias != "" {
		if !config.ValidName(request.Alias) {
			return recipe.Preview{}, fault.New(protocol.InvalidRequest, "Alias must be an identifier.")
		}
		entry.Alias = request.Alias
	}
	for _, other := range next.Installations {
		if other.ID != entry.ID && strings.EqualFold(other.Alias, entry.Alias) {
			return recipe.Preview{}, fault.New(protocol.InvalidRequest, "Alias is already in use.")
		}
	}
	if request.Operation == "rename" && (request.Mappings != nil || request.Assignments != nil) {
		return recipe.Preview{}, fault.New(protocol.InvalidRequest, "Rename changes the display alias only.")
	}
	if request.Mappings != nil {
		entry.Mappings = request.Mappings
	}
	if request.Assignments != nil {
		entry.Assignments = request.Assignments
	}
	if request.Operation == "activate" || request.Operation == "update" || request.Operation == "rollback" {
		entry.BaseDigest = recipe.BaseDigest(r.baseConfig)
	}
	for i, existing := range next.Installations {
		if existing.ID == entry.ID {
			if request.Operation == "remove" {
				next.Installations = slices.Delete(next.Installations, i, i+1)
			} else {
				next.Installations[i] = entry
			}
			break
		}
	}
	packages := map[string]*recipe.Package{}
	for _, installed := range next.Installations {
		if !installed.Active || request.Operation == "cleanup" {
			continue
		}
		p, err := store.Package(ctx, installed.Content)
		if err != nil {
			return recipe.Preview{}, recipeError(err)
		}
		packages[installed.Content] = p
	}
	composition, err := recipe.Compose(r.baseConfig, next.Installations, packages)
	if request.Operation == "cleanup" {
		composition = r.recipeComposition
		err = nil
	}
	if err != nil {
		return recipe.Preview{}, recipeError(err)
	}
	p, err := store.Package(ctx, entry.Content)
	if err != nil {
		if request.Operation != "remove" && request.Operation != "deactivate" && request.Operation != "cleanup" {
			return recipe.Preview{}, recipeError(err)
		}
		p = &recipe.Package{Manifest: recipe.Manifest{ID: entry.DeclaredID}}
	}
	prior, _ := store.Package(ctx, old.Content)
	prefix := recipe.Namespace(entry.ID)
	resets := []string{}
	for name, previous := range r.parameters {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if current, ok := composition.Config.Parameters[name]; ok {
			if _, err := current.ValidateValue(previous.Value); err != nil || current.Type != previous.Type {
				resets = append(resets, name)
			}
		}
	}
	slices.Sort(resets)
	if len(resets) > 0 && !request.Reset {
		return recipe.Preview{}, fault.New(protocol.InvalidRequest, "Parameter values no longer satisfy the recipe. Review with reset_parameters=true to use declared defaults.")
	}
	definitions, err := r.recipeDefinitions(composition, entry, p.Manifest)
	if err != nil {
		return recipe.Preview{}, recipeError(err)
	}
	if err := r.checkPublication(composition.Config); err != nil {
		return recipe.Preview{}, err
	}
	preview := recipe.Preview{ID: identity.New(), ExpiresAt: now.Add(time.Minute).UTC(), Instance: r.instance, Generation: r.generation, StoreRevision: next.Revision, Operation: request.Operation, Installation: entry, Project: recipe.Project(entry, p.Manifest), Definitions: definitions, Changes: recipeChanges(old, entry, request.Operation, p, store, ctx), Disabled: composition.Disabled, Resets: resets, Inventory: p.Manifest.Inventory}
	preview.ExperimentSteps, err = r.recipeExperimentSteps(composition, entry, p.Manifest)
	if err != nil {
		return recipe.Preview{}, recipeError(err)
	}
	preview.After = &p.Manifest
	preview.Workflows = map[string]config.Workflow{}
	preview.Experiments = map[string]protocol.Experiment{}
	for name, w := range composition.Config.Workflows {
		if strings.HasPrefix(name, prefix) {
			preview.Workflows[name] = w
		}
	}
	for name, e := range composition.Config.Experiments {
		if strings.HasPrefix(name, prefix) {
			preview.Experiments[name] = e
		}
	}
	if request.Operation == "cleanup" {
		preview.Cleanup, _, err = store.Unused()
		if err != nil {
			return recipe.Preview{}, recipeError(err)
		}
	}
	if prior != nil {
		preview.Before = &prior.Manifest
	}
	private, err := json.Marshal(struct {
		Preview recipe.Preview
		Config  *config.Config
		Inputs  string
	}{preview, composition.Config, r.recipeInputDigest()})
	if err != nil {
		return recipe.Preview{}, fault.New(protocol.Internal, "Cannot serialize recipe preview.")
	}
	total := len(private)
	for _, p := range r.recipePreviews {
		total += p.bytes
	}
	if len(private) > 1<<20 || total > 16<<20 {
		return recipe.Preview{}, fault.New(protocol.Busy, "Recipe previews exceed their memory allowance.")
	}
	mac := hmac.New(sha256.New, r.captureKey)
	_, _ = mac.Write(private)
	preview.Digest = hex.EncodeToString(mac.Sum(nil))
	if r.recipePreviews == nil {
		r.recipePreviews = map[string]recipePreparation{}
	}
	r.recipePreviews[preview.ID] = recipePreparation{preview: preview, selection: next, composition: composition, inputs: r.recipeInputDigest(), bytes: len(private)}
	return evidence.Clone(preview), nil
}
func (r *Runtime) CommitRecipe(ctx context.Context, name string, request recipe.Commit) (recipe.ManagementResult, error) {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	store, err := r.recipeStore()
	if err != nil {
		return recipe.ManagementResult{}, err
	}
	bytes, _ := json.Marshal(struct {
		Name    string
		Request recipe.Commit
	}{name, request})
	digest := evidence.Digest(bytes)
	if response, known, err := store.Lookup(request.RequestID, digest); known {
		return response, err
	}
	if err := r.writable(ctx); err != nil {
		return recipe.ManagementResult{}, err
	}
	p, ok := r.recipePreviews[request.Preparation]
	if !ok || !hmac.Equal([]byte(p.preview.Digest), []byte(request.Digest)) || p.preview.Installation.ID != name || p.preview.Generation != r.generation || p.inputs != r.recipeInputDigest() || !time.Now().Before(p.preview.ExpiresAt) {
		return recipe.ManagementResult{}, fault.New(protocol.StalePreparation, "Recipe preview expired or changed; review again.")
	}
	if !request.Confirmed {
		return recipe.ManagementResult{}, fault.New(protocol.ConfirmationRequired, "Review the exact recipe composition and confirm activation.")
	}
	disk, err := config.Load(r.path)
	if err != nil || recipe.BaseDigest(disk) != recipe.BaseDigest(r.baseConfig) {
		return recipe.ManagementResult{}, fault.New(protocol.StalePreparation, "Host configuration changed; reload and review again.")
	}
	if err := r.checkPublication(p.composition.Config); err != nil {
		return recipe.ManagementResult{}, err
	}
	result, err := store.Commit(p.preview.StoreRevision, p.selection, request.RequestID, digest, recipe.ManagementResult{ID: name, Operation: p.preview.Operation, Content: p.preview.Installation.Content})
	if err != nil {
		if errors.Is(err, recipe.ErrUncertain) {
			r.compositionUncertain = true
			return recipe.ManagementResult{}, fault.New(protocol.RecordingFailed, err.Error())
		}
		return recipe.ManagementResult{}, recipeError(err)
	}
	if _, err := r.publishConfig(context.Background(), p.composition.Config); err != nil {
		r.compositionUncertain = true
		return recipe.ManagementResult{}, fault.New(protocol.RecordingFailed, "Selection committed but runtime publication failed; restart to recover the selected composition.")
	}
	r.recipeComposition = p.composition
	if len(p.preview.Cleanup) > 0 {
		if err := store.Cleanup(p.preview.Cleanup); err != nil {
			r.recipeError = fmt.Errorf("cleanup committed; unused files retained: %w", err)
		}
	}
	return result, nil
}

// A failed state serialization cannot occur after selecting the durable recipe
// manifest. The actual publication runs under the same runtime lock.
func (r *Runtime) checkPublication(candidate *config.Config) error {
	parameters := cloneParameters(candidate.Parameters)
	for name, current := range r.parameters {
		p, exists := parameters[name]
		if !exists || p.Type != current.Type {
			continue
		}
		if value, err := p.ValidateValue(current.Value); err == nil {
			p.Value = value
			parameters[name] = p
		}
	}
	values := map[string]any{}
	for name, p := range parameters {
		if p.Persistent {
			values[name] = p.Value
		}
	}
	data, err := json.Marshal(state.Snapshot{Version: 1, Context: r.context, Parameters: values})
	if err != nil || len(data) > state.MaxBytes {
		return fault.New(protocol.StorageFull, "Recipe composition exceeds persistent state limits.")
	}
	return nil
}
func (r *Runtime) recipeDefinitions(composition recipe.Composition, entry recipe.Installation, m recipe.Manifest) ([]recipe.DefinitionPreview, error) {
	definitions := []recipe.DefinitionPreview{}
	prefix := recipe.Namespace(entry.ID)
	project := recipe.Project(entry, m)
	// Preview rendering reuses command expansion without provider Run/Describe calls.
	scratch := &Runtime{cfg: composition.Config, path: r.path, context: cloneContext(r.context), opener: r.opener}
	scratch.context.Project = project
	for _, name := range slices.Sorted(maps.Keys(composition.Config.Actions)) {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		a := composition.Config.Actions[name]
		view := recipe.DefinitionPreview{Name: name, Type: a.Type, Safety: string(a.Safety), Workflow: a.Workflow, Target: a.Target, Device: a.Device, Operation: a.Operation, Channel: a.Channel, Parameter: a.Parameter, Plugin: a.Plugin, Prompt: a.Prompt, Files: slices.Clone(a.Files), Inputs: map[string]any{}}
		if a.Type == "plugin" {
			view.Safety = string(permission.Strongest(a.Safety, r.plugins.Risk(a.Plugin, a.Operation)))
		}
		values := map[string]any{}
		for key, input := range a.Inputs {
			value := input.Default
			if input.Required {
				value = "<input:" + key + ">"
			}
			if input.Sensitive {
				value = "<redacted:" + key + ">"
			}
			values[key] = value
			view.Inputs[key] = value
		}
		if a.Type == "exec" || a.Type == "open" || a.Type == "git" {
			// Git argument builders need valid typed values; the semantic operation/input
			// declaration remains the preview when required arguments have no values yet.
			ready := true
			for _, input := range a.Inputs {
				if input.Required && a.Type == "git" {
					ready = false
				}
			}
			if ready {
				command, risk, err := scratch.prepareCommand(a, values)
				if err != nil {
					return nil, err
				}
				view.Safety = string(permission.Strongest(a.Safety, risk))
				view.Executable, view.Arguments, view.Directory = command.Path, command.Args, command.Dir
				for _, env := range command.Env {
					key, _, _ := strings.Cut(env, "=")
					view.EnvironmentNames = append(view.EnvironmentNames, key)
				}
			}
		}
		definitions = append(definitions, view)
	}
	return definitions, nil
}
func recipeChanges(old, next recipe.Installation, operation string, p *recipe.Package, store *recipe.Store, ctx context.Context) []string {
	changes := []string{operation + " installation " + next.ID}
	if old.Alias != next.Alias {
		changes = append(changes, "display alias changed")
	}
	if old.Content != next.Content {
		prior, err := store.Package(ctx, old.Content)
		if err != nil {
			return append(changes, "previous package unavailable")
		}
		before, _ := json.Marshal(prior.Manifest)
		after, _ := json.Marshal(p.Manifest)
		var a, b map[string]any
		_ = json.Unmarshal(before, &a)
		_ = json.Unmarshal(after, &b)
		for _, field := range []string{"actions", "parameters", "workflows", "experiments", "controls", "requirements", "inventory", "features", "schemas"} {
			left, _ := json.Marshal(a[field])
			right, _ := json.Marshal(b[field])
			if string(left) != string(right) {
				changes = append(changes, fmt.Sprintf("%s changed; inspect selected definitions and package inventory", field))
			}
		}
	}
	return changes
}

// Expand experiment commands with the values that publication will retain. This
// is description only: it neither reads agent files nor contacts a provider.
func (r *Runtime) recipeExperimentSteps(c recipe.Composition, entry recipe.Installation, m recipe.Manifest) (map[string][]recipe.DefinitionPreview, error) {
	out := map[string][]recipe.DefinitionPreview{}
	prefix := recipe.Namespace(entry.ID)
	project := recipe.Project(entry, m)
	scratch := &Runtime{cfg: c.Config, path: r.path, context: cloneContext(r.context), opener: r.opener}
	scratch.context.Project = project
	for _, name := range slices.Sorted(maps.Keys(c.Config.Experiments)) {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		experiment := c.Config.Experiments[name]
		steps, err := c.Config.ExperimentSteps(experiment, project)
		if err != nil {
			return nil, err
		}
		for i, step := range steps {
			args := maps.Clone(step.Args)
			if args == nil {
				args = map[string]any{}
			}
			for _, mapping := range experiment.Inputs {
				if mapping.Step == i {
					p := c.Config.Parameters[mapping.Parameter]
					value := p.Value
					if current, exists := r.parameters[mapping.Parameter]; exists && current.Type == p.Type {
						if retained, err := p.ValidateValue(current.Value); err == nil {
							value = retained
						}
					}
					args[mapping.Input] = value
				}
			}
			values, err := step.Definition.Arguments(args)
			if err != nil {
				return nil, err
			}
			a := step.Definition
			// Preserve field positions but never expand a secret argument into the preview.
			for key, input := range a.Inputs {
				if input.Sensitive {
					values[key] = "<redacted:" + key + ">"
				}
			}
			view := recipe.DefinitionPreview{Name: step.Name, Type: a.Type, Safety: string(a.Safety), Device: a.Device, Operation: a.Operation, Channel: a.Channel, Parameter: a.Parameter, Inputs: values, Plugin: a.Plugin, Prompt: a.Prompt, Files: slices.Clone(a.Files)}
			if a.Type == "exec" || a.Type == "open" || a.Type == "git" {
				command, risk, err := scratch.prepareCommand(a, values)
				if err != nil {
					return nil, err
				}
				view.Executable, view.Arguments, view.Directory = command.Path, command.Args, command.Dir
				view.Safety = string(permission.Strongest(a.Safety, risk))
				for _, env := range command.Env {
					key, _, _ := strings.Cut(env, "=")
					view.EnvironmentNames = append(view.EnvironmentNames, key)
				}
			}
			if a.Type == "plugin" {
				view.Safety = string(permission.Strongest(a.Safety, r.plugins.Risk(a.Plugin, a.Operation)))
			}
			out[name] = append(out[name], view)
		}
	}
	return out, nil
}
