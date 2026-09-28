package runtime

import (
	"context"
	"patchbay/internal/binding"
	"patchbay/internal/config"
	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
	"testing"
	"time"
)

func TestAgentPhysicalReviewSelectionAndLease(t *testing.T) {
	ctx := context.Background()
	r, a := proposalRuntime(t, []supervisor.Suggestion{{Kind: "action", Target: "echo"}, {Kind: "action", Target: "echo"}}, &fakeRunner{}, supervisor.Options{})
	changeProposalConfig(t, r, func(c *config.Config) {
		for _, op := range []string{"approve", "review", "reject", "select_proposal", "select_job", "cancel"} {
			c.Bindings = append(c.Bindings, binding.Binding{Control: op, Press: &binding.Target{Agent: op}})
		}
	})
	grantProposal(t, r, "action", "echo", nil)
	s := generateProposals(t, r)
	revision := r.AgentSelection().Revision
	sel, err := r.SelectAgent(ctx, protocol.AgentSelect{Revision: revision, Proposal: s.Proposals[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SelectAgent(ctx, protocol.AgentSelect{Revision: revision, Proposal: s.Proposals[1].ID}); fault.Safe(err).Code != protocol.RequestConflict {
		t.Fatal(err)
	}
	send := func(control, token string, revision uint64) (protocol.EventResponse, error) {
		return r.Control(ctx, controlRequest(t, event.ControlPressed, controlPayload{Control: control, Guard: &protocol.ControlGuard{Instance: r.instance, Revision: r.controlRevision}, AgentRevision: revision, Confirmation: token}))
	}
	if _, err = send("approve", "", sel.Revision); fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal(err)
	}
	p, err := r.PrepareAgentProposal(ctx, s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	sel, err = r.LinkAgentReview(ctx, protocol.AgentReviewLink{Revision: sel.Revision, Preparation: p.ID, Digest: p.Digest})
	if err != nil {
		t.Fatal(err)
	}
	_, err = send("approve", "", sel.Revision)
	challenge := fault.Safe(err).Confirmation
	if challenge == nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.agentReviewUntil = time.Now().Add(-time.Second)
	r.mu.Unlock()
	if _, err = send("approve", challenge.Token, sel.Revision); fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal(err)
	}
	if _, err = r.LinkAgentReview(ctx, protocol.AgentReviewLink{Revision: sel.Revision, Preparation: p.ID, Digest: p.Digest, Renew: true}); err == nil {
		t.Fatal("lease resurrected")
	}
	sel, err = r.LinkAgentReview(ctx, protocol.AgentReviewLink{Revision: sel.Revision, Preparation: p.ID, Digest: p.Digest})
	if err != nil {
		t.Fatal(err)
	}
	_, err = send("approve", "", sel.Revision)
	challenge = fault.Safe(err).Confirmation
	if challenge == nil {
		t.Fatal(err)
	}
	previous := sel.Revision
	sel, err = r.SelectAgent(ctx, protocol.AgentSelect{Revision: sel.Revision, Proposal: s.Proposals[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = send("approve", challenge.Token, previous); err == nil {
		t.Fatal("stale selection executed")
	}
	if _, err = send("select_proposal", "", sel.Revision); err != nil {
		t.Fatal(err)
	}
	sel = r.AgentSelection()
	if sel.Proposal == "" {
		t.Fatal(sel)
	}
	p, err = r.PrepareAgentProposal(ctx, sel.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	sel, err = r.LinkAgentReview(ctx, protocol.AgentReviewLink{Revision: sel.Revision, Preparation: p.ID, Digest: p.Digest})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotControls(t, r, protocol.ControlRef{Control: "approve"})
	if !snapshot.Controls[0].Targets[event.ControlPressed].Enabled {
		t.Fatal(snapshot)
	}
	yes := true
	if _, err = r.Control(ctx, controlRequest(t, event.ControlPressed, controlPayload{Control: "approve", Confirmed: &yes, AgentRevision: sel.Revision})); err == nil {
		t.Fatal("unguarded approval")
	}
	_, err = send("approve", "", sel.Revision)
	challenge = fault.Safe(err).Confirmation
	if challenge == nil {
		t.Fatal(err)
	}
	result, err := send("approve", challenge.Token, sel.Revision)
	if err != nil || result.RunID == "" {
		t.Fatal(result, err)
	}
	if _, err = send("approve", challenge.Token, sel.Revision); err == nil {
		t.Fatal("replay")
	}
	if a.count() != 1 {
		t.Fatal("review generated another provider request")
	}
}

func TestAgentSelectionCancelCannotFollowNewJob(t *testing.T) {
	r, _ := proposalRuntime(t, []supervisor.Suggestion{{Kind: "action", Target: "echo"}}, &fakeRunner{}, supervisor.Options{})
	changeProposalConfig(t, r, func(c *config.Config) {
		c.Bindings = append(c.Bindings, binding.Binding{Control: "cancel", Press: &binding.Target{Agent: "cancel"}})
	})
	grantProposal(t, r, "action", "echo", nil)
	s := generateProposals(t, r)
	_, err := r.sessions.Change(s.ID, func(s *supervisor.Session) error { s.State = "executing"; s.JobID = "original"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	sel, err := r.SelectAgent(context.Background(), protocol.AgentSelect{Revision: r.AgentSelection().Revision, Session: s.ID, Job: "original"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.sessions.Change(s.ID, func(s *supervisor.Session) error { s.JobID = "replacement"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	guard := r.controlGuard()
	_, err = r.Control(context.Background(), controlRequest(t, event.ControlPressed, controlPayload{Control: "cancel", Guard: &guard, AgentRevision: sel.Revision}))
	if fault.Safe(err).Code != protocol.RequestConflict {
		t.Fatal(err)
	}
	current, _ := r.AgentSession(s.ID)
	if current.State != "executing" {
		t.Fatal("cancel followed replacement", current)
	}
}
