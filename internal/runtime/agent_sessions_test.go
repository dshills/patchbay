package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/provider"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
)

type sessionAgent struct {
	mu        sync.Mutex
	requests  []provider.AgentRequest
	release   chan struct{}
	output    string
	truncated bool
}

func (*sessionAgent) Health(context.Context) provider.Health { return provider.Health{Available: true} }
func (a *sessionAgent) Run(ctx context.Context, req provider.AgentRequest, _ *provider.Budget, publish func(action.Result)) (action.Result, error) {
	a.mu.Lock()
	a.requests = append(a.requests, req)
	a.mu.Unlock()
	publish(action.Result{Status: "running", Data: map[string]any{"stdout": `{"schema_version":1,"proposals":[{"target":"echo"`}})
	if a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return action.Result{Status: action.Cancelled}, ctx.Err()
		}
	}
	return action.Result{Status: action.Success, Data: map[string]any{"stdout": a.output, "truncated": a.truncated, "usage": map[string]int64{"input_tokens": 42, "output_tokens": 12, "total_tokens": 54}}}, nil
}
func (a *sessionAgent) count() int { a.mu.Lock(); defer a.mu.Unlock(); return len(a.requests) }

const sessionOutput = `{"schema_version":1,"summary":"<script>untrusted</script>","context_refs":["invented"],"proposals":[]}`

func sessionRuntime(t *testing.T, a *sessionAgent, options supervisor.Options) (*Runtime, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	cfg := strings.Replace(agentConfig, "agents: {codex: {model: test-model}}", "agents: {proposals: {enabled: true}, codex: {model: test-model}}", 1)
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := New(path, Options{Agent: a, Supervisor: options})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = r.Close(ctx)
	})
	return r, r.cfg.Projects["demo"].Path
}
func contextRequest() supervisor.Selection {
	return supervisor.Selection{Prompt: "Explain the selected result.", RequestID: evidence.NewRequestID(time.Now())}
}
func startRequest(p supervisor.ContextPreview) supervisor.Start {
	return supervisor.Start{Preparation: p.ID, Digest: p.Digest, RequestID: p.RequestID, Confirmed: true}
}
func waitSession(t *testing.T, r *Runtime, id string) supervisor.Session {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, err := r.AgentSession(id)
		if err != nil {
			t.Fatal(err)
		}
		if s.State != "generating" {
			return s
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("session did not finish")
	return supervisor.Session{}
}
func TestAgentSessionFrozenConsentDedupAndPrivacy(t *testing.T) {
	a := &sessionAgent{output: sessionOutput, release: make(chan struct{})}
	r, root := sessionRuntime(t, a, supervisor.Options{})
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("old selected text"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PLANTED_CREDENTIAL=must-not-appear"), 0600); err != nil {
		t.Fatal(err)
	}
	selection := contextRequest()
	selection.Files = []string{"selected.txt"}
	p, err := r.PrepareAgentContext(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if a.count() != 0 || strings.Contains(p.Input, "PLANTED") {
		t.Fatal("preview caused call or selected hidden file")
	}
	request := startRequest(p)
	request.Confirmed = false
	if _, err := r.StartAgent(context.Background(), request); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal(err)
	}
	request.Confirmed = true
	s, err := r.StartAgent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("new text after admission"), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got, err := r.StartAgent(context.Background(), request)
			if err != nil || got.ID != s.ID {
				t.Errorf("duplicate: %v %v", got, err)
			}
		})
	}
	wg.Wait()
	close(a.release)
	final := waitSession(t, r, s.ID)
	if final.State != "completed" || final.Snapshot != "" || final.Items[0].Text != "" || final.Output == nil || len(final.UnsupportedRefs) != 1 || final.Usage["total_tokens"] != 54 || a.count() != 1 {
		t.Fatal(final, a.count())
	}
	a.mu.Lock()
	actual := a.requests[0]
	a.mu.Unlock()
	if actual.FrozenInput != p.Input || len(actual.Files) != 0 || actual.Prompt != "" {
		t.Fatal("request did not use exact consented bytes")
	}
	if err := r.ForgetAgent(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.StartAgent(context.Background(), request); fault.Safe(err).Code != protocol.NotFound {
		t.Fatal(err)
	}
}
func TestAgentSessionChangedProtectedExpiredAndDisabled(t *testing.T) {
	a := &sessionAgent{output: sessionOutput}
	r, root := sessionRuntime(t, a, supervisor.Options{})
	file := filepath.Join(root, "test.txt")
	_ = os.WriteFile(file, []byte("before"), 0600)
	for _, name := range []string{".env", "config.yaml", "../outside"} {
		q := contextRequest()
		q.Files = []string{name}
		if _, err := r.PrepareAgentContext(context.Background(), q); err == nil {
			t.Fatal(name)
		}
	}
	q := contextRequest()
	q.Files = []string{"test.txt"}
	p, err := r.PrepareAgentContext(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(file, []byte("after"), 0600)
	if _, err := r.StartAgent(context.Background(), startRequest(p)); fault.Safe(err).Code != protocol.ContextChanged {
		t.Fatal(err)
	}
	p, err = r.PrepareAgentContext(context.Background(), contextRequest())
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	prep := r.contexts[p.ID]
	prep.preview.ExpiresAt = time.Now().Add(-time.Second)
	r.contexts[p.ID] = prep
	r.mu.Unlock()
	if _, err := r.StartAgent(context.Background(), startRequest(p)); fault.Safe(err).Code != protocol.StalePreparation {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.cfg.Agents.Proposals.Enabled = false
	r.mu.Unlock()
	if _, err := r.PrepareAgentContext(context.Background(), contextRequest()); fault.Safe(err).Code != protocol.PermissionDenied {
		t.Fatal(err)
	}
	if a.count() != 0 {
		t.Fatal("unexpected generation")
	}
}
func TestAgentSessionQuotaCancelAndInvalidOutput(t *testing.T) {
	a := &sessionAgent{output: sessionOutput, release: make(chan struct{})}
	r, _ := sessionRuntime(t, a, supervisor.Options{})
	sessions := []supervisor.Session{}
	for range 2 {
		p, err := r.PrepareAgentContext(context.Background(), contextRequest())
		if err != nil {
			t.Fatal(err)
		}
		s, err := r.StartAgent(context.Background(), startRequest(p))
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, s)
	}
	p, err := r.PrepareAgentContext(context.Background(), contextRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.StartAgent(context.Background(), startRequest(p)); fault.Safe(err).Code != protocol.Busy {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if _, err := r.CancelAgent(context.Background(), s.ID); err != nil {
			t.Fatal(err)
		}
	}
	close(a.release)
	for _, s := range sessions {
		if result := waitSession(t, r, s.ID); result.State != "cancelled" {
			t.Fatal(result)
		}
	}
	for _, output := range []string{"```json\n" + sessionOutput + "\n```", strings.Replace(sessionOutput, `"schema_version":1`, `"schema_version":99`, 1)} {
		t.Run(output[:8], func(t *testing.T) {
			a := &sessionAgent{output: output}
			r, _ := sessionRuntime(t, a, supervisor.Options{})
			p, err := r.PrepareAgentContext(context.Background(), contextRequest())
			if err != nil {
				t.Fatal(err)
			}
			s, err := r.StartAgent(context.Background(), startRequest(p))
			if err != nil {
				t.Fatal(err)
			}
			final := waitSession(t, r, s.ID)
			if final.State != "failed" || final.Error.Code != protocol.InvalidProposal || a.count() != 1 {
				t.Fatal(final)
			}
		})
	}
}
func TestAgentAuditFailurePreventsProvider(t *testing.T) {
	a := &sessionAgent{output: sessionOutput}
	r, _ := sessionRuntime(t, a, supervisor.Options{Fault: func(op string) error {
		if op == "write" {
			return errors.New("disk")
		}
		return nil
	}})
	p, err := r.PrepareAgentContext(context.Background(), contextRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.StartAgent(context.Background(), startRequest(p)); err == nil {
		t.Fatal("failed audit admitted")
	}
	if a.count() != 0 {
		t.Fatal("provider called before durable audit")
	}
}
func TestAgentRetainedSnapshotIsExplicit(t *testing.T) {
	a := &sessionAgent{output: sessionOutput}
	r, _ := sessionRuntime(t, a, supervisor.Options{})
	q := contextRequest()
	q.Retain = true
	p, err := r.PrepareAgentContext(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.StartAgent(context.Background(), startRequest(p))
	if err != nil {
		t.Fatal(err)
	}
	final := waitSession(t, r, s.ID)
	b, _ := json.Marshal(final)
	if final.Snapshot != p.Input || !strings.Contains(string(b), "Explain the selected result") {
		t.Fatal(final)
	}
}

func TestAgentRawCredentialAndFileLimit(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fixture<&credential")
	a := &sessionAgent{output: sessionOutput}
	r, root := sessionRuntime(t, a, supervisor.Options{})
	q := contextRequest()
	q.Prompt = "Include fixture<&credential here"
	if _, err := r.PrepareAgentContext(context.Background(), q); fault.Safe(err).Code != protocol.PermissionDenied {
		t.Fatal(err)
	}
	q = contextRequest()
	q.Files = []string{"data.txt"}
	_ = os.WriteFile(filepath.Join(root, "data.txt"), []byte("fixture<&credential"), 0600)
	if _, err := r.PrepareAgentContext(context.Background(), q); fault.Safe(err).Code != protocol.PermissionDenied {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "data.txt"), []byte(strings.Repeat("x", 128<<10)), 0600)
	p, err := r.PrepareAgentContext(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.StartAgent(context.Background(), startRequest(p))
	if err != nil {
		t.Fatal(err)
	}
	if final := waitSession(t, r, s.ID); final.State != "completed" {
		t.Fatal(final)
	}
	_ = os.WriteFile(filepath.Join(root, "data.txt"), []byte(strings.Repeat("x", (128<<10)+1)), 0600)
	q.RequestID = evidence.NewRequestID(time.Now())
	if _, err := r.PrepareAgentContext(context.Background(), q); fault.Safe(err).Code != protocol.InvalidRequest {
		t.Fatal(err)
	}
}

func TestAgentContextSelectedArtifactsAndSymlinks(t *testing.T) {
	a := &sessionAgent{output: sessionOutput}
	r, root := sessionRuntime(t, a, supervisor.Options{})
	run := protocol.Run{Experiment: protocol.Experiment{SchemaVersion: 1, ID: "fixture", Title: "Fixture"}, Project: "demo", RequestID: evidence.NewRequestID(time.Now()), RequestDigest: evidence.Digest([]byte("artifact request")), Parameters: map[string]any{}}
	reserved, _, err := r.runs.Reserve(run)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := r.runs.AddArtifact(reserved.RunID, "selected", "text/plain", []byte("selected run evidence"))
	if err != nil {
		t.Fatal(err)
	}
	q := contextRequest()
	q.Artifacts = []supervisor.ArtifactSelection{{Run: reserved.RunID, Artifact: artifact.ID}}
	p, err := r.PrepareAgentContext(context.Background(), q)
	if err != nil || len(p.Items) != 1 || p.Items[0].Run != reserved.RunID || !strings.Contains(p.Input, "selected run evidence") {
		t.Fatal(p, err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ok.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	q = contextRequest()
	q.Files = []string{"link.txt"}
	if _, err := r.PrepareAgentContext(context.Background(), q); err == nil {
		t.Fatal("linked selection accepted")
	}
}
