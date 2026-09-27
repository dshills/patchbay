package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/fault"
	"patchbay/internal/job"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
)

const agentConfig = `version: 1
server: {socket: private/sock}
state: {path: private/state, flush_interval: 1h}
context: {defaults: {project: demo, mode: review}}
projects:
  demo: {name: Demo, path: ., language: go, conventions: [test]}
  off: {name: Off, path: .}
conventions:
  go: {test: {command: /bin/echo, args: [tested]}}
agents: {codex: {model: test-model}}
prompts: {review: 'OLD {{ .project.name }} {{ .args.focus }}'}
actions:
  review:
    type: agent
    provider: codex
    prompt: review
    safety: safe
    inputs: {focus: {type: string, default: correctness}}
  echo: {type: exec, safety: safe, command: /bin/echo, args: [static]}
workflows:
  review: {steps: [{action: review}, {action: echo}]}
  check: {steps: [{action: project.test}]}
`

type contractAgent struct {
	started chan provider.AgentRequest
	release chan struct{}
}

func (*contractAgent) Health(context.Context) provider.Health {
	return provider.Health{Available: true}
}
func (a *contractAgent) Run(ctx context.Context, r provider.AgentRequest, _ *provider.Budget, publish func(action.Result)) (action.Result, error) {
	publish(action.Result{Status: "running", Message: "Streaming", Data: map[string]any{"stdout": "partial"}})
	select {
	case a.started <- r:
	case <-ctx.Done():
		return action.Result{}, ctx.Err()
	}
	select {
	case <-a.release:
		return action.Result{Status: action.Success, Message: "Summary", Data: map[string]any{"stdout": "Do not execute this model text as a command."}}, nil
	case <-ctx.Done():
		return action.Result{Data: map[string]any{"stdout": "partial"}}, ctx.Err()
	}
}
func newAgentRuntime(t *testing.T) (*Runtime, *contractAgent, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(agentConfig), 0600); err != nil {
		t.Fatal(err)
	}
	agent := &contractAgent{started: make(chan provider.AgentRequest, 8), release: make(chan struct{})}
	r, err := New(path, Options{Agent: agent})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := r.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return r, agent, path
}
func waitAgent(t *testing.T, a *contractAgent) provider.AgentRequest {
	t.Helper()
	select {
	case r := <-a.started:
		return r
	case <-time.After(time.Second):
		t.Fatal("agent not started")
		return provider.AgentRequest{}
	}
}
func waitAgentJob(t *testing.T, h *job.Handle) job.Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	j, err := h.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestAgentPermissionMetadataAndCancellation(t *testing.T) {
	r, a, _ := newAgentRuntime(t)
	for _, workflow := range []bool{false, true} {
		if _, err := r.Invoke(context.Background(), "review", workflow, protocol.Invocation{}); fault.Safe(err).Code != protocol.ConfirmationRequired {
			t.Fatal(err)
		}
	}
	if len(r.Jobs().List()) != 0 || len(a.started) != 0 {
		t.Fatal("agent started without confirmation")
	}
	meta, err := r.Action("review")
	if err != nil || meta.Safety != "confirm" || meta.Agent == nil || meta.Agent.WorkspaceWrite || len(meta.Agent.Tools) != 0 || meta.Agent.Network != "api.openai.com" {
		t.Fatal(meta, err)
	}
	h, err := r.Invoke(context.Background(), "review", false, protocol.Invocation{Confirmed: true, Args: map[string]any{"focus": "{{ .context.mode }}"}})
	if err != nil {
		t.Fatal(err)
	}
	request := waitAgent(t, a)
	if request.Prompt != "OLD Demo {{ .context.mode }}" || request.Project != filepath.Dir(r.path) || request.Dir != request.Project {
		t.Fatal(request)
	}
	current, err := r.Jobs().Get(h.ID)
	if err != nil || current.State != job.Running || current.Result.Data["stdout"] != "partial" {
		t.Fatal(current, err)
	}
	current.Result.Data["stdout"] = "mutated"
	fresh, _ := r.Jobs().Get(h.ID)
	if fresh.Result.Data["stdout"] != "partial" {
		t.Fatal("mutable partial snapshot")
	}
	if _, err := r.Jobs().Cancel(h.ID); err != nil {
		t.Fatal(err)
	}
	terminal := waitAgentJob(t, h)
	if terminal.State != job.Cancelled || terminal.Error.Code != protocol.Cancelled || terminal.Result.Data["stdout"] != "partial" {
		t.Fatal(terminal)
	}
	r.Jobs().Update(h.ID, action.Result{Status: "running"})
	fresh, _ = r.Jobs().Get(h.ID)
	if fresh.State != job.Cancelled || fresh.Result.Data["stdout"] != "partial" {
		t.Fatal("late update changed terminal job")
	}
}

func TestAgentReloadPinsPromptGenerationAndWorkflowIsDeterministic(t *testing.T) {
	r, a, path := newAgentRuntime(t)
	h, err := r.Invoke(context.Background(), "review", true, protocol.Invocation{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	old := waitAgent(t, a)
	if err := os.WriteFile(path, []byte(strings.Replace(agentConfig, "OLD ", "NEW ", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	generation, err := r.Reload(context.Background())
	if err != nil || generation != 2 {
		t.Fatal(generation, err)
	}
	close(a.release)
	oldJob := waitAgentJob(t, h)
	if oldJob.Generation != 1 || oldJob.State != job.Success || !strings.HasPrefix(old.Prompt, "OLD ") {
		t.Fatal(oldJob, old)
	}
	steps := oldJob.Result.Data["steps"].([]any)
	if len(steps) != 2 || steps[1].(map[string]any)["result"].(map[string]any)["data"].(map[string]any)["stdout"] != "static\n" {
		t.Fatal(steps)
	}
	next, err := r.Invoke(context.Background(), "review", false, protocol.Invocation{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	request := waitAgent(t, a)
	newJob := waitAgentJob(t, next)
	if !strings.HasPrefix(request.Prompt, "NEW ") || newJob.Generation != 2 {
		t.Fatal(request, newJob)
	}
}

func TestAgentTimeoutAndMissingProject(t *testing.T) {
	r, a, _ := newAgentRuntime(t)
	h, err := r.Invoke(context.Background(), "review", false, protocol.Invocation{Confirmed: true, TimeoutMS: 20})
	if err != nil {
		t.Fatal(err)
	}
	waitAgent(t, a)
	j := waitAgentJob(t, h)
	if j.Error == nil || j.Error.Code != protocol.Timeout {
		t.Fatal(j)
	}
	empty := ""
	if _, err := r.PatchContext(context.Background(), protocol.ContextPatch{Project: &empty}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Invoke(context.Background(), "review", false, protocol.Invocation{Confirmed: true}); fault.Safe(err).Code != protocol.InvalidRequest {
		t.Fatal(err)
	}
}

func TestConventionWorkflowIsScopedAndConfirmed(t *testing.T) {
	r, _, _ := newAgentRuntime(t)
	metadata, err := r.Action("project.test")
	if err != nil || metadata.Origin != "convention:go" || metadata.Safety != "confirm" {
		t.Fatal(metadata, err)
	}
	if _, err := r.Invoke(context.Background(), "check", true, protocol.Invocation{}); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal(err)
	}
	h, err := r.Invoke(context.Background(), "check", true, protocol.Invocation{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitAgentJob(t, h); j.State != job.Success {
		t.Fatal(j)
	}
	off := "off"
	if _, err := r.PatchContext(context.Background(), protocol.ContextPatch{Project: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Action("project.test"); fault.Safe(err).Code != protocol.ActionNotFound {
		t.Fatal(err)
	}
	if _, err := r.Invoke(context.Background(), "check", true, protocol.Invocation{Confirmed: true}); fault.Safe(err).Code != protocol.ActionNotFound {
		t.Fatal(err)
	}
}
