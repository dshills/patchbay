package runtime

import (
	"context"
	"errors"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
	"time"
)

func (r *Runtime) clearAgentSelection() {
	r.agentSelection = protocol.AgentSelection{Revision: r.agentSelection.Revision + 1, Project: r.context.Project}
	r.agentReview = ""
	r.agentReviewUntil = time.Time{}
}

// agentSelectionView requires r.mu, like the other private control helpers.
func (r *Runtime) agentSelectionView() protocol.AgentSelection {
	value := r.agentSelection
	value.Project = r.context.Project
	value.ReviewActive = false
	if value.Session != "" && r.sessions != nil {
		if s, err := r.sessions.Get(value.Session); err == nil {
			value.State = s.State
			for _, p := range s.Proposals {
				if p.ID == value.Proposal {
					value.State = p.State
				}
			}
		}
	}
	if p, ok := r.proposalPreviews[r.agentReview]; ok {
		value.ReviewActive = value.Proposal == p.preview.Proposal && value.State == "pending" && time.Now().Before(p.preview.ExpiresAt) && time.Now().Before(r.agentReviewUntil)
	}
	return value
}
func (r *Runtime) AgentSelection() protocol.AgentSelection {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.agentSelectionView()
}
func (r *Runtime) SelectAgent(ctx context.Context, request protocol.AgentSelect) (protocol.AgentSelection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.selectAgent(ctx, request)
}
func (r *Runtime) selectAgent(ctx context.Context, request protocol.AgentSelect) (protocol.AgentSelection, error) {
	if err := r.writable(ctx); err != nil {
		return protocol.AgentSelection{}, err
	}
	if request.Revision != r.agentSelection.Revision {
		return protocol.AgentSelection{}, fault.New(protocol.RequestConflict, "Selection changed; inspect it before choosing a target.")
	}
	var s supervisor.Session
	var err error
	if request.Proposal != "" && request.Job == "" && request.Session == "" {
		s, _, err = r.findProposal(request.Proposal)
	} else if request.Job != "" && request.Session != "" && request.Proposal == "" && r.sessions != nil {
		s, err = r.sessions.Get(request.Session)
		if err == nil && (s.JobID != request.Job || (s.State != "generating" && s.State != "executing")) {
			err = fault.New(protocol.RequestConflict, "Selected job is no longer active in this session.")
		}
	} else {
		err = fault.New(protocol.InvalidRequest, "Select exactly one proposal or an exact active session/job pair.")
	}
	if err != nil {
		return protocol.AgentSelection{}, err
	}
	if s.Project != r.context.Project {
		return protocol.AgentSelection{}, fault.New(protocol.PermissionDenied, "Selection belongs to another project.")
	}
	r.clearAgentSelection()
	r.agentSelection.Session = s.ID
	r.agentSelection.Proposal = request.Proposal
	r.agentSelection.Job = request.Job
	return r.agentSelectionView(), nil
}

// Linking renews only the displayed review lease. It never prepares or executes work.
func (r *Runtime) LinkAgentReview(ctx context.Context, request protocol.AgentReviewLink) (protocol.AgentSelection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.proposalPreviews[request.Preparation]
	if request.Revision != r.agentSelection.Revision || !ok || p.preview.Proposal != r.agentSelection.Proposal || p.preview.Digest != request.Digest || !time.Now().Before(p.preview.ExpiresAt) {
		return protocol.AgentSelection{}, fault.New(protocol.StalePreparation, "The selected full review changed or expired.")
	}
	if request.Renew && (r.agentReview != request.Preparation || !time.Now().Before(r.agentReviewUntil)) {
		return protocol.AgentSelection{}, fault.New(protocol.StalePreparation, "Full review disconnected. Explicitly reopen it to pair the deck.")
	}
	if !request.Renew {
		s, i, err := r.findProposal(p.preview.Proposal)
		if err != nil {
			return protocol.AgentSelection{}, err
		}
		if err = r.proposalReady(ctx, s, i); err != nil {
			return protocol.AgentSelection{}, err
		}
		r.agentSelection.Revision++
	}
	r.agentReview = request.Preparation
	r.agentReviewUntil = time.Now().Add(5 * time.Second)
	return r.agentSelectionView(), nil
}
func (r *Runtime) agentControlView(operation string) protocol.ControlTarget {
	selection := r.agentSelectionView()
	enabled := r.cfg.Agents.Proposals.Enabled && r.sessions != nil
	switch operation {
	case "approve":
		enabled = enabled && selection.ReviewActive
	case "review", "reject":
		enabled = enabled && selection.Proposal != "" && selection.State == "pending"
	case "cancel":
		enabled = enabled && selection.Job != "" && (selection.State == "generating" || selection.State == "executing")
	}
	return protocol.ControlTarget{Agent: &selection, Action: "agent." + operation, Safety: "confirm", Enabled: enabled}
}
func (r *Runtime) agentControl(ctx context.Context, event protocol.EventRequest, payload controlPayload, operation string, approved uint64) (supervisor.Admission, error) {
	if payload.Guard == nil || payload.Confirmed != nil || payload.AgentRevision != r.agentSelection.Revision {
		return supervisor.Admission{}, fault.New(protocol.RequestConflict, "Agent input requires the exact displayed selection and control guard.")
	}
	if r.sessions == nil || !r.cfg.Agents.Proposals.Enabled {
		return supervisor.Admission{}, fault.New(protocol.PermissionDenied, "Agent supervision is disabled.")
	}
	selection := r.agentSelectionView()
	switch operation {
	case "select_proposal", "select_job":
		choices := r.sessions.SelectionChoices(r.context.Project, operation)
		if len(choices) == 0 {
			return supervisor.Admission{}, fault.New(protocol.NotFound, "No matching proposals or jobs.")
		}
		next := 0
		for i, c := range choices {
			if c.Proposal != "" && c.Proposal == selection.Proposal || c.Job != "" && c.Job == selection.Job {
				next = (i + 1) % len(choices)
				break
			}
		}
		choice := choices[next]
		choice.Revision = selection.Revision
		_, err := r.selectAgent(ctx, choice)
		return supervisor.Admission{}, err
	case "review":
		if selection.Proposal == "" {
			return supervisor.Admission{}, fault.New(protocol.InvalidRequest, "Select a proposal first.")
		}
		r.agentReview = ""
		r.agentSelection.Revision++
		r.agentSelection.ReviewRequested++
		return supervisor.Admission{}, nil
	case "reject":
		if selection.Proposal == "" {
			return supervisor.Admission{}, fault.New(protocol.InvalidRequest, "Select a proposal first.")
		}
		_, err := r.rejectAgentProposal(ctx, selection.Proposal)
		if err == nil {
			r.clearAgentSelection()
		}
		return supervisor.Admission{}, err
	case "cancel":
		s, err := r.sessions.Get(selection.Session)
		if err != nil {
			return supervisor.Admission{}, err
		}
		if selection.Job == "" || s.JobID != selection.Job || (s.State != "generating" && s.State != "executing") {
			return supervisor.Admission{}, fault.New(protocol.RequestConflict, "Selected job changed; select the exact active job again.")
		}
		_, err = r.cancelAgent(ctx, s.ID)
		if err == nil {
			r.clearAgentSelection()
		}
		return supervisor.Admission{}, err
	case "approve":
		if !selection.ReviewActive {
			return supervisor.Admission{}, fault.New(protocol.StalePreparation, "Open the complete review in the workbench before physical approval.")
		}
		if approved == 0 {
			err := r.challenge(event.Source, payload, event.Type, "agent.approve")
			var known *protocol.Error
			if errors.As(err, &known) && known.Confirmation != nil {
				ticket := r.confirmations[known.Confirmation.Token]
				ticket.agentRevision = selection.Revision
				r.confirmations[known.Confirmation.Token] = ticket
			}
			return supervisor.Admission{}, err
		}
		if approved != selection.Revision {
			return supervisor.Admission{}, fault.New(protocol.StalePreparation, "Reviewed selection changed after the hold challenge.")
		}
		p := r.proposalPreviews[r.agentReview].preview
		result, err := r.approveAgentProposal(ctx, selection.Proposal, supervisor.Approve{Preparation: p.ID, Digest: p.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true})
		if err == nil {
			r.clearAgentSelection()
		}
		return result, err
	}
	return supervisor.Admission{}, fault.New(protocol.InvalidRequest, "Unsupported agent control.")
}
