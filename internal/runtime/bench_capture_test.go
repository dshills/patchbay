package runtime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"patchbay/internal/event"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/scpitest"
	"patchbay/pkg/protocol"
)

func TestBenchCaptureDurableObservationsAndSuspectTrace(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "changed"}[changed], func(t *testing.T) {
			var states atomic.Int32
			scope := scpitest.New(t, func(c string) string {
				if c == ":TRIG:STAT?" && states.Add(1) == 3 && changed {
					return "RUN\n"
				}
				return scpitest.Scope(c)
			})
			g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.1}
			generator := scpitest.New(t, g.Handle)
			raw, err := os.ReadFile("../../configs/bench.yaml")
			if err != nil {
				t.Fatal(err)
			}
			source := strings.ReplaceAll(string(raw), "~/.deckd/", "private/")
			source = strings.Replace(source, "127.0.0.1:5501", generator.Address, 1)
			source = strings.Replace(source, "127.0.0.1:5502", scope.Address, 1)
			r, _ := setup(t, source, &fakeRunner{})
			response, err := r.Capture(context.Background(), prepareTestCapture(t, r, "bench.trace"))
			if err != nil {
				t.Fatal(err)
			}
			run := waitCapture(t, r, response)
			want := "success"
			if changed {
				want = "failed"
			}
			if run.State != want {
				t.Fatal(run.State, run.Error)
			}
			if len(run.Outcomes) != 3 || len(run.Artifacts) != 1 || len(run.Parameters) != 2 {
				t.Fatal("missing bench evidence", run)
			}
			for _, outcome := range run.Outcomes {
				if outcome.Instrument == nil || outcome.Instrument.Model == "" || outcome.Instrument.Firmware == "" {
					t.Fatal("lost instrument identity")
				}
			}
			if run.Outcomes[2].State != "success" {
				t.Fatal("after inspection skipped")
			}
			data, _, err := r.runs.Artifact(run.ID, run.Artifacts[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			var series protocol.Series
			if err = json.Unmarshal(data, &series); err != nil {
				t.Fatal(err)
			}
			if (series.Quality == "suspect") != changed {
				t.Fatal("wrong quality", series.Quality)
			}
			_, err = r.SetBaseline("", "bench.trace", protocol.BaselineUpdate{RunID: run.ID})
			if (err != nil) != changed {
				t.Fatal("baseline admitted suspect result", err)
			}
			if changed && evidence.CompareSeries(series, series).Overlay {
				t.Fatal("suspect overlay")
			}
			for _, c := range generator.Commands() {
				if !strings.Contains(c, "?") {
					t.Fatal("capture changed generator", c)
				}
			}
		})
	}
}

func TestPhysicalCapturePinsPreparationAndBaselineSelection(t *testing.T) {
	source := strings.Replace(fixture, "actions:\n  echo:", "actions:\n  capture: {type: experiment, experiment: compare, safety: safe}\n  echo:", 1)
	source += "  - control: capture\n    press: {action: capture}\n  - control: baseline\n    press: {baseline: compare}\n  - control: result\n    press: {result: compare}\n" + captureFixture
	r, _ := setup(t, source, &captureRunner{})
	ctx := context.Background()
	snapshot := snapshotControls(t, r, protocol.ControlRef{Control: "capture"})
	payload := protocol.ControlPayload{Control: "capture", Guard: &snapshot.Guard}
	_, err := r.Control(ctx, controlRequest(t, event.ControlPressed, payload))
	known := fault.Safe(err)
	if known.Code != protocol.ConfirmationRequired || known.Confirmation == nil || known.Confirmation.Capture == nil {
		t.Fatal("missing pinned capture challenge", err)
	}
	preview := known.Confirmation.Capture
	payload.Confirmation = known.Confirmation.Token
	response, err := r.Control(ctx, controlRequest(t, event.ControlPressed, payload))
	if err != nil {
		t.Fatal(err)
	}
	first := waitCapture(t, r, protocol.CaptureResponse{RunID: response.RunID, JobID: response.JobID})
	if first.PlanDigest != preview.Digest {
		t.Fatal("approved capture was prepared again")
	}
	_, err = r.Control(ctx, controlRequest(t, event.ControlPressed, payload))
	wantCode(t, err, protocol.InvalidRequest)
	snapshot = snapshotControls(t, r, protocol.ControlRef{Control: "baseline"}, protocol.ControlRef{Control: "result"})
	displayed := snapshot.Controls[0].Targets[event.ControlPressed].Result.RunID
	next, err := r.Capture(ctx, prepareTestCapture(t, r, "compare"))
	if err != nil {
		t.Fatal(err)
	}
	_ = waitCapture(t, r, next)
	_, err = r.Control(ctx, controlRequest(t, event.ControlPressed, protocol.ControlPayload{Control: "baseline", Guard: &snapshot.Guard, RunID: displayed}))
	if err != nil {
		t.Fatal(err)
	}
	if r.runs.Baseline("p", "compare").RunID != first.ID {
		t.Fatal("baseline silently selected newer run")
	}
	snapshot = snapshotControls(t, r, protocol.ControlRef{Control: "capture"})
	_, err = r.Control(ctx, controlRequest(t, event.ControlPressed, protocol.ControlPayload{Control: "capture", Guard: &snapshot.Guard}))
	known = fault.Safe(err)
	if known.Confirmation == nil {
		t.Fatal(err)
	}
	_, err = r.SetParameter(ctx, "label", "changed")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Control(ctx, controlRequest(t, event.ControlPressed, protocol.ControlPayload{Control: "capture", Guard: &snapshot.Guard, Confirmation: known.Confirmation.Token}))
	wantCode(t, err, protocol.StalePreparation)
}
