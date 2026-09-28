package runtime

import (
	"context"
	"encoding/json"
	"time"

	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/job"
	"patchbay/internal/patching"
	"patchbay/internal/permission"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
)

type agentProposalPreparation struct {
	patch   *patching.Plan
	preview supervisor.ProposalPreview
	capture capturePreparation
	session supervisor.Session
	bytes   int
}

func (r *Runtime) generatedProposals(ctx context.Context, s supervisor.Session, out supervisor.Output) []supervisor.Proposal {
	r.mu.Lock()
	defer r.mu.Unlock()
	proposals := []supervisor.Proposal{}
	sourceErr := r.agentSourceCurrent(ctx, s)
	for _, suggestion := range out.Proposals {
		p := supervisor.Proposal{ID: identity.New(), Suggestion: suggestion, State: "pending", Digest: supervisor.Hash(suggestion), ExpiresAt: time.Now().Add(10 * time.Minute).UTC()}
		_, _, err := r.validateSuggestion(suggestion)
		if err == nil && suggestion.Kind == "patch" {
			_, err = r.patchPlan(ctx, suggestion)
		}
		if sourceErr != nil {
			err = sourceErr
		}
		if err != nil {
			p.State = "invalidated"
			p.Message = fault.Safe(err).Message
		}
		proposals = append(proposals, p)
	}
	return proposals
}
func (r *Runtime) findProposal(id string) (supervisor.Session, int, error) {
	if r.sessions == nil {
		return supervisor.Session{}, 0, fault.New(protocol.RecordingFailed, "Agent store unavailable.")
	}
	return r.sessions.FindProposal(id)
}
func (r *Runtime) proposalReady(ctx context.Context, s supervisor.Session, index int) error {
	p := s.Proposals[index]
	if s.State == "executing" {
		return fault.New(protocol.Busy, "This session already has an admitted proposal.")
	}
	if s.State != "awaiting_review" || p.State != "pending" {
		return fault.New(protocol.InvalidProposal, "This proposal is not pending review.")
	}
	if !time.Now().Before(p.ExpiresAt) {
		return fault.New(protocol.StalePreparation, "Proposal expired. Explicit duplication requires fresh review.")
	}
	if err := r.agentSourceCurrent(ctx, s); err != nil {
		return err
	}
	_, _, err := r.validateSuggestion(p.Suggestion)
	return err
}
func (r *Runtime) PrepareAgentProposal(ctx context.Context, id string) (supervisor.ProposalPreview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, index, err := r.findProposal(id)
	if err != nil {
		return supervisor.ProposalPreview{}, err
	}
	if err := r.proposalReady(ctx, s, index); err != nil {
		return supervisor.ProposalPreview{}, err
	}
	used := 0
	for key, p := range r.proposalPreviews {
		if !time.Now().Before(p.preview.ExpiresAt) {
			delete(r.proposalPreviews, key)
		} else {
			used += p.bytes
		}
	}
	if len(r.proposalPreviews) >= 64 {
		return supervisor.ProposalPreview{}, fault.New(protocol.Busy, "Too many live proposal previews.")
	}
	p := s.Proposals[index]
	target, values, err := r.validateSuggestion(p.Suggestion)
	if err != nil {
		return supervisor.ProposalPreview{}, err
	}
	if target.Kind == "patch" {
		return r.prepareAgentPatch(ctx, s, index, target, used)
	}
	var experiment protocol.Experiment
	var args, parameters map[string]any
	if target.Kind == "experiment" {
		experiment = r.cfg.Experiments[target.ID]
		parameters = values
	} else {
		experiment = protocol.Experiment{SchemaVersion: 1, ID: "agent." + target.Kind + "." + evidence.Digest([]byte(target.ID))[:20], Title: "Agent " + target.Kind + " · " + target.ID, Projects: []string{s.Project}, Collectors: []protocol.Collector{}}
		if target.Kind == "action" {
			experiment.Action = target.ID
			args = values
		} else {
			experiment.Workflow = target.ID
		}
	}
	capture, err := r.prepareExperiment(ctx, experiment, &config.Action{Safety: permission.Confirm}, args, parameters)
	if err != nil {
		return supervisor.ProposalPreview{}, err
	}
	prepared := r.captures[capture.ID]
	delete(r.captures, capture.ID) // proposal plans never enter the generic confirmed capture route
	if prepared.plan.risk == permission.Dangerous {
		return supervisor.ProposalPreview{}, fault.New(protocol.PermissionDenied, "Dangerous targets are excluded from agent approval.")
	}
	capture.Steps = r.maskedAgentSteps(prepared.plan)
	capture.Context.Values = nil
	prepared.preview = capture
	preview := supervisor.ProposalPreview{ID: identity.New(), Session: s.ID, Proposal: id, ExpiresAt: time.Now().Add(time.Minute).UTC(), Target: target, Capture: capture, Context: evidence.Clone(s.Items), Rationale: p.Suggestion.Rationale, Expected: p.Suggestion.Expected}
	preview.Digest = r.sessions.Sign(preview)
	if used+prepared.bytes > 16<<20 {
		return supervisor.ProposalPreview{}, fault.New(protocol.Busy, "Proposal preview memory budget reached.")
	}
	r.proposalPreviews[preview.ID] = agentProposalPreparation{preview: preview, capture: prepared, session: s, bytes: prepared.bytes}
	return evidence.Clone(preview), nil
}
func approvalDigest(id string, request supervisor.Approve) string {
	return supervisor.Hash(struct {
		Proposal string
		Request  supervisor.Approve
	}{id, request})
}
func (r *Runtime) ApproveAgentProposal(ctx context.Context, id string, request supervisor.Approve) (supervisor.Admission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.approveAgentProposal(ctx, id, request)
}
func (r *Runtime) approveAgentProposal(ctx context.Context, id string, request supervisor.Approve) (supervisor.Admission, error) {
	if r.runs == nil {
		return supervisor.Admission{}, fault.New(protocol.RecordingFailed, "Run evidence unavailable; no proposal can execute.")
	}
	digest := approvalDigest(id, request)
	if response, known, err := r.runs.Lookup(request.RequestID, digest); known {
		if err != nil {
			return supervisor.Admission{}, err
		}
		run, err := r.runs.Get(response.RunID)
		if err != nil {
			return supervisor.Admission{}, err
		}
		if run.Agent == nil || run.Agent.Proposal != id {
			return supervisor.Admission{}, fault.New(protocol.RequestConflict, "Request belongs to another operation.")
		}
		return supervisor.Admission{Session: run.Agent.Session, Proposal: id, RunID: run.ID, JobID: run.JobID}, nil
	}
	if err := r.agentReady(ctx); err != nil {
		return supervisor.Admission{}, err
	}
	prep, ok := r.proposalPreviews[request.Preparation]
	if !ok || prep.preview.Proposal != id || prep.preview.Digest != request.Digest || !time.Now().Before(prep.preview.ExpiresAt) {
		return supervisor.Admission{}, fault.New(protocol.StalePreparation, "Approval expired or does not match the reviewed proposal.")
	}
	if !request.Confirmed {
		return supervisor.Admission{}, fault.New(protocol.ConfirmationRequired, "Review the complete effective action and explicitly approve it.")
	}
	s, index, err := r.findProposal(id)
	if err != nil {
		return supervisor.Admission{}, err
	}
	if err := r.proposalReady(ctx, s, index); err != nil {
		return supervisor.Admission{}, err
	}
	if s.Proposals[index].Digest != prep.session.Proposals[index].Digest {
		return supervisor.Admission{}, fault.New(protocol.StalePreparation, "Suggestion changed after review.")
	}
	if prep.patch != nil {
		if err := patching.Validate(ctx, *prep.patch); err != nil {
			return supervisor.Admission{}, fault.New(protocol.ContextChanged, err.Error())
		}
	}
	store := r.sessions
	hooks := &captureHooks{After: func() { r.recheckPendingProposals(s.ID, index) }, Digest: digest, Agent: &protocol.AgentOrigin{Session: s.ID, Proposal: id}, Before: func(response protocol.CaptureResponse) error {
		if prep.patch != nil {
			// Evidence quota and durable private intent must both succeed before session admission.
			data, _ := json.Marshal(prep.patch)
			if _, err := r.runs.AddArtifact(response.RunID, "Approved patch and private preimages", "application/json", data); err != nil {
				return err
			}
			if err := r.patches.Begin(id, response.RunID, s.Project, *prep.patch); err != nil {
				return fault.New(protocol.RecordingFailed, err.Error())
			}
		}
		_, err := store.Change(s.ID, func(current *supervisor.Session) error {
			if current.State != "awaiting_review" || current.Proposals[index].State != "pending" {
				return fault.New(protocol.RequestConflict, "Proposal decision changed.")
			}
			p := &current.Proposals[index]
			p.State = "admitted"
			p.JobID = response.JobID
			p.RunID = response.RunID
			current.State = "executing"
			current.JobID = response.JobID
			return nil
		})
		if err != nil {
			store.RecordingFailure(s.ID)
			if prep.patch != nil {
				r.patches.FinalizePending(id, "Approval audit failed; no file was changed.")
			}
		}
		return err
	}, Completed: func(finished job.Job, runID string) {
		if prep.patch != nil {
			r.patches.FinalizePending(id, "Job finished before patch execution.")
		}
		_, err := store.Change(s.ID, func(current *supervisor.Session) error {
			p := &current.Proposals[index]
			p.State = string(finished.State)
			if saved, getErr := r.runs.Get(runID); getErr == nil && saved.State == "recording_failed" {
				p.State = "recording_failed"
				p.Message = "Run evidence could not be saved; inspect run storage."
			}
			p.RunID = runID
			p.JobID = finished.ID
			if finished.Error != nil {
				p.Message = finished.Error.Message
			}
			if current.State != "cancelled" {
				current.State = "completed"
				for i := range current.Proposals {
					other := &current.Proposals[i]
					if other.State == "pending" {
						if time.Now().Before(other.ExpiresAt) {
							current.State = "awaiting_review"
						} else {
							other.State = "expired"
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			store.RecordingFailure(s.ID)
		}
	}}
	cap := prep.capture
	r.captures[cap.preview.ID] = cap
	defer delete(r.captures, cap.preview.ID)
	response, err := r.captureWith(ctx, protocol.CaptureRequest{Preparation: cap.preview.ID, Digest: cap.preview.Digest, RequestID: request.RequestID, Confirmed: true}, nil, context.Background(), hooks)
	if err != nil {
		return supervisor.Admission{}, err
	}
	delete(r.proposalPreviews, request.Preparation)
	return supervisor.Admission{Session: s.ID, Proposal: id, RunID: response.RunID, JobID: response.JobID}, nil
}
func (r *Runtime) RejectAgentProposal(ctx context.Context, id string) (supervisor.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rejectAgentProposal(ctx, id)
}
func (r *Runtime) rejectAgentProposal(ctx context.Context, id string) (supervisor.Session, error) {
	if err := r.writable(ctx); err != nil {
		return supervisor.Session{}, err
	}
	s, index, err := r.findProposal(id)
	if err != nil {
		return s, err
	}
	return r.sessions.Change(s.ID, func(current *supervisor.Session) error {
		p := &current.Proposals[index]
		if p.State == "rejected" {
			return nil
		}
		if p.State != "pending" {
			return fault.New(protocol.RequestConflict, "Only a pending proposal can be rejected.")
		}
		p.State = "rejected"
		if current.State != "executing" {
			current.State = "completed"
			for _, p := range current.Proposals {
				if p.State == "pending" {
					current.State = "awaiting_review"
				}
			}
		}
		return nil
	})
}
func (r *Runtime) DuplicateAgentProposal(ctx context.Context, id, requestID string) (supervisor.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, index, err := r.findProposal(id)
	if err != nil {
		return s, err
	}
	digest := supervisor.Hash([]string{"duplicate", id, requestID})
	if known, ok, err := r.sessions.Lookup(requestID, digest); ok {
		return known, err
	}
	if err := r.agentSourceCurrent(ctx, s); err != nil {
		return supervisor.Session{}, err
	}
	old := s.Proposals[index]
	if time.Now().Before(old.ExpiresAt) || (old.State != "pending" && old.State != "expired") {
		return supervisor.Session{}, fault.New(protocol.InvalidProposal, "Only an expired suggestion can be explicitly duplicated.")
	}
	if _, _, err := r.validateSuggestion(old.Suggestion); err != nil {
		return supervisor.Session{}, err
	}
	old.ID = identity.New()
	old.State = "pending"
	old.ExpiresAt = time.Now().Add(10 * time.Minute).UTC()
	old.JobID = ""
	old.RunID = ""
	old.Message = ""
	s.Parent = s.ID
	s.ID = identity.New()
	s.RequestID = requestID
	s.RequestDigest = digest
	s.CreatedAt = time.Now().UTC()
	s.State = "awaiting_review"
	s.JobID = ""
	s.GenerationJobID = ""
	s.Error = nil
	s.Proposals = []supervisor.Proposal{old}
	return r.sessions.Create(s)
}

func (r *Runtime) recheckPendingProposals(sessionID string, admitted int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, err := r.sessions.Get(sessionID)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sourceErr := r.agentSourceCurrent(ctx, s)
	messages := map[int]string{}
	for i, p := range s.Proposals {
		if i == admitted || p.State != "pending" {
			continue
		}
		_, _, err := r.validateSuggestion(p.Suggestion)
		if err == nil && p.Suggestion.Kind == "patch" {
			_, err = r.patchPlan(ctx, p.Suggestion)
		}
		if sourceErr != nil {
			err = sourceErr
		}
		if err != nil {
			messages[i] = fault.Safe(err).Message
		}
	}
	if len(messages) == 0 {
		return
	}
	if _, err := r.sessions.Change(sessionID, func(current *supervisor.Session) error {
		for i, message := range messages {
			if current.Proposals[i].State == "pending" {
				current.Proposals[i].State = "invalidated"
				current.Proposals[i].Message = message
			}
		}
		return nil
	}); err != nil {
		r.sessions.RecordingFailure(sessionID)
	}
}

// Run reservations are authoritative admission receipts even if a crash occurred
// before the separate session audit rename. Recovery links them without dispatch.
func (r *Runtime) reconcileAgentRuns() {
	if r.sessions == nil || r.runs == nil {
		return
	}
	if err := r.sessions.ReconcileRuns(r.runs.AgentReservations()); err != nil {
		r.sessionError = err
		r.sessions.RecordingFailure("")
	}
}
