package streamdeck

import (
	"context"
	"encoding/json"
	"fmt"
	"go.yaml.in/yaml/v3"
	"os"
	"patchbay/internal/action"
	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/provider"
	runtimecore "patchbay/internal/runtime"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
	"path/filepath"
	"testing"
	"time"
)

type supervisionFixture struct{}

func (supervisionFixture) Health(context.Context) provider.Health {
	return provider.Health{Available: true}
}
func (supervisionFixture) Run(context.Context, provider.AgentRequest, *provider.Budget, func(action.Result)) (action.Result, error) {
	return action.Result{Status: action.Success, Data: map[string]any{"stdout": `{"schema_version":1,"summary":"Fixture","context_refs":[],"proposals":[{"kind":"action","target":"echo","rationale":"test","expected_outcome":"success"}]}`}}, nil
}
func TestAgentDeckInputsWithRealDaemon(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Keep Unix socket paths shorter than the platform's sockaddr limit.
	sockDir, err := os.MkdirTemp("/tmp", "pb-ag-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	d := &wireDaemon{path: filepath.Join(dir, "config.yaml"), socket: filepath.Join(sockDir, "sock"), options: runtimecore.Options{Agent: supervisionFixture{}}}
	raw := fmt.Sprintf(wireConfig, d.socket, filepath.Join(dir, "state")) + fmt.Sprintf("\nprojects:\n  demo: {name: Demo, path: %q}\nagents: {codex: {model: fixture}, proposals: {enabled: true}}\n", dir)
	if err = os.WriteFile(d.path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(d.path)
	if err != nil {
		t.Fatal(err)
	}
	c.Context.Defaults.Project = "demo"
	c.Bindings[1].Press.Action = ""
	c.Bindings[1].Press.Agent = "approve"
	c.Bindings[1].LongPress = nil
	data, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(d.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	d.start(t)
	t.Cleanup(func() { d.stop(t) })
	review, err := d.runtime.InspectAgentGrant(context.Background(), "action", "echo")
	if err != nil {
		t.Fatal(err)
	}
	p := c.Projects["demo"]
	p.AgentGrants = []config.AgentGrant{{Kind: "action", Target: "echo", Digest: review.Target.Digest}}
	c.Projects["demo"] = p
	data, err = yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(d.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = d.runtime.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	preview, err := d.runtime.PrepareAgentContext(context.Background(), supervisor.Selection{Prompt: "Suggest echo", RequestID: evidence.NewRequestID(time.Now())})
	if err != nil {
		t.Fatal(err)
	}
	session, err := d.runtime.StartAgent(context.Background(), supervisor.Start{Preparation: preview.ID, Digest: preview.Digest, RequestID: preview.RequestID, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for session.State == "generating" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		session, err = d.runtime.AgentSession(session.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if session.State != "awaiting_review" {
		t.Fatal(session)
	}
	backend, err := NewHTTPBackend(d.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	device := &fakeDevice{now: time.Now()}
	engine := NewEngine(backend, []Device{{ID: "device", Type: 7}}, func() time.Time { return device.now })
	input(t, engine, device, message("Keypad", "willAppear"))
	selection, err := d.runtime.SelectAgent(context.Background(), protocol.AgentSelect{Revision: d.runtime.AgentSelection().Revision, Proposal: session.Proposals[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	full, err := d.runtime.PrepareAgentProposal(context.Background(), session.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.runtime.LinkAgentReview(context.Background(), protocol.AgentReviewLink{Revision: selection.Revision, Preparation: full.ID, Digest: full.Digest}); err != nil {
		t.Fatal(err)
	}
	if err = engine.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	click(t, engine, device, "Keypad", 10*time.Millisecond)
	if engine.controls["Keypad"].pending == nil {
		t.Fatal("missing challenge")
	}
	click(t, engine, device, "Keypad", HoldDuration)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		session, err = d.runtime.AgentSession(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if session.State == "completed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if session.State != "completed" || session.Proposals[0].RunID == "" {
		b, _ := json.Marshal(session)
		t.Fatal(string(b))
	}
}
