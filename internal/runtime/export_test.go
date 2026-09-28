package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

func TestExportPreviewPrivacyReferencesAndExpiry(t *testing.T) {
	r, _ := setup(t, fixture+captureFixture, &captureRunner{})
	response, err := r.Capture(context.Background(), prepareTestCapture(t, r, "compare"))
	if err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, response)
	if _, err := r.runs.Annotate(run.ID, protocol.AnnotationUpdate{Note: "private note"}); err != nil {
		t.Fatal(err)
	}
	preview, err := r.PrepareExport(context.Background(), protocol.ExportPrepare{Runs: []string{run.ID}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(preview.Document)
	if strings.Contains(string(data), "private note") || strings.Contains(string(data), "initial dev") || strings.Contains(string(data), "/bin/echo") || strings.Contains(string(data), "parameters") {
		t.Fatalf("private export: %s", data)
	}
	if err := r.runs.Delete(run.ID, false); fault.Safe(err).Code != protocol.RequestConflict {
		t.Fatal("export reference failed to protect run")
	}
	file, err := r.Export(context.Background(), protocol.ExportRequest{Preparation: preview.ID, Digest: preview.Digest, Format: "html"})
	if err != nil || !strings.Contains(string(file.Data), "Patchbay") {
		t.Fatalf("export: %v", err)
	}
	if _, err := r.Export(context.Background(), protocol.ExportRequest{Preparation: preview.ID, Digest: preview.Digest, Format: "html"}); fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal("consumed export accepted")
	}
	preview, err = r.PrepareExport(context.Background(), protocol.ExportPrepare{Runs: []string{run.ID}, Options: protocol.ExportOptions{Notes: true, Logs: true, Inputs: true}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Document.Runs[0].Note != "private note" || len(preview.Document.Runs[0].Logs) != 1 {
		t.Fatal("selected fields omitted")
	}
	r.mu.Lock()
	expired := r.exports[preview.ID]
	expired.preview.ExpiresAt = time.Now().Add(-time.Second)
	r.exports[preview.ID] = expired
	r.mu.Unlock()
	if _, err := r.Export(context.Background(), protocol.ExportRequest{Preparation: preview.ID, Digest: preview.Digest, Format: "json"}); fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal("expired export accepted")
	}
	if err := r.runs.Delete(run.ID, false); err != nil {
		t.Fatal("expired export retained reference", err)
	}
}

func TestCancelledEvidenceReadsDoNotConsumeApproval(t *testing.T) {
	r, _ := setup(t, fixture+captureFixture, &captureRunner{})
	response, err := r.Capture(context.Background(), prepareTestCapture(t, r, "compare"))
	if err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, response)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Compare(ctx, protocol.ComparisonRequest{Baseline: protocol.ResultReference{Kind: "run", ID: run.ID}, Candidate: protocol.ResultReference{Kind: "run", ID: run.ID}}); fault.Safe(err).Code != protocol.Cancelled {
		t.Fatal("comparison ignored cancellation")
	}
	if _, err := r.PrepareExport(ctx, protocol.ExportPrepare{Runs: []string{run.ID}}); fault.Safe(err).Code != protocol.Cancelled {
		t.Fatal("export preparation ignored cancellation")
	}
	preview, err := r.PrepareExport(context.Background(), protocol.ExportPrepare{Runs: []string{run.ID}})
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.ExportRequest{Preparation: preview.ID, Digest: preview.Digest, Format: "html"}
	if _, err := r.Export(ctx, request); fault.Safe(err).Code != protocol.Cancelled {
		t.Fatal("export ignored cancellation")
	}
	if _, err := r.Export(context.Background(), request); err != nil {
		t.Fatal("cancelled export consumed approval", err)
	}
}
