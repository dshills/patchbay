package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const patchDiff = "--- a/code.txt\n+++ b/code.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n"

func patchRuntime(t *testing.T) (*Runtime, *sessionAgent, *fakeRunner, string) {
	t.Helper()
	runner := &fakeRunner{}
	r, a := proposalRuntime(t, []supervisor.Suggestion{{Kind: "patch", Target: "workspace.apply_patch", Diff: patchDiff}}, runner, supervisor.Options{})
	root := r.cfg.Projects["p"].Path
	for name, data := range map[string]string{"code.txt": "old\n", ".gitignore": "*\n!code.txt\n!.gitignore\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0640); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "code.txt", ".gitignore"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	changeProposalConfig(t, r, func(c *config.Config) {
		c.Agents.Proposals.Patches = true
		p := c.Projects["p"]
		p.AgentPatchPaths = []string{"code.txt"}
		c.Projects["p"] = p
	})
	grantProposal(t, r, "patch", "workspace.apply_patch", nil)
	return r, a, runner, root
}
func TestAgentPatchApprovalDedupAndSeparateRestoration(t *testing.T) {
	ctx := context.Background()
	r, a, runner, root := patchRuntime(t)
	s := generateProposals(t, r)
	if s.State != "awaiting_review" {
		t.Fatal(s)
	}
	full, err := r.PrepareAgentProposal(ctx, s.Proposals[0].ID)
	if err != nil || full.Patch == nil || full.Patch.Diff != patchDiff {
		t.Fatal(full, err)
	}
	approval := proposalApproval(full)
	var wg sync.WaitGroup
	answers := make(chan supervisor.Admission, 8)
	for range 8 {
		wg.Go(func() {
			admitted, e := r.ApproveAgentProposal(ctx, full.Proposal, approval)
			if e != nil {
				t.Error(e)
			}
			answers <- admitted
		})
	}
	wg.Wait()
	close(answers)
	var admitted supervisor.Admission
	for answer := range answers {
		if admitted.RunID != "" && answer != admitted {
			t.Fatal(answers)
		}
		admitted = answer
	}
	run := waitCapture(t, r, protocol.CaptureResponse{RunID: admitted.RunID, JobID: admitted.JobID})
	if run.State != "success" || len(run.Outcomes) != 1 || run.Outcomes[0].Patch == nil || run.Outcomes[0].Patch.Files[0].State != "applied" {
		t.Fatal(run)
	}
	data, _ := os.ReadFile(filepath.Join(root, "code.txt"))
	if string(data) != "new\n" || a.count() != 1 || runner.count() != 0 {
		t.Fatal("patch triggered unrelated provider/action", string(data), a.count(), runner.count())
	}
	restored, err := r.RestorePatch(ctx, full.Proposal, evidence.NewRequestID(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(root, "code.txt"))
	if string(data) != "new\n" {
		t.Fatal("restore executed without review")
	}
	preview, err := r.PrepareAgentProposal(ctx, restored.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := r.ApproveAgentProposal(ctx, preview.Proposal, proposalApproval(preview))
	if err != nil {
		t.Fatal(err)
	}
	run = waitCapture(t, r, protocol.CaptureResponse{RunID: next.RunID, JobID: next.JobID})
	if run.State != "success" {
		t.Fatal(run)
	}
	data, _ = os.ReadFile(filepath.Join(root, "code.txt"))
	if string(data) != "old\n" || a.count() != 1 {
		t.Fatal("restoration wrong or generated again")
	}
	history, err := r.PatchHistory(ctx)
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	if err = r.ForgetPatch(ctx, full.Proposal); err != nil {
		t.Fatal(err)
	}
	// The ordinary durable run identity survives forgetting the restoration record.
	if got, err := r.ApproveAgentProposal(ctx, full.Proposal, approval); err != nil || got != admitted {
		t.Fatal(got, err)
	}
}
func TestAgentPatchStalePreviewAuditFailureAndOptIn(t *testing.T) {
	for _, kind := range []string{"preimage", "head", "journal", "disabled", "protected"} {
		t.Run(kind, func(t *testing.T) {
			r, _, _, root := patchRuntime(t)
			s := generateProposals(t, r)
			full, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "preimage":
				err = os.WriteFile(filepath.Join(root, "code.txt"), []byte("external\n"), 0640)
			case "head":
				err = exec.Command("git", "-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "changed").Run()
			case "journal":
				r.patches.Fault = func(string) error { return errors.New("disk full") }
			case "disabled":
				changeProposalConfig(t, r, func(c *config.Config) { c.Agents.Proposals.Patches = false })
			case "protected":
				changeProposalConfig(t, r, func(c *config.Config) { c.Agents.Proposals.ProtectedPaths = []string{"code.txt"} })
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.ApproveAgentProposal(context.Background(), full.Proposal, proposalApproval(full))
			if err == nil {
				t.Fatal("invalidated patch admitted")
			}
			data, _ := os.ReadFile(filepath.Join(root, "code.txt"))
			if string(data) == "new\n" {
				t.Fatal("effect before valid audit")
			}
			if kind == "journal" && fault.Safe(err).Code != protocol.RecordingFailed {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentPatchLocalDiffStillRequiresExactApprovalAndGrant(t *testing.T) {
	r, a, runner, root := patchRuntime(t)
	ctx := context.Background()
	request := evidence.NewRequestID(time.Now())
	s, err := r.ProposePatch(ctx, patchDiff, request)
	if err != nil {
		t.Fatal(err)
	}
	same, err := r.ProposePatch(ctx, patchDiff, request)
	if err != nil || s.ID != same.ID {
		t.Fatal(same, err)
	}
	if _, err = r.ProposePatch(ctx, patchDiff+"extra", request); fault.Safe(err).Code != protocol.RequestConflict {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "code.txt"))
	if string(data) != "old\n" || a.count() != 0 || runner.count() != 0 {
		t.Fatal("local suggestion executed")
	}
	p, err := r.PrepareAgentProposal(ctx, s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	approval := proposalApproval(p)
	approval.Confirmed = false
	if _, err = r.ApproveAgentProposal(ctx, p.Proposal, approval); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal(err)
	}
}

func TestAgentPatchDaemonRestartReconcilesEffectsWithoutReplay(t *testing.T) {
	if path := os.Getenv("PATCHBAY_AGENT_PATCH_CRASH_CONFIG"); path != "" {
		output := `{"schema_version":1,"summary":"fixture","context_refs":[],"proposals":[]}`
		agent := &sessionAgent{output: output}
		r, err := New(path, Options{Agent: agent, PatchFault: func(phase string) error {
			if phase == "replaced" {
				os.Exit(73)
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		review, err := r.InspectAgentGrant(context.Background(), "patch", "workspace.apply_patch")
		if err != nil {
			t.Fatal(err)
		}
		changeProposalConfig(t, r, func(c *config.Config) {
			p := c.Projects["p"]
			p.AgentGrants = []config.AgentGrant{{Kind: "patch", Target: "workspace.apply_patch", Digest: review.Target.Digest}}
			c.Projects["p"] = p
		})
		s, err := r.ProposePatch(context.Background(), patchDiff, evidence.NewRequestID(time.Now()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		admitted, err := r.ApproveAgentProposal(context.Background(), p.Proposal, proposalApproval(p))
		if err != nil {
			t.Fatal(err)
		}
		_ = waitCapture(t, r, protocol.CaptureResponse{RunID: admitted.RunID, JobID: admitted.JobID})
		t.Fatal("missing crash")
	}
	r, _, _, root := patchRuntime(t)
	path := r.path
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAgentPatchDaemonRestartReconcilesEffectsWithoutReplay$")
	child.Env = append(os.Environ(), "PATCHBAY_AGENT_PATCH_CRASH_CONFIG="+path)
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatal(string(output), err)
	}
	agent := &sessionAgent{}
	restarted, err := New(path, Options{Agent: agent})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close(context.Background()) }()
	history, err := restarted.PatchHistory(context.Background())
	if err != nil || len(history) != 1 || history[0].State != "interrupted" || history[0].Files[0].State != "applied" {
		t.Fatal(history, err)
	}
	run, err := restarted.runs.Get(history[0].Run)
	if err != nil || run.State != "interrupted" || run.Agent == nil || run.Agent.Proposal != history[0].Operation {
		t.Fatal(run, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "code.txt"))
	if string(data) != "new\n" || agent.count() != 0 {
		t.Fatal("restart replayed or reverted")
	}
}

func TestAgentPatchConcurrentPreparationHoldsRuntimeLock(t *testing.T) {
	r, _, _, _ := patchRuntime(t)
	s := generateProposals(t, r)
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for range 12 {
		wg.Go(func() {
			p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
			if err != nil {
				t.Error(err)
				return
			}
			if p.Patch == nil || p.Patch.Diff != patchDiff {
				t.Error("incomplete preview")
			}
			ids <- p.ID
		})
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("reused preparation")
		}
		seen[id] = true
	}
	if len(seen) != 12 {
		t.Fatal(len(seen))
	}
}

func TestAgentPatchPollingDoesNotBlockCancellation(t *testing.T) {
	r, _, _, root := patchRuntime(t)
	s := generateProposals(t, r)
	preview, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	ready, release := make(chan struct{}), make(chan struct{})
	r.patches.Fault = func(phase string) error {
		if phase == "before_replace" {
			close(ready)
			<-release
		}
		return nil
	}
	admitted, err := r.ApproveAgentProposal(context.Background(), preview.Proposal, proposalApproval(preview))
	if err != nil {
		t.Fatal(err)
	}
	<-ready
	polled := make(chan error, 1)
	go func() { _, err := r.PatchHistory(context.Background()); polled <- err }()
	select {
	case err = <-polled:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("history blocked during write")
	}
	cancelled := make(chan error, 1)
	go func() { _, err := r.CancelAgent(context.Background(), s.ID); cancelled <- err }()
	select {
	case err = <-cancelled:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("cancellation blocked during write")
	}
	close(release)
	run := waitCapture(t, r, protocol.CaptureResponse{RunID: admitted.RunID, JobID: admitted.JobID})
	if run.State != "cancelled" {
		t.Fatal(run)
	}
	data, _ := os.ReadFile(filepath.Join(root, "code.txt"))
	if string(data) != "old\n" {
		t.Fatal("write continued after cancellation")
	}
}
