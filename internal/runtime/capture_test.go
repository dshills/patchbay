package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
)

func prepareTestCapture(t *testing.T, r *Runtime, name string) protocol.CaptureRequest {
	t.Helper()
	p, err := r.PrepareCapture(context.Background(), protocol.CapturePrepare{Experiment: name})
	if err != nil {
		t.Fatal(err)
	}
	return protocol.CaptureRequest{Preparation: p.ID, Digest: p.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true}
}
func waitCapture(t *testing.T, r *Runtime, response protocol.CaptureResponse) protocol.Run {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := r.Jobs().Wait(ctx, response.JobID); err != nil {
		t.Fatal(err)
	}
	run, err := r.runs.Get(response.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}
func TestCaptureConcurrentRetryAndImmutableInputs(t *testing.T) {
	runner := &captureRunner{}
	r, _ := setup(t, fixture+captureFixture, runner)
	request := prepareTestCapture(t, r, "compare")
	var wg sync.WaitGroup
	responses := make(chan protocol.CaptureResponse, 12)
	failures := make(chan error, 12)
	for range 12 {
		wg.Go(func() { value, err := r.Capture(context.Background(), request); responses <- value; failures <- err })
	}
	wg.Wait()
	close(responses)
	close(failures)
	var response protocol.CaptureResponse
	for v := range responses {
		if response.RunID != "" && v.RunID != response.RunID {
			t.Fatal("duplicate execution")
		}
		response = v
	}
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	run := waitCapture(t, r, response)
	if run.State != "success" || len(run.Outcomes) != 2 || len(run.Artifacts) != 1 {
		t.Fatalf("capture state=%s outcomes=%d artifacts=%d error=%v", run.State, len(run.Outcomes), len(run.Artifacts), run.Error)
	}
	runner.mu.Lock()
	calls := len(runner.commands)
	runner.mu.Unlock()
	if calls != 2 {
		t.Fatalf("executed %d leaves", calls)
	}
	request.Confirmed = false
	_, err := r.Capture(context.Background(), request)
	if fault.Safe(err).Code != protocol.RequestConflict {
		t.Fatal("changed retry accepted")
	}
	if _, err := r.SetParameter(context.Background(), "label", "new"); err != nil {
		t.Fatal(err)
	}
	if run.Parameters["label"] != "initial" {
		t.Fatal("saved inputs changed")
	}
	if err := r.runs.Delete(run.ID, false); err != nil {
		t.Fatal(err)
	}
	request.Confirmed = true
	again, err := r.Capture(context.Background(), request)
	if err != nil || !again.Deleted || again.RunID != run.ID {
		t.Fatal("deleted retry admitted")
	}
}
func TestStaleCaptureDoesNotDispatch(t *testing.T) {
	runner := &fakeRunner{}
	r, _ := setup(t, fixture+captureFixture, runner)
	request := prepareTestCapture(t, r, "compare")
	if _, err := r.SetParameter(context.Background(), "label", "other"); err != nil {
		t.Fatal(err)
	}
	_, err := r.Capture(context.Background(), request)
	if fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal(err)
	}
	if len(r.Jobs().List()) != 0 {
		t.Fatal("stale capture queued")
	}
	request = prepareTestCapture(t, r, "compare")
	r.mu.Lock()
	expired := r.captures[request.Preparation]
	expired.preview.ExpiresAt = time.Now().Add(-time.Second)
	r.captures[request.Preparation] = expired
	r.mu.Unlock()
	_, err = r.Capture(context.Background(), request)
	if fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal(err)
	}
}
func TestQueuedCaptureCancellationIsDurable(t *testing.T) {
	runner := &fakeRunner{started: make(chan provider.Command, 1), release: make(chan struct{})}
	r, _ := setup(t, fixture+captureFixture, runner)
	blocker, err := r.Invoke(context.Background(), "slow", false, protocol.Invocation{})
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started
	response, err := r.Capture(context.Background(), prepareTestCapture(t, r, "compare"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Jobs().Cancel(response.JobID); err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, response)
	if run.State != "cancelled" || len(run.Outcomes) != 2 || run.Outcomes[0].State != "skipped" {
		t.Fatalf("queued cancellation %#v", run)
	}
	close(runner.release)
	if _, err := blocker.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type resultRunner struct{ result action.Result }

func (f resultRunner) Run(context.Context, provider.Command, *provider.Budget) (action.Result, error) {
	return f.result, nil
}
func TestCollectionRequiredOptionalAndTruncation(t *testing.T) {
	base := strings.Replace(fixture+captureFixture, "kind: text, source: native, path: [stdout]", "kind: measurement, source: json_stdout, path: [duration], unit: ms, quantity: duration", 1)
	for _, tc := range []struct {
		name, stdout                 string
		truncated, optional, success bool
	}{
		{"valid", `{"schema_version":1,"data":{"duration":{"value":2,"unit":"ms","quantity":"duration","repeats":3,"spread":0.5}}}`, false, false, true},
		{"zero", `{"schema_version":1,"data":{"duration":0}}`, false, false, true},
		{"duplicate", `{"schema_version":1,"schema_version":1,"data":{"duration":2}}`, false, false, false},
		{"unknown", `{"schema_version":2,"data":{"duration":2}}`, false, false, false},
		{"wrong-unit", `{"schema_version":1,"data":{"duration":{"value":2,"unit":"s","quantity":"duration"}}}`, false, false, false},
		{"truncated", `{"schema_version":1,"data":{"duration":2}}`, true, false, false},
		{"optional", "not JSON", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := base
			if tc.optional {
				text = strings.Replace(text, "unit: ms, quantity: duration", "unit: ms, quantity: duration, optional: true", 1)
			}
			r, _ := setup(t, text, resultRunner{action.Result{Status: action.Success, Data: map[string]any{"stdout": tc.stdout, "truncated": tc.truncated}}})
			response, err := r.Capture(context.Background(), prepareTestCapture(t, r, "compare"))
			if err != nil {
				t.Fatal(err)
			}
			run := waitCapture(t, r, response)
			if (run.State == "success") != tc.success {
				data, _ := json.Marshal(run)
				t.Fatalf("outcome: %s", data)
			}
			if run.Outcomes[0].State != "success" {
				t.Fatal("collector rewrote subprocess outcome")
			}
			if len(run.Measurements) != 1 {
				t.Fatal("missing collector status")
			}
		})
	}
}

type captureRunner struct{ fakeRunner }

func (f *captureRunner) Run(ctx context.Context, command provider.Command, budget *provider.Budget) (action.Result, error) {
	result, err := f.fakeRunner.Run(ctx, command, budget)
	if result.Data == nil {
		result.Data = map[string]any{}
	}
	result.Data["stdout"] = strings.Join(command.Args, " ")
	return result, err
}

func TestIndependentCapturesOwnCollectors(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	runner := callbackRunner{run: func(ctx context.Context, c provider.Command, _ *provider.Budget) (action.Result, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return action.Result{}, ctx.Err()
		}
		return action.Result{Status: action.Success, Data: map[string]any{"stdout": c.Args[0]}}, nil
	}}
	r, _ := setup(t, strings.Replace(fixture, "concurrency: 1", "concurrency: 2", 1)+captureFixture, runner)
	first, err := r.Capture(context.Background(), prepareTestCapture(t, r, "compare"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetParameter(context.Background(), "label", "second"); err != nil {
		t.Fatal(err)
	}
	second, err := r.Capture(context.Background(), prepareTestCapture(t, r, "compare"))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("captures did not run concurrently")
		}
	}
	close(release)
	for _, tc := range []struct {
		response protocol.CaptureResponse
		want     string
	}{{first, "initial"}, {second, "second"}} {
		run := waitCapture(t, r, tc.response)
		if run.State != "success" || len(run.Artifacts) != 1 {
			t.Fatal("independent capture lost evidence")
		}
		data, _, err := r.runs.Artifact(run.ID, run.Artifacts[0].ID)
		if err != nil || string(data) != tc.want {
			t.Fatalf("mixed capture data %s %v", data, err)
		}
	}
}
