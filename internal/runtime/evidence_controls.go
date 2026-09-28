package runtime

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"patchbay/internal/binding"
	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/permission"
	"patchbay/pkg/protocol"
)

// These helpers are called under the runtime lock. A baseline targets the exact
// run that the client displayed, even if a newer capture completes meanwhile.
func (r *Runtime) evidenceControlView(target *binding.Target) protocol.ControlTarget {
	id := target.Result
	if id == "" {
		id = target.Baseline
	}
	view := protocol.ControlTarget{Action: "result." + id, Result: &protocol.ControlResult{Experiment: id, State: "unavailable"}}
	if target.Baseline != "" {
		view.Action = "baseline." + id
	}
	e, ok := r.cfg.Experiments[id]
	if !ok || r.runs == nil || len(e.Projects) > 0 && !slices.Contains(e.Projects, r.context.Project) {
		return view
	}
	page, err := r.runs.List(r.context.Project, id, "", 1)
	if err != nil || len(page.Runs) == 0 {
		return view
	}
	run, err := r.runs.Get(page.Runs[0].ID)
	if err != nil {
		return view
	}
	raw, _ := json.Marshal(e)
	view.Result.RunID, view.Result.State = run.ID, run.State
	view.Result.Baseline = r.runs.Baseline(r.context.Project, id).RunID == run.ID
	if len(run.Measurements) > 0 {
		value := run.Measurements[0]
		view.Result.Measurement = &value
	}
	view.Enabled = target.Result != "" || run.State == "success" && run.ExperimentDigest == evidence.Digest(raw) && !r.runs.Status().ReadOnly
	return view
}

func (r *Runtime) evidenceControl(target *binding.Target, payload controlPayload) (string, error) {
	if payload.Guard == nil || payload.RunID == "" || r.runs == nil {
		return "", fault.New(protocol.InvalidRequest, "Refresh the control and select a saved run first.")
	}
	id := target.Result
	if id == "" {
		id = target.Baseline
	}
	run, err := r.runs.Get(payload.RunID)
	if err != nil {
		return "", err
	}
	e, ok := r.cfg.Experiments[id]
	if !ok || run.Project != r.context.Project || run.Experiment.ID != id || len(e.Projects) > 0 && !slices.Contains(e.Projects, r.context.Project) {
		return "", fault.New(protocol.InvalidRequest, "Selected run does not match this control.")
	}
	if target.Baseline != "" {
		previous := r.runs.Baseline(r.context.Project, id)
		if _, err := r.setBaseline(r.context.Project, id, protocol.BaselineUpdate{RunID: run.ID, Revision: previous.Revision}); err != nil {
			return "", err
		}
	}
	return run.ID, nil
}

func (r *Runtime) captureControl(ctx context.Context, request protocol.EventRequest, payload controlPayload, name string, definition config.Action, approved *protocol.CapturePreview) (protocol.CaptureResponse, error) {
	if approved != nil {
		return r.capture(ctx, protocol.CaptureRequest{Preparation: approved.ID, Digest: approved.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true}, nil, context.Background())
	}
	definition.Safety = permission.Strongest(definition.Safety, permission.Confirm)
	preview, err := r.prepareCapture(ctx, protocol.CapturePrepare{Experiment: definition.Experiment}, &definition)
	if err != nil {
		return protocol.CaptureResponse{}, err
	}
	challenge := r.challenge(request.Source, payload, request.Type, name)
	known, ok := challenge.(*protocol.Error)
	if !ok || known.Confirmation == nil {
		delete(r.captures, preview.ID)
		return protocol.CaptureResponse{}, challenge
	}
	ticket := r.confirmations[known.Confirmation.Token]
	ticket.capture = &preview
	r.confirmations[known.Confirmation.Token] = ticket
	known.Confirmation.Capture = &preview
	return protocol.CaptureResponse{}, known
}
