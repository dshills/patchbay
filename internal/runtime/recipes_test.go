package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/provider"
	"patchbay/internal/recipe"
	"patchbay/pkg/protocol"
)

func recipePackage(t *testing.T, version string) *recipe.Package {
	t.Helper()
	p, err := recipe.Inspect(context.Background(), "../../recipes/benchmark")
	if err != nil {
		t.Fatal(err)
	}
	if version != "" {
		p.Manifest.Version = version
		delete(p.Files, "recipe.yaml")
		p, err = recipe.Build(p.Manifest, p.Files)
		if err != nil {
			t.Fatal(err)
		}
	}
	return p
}
func importRecipe(t *testing.T, r *Runtime, p *recipe.Package, target string) recipe.Installation {
	t.Helper()
	data, err := recipe.ZIP(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.ImportRecipe(context.Background(), data, target, "")
	if err != nil {
		t.Fatal(err)
	}
	return v.Installation
}
func recipeMapping() map[string]recipe.Mapping {
	return map[string]recipe.Mapping{"benchmark": {Project: "p"}, "demo": {Tool: "/bin/echo"}}
}
func prepareRecipe(t *testing.T, r *Runtime, id, op string) recipe.Preview {
	t.Helper()
	p, err := r.PrepareRecipe(context.Background(), id, recipe.Prepare{Operation: op, Mappings: recipeMapping()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func commitRecipe(t *testing.T, r *Runtime, p recipe.Preview) recipe.Commit {
	t.Helper()
	request := recipe.Commit{Preparation: p.ID, Digest: p.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true}
	if _, err := r.CommitRecipe(context.Background(), p.Installation.ID, request); err != nil {
		t.Fatal(err)
	}
	return request
}
func TestRecipeLifecycleDurabilityAndHostInvalidation(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	r, path := setup(t, fixture, runner)
	original, _ := os.ReadFile(path)
	first := importRecipe(t, r, recipePackage(t, ""), "")
	second := importRecipe(t, r, recipePackage(t, "1.0.1"), "")
	if first.ID == second.ID || first.Alias == second.Alias {
		t.Fatal("same publisher identity collided")
	}
	p := prepareRecipe(t, r, first.ID, "activate")
	if len(p.Definitions) != 2 || p.Before == nil || p.After == nil || p.Definitions[0].Safety != "confirm" {
		t.Fatal("incomplete preview")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != string(original) || runner.count() != 0 {
		t.Fatal("preview executed or modified host")
	}
	request := commitRecipe(t, r, p)
	generation := r.generation
	if _, err := r.CommitRecipe(ctx, first.ID, request); err != nil || r.generation != generation {
		t.Fatal("retry republished", err)
	}
	prefix := recipe.Namespace(first.ID)
	action := prefix + "benchmark.measure"
	if _, err := r.Invoke(ctx, action, false, protocol.Invocation{Args: map[string]any{"iterations": 10, "repeats": 2}}); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal("activation granted invocation", err)
	}
	h, err := r.Invoke(ctx, action, false, protocol.Invocation{Confirmed: true, Args: map[string]any{"iterations": 10, "repeats": 2}})
	if err != nil {
		t.Fatal(err)
	}
	finished(t, h)
	if _, err := r.SetParameter(ctx, prefix+"iterations", 20000); err != nil {
		t.Fatal(err)
	}
	candidate := recipePackage(t, "1.1.0")
	importRecipe(t, r, candidate, first.ID)
	commitRecipe(t, r, prepareRecipe(t, r, first.ID, "update"))
	if r.parameters[prefix+"iterations"].Value != int64(20000) {
		t.Fatal("compatible value lost")
	}
	commitRecipe(t, r, prepareRecipe(t, r, first.ID, "rollback"))
	entry, _ := r.Recipe(ctx, first.ID)
	if entry.Installation.Content != first.Content {
		t.Fatal("rollback failed")
	}
	// Offline host changes disable approvals on restart; alias changes cannot renew them.
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, path, strings.Replace(fixture, "tag: initial", "tag: revised", 1))
	r2, err := New(path, Options{Runner: runner, Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r2.Close(ctx) }()
	if _, err := r2.Action(action); err == nil {
		t.Fatal("offline host change retained recipe approval")
	}
	rename, err := r2.PrepareRecipe(ctx, first.ID, recipe.Prepare{Operation: "rename", Alias: "new-name"})
	if err != nil {
		t.Fatal(err)
	}
	commitRecipe(t, r2, rename)
	if _, err := r2.Action(action); err == nil {
		t.Fatal("rename regranted approval")
	}
	commitRecipe(t, r2, prepareRecipe(t, r2, first.ID, "activate"))
	if runner.count() != 1 {
		t.Fatal("lifecycle executed provider")
	}
	commitRecipe(t, r2, prepareRecipe(t, r2, first.ID, "remove"))
	list, err := r2.Recipes(ctx)
	if err != nil || len(list.Installations) != 1 || list.Installations[0].Installation.ID != second.ID {
		t.Fatal("unrelated installation changed", err)
	}
}
func TestRecipeStalePreviewResetAndPinnedJob(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{started: make(chan provider.Command, 1), release: make(chan struct{})}
	r, _ := setup(t, fixture, runner)
	p := recipePackage(t, "")
	a := p.Manifest.Actions["benchmark.measure"]
	a.Args = []string{"block"}
	p.Manifest.Actions["benchmark.measure"] = a
	delete(p.Files, "recipe.yaml")
	p, err := recipe.Build(p.Manifest, p.Files)
	if err != nil {
		t.Fatal(err)
	}
	entry := importRecipe(t, r, p, "")
	stale := prepareRecipe(t, r, entry.ID, "activate")
	if _, err := r.SetParameter(ctx, "count", 2); err != nil {
		t.Fatal(err)
	}
	_, err = r.CommitRecipe(ctx, entry.ID, recipe.Commit{Preparation: stale.ID, Digest: stale.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true})
	if fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal("stale values accepted", err)
	}
	commitRecipe(t, r, prepareRecipe(t, r, entry.ID, "activate"))
	prefix := recipe.Namespace(entry.ID)
	if _, err := r.SetParameter(ctx, prefix+"iterations", 20000); err != nil {
		t.Fatal(err)
	}
	p.Manifest.Version = "2.0.0"
	param := p.Manifest.Parameters["iterations"]
	param.Max = 15000
	delete(p.Files, "samples/benchmark-large.json")
	p.Manifest.Parameters["iterations"] = param
	delete(p.Files, "recipe.yaml")
	p, err = recipe.Build(p.Manifest, p.Files)
	if err != nil {
		t.Fatal(err)
	}
	importRecipe(t, r, p, entry.ID)
	if _, err := r.PrepareRecipe(ctx, entry.ID, recipe.Prepare{Operation: "update"}); err == nil {
		t.Fatal("invalid value silently reset")
	}
	reset, err := r.PrepareRecipe(ctx, entry.ID, recipe.Prepare{Operation: "update", Reset: true})
	if err != nil || len(reset.Resets) != 1 {
		t.Fatal("missing reset preview", err)
	}
	commitRecipe(t, r, reset)
	if r.parameters[prefix+"iterations"].Value != int64(10000) {
		t.Fatal("explicit reset not applied")
	}
	response, err := r.Capture(ctx, prepareTestCapture(t, r, prefix+"benchmark"))
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started
	commitRecipe(t, r, prepareRecipe(t, r, entry.ID, "remove"))
	close(runner.release)
	run := waitCapture(t, r, response)
	if run.Recipe == nil || run.Recipe.Installation != entry.ID || len(run.Outcomes) != 1 {
		t.Fatal("pinned run lost provenance")
	}
	if _, err := r.PrepareCapture(ctx, protocol.CapturePrepare{Experiment: prefix + "benchmark"}); err == nil {
		t.Fatal("removed recipe admitted work")
	}
	if _, err := r.runs.Get(run.ID); err != nil {
		t.Fatal("removal deleted saved evidence", err)
	}
}

func TestRecipeManagementCASCleanupAndExpiredPreview(t *testing.T) {
	ctx := context.Background()
	r, _ := setup(t, fixture, &fakeRunner{})
	first := importRecipe(t, r, recipePackage(t, ""), "")
	a := prepareRecipe(t, r, first.ID, "activate")
	b := prepareRecipe(t, r, first.ID, "activate")
	commitRecipe(t, r, a)
	_, err := r.CommitRecipe(ctx, first.ID, recipe.Commit{Preparation: b.ID, Digest: b.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true})
	if fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal("second client overwrote selection", err)
	}
	stale := prepareRecipe(t, r, first.ID, "deactivate")
	r.mu.Lock()
	p := r.recipePreviews[stale.ID]
	p.preview.ExpiresAt = time.Now().Add(-time.Second)
	r.recipePreviews[stale.ID] = p
	r.mu.Unlock()
	_, err = r.CommitRecipe(ctx, first.ID, recipe.Commit{Preparation: stale.ID, Digest: stale.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true})
	if fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal("expired approval accepted", err)
	}
	for _, version := range []string{"1.1.0", "1.2.0"} {
		importRecipe(t, r, recipePackage(t, version), first.ID)
		commitRecipe(t, r, prepareRecipe(t, r, first.ID, "update"))
	}
	list, err := r.Recipes(ctx)
	if err != nil || len(list.Unused) != 1 {
		t.Fatal("older unused version not visible", err)
	}
	cleanup, err := r.PrepareRecipe(ctx, "store", recipe.Prepare{Operation: "cleanup"})
	if err != nil || len(cleanup.Cleanup) != 1 {
		t.Fatal("cleanup preview", err)
	}
	commitRecipe(t, r, cleanup)
	list, err = r.Recipes(ctx)
	if err != nil || len(list.Unused) != 0 {
		t.Fatal("unused files retained", err)
	}
	commitRecipe(t, r, prepareRecipe(t, r, first.ID, "rollback"))
	if _, err := r.Recipe(ctx, first.ID); err != nil {
		t.Fatal("cleanup removed selected content", err)
	}
}

func TestRecipeSelectionUncertaintyBlocksAdmissionUntilRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, fixture)
	armed := false
	runner := &fakeRunner{}
	r, err := New(path, Options{Runner: runner, Log: io.Discard, Recipes: recipe.StoreOptions{Fault: func(op, name string) error {
		if armed && op == "dirsync" && name == "selection.json" {
			return errors.New("injected directory sync failure")
		}
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(ctx) }()
	entry := importRecipe(t, r, recipePackage(t, ""), "")
	p := prepareRecipe(t, r, entry.ID, "activate")
	armed = true
	req := recipe.Commit{Preparation: p.ID, Digest: p.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true}
	if _, err := r.CommitRecipe(ctx, entry.ID, req); fault.Safe(err).Code != protocol.RecordingFailed {
		t.Fatal("uncertainty hidden", err)
	}
	if _, err := r.Invoke(ctx, "echo", false, protocol.Invocation{}); fault.Safe(err).Code != protocol.RecordingFailed {
		t.Fatal("admitted work against uncertain composition", err)
	}
	if runner.count() != 0 {
		t.Fatal("activation executed a provider")
	}
	_ = r.Close(ctx)
	restored, err := New(path, Options{Runner: runner, Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close(ctx) }()
	if _, err := restored.Action(recipe.Namespace(entry.ID) + "benchmark.measure"); err != nil {
		t.Fatal("durably selected composition not recovered", err)
	}
	if _, err := restored.CommitRecipe(ctx, entry.ID, req); err != nil {
		t.Fatal("receipt missing after restart", err)
	}
	if runner.count() != 0 {
		t.Fatal("restart replayed execution")
	}
}
