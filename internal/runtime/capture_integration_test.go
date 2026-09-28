package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
)

func TestRecordingFailureAfterExternalEffectDoesNotRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(fixture+captureFixture), 0600); err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	var calls atomic.Int32
	runner := callbackRunner{run: func(context.Context, provider.Command, *provider.Budget) (action.Result, error) {
		calls.Add(1)
		fail.Store(true)
		return action.Result{Status: action.Success, Data: map[string]any{"stdout": "done"}}, nil
	}}
	r, err := New(path, Options{Runner: runner, Evidence: evidence.Options{Fault: func(op, name string) error {
		if fail.Load() && op == "sync" && strings.HasPrefix(name, "artifact-") {
			return errors.New("disk failed")
		}
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(context.Background()) }()
	request := prepareTestCapture(t, r, "compare")
	response, err := r.Capture(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, response)
	job, jobErr := r.Jobs().Get(response.JobID)
	if jobErr != nil {
		t.Fatal(jobErr)
	}
	encoded, _ := json.Marshal(job)
	if !strings.Contains(string(encoded), `"execution_outcome":{"status":"success"}`) {
		t.Fatal("recording failure hid the successful subprocess outcome")
	}
	if run.State != "recording_failed" || calls.Load() != 1 {
		t.Fatalf("state %s calls %d", run.State, calls.Load())
	}
	again, err := r.Capture(context.Background(), request)
	if err != nil || again.RunID != run.ID || calls.Load() != 1 {
		t.Fatal("uncertain external operation replayed")
	}
}
func TestAdmissionPersistenceFailurePreventsDispatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(fixture+captureFixture), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &captureRunner{}
	r, err := New(path, Options{Runner: runner, Evidence: evidence.Options{Fault: func(op, name string) error {
		if op == "rename" && strings.HasPrefix(name, "run-") {
			return errors.New("disk failed")
		}
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(context.Background()) }()
	_, err = r.Capture(context.Background(), prepareTestCapture(t, r, "compare"))
	if fault.Safe(err).Code != protocol.RecordingFailed {
		t.Fatal(err)
	}
	if len(r.Jobs().List()) != 0 || len(runner.commands) != 0 {
		t.Fatal("dispatched before durable identity")
	}
}
func TestRealDemoCaptureCompareAndExport(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the packaged demo")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "deckdemo")
	command := exec.Command("go", "build", "-o", binary, "../../cmd/deckdemo")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("demo build: %v %s", err, output)
	}
	source, err := os.ReadFile("../../configs/benchmark.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(source), "../bin/deckdemo", binary)
	text = strings.ReplaceAll(text, "../.cache/benchmark/", "private/")
	text = strings.ReplaceAll(text, "path: ..}", "path: .}")
	r, path := setup(t, text, provider.ProcessRunner{})
	first, err := r.Capture(context.Background(), prepareTestCapture(t, r, "benchmark"))
	if err != nil {
		t.Fatal(err)
	}
	base := waitCapture(t, r, first)
	if base.State != "success" || len(base.Measurements) != 1 || base.Measurements[0].Repeats != 5 || base.Measurements[0].Spread == nil {
		t.Fatalf("demo outcome %s %v", base.State, base.Error)
	}
	if _, err := r.SetParameter(context.Background(), "iterations", 20000); err != nil {
		t.Fatal(err)
	}
	second, err := r.Capture(context.Background(), prepareTestCapture(t, r, "benchmark"))
	if err != nil {
		t.Fatal(err)
	}
	candidate := waitCapture(t, r, second)
	comparison, err := r.Compare(context.Background(), protocol.ComparisonRequest{Baseline: protocol.ResultReference{Kind: "run", ID: base.ID}, Candidate: protocol.ResultReference{Kind: "run", ID: candidate.ID}})
	if err != nil || !comparison.Compatible || comparison.Metrics[0].Delta == nil {
		t.Fatalf("compare: %#v %v", comparison, err)
	}
	sample, err := r.Compare(context.Background(), protocol.ComparisonRequest{Baseline: protocol.ResultReference{Kind: "sample", ID: "benchmark-small"}, Candidate: protocol.ResultReference{Kind: "run", ID: candidate.ID}})
	if err != nil || !sample.Illustrative || !sample.Compatible {
		t.Fatalf("sample comparison %#v %v", sample, err)
	}
	preview, err := r.PrepareExport(context.Background(), protocol.ExportPrepare{Runs: []string{base.ID, candidate.ID}})
	if err != nil {
		t.Fatal(err)
	}
	file, err := r.Export(context.Background(), protocol.ExportRequest{Preparation: preview.ID, Digest: preview.Digest, Format: "html"})
	if err != nil || len(file.Data) == 0 {
		t.Fatal(err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := New(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close(context.Background()) }()
	saved, err := restored.runs.Get(candidate.ID)
	if err != nil || saved.State != "success" {
		t.Fatal("terminal evidence did not survive restart", err)
	}
}

type callbackRunner struct {
	run func(context.Context, provider.Command, *provider.Budget) (action.Result, error)
}

func (r callbackRunner) Run(c context.Context, p provider.Command, b *provider.Budget) (action.Result, error) {
	return r.run(c, p, b)
}
func TestExperimentWrapperUsesCapturePath(t *testing.T) {
	text := strings.Replace(fixture+captureFixture, "  echo:\n", "  capture: {type: experiment, experiment: compare, safety: confirm, timeout: 10s}\n  echo:\n", 1)
	r, _ := setup(t, text, &captureRunner{})
	if _, err := r.Invoke(context.Background(), "capture", false, protocol.Invocation{}); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal("wrapper lowered permission", err)
	}
	handle, err := r.Invoke(context.Background(), "capture", false, protocol.Invocation{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := handle.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := r.runs.List("", "", "", 10)
	if err != nil || len(runs.Runs) != 1 || runs.Runs[0].JobID != handle.ID {
		t.Fatal("wrapper bypassed run store", err)
	}
}

func TestSourceChangesRemainVisible(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, output)
		}
	}
	git("init", "-q")
	tracked := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.txt")
	git("-c", "user.name=Patchbay test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "fixture")
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(fixture+captureFixture), 0600); err != nil {
		t.Fatal(err)
	}
	runner := callbackRunner{run: func(context.Context, provider.Command, *provider.Budget) (action.Result, error) {
		err := os.WriteFile(tracked, []byte("after"), 0600)
		return action.Result{Status: action.Success, Data: map[string]any{"stdout": "done"}}, err
	}}
	r, err := New(path, Options{Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(context.Background()) }()
	response, err := r.Capture(context.Background(), prepareTestCapture(t, r, "compare"))
	if err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, response)
	if run.State != "success" || run.SourceStart.Status != "observed" || run.SourceEnd.Status != "observed" || !run.SourceChanged {
		t.Fatalf("source start=%#v end=%#v changed=%t", run.SourceStart, run.SourceEnd, run.SourceChanged)
	}
}
