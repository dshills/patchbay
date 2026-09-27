package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"patchbay/internal/action"
	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/internal/job"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixture = `version: 1
server: {socket: private/deckd.sock, shutdown_grace: 2s}
state: {path: private/state.json, flush_interval: 5ms}
jobs: {concurrency: 1, queue_capacity: 8, history_limit: 20, output_limit_bytes: 1024}
context:
  defaults: {project: p, mode: dev, values: {tag: initial}}
projects:
  p:
    name: Project
    path: .
    environment: {PATCHBAY_OVERLAY: project}
    actions:
      scoped: {type: exec, safety: confirm, command: /bin/echo, args: [project]}
actions:
  echo:
    type: exec
    safety: safe
    command: /bin/echo
    args: ['{{ .args.text }}', '{{ .context.mode }}', '{{ .context.values.tag }}']
    cwd: '{{ .project.path }}'
    environment: {PATCHBAY_OVERLAY: action}
    inputs: {text: {type: string, default: old}}
  scoped: {type: exec, safety: safe, command: /bin/echo, args: [global]}
  slow: {type: exec, safety: safe, command: /bin/echo, args: [block]}
  fail: {type: exec, safety: safe, command: /bin/echo, args: [fail]}
  panic: {type: exec, safety: safe, command: /bin/echo, args: [panic]}
  confirm: {type: exec, safety: confirm, command: /bin/echo, args: [confirmed]}
  danger: {type: exec, safety: dangerous, command: /bin/echo}
  missing: {type: exec, safety: safe, command: /deckd/nonexistent}
  variable: {type: exec, safety: safe, command: /bin/echo, args: ['{{ .context.values.absent }}']}
  link: {type: open, safety: safe, target: '{{ .args.url }}', inputs: {url: {type: string, required: true}}}
  branch: {type: git, safety: safe, operation: branch, inputs: {mode: {type: enum, enum: [list, create, delete, switch]}, name: {type: string}}}
  nested: {type: workflow, safety: safe, workflow: inner}
workflows:
  inner: {steps: [{action: echo}]}
  outer: {steps: [{action: nested}, {action: echo}]}
  protected: {steps: [{action: echo}, {action: confirm}]}
  invalid: {steps: [{action: echo}, {action: missing}]}
  continue: {stop_on_error: false, steps: [{action: fail}, {action: echo}]}
parameters:
  count: {type: integer, value: 1, min: 0, max: 10000, step: 1, persistent: true}
  level: {type: float, value: 0.5, min: 0, max: 1, step: 0.1}
  enabled: {type: boolean, value: false}
  choice: {type: enum, value: a, enum: [a, b]}
  label: {type: string, value: initial}
bindings:
  - control: dial
    rotate: {parameter: count}
  - control: key
    press: {action: confirm}
  - control: safe
    press: {action: echo}
`

type fakeRunner struct {
	mu       sync.Mutex
	commands []provider.Command
	started  chan provider.Command
	release  chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, c provider.Command, _ *provider.Budget) (action.Result, error) {
	f.mu.Lock()
	f.commands = append(f.commands, c)
	f.mu.Unlock()
	if f.started != nil {
		f.started <- c
	}
	if len(c.Args) > 0 {
		switch c.Args[0] {
		case "block":
			select {
			case <-ctx.Done():
				return action.Result{}, ctx.Err()
			case <-f.release:
			}
		case "fail":
			return action.Result{Status: action.Failed}, fault.New(protocol.ExecutionFailed, "Fixture failed.")
		case "panic":
			panic("planted-secret")
		}
	}
	return action.Result{Status: action.Success, Data: map[string]any{"args": c.Args}}, nil
}
func (f *fakeRunner) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.commands) }
func writeConfig(t testing.TB, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func setup(t testing.TB, text string, runner provider.Runner) (*Runtime, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, text)
	r, err := New(path, Options{Runner: runner, Opener: "/bin/echo", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := r.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return r, path
}
func finished(t *testing.T, h *job.Handle) job.Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	j, err := h.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func invoke(t *testing.T, r *Runtime, name string, workflow bool, request protocol.Invocation) job.Job {
	t.Helper()
	h, err := r.Invoke(context.Background(), name, workflow, request)
	if err != nil {
		t.Fatal(err)
	}
	return finished(t, h)
}
func wantCode(t *testing.T, err error, code protocol.Code) {
	t.Helper()
	if err == nil || fault.Safe(err).Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestSingleActionMetadataUsesEffectiveRegistryAndCopiesInputs(t *testing.T) {
	r, _ := setup(t, fixture, &fakeRunner{})
	for _, listed := range r.Actions() {
		got, err := r.Action(listed.Name)
		if err != nil || !reflect.DeepEqual(got, listed) {
			t.Fatal(got, listed, err)
		}
	}
	scoped, err := r.Action("scoped")
	if err != nil || scoped.Safety != "confirm" {
		t.Fatal(scoped, err)
	}
	branch, err := r.Action("branch")
	if err != nil {
		t.Fatal(err)
	}
	branch.Inputs["mode"].Enum[0] = "mutated"
	delete(branch.Inputs, "name")
	again, err := r.Action("branch")
	if err != nil || again.Inputs["mode"].Enum[0] != "list" || again.Inputs["name"].Type != "string" {
		t.Fatal("metadata shares registry state", again, err)
	}
	_, err = r.Action("missing-action")
	wantCode(t, err, protocol.ActionNotFound)
}

func TestStateParametersEventsAndRestart(t *testing.T) {
	r, path := setup(t, fixture, &fakeRunner{})
	sub := r.Events().Subscribe(context.Background())
	defer sub.Close()
	values := map[string]string{"tag": "changed"}
	mode := "review"
	state, err := r.PatchContext(context.Background(), protocol.ContextPatch{Mode: &mode, Values: &values})
	if err != nil {
		t.Fatal(err)
	}
	state.Values["tag"] = "outside"
	values["tag"] = "also-outside"
	if r.Context().Values["tag"] != "changed" {
		t.Fatal("context map shared")
	}
	for _, c := range []struct {
		name  string
		value any
	}{{"count", json.Number("42")}, {"level", 0.8}, {"enabled", true}, {"choice", "b"}, {"label", "updated"}} {
		if _, err := r.SetParameter(context.Background(), c.name, c.value); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name  string
		value any
	}{{"count", 10001}, {"count", 1.5}, {"level", 2.0}, {"enabled", "true"}, {"choice", "c"}, {"label", false}} {
		_, err := r.SetParameter(context.Background(), c.name, c.value)
		wantCode(t, err, protocol.InvalidRequest)
	}
	p, _ := r.Parameter("choice")
	p.Enum[0] = "mutated"
	p, _ = r.Parameter("choice")
	if p.Enum[0] != "a" {
		t.Fatal("enum shared")
	}
	missing := "missing"
	_, err = r.PatchContext(context.Background(), protocol.ContextPatch{Project: &missing})
	wantCode(t, err, protocol.ProjectNotFound)
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			_, err := r.Control(context.Background(), protocol.EventRequest{Type: event.ControlRotated, Source: "test", Payload: json.RawMessage(`{"control":"dial","delta":1}`)})
			if err != nil {
				t.Error(err)
			}
			_ = r.Parameters()
			_ = r.Context()
		})
	}
	wg.Wait()
	p, _ = r.Parameter("count")
	if p.Value != int64(142) {
		t.Fatal("rotation lost updates", p)
	}
	if sub.Dropped() == 0 {
		t.Fatal("stalled subscriber should overflow")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(path, Options{Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close(context.Background()) }()
	p, _ = restarted.Parameter("count")
	if p.Value != int64(142) || restarted.Context().Mode != "review" || restarted.Context().Values["tag"] != "changed" {
		t.Fatal(p, restarted.Context())
	}
	p, _ = restarted.Parameter("level")
	if p.Value != 0.5 {
		t.Fatal("nonpersistent value restored", p)
	}
	if _, err := New(path, Options{}); err == nil {
		t.Fatal("duplicate state owner accepted")
	}
}

func TestInvocationPreflightPolicyTemplatesAndWorkflows(t *testing.T) {
	fake := &fakeRunner{}
	r, _ := setup(t, fixture, fake)
	for _, c := range []struct {
		name    string
		wf      bool
		request protocol.Invocation
		code    protocol.Code
	}{
		{"unknown", false, protocol.Invocation{}, protocol.ActionNotFound}, {"unknown", true, protocol.Invocation{}, protocol.NotFound},
		{"confirm", false, protocol.Invocation{}, protocol.ConfirmationRequired}, {"danger", false, protocol.Invocation{Confirmed: true}, protocol.PermissionDenied},
		{"scoped", false, protocol.Invocation{}, protocol.ConfirmationRequired}, {"protected", true, protocol.Invocation{}, protocol.ConfirmationRequired},
		{"invalid", true, protocol.Invocation{}, protocol.ProviderUnavailable}, {"variable", false, protocol.Invocation{}, protocol.InvalidRequest},
		{"echo", false, protocol.Invocation{Args: map[string]any{"command": "bad"}}, protocol.InvalidRequest},
		{"echo", false, protocol.Invocation{Mode: "bad"}, protocol.InvalidRequest}, {"echo", false, protocol.Invocation{TimeoutMS: -1}, protocol.InvalidRequest},
		{"inner", true, protocol.Invocation{Args: map[string]any{"text": "no"}}, protocol.InvalidRequest},
		{"link", false, protocol.Invocation{}, protocol.InvalidRequest}, {"link", false, protocol.Invocation{Args: map[string]any{"url": "javascript:alert(1)"}}, protocol.InvalidRequest},
		{"branch", false, protocol.Invocation{Args: map[string]any{"mode": "create", "name": "new"}}, protocol.ConfirmationRequired},
	} {
		_, err := r.Invoke(context.Background(), c.name, c.wf, c.request)
		wantCode(t, err, c.code)
	}
	if fake.count() != 0 {
		t.Fatal("side effect before full preflight")
	}
	_, err := r.Control(context.Background(), protocol.EventRequest{Type: event.ControlPressed, Source: "test", Payload: json.RawMessage(`{"control":"key"}`)})
	wantCode(t, err, protocol.ConfirmationRequired)
	if fake.count() != 0 {
		t.Fatal("binding bypass")
	}
	injection := `$(touch forbidden); {{ .context.mode }}`
	j := invoke(t, r, "echo", false, protocol.Invocation{Args: map[string]any{"text": injection}})
	if j.State != job.Success {
		t.Fatal(j)
	}
	fake.mu.Lock()
	command := fake.commands[0]
	fake.mu.Unlock()
	if command.Args[0] != injection || command.Args[1] != "dev" || !strings.Contains(strings.Join(command.Env, "\n"), "PATCHBAY_OVERLAY=action") {
		t.Fatal(command.Args)
	}
	if invoke(t, r, "outer", true, protocol.Invocation{}).State != job.Success {
		t.Fatal("nested workflow failed with one worker")
	}
	if invoke(t, r, "continue", true, protocol.Invocation{}).State != job.Failed {
		t.Fatal("continue workflow did not aggregate failure")
	}
	if invoke(t, r, "protected", true, protocol.Invocation{Confirmed: true}).State != job.Success {
		t.Fatal("confirmed workflow")
	}
	if invoke(t, r, "panic", false, protocol.Invocation{}).Error.Code != protocol.Internal {
		t.Fatal("panic not isolated")
	}
	if invoke(t, r, "branch", false, protocol.Invocation{}).State != job.Success {
		t.Fatal("branch listing failed")
	}
	fake.mu.Lock()
	gitCommand := fake.commands[len(fake.commands)-1]
	fake.mu.Unlock()
	if !strings.Contains(strings.Join(gitCommand.Env, "\n"), "GIT_SSH_COMMAND=ssh -oBatchMode=yes") {
		t.Fatal("interactive SSH enabled")
	}
	if invoke(t, r, "link", false, protocol.Invocation{Args: map[string]any{"url": "https://example.com/a b"}}).State != job.Success {
		t.Fatal("open failed")
	}
	fake.mu.Lock()
	last := fake.commands[len(fake.commands)-1]
	fake.mu.Unlock()
	if len(last.Args) != 2 || last.Args[0] != "--" {
		t.Fatal(last.Args)
	}
	empty := ""
	if _, err := r.PatchContext(context.Background(), protocol.ContextPatch{Project: &empty}); err != nil {
		t.Fatal(err)
	}
	if invoke(t, r, "scoped", false, protocol.Invocation{}).State != job.Success {
		t.Fatal("global scope")
	}
	_, err = r.Invoke(context.Background(), "echo", false, protocol.Invocation{})
	wantCode(t, err, protocol.InvalidRequest)
}

func TestReloadPinsAdmittedJobsAndIsAtomic(t *testing.T) {
	fake := &fakeRunner{started: make(chan provider.Command, 20), release: make(chan struct{})}
	r, path := setup(t, fixture, fake)
	slow, err := r.Invoke(context.Background(), "slow", false, protocol.Invocation{})
	if err != nil {
		t.Fatal(err)
	}
	<-fake.started
	old, err := r.Invoke(context.Background(), "echo", false, protocol.Invocation{})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, path, "invalid: true")
	if _, err := r.Reload(context.Background()); err == nil {
		t.Fatal("invalid reload")
	}
	if r.Status().Generation != 1 || r.Context().Project != "p" {
		t.Fatal("failed reload mutated state")
	}
	writeConfig(t, path, strings.Replace(fixture, "shutdown_grace: 2s", "shutdown_grace: 3s", 1))
	if _, err := r.Reload(context.Background()); err == nil {
		t.Fatal("restart settings changed")
	}
	updated := strings.Replace(fixture, "default: old", "default: new", 1)
	updated = strings.Replace(updated, "max: 10000", "max: 5", 1)
	if _, err := r.SetParameter(context.Background(), "count", 10); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, path, updated)
	if generation, err := r.Reload(context.Background()); err != nil || generation != 2 {
		t.Fatal(generation, err)
	}
	p, _ := r.Parameter("count")
	if p.Value != int64(1) {
		t.Fatal("out of range retained", p)
	}
	newer, err := r.Invoke(context.Background(), "echo", false, protocol.Invocation{})
	if err != nil {
		t.Fatal(err)
	}
	close(fake.release)
	finished(t, slow)
	oldJob, newJob := finished(t, old), finished(t, newer)
	if oldJob.Generation != 1 || newJob.Generation != 2 || oldJob.Result.Data["args"].([]any)[0] != "old" || newJob.Result.Data["args"].([]any)[0] != "new" {
		t.Fatal(oldJob, newJob)
	}
	// Removing definitions reconciles active state and keeps terminal jobs readable.
	minimal := "version: 1\nserver: {socket: private/deckd.sock, shutdown_grace: 2s}\nstate: {path: private/state.json, flush_interval: 5ms}\n"
	writeConfig(t, path, minimal)
	if _, err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Context().Project != "" || len(r.Parameters()) != 0 || len(r.Actions()) != 0 || len(r.Workflows()) != 0 {
		t.Fatal("removed definitions survived")
	}
	if _, err := r.Jobs().Get(newer.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncLifetimeSyncCancellationAndControlValidation(t *testing.T) {
	fake := &fakeRunner{started: make(chan provider.Command, 20), release: make(chan struct{})}
	r, _ := setup(t, fixture, fake)
	ctx, cancel := context.WithCancel(context.Background())
	async, err := r.Invoke(ctx, "slow", false, protocol.Invocation{Mode: protocol.Async})
	if err != nil {
		t.Fatal(err)
	}
	<-fake.started
	cancel()
	if j, _ := r.Jobs().Get(async.ID); j.State != job.Running {
		t.Fatal(j)
	}
	if _, err := r.Jobs().Cancel(async.ID); err != nil {
		t.Fatal(err)
	}
	if finished(t, async).State != job.Cancelled {
		t.Fatal("async cancellation")
	}
	ctx, cancel = context.WithCancel(context.Background())
	syncJob, err := r.Invoke(ctx, "slow", false, protocol.Invocation{Mode: protocol.Sync})
	if err != nil {
		t.Fatal(err)
	}
	<-fake.started
	cancel()
	if finished(t, syncJob).State != job.Cancelled {
		t.Fatal("sync disconnect")
	}
	if j := invoke(t, r, "slow", false, protocol.Invocation{TimeoutMS: 5}); j.Error.Code != protocol.Timeout {
		t.Fatal(j)
	}
	for _, request := range []protocol.EventRequest{
		{Type: event.JobFinished, Source: "test", Payload: json.RawMessage(`{}`)},
		{Type: event.ControlRotated, Source: "test", Payload: json.RawMessage(`{"control":"dial","delta":1,"confirmed":true}`)},
		{Type: event.ControlPressed, Source: "test", Payload: json.RawMessage(`{"control":"key","delta":1}`)},
		{Type: event.ControlPressed, Source: "test", Payload: json.RawMessage(`{"control":"key","control":"safe"}`)},
	} {
		_, err := r.Control(context.Background(), request)
		wantCode(t, err, protocol.InvalidRequest)
	}
	response, err := r.Control(context.Background(), protocol.EventRequest{Type: event.ControlReleased, Source: "test", Payload: json.RawMessage(`{"control":"unknown"}`)})
	if err != nil || response.Matched || response.EventID == "" {
		t.Fatal(response, err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = r.SetParameter(context.Background(), "count", 1)
	wantCode(t, err, protocol.ShuttingDown)
}

func BenchmarkControlRotation(b *testing.B) {
	r, _ := setup(b, fixture, &fakeRunner{})
	request := protocol.EventRequest{Type: event.ControlRotated, Source: "test", Payload: json.RawMessage(`{"control":"dial","delta":1}`)}
	b.ReportAllocs()
	positive := true
	for b.Loop() {
		if _, err := r.Control(context.Background(), request); err != nil {
			b.Fatal(err)
		}
		positive = !positive
		if positive {
			request.Payload = json.RawMessage(`{"control":"dial","delta":1}`)
		} else {
			request.Payload = json.RawMessage(`{"control":"dial","delta":-1}`)
		}
	}
}

func TestCorruptStateRecoveryAndReconciliation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeConfig(t, path, fixture)
	private := filepath.Join(dir, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(private, "state.json")
	for _, saved := range []string{`corrupt-planted-secret`, `{"version":1,"context":{"project":"removed","mode":"bad mode","values":{"bad.key":"discard","tag":"saved"}},"parameters":{"count":20000,"enabled":true,"removed":1}}`} {
		if err := os.WriteFile(statePath, []byte(saved), 0600); err != nil {
			t.Fatal(err)
		}
		var logs bytes.Buffer
		r, err := New(path, Options{Runner: &fakeRunner{}, Log: &logs})
		if err != nil {
			t.Fatal(err)
		}
		p, _ := r.Parameter("count")
		if p.Value != int64(1) {
			t.Fatal("invalid value restored", p)
		}
		p, _ = r.Parameter("enabled")
		if p.Value != false {
			t.Fatal("nonpersistent value restored", p)
		}
		if r.Context().Mode != "dev" {
			t.Fatal(r.Context())
		}
		if saved[0] == '{' && (r.Context().Project != "" || r.Context().Values["tag"] != "saved") {
			t.Fatal(r.Context())
		}
		if err := r.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(logs.String(), "planted-secret") {
			t.Fatal("state contents leaked")
		}
		if saved[0] != '{' && !strings.Contains(logs.String(), `"outcome":"recovered"`) {
			t.Fatal("missing recovery diagnostic", logs.String())
		}
	}
}

func TestPolicyReloadAndPanicEvents(t *testing.T) {
	fake := &fakeRunner{}
	r, path := setup(t, fixture, fake)
	writeConfig(t, path, fixture+"security: {allow_dangerous_actions: true}\n")
	if _, err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := r.Invoke(context.Background(), "danger", false, protocol.Invocation{})
	wantCode(t, err, protocol.ConfirmationRequired)
	if invoke(t, r, "danger", false, protocol.Invocation{Confirmed: true}).State != job.Success {
		t.Fatal("dangerous opt-in failed")
	}
	_, err = r.Invoke(context.Background(), "danger", false, protocol.Invocation{})
	wantCode(t, err, protocol.ConfirmationRequired)
	sub := r.Events().Subscribe(context.Background())
	defer sub.Close()
	j := invoke(t, r, "panic", false, protocol.Invocation{})
	if j.Error.Code != protocol.Internal {
		t.Fatal(j)
	}
	started, finishedID := "", ""
	for len(sub.C) > 0 {
		e := <-sub.C
		var data map[string]string
		_ = json.Unmarshal(e.Payload, &data)
		switch e.Type {
		case event.ActionStarted:
			started = data["action_id"]
		case event.ActionFinished:
			finishedID = data["action_id"]
		}
	}
	if started == "" || finishedID != started {
		t.Fatal("panic left an unfinished action event", started, finishedID)
	}
}
