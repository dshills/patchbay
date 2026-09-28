package runtime

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"os"
	"strings"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/job"
	"patchbay/internal/permission"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
)

func (r *Runtime) Capture(ctx context.Context, request protocol.CaptureRequest) (protocol.CaptureResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.capture(ctx, request, nil, context.Background())
}
func (r *Runtime) capture(ctx context.Context, request protocol.CaptureRequest, admitted **job.Handle, parent context.Context) (protocol.CaptureResponse, error) {
	if err := r.writable(ctx); err != nil {
		return protocol.CaptureResponse{}, err
	}
	if r.runs == nil {
		return protocol.CaptureResponse{}, fault.New(protocol.RecordingFailed, "Run storage is unavailable.")
	}
	content, _ := json.Marshal(request)
	digest := evidence.Digest(content)
	if response, known, err := r.runs.Lookup(request.RequestID, digest); known {
		return response, err
	}
	prep, ok := r.captures[request.Preparation]
	if !ok || !hmac.Equal([]byte(prep.preview.Digest), []byte(request.Digest)) || !time.Now().Before(prep.preview.ExpiresAt) || prep.preview.Generation != r.generation || prep.preview.Revision != r.controlRevision {
		return protocol.CaptureResponse{}, fault.New(protocol.StalePreparation, "Preparation expired or inputs changed. Prepare and review again.")
	}
	allow := r.cfg.Security.AllowDangerousActions
	if err := permission.Check(prep.plan.risk, allow, request.Confirmed); err != nil {
		return protocol.CaptureResponse{}, err
	}
	run := protocol.Run{Experiment: prep.experiment, ExperimentDigest: prep.preview.ExperimentDigest, PlanDigest: prep.preview.Digest, Project: prep.preview.Context.Project, Context: protocol.Context{Project: prep.preview.Context.Project, Mode: prep.preview.Context.Mode}, Instance: r.instance, Generation: r.generation, RequestID: request.RequestID, RequestDigest: digest, Parameters: prep.preview.Parameters, Steps: prep.preview.Steps}
	for prefix, origin := range r.recipeComposition.Origins {
		if strings.HasPrefix(prep.experiment.ID, prefix) {
			copy := origin
			run.Recipe = &copy
		}
	}
	var response protocol.CaptureResponse
	root := r.cfg.Projects[r.context.Project].Path
	budget := provider.NewBudget(r.cfg.Jobs.OutputLimitBytes)
	handle, err := r.jobs.SubmitDurable(parent, "experiment."+prep.experiment.ID, r.generation, prep.plan.timeout, func(jobCtx context.Context, jobID string) (action.Result, error) {
		local, err := r.runs.Get(response.RunID)
		if err != nil {
			return action.Result{}, err
		}
		now := time.Now().UTC()
		local.State, local.StartedAt = "running", &now
		if err := r.runs.Update(local); err != nil {
			return action.Result{}, err
		}
		local.SourceStart = observeSource(jobCtx, root)
		if err := r.runs.Update(local); err != nil {
			return action.Result{}, err
		}
		execution, cancel := context.WithCancel(jobCtx)
		defer cancel()
		collected := captureCollector(r.runs, &local, prep.plan, prep.experiment, cancel)
		result, executeErr := r.executeObserved(execution, jobID, prep.plan, budget, allow, request.Confirmed, collected)
		local.SourceEnd = observeSource(jobCtx, root)
		local.SourceChanged = local.SourceStart.Status != local.SourceEnd.Status || local.SourceStart.Commit != local.SourceEnd.Commit || local.SourceStart.Dirty != local.SourceEnd.Dirty || local.SourceStart.StatusDigest != local.SourceEnd.StatusDigest
		// Collection may have appended immutable artifacts. Refresh only the inventory.
		if current, getErr := r.runs.Get(local.ID); getErr == nil {
			local.Artifacts = current.Artifacts
		}
		if err := r.runs.Update(local); err != nil {
			r.runs.RecordingFailure(local.ID, local)
		}
		return result, executeErr
	}, job.Lifecycle{Before: func(jobID string) error {
		run.JobID = jobID
		value, duplicate, err := r.runs.Reserve(run)
		if duplicate {
			return fault.New(protocol.RequestConflict, "Capture already admitted.")
		}
		response = value
		return err
	}, Completed: func(finished job.Job) {
		saved, err := r.runs.Get(response.RunID)
		if err != nil {
			return
		}
		if saved.State == "recording_failed" {
			return
		}
		saved.State = string(finished.State)
		saved.StartedAt = finished.StartedAt
		saved.FinishedAt = finished.FinishedAt
		saved.Error = finished.Error
		seen := map[int]bool{}
		for _, o := range saved.Outcomes {
			seen[o.Index] = true
		}
		for _, step := range saved.Steps {
			if !seen[step.Index] {
				saved.Outcomes = append(saved.Outcomes, protocol.StepOutcome{Index: step.Index, Action: step.Action, State: "skipped"})
			}
		}
		if err := r.runs.Update(saved); err != nil {
			r.runs.RecordingFailure(saved.ID, saved)
		}
	}})
	if err != nil {
		return protocol.CaptureResponse{}, err
	}
	if admitted != nil {
		*admitted = handle
	}
	delete(r.captures, request.Preparation)
	return response, nil
}

// Git observations are bounded and read-only. Missing Git, a detached/non-repo
// path, cancellation, and oversized status remain explicit unavailable states.
func observeSource(ctx context.Context, root string) *protocol.SourceObservation {
	observation := &protocol.SourceObservation{Status: "unavailable", ObservedAt: time.Now().UTC()}
	if root == "" {
		return observation
	}
	child, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	env := append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat")
	path, err := provider.ResolveExecutable("git", root, env)
	if err != nil {
		return observation
	}
	budget := provider.NewBudget(64 << 10)
	read := func(args ...string) (string, bool) {
		result, err := (provider.ProcessRunner{}).Run(child, provider.Command{Path: path, Dir: root, Env: env, Args: args}, budget)
		if err != nil || result.Data["truncated"] == true {
			return "", false
		}
		text, ok := result.Data["stdout"].(string)
		return text, ok
	}
	commit, ok := read("-c", "core.fsmonitor=false", "rev-parse", "--verify", "HEAD")
	if !ok {
		return observation
	}
	status, ok := read("-c", "core.fsmonitor=false", "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if !ok {
		return observation
	}
	observation.Status = "observed"
	observation.Commit = strings.TrimSpace(commit)
	observation.Dirty = status != ""
	observation.StatusDigest = evidence.Digest([]byte(status))
	observation.ObservedAt = time.Now().UTC()
	return observation
}
