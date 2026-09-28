package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"patchbay/internal/action"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/recipe"
	"patchbay/pkg/protocol"
)

func TestRecipeExportExactPrivacyAndRemappedSamples(t *testing.T) {
	ctx := context.Background()
	r, _ := setup(t, fixture, resultRunner{result: action.Result{Status: action.Success, Data: map[string]any{"stdout": `{"schema_version":1,"data":{"duration":{"value":2.5,"unit":"ms","quantity":"duration"},"host":"PRIVATE_HOST","work":"PRIVATE_LOG"}}`}}})
	entry := importRecipe(t, r, recipePackage(t, ""), "")
	commitRecipe(t, r, prepareRecipe(t, r, entry.ID, "activate"))
	req := protocol.ComparisonRequest{Baseline: protocol.ResultReference{Kind: "recipe_sample", Installation: entry.ID, Content: entry.Content, ID: "benchmark-small"}, Candidate: protocol.ResultReference{Kind: "recipe_sample", Installation: entry.ID, Content: entry.Content, ID: "benchmark-large"}}
	comparison, err := r.Compare(ctx, req)
	if err != nil || !comparison.Compatible || !comparison.Illustrative {
		t.Fatal("remapped samples not comparable", err, comparison.Reasons)
	}
	response, err := r.Capture(ctx, prepareTestCapture(t, r, recipe.Namespace(entry.ID)+"benchmark"))
	if err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, response)
	if run.State != "success" {
		t.Fatalf("capture failed: %s outcomes=%+v error=%+v measurements=%+v", run.State, run.Outcomes, run.Error, run.Measurements)
	}
	req.Candidate = protocol.ResultReference{Kind: "run", ID: run.ID}
	comparison, err = r.Compare(ctx, req)
	if err != nil || !comparison.Compatible {
		t.Fatal("sample/local comparison failed", err, comparison.Reasons)
	}
	preview, err := r.PrepareRecipeExport(ctx, entry.ID, recipe.ExportPrepare{Samples: []string{"benchmark-small", "benchmark-large"}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.PackageDigest != entry.Content {
		t.Fatal("complete portable round trip changed identity")
	}
	if _, err := r.ExportRecipe(ctx, entry.ID, recipe.ExportRequest{Preparation: preview.ID, Digest: preview.Digest}); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal("export skipped consent")
	}
	file, err := r.ExportRecipe(ctx, entry.ID, recipe.ExportRequest{Preparation: preview.ID, Digest: preview.Digest, Confirmed: true})
	if err != nil || evidence.Digest(file.Data) != preview.Digest {
		t.Fatal(err)
	}
	imported, err := recipe.InspectZIP(ctx, file.Data)
	if err != nil || imported.Digest != entry.Content {
		t.Fatal("recipient cannot inspect", err)
	}
	second, _ := setup(t, fixture, &fakeRunner{})
	other := importRecipe(t, second, imported, "")
	commitRecipe(t, second, prepareRecipe(t, second, other.ID, "activate"))
	req.Baseline.Installation = other.ID
	req.Candidate = protocol.ResultReference{Kind: "recipe_sample", Installation: other.ID, Content: other.Content, ID: "benchmark-large"}
	if result, err := second.Compare(ctx, req); err != nil || !result.Compatible {
		t.Fatal("second installation namespace failed", err)
	}
	exported, err := r.PrepareRecipeExport(ctx, entry.ID, recipe.ExportPrepare{Runs: []string{run.ID}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(exported.Files)
	for _, private := range []string{entry.ID, run.ID, run.JobID, "PRIVATE_HOST", "PRIVATE_LOG", "PATCHBAY_OVERLAY", "/bin/echo"} {
		if private != "" && strings.Contains(string(data), private) {
			t.Fatal("local field leaked", private)
		}
	}
	var sample protocol.Sample
	if err := json.Unmarshal([]byte(exported.Files["samples/saved-1.json"]), &sample); err != nil || sample.Origin != "sample" || sample.ID == run.ID {
		t.Fatal("saved sample identity", err)
	}

}
func TestRecipeExportStaleSelectionAndPrivateDefaults(t *testing.T) {
	ctx := context.Background()
	r, _ := setup(t, fixture, &fakeRunner{})
	entry := importRecipe(t, r, recipePackage(t, ""), "")
	commitRecipe(t, r, prepareRecipe(t, r, entry.ID, "activate"))
	p, err := r.PrepareRecipeExport(ctx, entry.ID, recipe.ExportPrepare{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetParameter(ctx, recipe.Namespace(entry.ID)+"iterations", 12000); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExportRecipe(ctx, entry.ID, recipe.ExportRequest{Preparation: p.ID, Digest: p.Digest, Confirmed: true}); fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal("changed defaults retained approval")
	}
	if _, err := r.PrepareRecipeExport(ctx, entry.ID, recipe.ExportPrepare{Actions: []string{}}); err == nil {
		t.Fatal("broken export graph accepted")
	}
	if _, err := r.PrepareRecipeExport(ctx, entry.ID, recipe.ExportPrepare{Samples: []string{"missing"}}); err == nil {
		t.Fatal("unknown sample accepted")
	}
	p, err = r.PrepareRecipeExport(ctx, entry.ID, recipe.ExportPrepare{Defaults: []string{"iterations"}})
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	expired := r.recipeExports[p.ID]
	expired.preview.ExpiresAt = time.Now().Add(-time.Second)
	r.recipeExports[p.ID] = expired
	r.mu.Unlock()
	if _, err := r.ExportRecipe(ctx, entry.ID, recipe.ExportRequest{Preparation: p.ID, Digest: p.Digest, Confirmed: true}); fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal("expired export accepted")
	}
}

func TestRecipeDocumentUTF8AndAdvisoryPrivacy(t *testing.T) {
	ctx := context.Background()
	r, _ := setup(t, fixture, &fakeRunner{})
	p := recipePackage(t, "")
	p.Files["README.md"] = []byte(strings.Repeat("é", (256<<10)/2-1) + "界<script>window.recipeInjected=true</script>\napi_key = PRIVATE_TEXT")
	delete(p.Files, "recipe.yaml")
	p, err := recipe.Build(p.Manifest, p.Files)
	if err != nil {
		t.Fatal(err)
	}
	entry := importRecipe(t, r, p, "")
	view, err := r.Recipe(ctx, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, doc := range view.Documents {
		if doc.Path == "README.md" {
			found = true
			if !doc.Truncated || !utf8.ValidString(doc.Text) || len(doc.Text) > 256<<10 {
				t.Fatal("invalid document excerpt")
			}
		}
	}
	if !found {
		t.Fatal("missing readme")
	}
	preview, err := r.PrepareRecipeExport(ctx, entry.ID, recipe.ExportPrepare{})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Warnings) < 2 || !strings.Contains(preview.Files["README.md"], "PRIVATE_TEXT") {
		t.Fatal("advisory concealed included data")
	}
}
