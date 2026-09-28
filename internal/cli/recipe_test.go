package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"patchbay/internal/evidence"
	"patchbay/internal/recipe"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecipeInspectionIsOfflineAndHasHumanOutput(t *testing.T) {
	for _, format := range []string{"human", "json"} {
		var out, errOut bytes.Buffer
		args := []string{"recipe", "inspect", "../../recipes/benchmark"}
		if format == "json" {
			args = append(args, "--json")
		}
		if code := runDeckctl(context.Background(), args, &out, &errOut); code != 0 {
			t.Fatal(code, errOut.String())
		}
		if format == "json" {
			var result map[string]any
			if json.Unmarshal(out.Bytes(), &result) != nil || result["package_digest"] == nil {
				t.Fatal("missing verified identity")
			}
		} else if !strings.Contains(out.String(), "Offline inspection only") || !strings.Contains(out.String(), "Role demo: tool") {
			t.Fatal(out.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := runDeckctl(context.Background(), []string{"recipe", "inspect", "../../recipes/benchmark", "--socket", "/nonexistent"}, &out, &errOut); code != 2 {
		t.Fatal("offline inspection accepted transport options", code)
	}
}

func TestRecipeLifecycleCLI(t *testing.T) {
	d := newDaemon(t)
	text, _ := ctl(t, d, true, 0, "recipe", "import", "../../recipes/benchmark")
	var installed recipe.ImportResult
	if err := json.Unmarshal([]byte(text), &installed); err != nil {
		t.Fatal(err)
	}
	id := installed.Installation.ID
	ctl(t, d, true, 0, "recipe", "list")
	ctl(t, d, true, 0, "recipe", "show", id)
	text, _ = ctl(t, d, true, 0, "recipe", "prepare", id, `{"operation":"activate","mappings":{"benchmark":{"project":"demo"},"demo":{"tool":"/bin/echo"}},"assignments":{"capture":{"control":"recipe.capture","gesture":"press"}}}`)
	var p recipe.Preview
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		t.Fatal(err)
	}
	req := evidence.NewRequestID(time.Now())
	ctl(t, d, true, 4, "recipe", "commit", id, p.ID, p.Digest, req)
	one, _ := ctl(t, d, true, 0, "recipe", "commit", id, p.ID, p.Digest, req, "--confirm")
	d.stop(t)
	d.start(t)
	two, _ := ctl(t, d, true, 0, "recipe", "commit", id, p.ID, p.Digest, req, "--confirm")
	if one != two {
		t.Fatal("durable CLI retry changed result")
	}
	text, _ = ctl(t, d, true, 0, "recipe", "prepare", id, `{"operation":"activate","assignments":{}}`)
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		t.Fatal(err)
	}
	ctl(t, d, true, 0, "recipe", "commit", id, p.ID, p.Digest, evidence.NewRequestID(time.Now()), "--confirm")
	text, _ = ctl(t, d, true, 0, "recipe", "show", id)
	var view recipe.View
	if err := json.Unmarshal([]byte(text), &view); err != nil || len(view.Installation.Assignments) != 0 {
		t.Fatal("empty assignment selection was lost", err)
	}
	ctl(t, d, true, 0, "project", "use", "demo")
	ctl(t, d, true, 0, "action", "run", recipe.Namespace(id)+"benchmark.measure", "--arg", "iterations=10", "--arg", "repeats=2", "--confirm")
}

func TestRecipeExportCLIUsesNewFileAndReimport(t *testing.T) {
	d := newDaemon(t)
	out, _ := ctl(t, d, true, 0, "recipe", "import", "../../recipes/benchmark")
	var entry recipe.ImportResult
	if err := json.Unmarshal([]byte(out), &entry); err != nil {
		t.Fatal(err)
	}
	out, _ = ctl(t, d, true, 0, "recipe", "export-preview", entry.Installation.ID, `{"samples":["benchmark-small","benchmark-large"]}`)
	var preview recipe.ExportPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "shared.zip")
	ctl(t, d, true, 0, "recipe", "export-save", entry.Installation.ID, preview.ID, preview.Digest, destination, "--confirm")
	pkg, err := recipe.Inspect(context.Background(), destination)
	if err != nil || pkg.Digest != entry.Digest {
		t.Fatal("portable identity changed", err)
	}
	ctl(t, d, true, 2, "recipe", "export-save", entry.Installation.ID, preview.ID, preview.Digest, destination, "--confirm")
	if info, err := os.Stat(destination); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("export not private", err)
	}
}
