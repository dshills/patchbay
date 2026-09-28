package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"patchbay/internal/action"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/provider"
	"patchbay/internal/supervisor"
	"patchbay/internal/workflow"
	"patchbay/pkg/protocol"
)

func proposalRuntime(t *testing.T, suggestions []supervisor.Suggestion, runner provider.Runner, options supervisor.Options) (*Runtime, *sessionAgent) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	raw := fixture + "\nagents: {proposals: {enabled: true}, codex: {model: test-model}}\n"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	output, _ := json.Marshal(supervisor.Output{SchemaVersion: 1, Summary: "Untrusted model rationale", ContextRefs: []string{}, Proposals: suggestions})
	a := &sessionAgent{output: string(output)}
	r, err := New(path, Options{Agent: a, Runner: runner, Supervisor: options})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		r.mu.Lock()
		closed := r.closed
		r.mu.Unlock()
		if !closed {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = r.Close(ctx)
		}
	})
	return r, a
}
func changeProposalConfig(t *testing.T, r *Runtime, change func(*config.Config)) {
	t.Helper()
	c, err := config.Load(r.path)
	if err != nil {
		t.Fatal(err)
	}
	change(c)
	raw, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(r.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func grantProposal(t *testing.T, r *Runtime, kind, target string, inputs map[string]config.Input) {
	t.Helper()
	review, err := r.InspectAgentGrant(context.Background(), kind, target)
	if err != nil {
		t.Fatal(err)
	}
	changeProposalConfig(t, r, func(c *config.Config) {
		p := c.Projects["p"]
		p.AgentGrants = append(p.AgentGrants, config.AgentGrant{Kind: kind, Target: target, Digest: review.Target.Digest, Inputs: inputs})
		c.Projects["p"] = p
	})
	if got := r.AgentCatalog(context.Background()); len(got.Targets) == 0 {
		t.Fatal("grant did not survive config reload", got)
	}
}
func generateProposals(t *testing.T, r *Runtime) supervisor.Session {
	t.Helper()
	p, err := r.PrepareAgentContext(context.Background(), contextRequest())
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.StartAgent(context.Background(), startRequest(p))
	if err != nil {
		t.Fatal(err)
	}
	return waitSession(t, r, s.ID)
}
func proposalApproval(p supervisor.ProposalPreview) supervisor.Approve {
	return supervisor.Approve{Preparation: p.ID, Digest: p.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true}
}
func TestAgentExactProposalApprovalAndRestartDedup(t *testing.T) {
	runner := &fakeRunner{}
	r, a := proposalRuntime(t, []supervisor.Suggestion{{Kind: "action", Target: "echo", Inputs: map[string]any{"text": "approved"}, Rationale: "Expected only", Expected: "Output"}}, runner, supervisor.Options{})
	grantProposal(t, r, "action", "echo", map[string]config.Input{"text": {Type: "string"}})
	s := generateProposals(t, r)
	if s.State != "awaiting_review" || len(s.Proposals) != 1 || a.count() != 1 {
		t.Fatal(s)
	}
	p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Capture.Safety != "confirm" || p.Capture.Steps[0].Arguments[0] != "approved" {
		t.Fatal(p)
	}
	if _, err := r.Capture(context.Background(), protocol.CaptureRequest{Preparation: p.Capture.ID, Digest: p.Capture.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: true}); fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal("generic capture could execute agent plan", err)
	}
	request := proposalApproval(p)
	no := request
	no.Confirmed = false
	if _, err := r.ApproveAgentProposal(context.Background(), p.Proposal, no); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	responses := make(chan supervisor.Admission, 8)
	for range 8 {
		wg.Go(func() {
			response, err := r.ApproveAgentProposal(context.Background(), p.Proposal, request)
			if err != nil {
				t.Error(err)
			}
			responses <- response
		})
	}
	wg.Wait()
	close(responses)
	var admission supervisor.Admission
	for v := range responses {
		if admission.RunID != "" && admission.RunID != v.RunID {
			t.Fatal("duplicate admitted")
		}
		admission = v
	}
	run := waitCapture(t, r, protocol.CaptureResponse{RunID: admission.RunID, JobID: admission.JobID})
	if run.State != "success" || len(run.Outcomes) != 1 || run.Agent == nil || run.Agent.Proposal != p.Proposal {
		t.Fatal(run)
	}
	runner.mu.Lock()
	count := len(runner.commands)
	runner.mu.Unlock()
	if count != 1 {
		t.Fatal(count)
	}
	if _, err := r.ApproveAgentProposal(context.Background(), p.Proposal, no); fault.Safe(err).Code != protocol.RequestConflict {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := New(r.path, Options{Runner: runner, Agent: a})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close(ctx) }()
	duplicate, err := second.ApproveAgentProposal(context.Background(), p.Proposal, request)
	if err != nil || duplicate != admission || a.count() != 1 {
		t.Fatal(duplicate, err)
	}
	if catalog := second.AgentCatalog(context.Background()); len(catalog.Targets) != 1 {
		t.Fatal("stable grant lost on restart", catalog)
	}
}
func TestAgentForbiddenTargetsAndTypedBounds(t *testing.T) {
	cases := []supervisor.Suggestion{{Kind: "action", Target: "link"}, {Kind: "action", Target: "danger"}, {Kind: "action", Target: "branch"}, {Kind: "action", Target: "nonexistent"}, {Kind: "action", Target: "echo", Inputs: map[string]any{"command": "/bin/sh"}}, {Kind: "action", Target: "echo", Inputs: map[string]any{"text": true}}}
	for i, suggestion := range cases {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			r, _ := proposalRuntime(t, []supervisor.Suggestion{suggestion}, &fakeRunner{}, supervisor.Options{})
			grantProposal(t, r, "action", "echo", map[string]config.Input{"text": {Type: "string"}})
			s := generateProposals(t, r)
			if s.State != "failed" || s.Proposals[0].State != "invalidated" {
				t.Fatal(s)
			}
			if _, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID); err == nil {
				t.Fatal("invalid suggestion prepared")
			}
		})
	}
	r, _ := proposalRuntime(t, []supervisor.Suggestion{}, &fakeRunner{}, supervisor.Options{})
	changeProposalConfig(t, r, func(c *config.Config) {
		c.Workflows["forbidden"] = config.Workflow{Steps: []workflow.WorkflowStep{{Action: "link", Args: map[string]any{"url": "https://invalid.example"}}}}
		a := c.Actions["echo"]
		a.Type = "workflow"
		a.Command = ""
		a.Args = nil
		a.Inputs = nil
		a.Cwd = ""
		a.Environment = nil
		a.Workflow = "forbidden"
		c.Actions["wrapper"] = a
	})
	if _, err := r.InspectAgentGrant(context.Background(), "action", "wrapper"); err == nil {
		t.Fatal("nested protected provider granted")
	}
}
func TestAgentStalePolicyInputExpiryAndReject(t *testing.T) {
	for _, mutation := range []string{"parameter", "context", "config", "expiry", "reject"} {
		t.Run(mutation, func(t *testing.T) {
			r, _ := proposalRuntime(t, []supervisor.Suggestion{{Kind: "action", Target: "echo"}}, &fakeRunner{}, supervisor.Options{})
			grantProposal(t, r, "action", "echo", nil)
			s := generateProposals(t, r)
			p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "parameter":
				_, err = r.SetParameter(context.Background(), "count", 2)
			case "context":
				mode := "changed"
				_, err = r.PatchContext(context.Background(), protocol.ContextPatch{Mode: &mode})
			case "config":
				raw, _ := os.ReadFile(r.path)
				err = os.WriteFile(r.path, []byte(strings.Replace(string(raw), "command: /bin/echo", "command: /bin/true", 1)), 0600)
			case "expiry":
				r.mu.Lock()
				prep := r.proposalPreviews[p.ID]
				prep.preview.ExpiresAt = time.Now().Add(-time.Second)
				r.proposalPreviews[p.ID] = prep
				r.mu.Unlock()
			case "reject":
				_, err = r.RejectAgentProposal(context.Background(), p.Proposal)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.ApproveAgentProposal(context.Background(), p.Proposal, proposalApproval(p)); err == nil {
				t.Fatal("stale/rejected approval succeeded")
			}
		})
	}
}
func TestAgentApprovalAuditFailureNeverExecutes(t *testing.T) {
	var armed atomic.Bool
	runner := &fakeRunner{}
	r, _ := proposalRuntime(t, []supervisor.Suggestion{{Kind: "action", Target: "echo"}}, runner, supervisor.Options{Fault: func(op string) error {
		if armed.Load() && op == "write" {
			return errors.New("audit disk")
		}
		return nil
	}})
	grantProposal(t, r, "action", "echo", nil)
	s := generateProposals(t, r)
	p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	request := proposalApproval(p)
	if _, err := r.ApproveAgentProposal(context.Background(), p.Proposal, request); fault.Safe(err).Code != protocol.RecordingFailed {
		t.Fatal(err)
	}
	runner.mu.Lock()
	count := len(runner.commands)
	runner.mu.Unlock()
	if count != 0 {
		t.Fatal("executed without audit")
	}
	known, err := r.ApproveAgentProposal(context.Background(), p.Proposal, request)
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.runs.Get(known.RunID)
	if err != nil || run.State != "recording_failed" {
		t.Fatal(run, err)
	}
}
func TestAgentApprovalQueueFullPreservesTokenAndSessionSequence(t *testing.T) {
	runner := &fakeRunner{started: make(chan provider.Command, 10), release: make(chan struct{})}
	r, _ := proposalRuntime(t, []supervisor.Suggestion{{Kind: "action", Target: "echo"}, {Kind: "action", Target: "echo"}}, runner, supervisor.Options{})
	changeProposalConfig(t, r, func(c *config.Config) { c.Jobs.QueueCapacity = 1 })
	grantProposal(t, r, "action", "echo", nil)
	s := generateProposals(t, r)
	p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	slow, err := r.Invoke(context.Background(), "slow", false, protocol.Invocation{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started
	queued, err := r.Invoke(context.Background(), "echo", false, protocol.Invocation{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	request := proposalApproval(p)
	if _, err := r.ApproveAgentProposal(context.Background(), p.Proposal, request); fault.Safe(err).Code != protocol.Busy {
		t.Fatal(err)
	}
	if _, ok := r.proposalPreviews[p.ID]; !ok {
		t.Fatal("queue full consumed token")
	}
	close(runner.release)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = slow.Wait(ctx)
	_, _ = queued.Wait(ctx)
	admitted, err := r.ApproveAgentProposal(ctx, p.Proposal, request)
	if err != nil {
		t.Fatal(err)
	}
	_ = waitCapture(t, r, protocol.CaptureResponse{RunID: admitted.RunID, JobID: admitted.JobID})
	current, err := r.AgentSession(s.ID)
	if err != nil || current.State != "awaiting_review" || current.Proposals[1].State != "pending" {
		t.Fatal(current, err)
	}
	if _, err := r.RejectAgentProposal(ctx, current.Proposals[1].ID); err != nil {
		t.Fatal(err)
	}
}

func TestAgentSensitiveInputsAndNarrowedGrants(t *testing.T) {
	runner := &fakeRunner{}
	r, _ := proposalRuntime(t, []supervisor.Suggestion{{Kind: "action", Target: "secret"}}, runner, supervisor.Options{})
	changeProposalConfig(t, r, func(c *config.Config) {
		c.Actions["secret"] = config.Action{Type: "exec", Safety: "safe", Command: "/bin/echo", Args: []string{"prefix={{ .args.password }}", "end"}, Inputs: map[string]config.Input{"password": {Type: "string", Sensitive: true, Default: "PLANTED_SENSITIVE_DEFAULT"}}}
	})
	review, err := r.InspectAgentGrant(context.Background(), "action", "secret")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(review)
	if strings.Contains(string(encoded), "PLANTED") {
		t.Fatal("grant inspection exposed sensitive default")
	}
	grantProposal(t, r, "action", "secret", nil)
	s := generateProposals(t, r)
	p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(p)
	if strings.Contains(string(encoded), "PLANTED") || !strings.Contains(string(encoded), "redacted: input.password") {
		t.Fatal("secret preview lost reference/mask")
	}
	admitted, err := r.ApproveAgentProposal(context.Background(), p.Proposal, proposalApproval(p))
	if err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, protocol.CaptureResponse{RunID: admitted.RunID, JobID: admitted.JobID})
	encoded, _ = json.Marshal(run)
	if strings.Contains(string(encoded), "PLANTED") {
		t.Fatal("run retained raw sensitive input")
	}
	runner.mu.Lock()
	actual := runner.commands[0]
	runner.mu.Unlock()
	if actual.Args[0] != "prefix=PLANTED_SENSITIVE_DEFAULT" {
		t.Fatal("execution changed masked value")
	}
	r.mu.Lock()
	_, _, err = r.validateSuggestion(supervisor.Suggestion{Kind: "action", Target: "secret", Inputs: map[string]any{"password": "model override"}})
	r.mu.Unlock()
	if err == nil {
		t.Fatal("model supplied sensitive input")
	}
}
func TestAgentNumericGrantIntersectionAndChangedDefinition(t *testing.T) {
	r, _ := proposalRuntime(t, []supervisor.Suggestion{}, &fakeRunner{}, supervisor.Options{})
	changeProposalConfig(t, r, func(c *config.Config) {
		c.Actions["number"] = config.Action{Type: "exec", Safety: "safe", Command: "/bin/echo", Args: []string{"{{ .args.n }}"}, Inputs: map[string]config.Input{"n": {Type: "integer", Default: 5, Min: 0, Max: 10}}}
	})
	grantProposal(t, r, "action", "number", map[string]config.Input{"n": {Type: "integer", Min: 2, Max: 8}})
	for _, n := range []int{1, 9, 11} {
		r.mu.Lock()
		_, _, err := r.validateSuggestion(supervisor.Suggestion{Kind: "action", Target: "number", Inputs: map[string]any{"n": n}})
		r.mu.Unlock()
		if err == nil {
			t.Fatal(n)
		}
	}
	r.mu.Lock()
	_, values, err := r.validateSuggestion(supervisor.Suggestion{Kind: "action", Target: "number", Inputs: map[string]any{"n": 7}})
	r.mu.Unlock()
	if err != nil || values["n"] != int64(7) {
		t.Fatal(values, err)
	}
	changeProposalConfig(t, r, func(c *config.Config) {
		a := c.Actions["number"]
		a.Args = append(a.Args, "changed")
		c.Actions["number"] = a
	})
	if catalog := r.AgentCatalog(context.Background()); len(catalog.Targets) != 0 {
		t.Fatal("definition edit kept grant")
	}
}
func TestAgentExperimentInputsAndExplicitDuplicate(t *testing.T) {
	r, _ := proposalRuntime(t, []supervisor.Suggestion{{Kind: "experiment", Target: "measured", Inputs: map[string]any{"count": 7}}}, &fakeRunner{}, supervisor.Options{})
	changeProposalConfig(t, r, func(c *config.Config) {
		c.Actions["number"] = config.Action{Type: "exec", Safety: "safe", Command: "/bin/echo", Args: []string{"{{ .args.n }}"}, Inputs: map[string]config.Input{"n": {Type: "integer", Default: 1, Min: 0, Max: 10}}}
		c.Experiments = map[string]protocol.Experiment{"measured": {SchemaVersion: 1, Title: "Measured", Action: "number", Projects: []string{"p"}, Inputs: []protocol.ExperimentInput{{Step: 0, Input: "n", Parameter: "count"}}, Collectors: []protocol.Collector{{Name: "elapsed", Step: 0, Action: "number", Kind: "measurement", Source: "outcome", Path: []string{"duration_ms"}, Unit: "ms", Quantity: "duration"}}}}
	})
	grantProposal(t, r, "experiment", "measured", map[string]config.Input{"count": {Type: "integer", Min: 1, Max: 8}})
	s := generateProposals(t, r)
	p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Capture.Parameters["count"] != json.Number("7") || p.Capture.Steps[0].Arguments[0] != "7" {
		t.Fatal(p.Capture)
	}
	admission, err := r.ApproveAgentProposal(context.Background(), p.Proposal, proposalApproval(p))
	if err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, protocol.CaptureResponse{RunID: admission.RunID, JobID: admission.JobID})
	if run.State != "success" || len(run.Measurements) != 1 || run.Measurements[0].Status != "valid" {
		t.Fatal(run)
	}
	r.mu.Lock()
	if r.parameters["count"].Value != int64(1) {
		t.Fatal("proposal changed persistent parameter")
	}
	r.mu.Unlock()
	s = generateProposals(t, r)
	_, err = r.sessions.Change(s.ID, func(s *supervisor.Session) error { s.Proposals[0].ExpiresAt = time.Now().Add(-time.Second); return nil })
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := r.DuplicateAgentProposal(context.Background(), s.Proposals[0].ID, evidence.NewRequestID(time.Now()))
	if err != nil || duplicate.Parent != s.ID || duplicate.ID == s.ID || duplicate.Proposals[0].ID == s.Proposals[0].ID || duplicate.State != "awaiting_review" {
		t.Fatal(duplicate, err)
	}
}

func TestAgentOneExecutionPerSessionAndExactCancel(t *testing.T) {
	runner := &fakeRunner{started: make(chan provider.Command, 4), release: make(chan struct{})}
	r, _ := proposalRuntime(t, []supervisor.Suggestion{{Kind: "action", Target: "slow"}, {Kind: "action", Target: "echo"}}, runner, supervisor.Options{})
	grantProposal(t, r, "action", "slow", nil)
	grantProposal(t, r, "action", "echo", nil)
	s := generateProposals(t, r)
	first, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.PrepareAgentProposal(context.Background(), s.Proposals[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := r.ApproveAgentProposal(context.Background(), first.Proposal, proposalApproval(first))
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started
	if _, err := r.ApproveAgentProposal(context.Background(), second.Proposal, proposalApproval(second)); fault.Safe(err).Code != protocol.Busy {
		t.Fatal(err)
	}
	if _, err := r.CancelAgent(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, protocol.CaptureResponse{RunID: admission.RunID, JobID: admission.JobID})
	if run.State != "cancelled" {
		t.Fatal(run)
	}
	saved, err := r.AgentSession(s.ID)
	if err != nil || saved.State != "cancelled" || saved.Proposals[1].State != "invalidated" {
		t.Fatal(saved, err)
	}
}

func TestAgentApprovalCrashRecovery(t *testing.T) {
	if phase := os.Getenv("PATCHBAY_AGENT_CRASH_PHASE"); phase != "" {
		root := os.Getenv("PATCHBAY_AGENT_CRASH_ROOT")
		path := filepath.Join(root, "config.yaml")
		raw := fixture + "\nagents: {proposals: {enabled: true}, codex: {model: test-model}}\n"
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		var armed atomic.Bool
		a := &sessionAgent{output: `{"schema_version":1,"summary":"Crash fixture","context_refs":[],"proposals":[{"kind":"action","target":"echo","rationale":"r","expected_outcome":"e"}]}`}
		runner := markerRunner{path: filepath.Join(root, "unexpected-effect")}
		r, err := New(path, Options{Agent: a, Runner: runner, Supervisor: supervisor.Options{Fault: func(op string) error {
			if armed.Load() && op == phase {
				os.Exit(73)
			}
			return nil
		}}})
		if err != nil {
			t.Fatal(err)
		}
		grantProposal(t, r, "action", "echo", nil)
		s := generateProposals(t, r)
		p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		request := proposalApproval(p)
		b, _ := json.Marshal(struct {
			Proposal string
			Request  supervisor.Approve
		}{p.Proposal, request})
		if err = os.WriteFile(filepath.Join(root, "retry.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		armed.Store(true)
		_, _ = r.ApproveAgentProposal(context.Background(), p.Proposal, request)
		t.Fatal("crash point missed")
	}
	for _, phase := range []string{"write", "directory_sync"} {
		t.Run(phase, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestAgentApprovalCrashRecovery$")
			command.Env = append(os.Environ(), "PATCHBAY_AGENT_CRASH_PHASE="+phase, "PATCHBAY_AGENT_CRASH_ROOT="+root)
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("crash failed: %v %s", err, output)
			}
			if _, err := os.Stat(filepath.Join(root, "unexpected-effect")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("effect happened before crash", err)
			}
			a := &sessionAgent{output: sessionOutput}
			runner := &fakeRunner{}
			r, err := New(filepath.Join(root, "config.yaml"), Options{Agent: a, Runner: runner})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = r.Close(ctx)
			}()
			b, err := os.ReadFile(filepath.Join(root, "retry.json"))
			if err != nil {
				t.Fatal(err)
			}
			var retry struct {
				Proposal string
				Request  supervisor.Approve
			}
			if err = json.Unmarshal(b, &retry); err != nil {
				t.Fatal(err)
			}
			admitted, err := r.ApproveAgentProposal(context.Background(), retry.Proposal, retry.Request)
			if err != nil {
				t.Fatal(err)
			}
			run, err := r.runs.Get(admitted.RunID)
			if err != nil || run.State != "interrupted" {
				t.Fatal(run, err)
			}
			s, err := r.AgentSession(admitted.Session)
			if err != nil || s.Proposals[0].RunID != run.ID || s.Proposals[0].State != "interrupted" {
				t.Fatal(s, err)
			}
			if a.count() != 0 {
				t.Fatal("recovery generated")
			}
			runner.mu.Lock()
			count := len(runner.commands)
			runner.mu.Unlock()
			if count != 0 {
				t.Fatal("recovery dispatched")
			}
		})
	}
}

type markerRunner struct{ path string }

func (r markerRunner) Run(context.Context, provider.Command, *provider.Budget) (action.Result, error) {
	_ = os.WriteFile(r.path, []byte("effect"), 0600)
	return action.Result{Status: action.Success}, nil
}

func TestAgentApprovedRealHelperRecordsOutcome(t *testing.T) {
	r, _ := proposalRuntime(t, []supervisor.Suggestion{{Kind: "action", Target: "echo"}}, nil, supervisor.Options{})
	grantProposal(t, r, "action", "echo", nil)
	s := generateProposals(t, r)
	p, err := r.PrepareAgentProposal(context.Background(), s.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := r.ApproveAgentProposal(context.Background(), p.Proposal, proposalApproval(p))
	if err != nil {
		t.Fatal(err)
	}
	run := waitCapture(t, r, protocol.CaptureResponse{RunID: admission.RunID, JobID: admission.JobID})
	if run.State != "success" || len(run.Outcomes) != 1 || run.Outcomes[0].State != "success" {
		t.Fatal(run)
	}
	job, err := r.Jobs().Get(admission.JobID)
	if err != nil || job.Result.Data["stdout"] != "old dev initial\n" {
		t.Fatal(job, err)
	}
}

func TestAgentGitMaskPreservesUnrelatedFlags(t *testing.T) {
	step := protocol.PreparedStep{Arguments: []string{"--no-pager", "diff", "--no-ext-diff", "--no-textconv", "--cached", "--", "a"}}
	maskGitInputs(&step, config.Action{Type: "git", Operation: "diff", Inputs: map[string]config.Input{"path": {Sensitive: true}}})
	want := []string{"--no-pager", "diff", "--no-ext-diff", "--no-textconv", "--cached", "--", "<redacted: input.path>"}
	if !reflect.DeepEqual(step.Arguments, want) {
		t.Fatal(step.Arguments)
	}
	step.Arguments = []string{"--no-pager", "log", "--max-count=1", "--format=%H%x00%s%x00", "--"}
	maskGitInputs(&step, config.Action{Type: "git", Operation: "log", Inputs: map[string]config.Input{"limit": {Sensitive: true}}})
	if step.Arguments[2] != "--max-count=<redacted: input.limit>" || step.Arguments[0] != "--no-pager" || step.Arguments[3] != "--format=%H%x00%s%x00" {
		t.Fatal(step.Arguments)
	}
}
