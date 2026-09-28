package runtime

import (
	"context"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/patching"
	"patchbay/internal/permission"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
	"time"
)

func (r *Runtime) patchPlan(ctx context.Context, suggestion supervisor.Suggestion) (patching.Plan, error) {
	project := r.cfg.Projects[r.context.Project]
	plan, err := patching.Prepare(ctx, project.Path, suggestion.Diff, project.AgentPatchPaths, func(name string) bool { return r.protectedContextPath(project.Path, name) })
	if err != nil {
		return plan, fault.New(protocol.InvalidProposal, err.Error())
	}
	return plan, nil
}
func (r *Runtime) prepareAgentPatch(ctx context.Context, s supervisor.Session, index int, target supervisor.Target, used int) (supervisor.ProposalPreview, error) {
	suggestion := s.Proposals[index]
	plan, err := r.patchPlan(ctx, suggestion.Suggestion)
	if err != nil {
		return supervisor.ProposalPreview{}, err
	}
	size := 0
	for _, f := range plan.Files {
		size += len(f.BeforeText) + len(f.AfterText)
	}
	size += len(plan.Preview.Diff)
	if size+used > 16<<20 {
		return supervisor.ProposalPreview{}, fault.New(protocol.Busy, "Patch previews exceed the private memory budget.")
	}
	capture := protocol.CapturePreview{ID: identity.New(), Experiment: "agent.workspace.apply_patch", ExperimentDigest: r.sessions.Sign(target), Instance: r.instance, Generation: r.generation, Revision: r.controlRevision, Context: protocol.Context{Project: s.Project}, Parameters: map[string]any{}, Steps: []protocol.PreparedStep{{Index: 0, Action: "workspace.apply_patch", Type: "patch", Safety: "confirm", Directory: plan.Root}}, Safety: "confirm", ExpiresAt: time.Now().Add(time.Minute).UTC()}
	capture.Digest = r.sessions.Sign(plan)
	preview := supervisor.ProposalPreview{ID: identity.New(), Session: s.ID, Proposal: suggestion.ID, ExpiresAt: capture.ExpiresAt, Target: target, Capture: capture, Patch: &plan.Preview, Context: evidence.Clone(s.Items), Rationale: suggestion.Suggestion.Rationale, Expected: suggestion.Suggestion.Expected}
	preview.Digest = r.sessions.Sign(preview)
	prepared := capturePreparation{preview: capture, experiment: protocol.Experiment{SchemaVersion: 1, ID: capture.Experiment, Title: "Apply reviewed workspace patch", Projects: []string{s.Project}, Collectors: []protocol.Collector{}}, bytes: size, plan: &prepared{kind: "patch", name: "workspace.apply_patch", risk: permission.Confirm, timeout: time.Minute, patchOperation: suggestion.ID}}
	r.proposalPreviews[preview.ID] = agentProposalPreparation{preview: preview, capture: prepared, session: s, patch: &plan, bytes: size}
	return evidence.Clone(preview), nil
}
func (r *Runtime) PatchHistory(ctx context.Context) ([]protocol.PatchOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writable(ctx); err != nil {
		return nil, err
	}
	if r.patches == nil {
		return nil, fault.New(protocol.RecordingFailed, "Patch journal unavailable.")
	}
	return r.patches.List(r.context.Project), nil
}
func (r *Runtime) ForgetPatch(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writable(ctx); err != nil {
		return err
	}
	if r.patches == nil {
		return fault.New(protocol.RecordingFailed, "Patch journal unavailable.")
	}
	record, err := r.patches.Get(id)
	if err != nil || (record.Project != r.context.Project || record.Plan.Root != r.cfg.Projects[r.context.Project].Path) {
		return fault.New(protocol.NotFound, "Patch not found in this project.")
	}
	if err = r.patches.Forget(id); err != nil {
		return fault.New(protocol.RequestConflict, err.Error())
	}
	return nil
}

// A restoration is a new suggestion/session. It never applies or calls a provider.
func (r *Runtime) RestorePatch(ctx context.Context, id, requestID string) (supervisor.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.agentReady(ctx); err != nil {
		return supervisor.Session{}, err
	}
	digest := supervisor.Hash([]string{"restore_patch", id, requestID})
	if known, ok, err := r.sessions.Lookup(requestID, digest); ok {
		return known, err
	}
	if r.patches == nil {
		return supervisor.Session{}, fault.New(protocol.RecordingFailed, "Patch journal unavailable.")
	}
	record, err := r.patches.Get(id)
	if err != nil || (record.Project != r.context.Project || record.Plan.Root != r.cfg.Projects[r.context.Project].Path) {
		return supervisor.Session{}, fault.New(protocol.NotFound, "Patch not found in this project.")
	}
	diff, err := r.patches.Restoration(ctx, id)
	if err != nil {
		return supervisor.Session{}, fault.New(protocol.ContextChanged, err.Error())
	}
	return r.localPatchSession(ctx, requestID, digest, diff, "Explicit restoration requested from retained preimages.")
}

// ProposePatch imports untrusted diff text into a new review session without generation.
func (r *Runtime) ProposePatch(ctx context.Context, diff, requestID string) (supervisor.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.agentReady(ctx); err != nil {
		return supervisor.Session{}, err
	}
	digest := supervisor.Hash([]string{"local_patch", diff, requestID})
	if known, ok, err := r.sessions.Lookup(requestID, digest); ok {
		return known, err
	}
	return r.localPatchSession(ctx, requestID, digest, diff, "User supplied diff; no model request or file write has occurred.")
}
func (r *Runtime) localPatchSession(ctx context.Context, requestID, digest, diff, rationale string) (supervisor.Session, error) {
	suggestion := supervisor.Suggestion{Kind: "patch", Target: "workspace.apply_patch", Diff: diff, Rationale: rationale, Expected: "Apply only the exact reviewed bytes; validation remains separate."}
	if _, _, err := r.validateSuggestion(suggestion); err != nil {
		return supervisor.Session{}, err
	}
	if _, err := r.patchPlan(ctx, suggestion); err != nil {
		return supervisor.Session{}, err
	}
	source := observeSource(ctx, r.cfg.Projects[r.context.Project].Path)
	session := supervisor.Session{ID: identity.New(), Project: r.context.Project, State: "awaiting_review", CreatedAt: time.Now().UTC(), RequestID: requestID, RequestDigest: digest, Instance: r.instance, Generation: r.generation, Revision: r.controlRevision, Preconditions: r.recipeInputDigest(), Model: "local-patch-review", Destination: "local://patchbay/patch-review", Items: []supervisor.Item{}, Source: source, Proposals: []supervisor.Proposal{{ID: identity.New(), Suggestion: suggestion, State: "pending", Digest: supervisor.Hash(suggestion), ExpiresAt: time.Now().Add(10 * time.Minute).UTC()}}}
	return r.sessions.Create(session)
}
