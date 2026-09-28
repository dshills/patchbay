package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"patchbay/internal/evidence"
	"patchbay/internal/supervisor"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/api"
	"patchbay/internal/provider"
	runtimecore "patchbay/internal/runtime"
)

type cliAgent struct{ started chan struct{} }

func (*cliAgent) Health(context.Context) provider.Health { return provider.Health{Available: true} }
func (a *cliAgent) Run(ctx context.Context, _ provider.AgentRequest, _ *provider.Budget, update func(action.Result)) (action.Result, error) {
	update(action.Result{Status: "running", Data: map[string]any{"stdout": "streamed partial"}})
	close(a.started)
	<-ctx.Done()
	return action.Result{Data: map[string]any{"stdout": "streamed partial"}}, ctx.Err()
}

func TestAgentUsesExistingCLIAndJobRoutes(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "pb-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	d := &cliDaemon{path: filepath.Join(dir, "config.yaml"), socket: filepath.Join(dir, "sock")}
	text := fmt.Sprintf(`version: 1
server: {socket: %q}
state: {path: %q}
context: {defaults: {project: demo}}
projects: {demo: {name: Demo, path: ., language: go, conventions: [test]}}
agents: {codex: {model: test-model}}
prompts: {review: Review selected context.}
actions: {review: {type: agent, provider: codex, prompt: review, safety: safe}}
`, d.socket, filepath.Join(dir, "state.json"))
	if err := os.WriteFile(d.path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	agent := &cliAgent{started: make(chan struct{})}
	runtime, err := runtimecore.New(d.path, runtimecore.Options{Agent: agent})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := api.Listen(d.socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: api.NewHandler(runtime), ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Close(ctx)
		_ = server.Close()
		<-done
	})
	out, _ := ctl(t, d, false, 0, "action", "list")
	for _, want := range []string{"review", "confirm", "prompt=review", "workspace_write=false", "convention:go"} {
		if !strings.Contains(out, want) {
			t.Fatal(out)
		}
	}
	ctl(t, d, true, 4, "action", "run", "review")
	out, _ = ctl(t, d, false, 0, "action", "run", "review", "--confirm", "--async")
	id := strings.TrimSpace(out)
	select {
	case <-agent.started:
	case <-time.After(time.Second):
		t.Fatal("agent not started")
	}
	out, _ = ctl(t, d, false, 0, "job", "show", id)
	if !strings.Contains(out, "running") || !strings.Contains(out, "streamed partial") {
		t.Fatal(out)
	}
	ctl(t, d, true, 0, "job", "cancel", id)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := runtime.Jobs().Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	out, _ = ctl(t, d, false, 0, "job", "show", id)
	if !strings.Contains(out, "cancelled") {
		t.Fatal(out)
	}
}

type cliSessionAgent struct{ calls atomic.Int32 }

func (*cliSessionAgent) Health(context.Context) provider.Health {
	return provider.Health{Available: true}
}
func (a *cliSessionAgent) Run(_ context.Context, request provider.AgentRequest, _ *provider.Budget, _ func(action.Result)) (action.Result, error) {
	a.calls.Add(1)
	return action.Result{Status: action.Success, Data: map[string]any{"stdout": `{"schema_version":1,"summary":"Fixture explanation","context_refs":[],"proposals":[]}`}}, nil
}
func TestAgentSessionCLIConsentAndDurableRetry(t *testing.T) {
	dir, err := os.MkdirTemp("/private/tmp", "pb-sessions-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	d := &cliDaemon{path: filepath.Join(dir, "config.yaml"), socket: filepath.Join(dir, "private", "sock")}
	text := fmt.Sprintf(`version: 1
server: {socket: %q}
state: {path: %q}
context: {defaults: {project: demo}}
projects: {demo: {name: Demo, path: %q}}
agents: {proposals: {enabled: true}, codex: {model: test-model}}
`, d.socket, filepath.Join(dir, "private", "state"), dir)
	if err := os.WriteFile(d.path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	a := &cliSessionAgent{}
	r, err := runtimecore.New(d.path, runtimecore.Options{Agent: a})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := api.Listen(d.socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: api.NewHandler(r), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = r.Close(ctx)
	})
	ctl(t, d, true, 0, "agent", "catalog")
	requestID := evidence.NewRequestID(time.Now())
	selection, _ := json.Marshal(supervisor.Selection{Prompt: "Explain fixture", RequestID: requestID})
	out, _ := ctl(t, d, true, 0, "agent", "context", string(selection))
	var p supervisor.ContextPreview
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatal(err)
	}
	ctl(t, d, true, 4, "agent", "start", p.ID, p.Digest, requestID)
	out, _ = ctl(t, d, true, 0, "agent", "start", p.ID, p.Digest, requestID, "--confirm")
	var s supervisor.Session
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := r.Jobs().Wait(ctx, s.JobID); err != nil {
		t.Fatal(err)
	}
	retry, _ := ctl(t, d, true, 0, "agent", "start", p.ID, p.Digest, requestID, "--confirm")
	var got supervisor.Session
	_ = json.Unmarshal([]byte(retry), &got)
	if got.ID != s.ID || got.State != "completed" || a.calls.Load() != 1 {
		t.Fatal(got)
	}
	ctl(t, d, true, 0, "agent", "show", s.ID)
	ctl(t, d, true, 0, "agent", "list")
	ctl(t, d, true, 0, "agent", "forget", s.ID, "--confirm")
	ctl(t, d, true, 2, "agent", "start", p.ID, p.Digest, requestID, "--confirm")
	if a.calls.Load() != 1 {
		t.Fatal("forgotten generation replayed")
	}
}
