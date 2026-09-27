package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"patchbay/internal/daemon"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const cliConfig = `version: 1
server: {socket: %q, shutdown_grace: 2s}
state: {path: %q, flush_interval: 1h}
jobs: {concurrency: 1, queue_capacity: 2, history_limit: 50, output_limit_bytes: 128}
context: {defaults: {mode: dev}}
projects:
  demo:
    name: Demo
    path: .
    actions: {scoped: {type: exec, safety: confirm, command: /bin/echo, args: [override]}}
actions:
  echo:
    type: exec
    safety: safe
    command: /bin/echo
    args: ['{{ .args.text }}', '{{ .args.count }}', '{{ .args.enabled }}', '{{ .args.level }}', '{{ .args.choice }}']
    inputs:
      text: {type: string, default: hello}
      count: {type: integer, default: 1}
      enabled: {type: boolean, default: false}
      level: {type: float, default: 0.5}
      choice: {type: enum, enum: [a, b], default: a}
  confirm: {type: exec, safety: confirm, command: /bin/echo, args: [confirmed]}
  danger: {type: exec, safety: dangerous, command: /bin/echo}
  fail: {type: exec, safety: safe, command: /usr/bin/false}
  slow: {type: exec, safety: safe, command: /bin/sleep, args: ['30']}
  scoped: {type: exec, safety: safe, command: /bin/echo, args: [global]}
  git.status: {type: git, safety: safe, operation: status, cwd: '{{ .project.path }}'}
workflows:
  validate: {steps: [{action: echo}, {action: echo}]}
  fail-stop: {steps: [{action: fail}, {action: echo}]}
  fail-continue: {stop_on_error: false, steps: [{action: fail}, {action: echo}]}
  protected: {steps: [{action: echo}, {action: confirm}]}
parameters:
  count: {type: integer, value: 1, persistent: true}
  level: {type: float, value: 0.5, min: 0, max: 1, unit: V}
  enabled: {type: boolean, value: false}
  choice: {type: enum, value: a, enum: [a, b]}
  text: {type: string, value: initial}
bindings:
  - control: dial
    rotate: {parameter: count}
`

type readiness struct{ ready chan struct{} }

func (r readiness) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"outcome":"ready"`)) {
		select {
		case r.ready <- struct{}{}:
		default:
		}
	}
	return len(p), nil
}

type cliDaemon struct {
	path, socket, text string
	cancel             context.CancelFunc
	done               chan error
}

func newDaemon(t *testing.T) *cliDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pb-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	d := &cliDaemon{path: filepath.Join(dir, "config.yaml"), socket: filepath.Join(dir, "sock")}
	d.text = fmt.Sprintf(cliConfig, d.socket, filepath.Join(dir, "state.json"))
	d.write(t, d.text)
	if data, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatal(string(data), err)
	}
	d.start(t)
	t.Cleanup(func() { d.stop(t) })
	return d
}
func (d *cliDaemon) write(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(d.path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func (d *cliDaemon) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.done = make(chan error, 1)
	ready := make(chan struct{}, 1)
	go func() { d.done <- daemon.Run(ctx, d.path, readiness{ready}) }()
	select {
	case <-ready:
	case err := <-d.done:
		d.cancel = nil
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("daemon startup timed out")
	}
}
func (d *cliDaemon) stop(t *testing.T) {
	t.Helper()
	if d.cancel == nil {
		return
	}
	d.cancel()
	d.cancel = nil
	select {
	case err := <-d.done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon shutdown timed out")
	}
}
func ctl(t *testing.T, d *cliDaemon, jsonMode bool, want int, args ...string) (string, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	flags := []string{"--socket", d.socket}
	if jsonMode {
		flags = append(flags, "--json")
	}
	code := Run("deckctl", append(flags, args...), &out, &diagnostic)
	if code != want {
		t.Fatalf("%v: exit %d want %d\n%s\n%s", args, code, want, out.String(), diagnostic.String())
	}
	if jsonMode && !json.Valid(out.Bytes()) {
		t.Fatal("invalid JSON", out.String())
	}
	if want == 0 && diagnostic.Len() != 0 {
		t.Fatal("unexpected diagnostic", diagnostic.String())
	}
	return out.String(), diagnostic.String()
}
func object(t *testing.T, text string) map[string]any {
	t.Helper()
	var v map[string]any
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err, text)
	}
	return v
}

func TestAllRuntimeCommandsHumanAndJSON(t *testing.T) {
	d := newDaemon(t)
	for _, asJSON := range []bool{false, true} {
		for _, args := range [][]string{{"status"}, {"project", "list"}, {"project", "current"}, {"project", "use", "demo"}, {"context", "show"}, {"context", "set", "mode", "review"}, {"context", "set", "values", `{"branch":"main"}`}, {"context", "set", "project", "demo"}, {"action", "list"}, {"workflow", "list"}, {"job", "list"}, {"param", "list"}, {"param", "get", "level"}, {"param", "set", "level", "0.75"}, {"param", "set", "enabled", "true"}, {"param", "set", "choice", "b"}, {"param", "set", "text", "two words"}, {"param", "set", "count", "-10"}, {"config", "reload"}, {"action", "run", "git.status"}, {"workflow", "run", "validate"}} {
			text, _ := ctl(t, d, asJSON, 0, args...)
			if text == "" {
				t.Fatal("empty output", args)
			}
		}
		out, _ := ctl(t, d, true, 0, "action", "run", "echo")
		id := object(t, out)["id"].(string)
		ctl(t, d, asJSON, 0, "job", "show", id)
		ctl(t, d, asJSON, 0, "job", "cancel", id)
		out, _ = ctl(t, d, true, 0, "action", "run", "slow", "--async")
		id = object(t, out)["job_id"].(string)
		ctl(t, d, asJSON, 0, "job", "cancel", id)
	}
	text, _ := ctl(t, d, false, 0, "param", "get", "level")
	if !strings.Contains(text, "0.75 V") {
		t.Fatal(text)
	}
	text, _ = ctl(t, d, false, 0, "workflow", "run", "validate")
	if !strings.Contains(text, "Step 2 echo: success") {
		t.Fatal(text)
	}
}

func TestTypedValuesPermissionsOutcomesAndPersistence(t *testing.T) {
	d := newDaemon(t)
	out, _ := ctl(t, d, true, 0, "action", "run", "echo", "--arg", "text=$(touch never); {{ literal }}", "--arg=count=9223372036854775807", "--arg", "enabled=true", "--arg", "level=0.25", "--arg", "choice=b")
	if !strings.Contains(out, "9223372036854775807") || !strings.Contains(out, "$(touch never)") {
		t.Fatal(out)
	}
	out, _ = ctl(t, d, true, 0, "param", "set", "count", "9223372036854775807")
	if object(t, out)["value"] != json.Number("9223372036854775807") {
		t.Fatal(out)
	}
	for _, args := range [][]string{{"param", "set", "count", "1.5"}, {"param", "set", "level", "NaN"}, {"param", "set", "level", "2"}, {"param", "set", "enabled", "yes"}, {"param", "set", "choice", "c"}, {"action", "run", "echo", "--arg", "enabled=yes"}, {"action", "run", "echo", "--arg", "unknown=secret"}, {"context", "set", "unknown", "v"}, {"context", "set", "values", `{"x":1}`}, {"context", "set", "values", `{"x":"a","x":"b"}`}, {"project", "use", "missing"}, {"job", "show", "missing"}} {
		ctl(t, d, true, 2, args...)
	}
	_, diagnostic := ctl(t, d, true, 4, "action", "run", "confirm")
	if !strings.Contains(diagnostic, "--confirm") {
		t.Fatal(diagnostic)
	}
	ctl(t, d, true, 0, "action", "run", "confirm", "--confirm")
	ctl(t, d, true, 4, "action", "run", "danger", "--confirm")
	ctl(t, d, true, 4, "workflow", "run", "protected")
	ctl(t, d, true, 0, "workflow", "run", "protected", "--confirm")
	ctl(t, d, true, 0, "project", "use", "demo")
	ctl(t, d, true, 4, "action", "run", "scoped")
	ctl(t, d, true, 0, "action", "run", "scoped", "--confirm")
	out, _ = ctl(t, d, false, 5, "workflow", "run", "fail-stop")
	if !strings.Contains(out, "Step 2 echo: skipped") {
		t.Fatal(out)
	}
	out, _ = ctl(t, d, false, 5, "workflow", "run", "fail-continue")
	if !strings.Contains(out, "Step 2 echo: success") {
		t.Fatal(out)
	}
	out, _ = ctl(t, d, true, 5, "action", "run", "fail")
	ctl(t, d, true, 0, "job", "show", object(t, out)["id"].(string))
	ctl(t, d, true, 124, "action", "run", "slow", "--timeout", "10ms")
	out, _ = ctl(t, d, false, 0, "action", "run", "echo", "--arg", "text="+strings.Repeat("x", 200))
	if !strings.Contains(out, "[output truncated]") {
		t.Fatal(out)
	}
	ctl(t, d, true, 0, "context", "set", "mode", "persisted")
	d.stop(t)
	d.start(t)
	out, _ = ctl(t, d, true, 0, "context", "show")
	state := object(t, out)
	if state["project"] != "demo" || state["mode"] != "persisted" {
		t.Fatal(state)
	}
	out, _ = ctl(t, d, true, 0, "param", "get", "count")
	if object(t, out)["value"] != json.Number("9223372036854775807") {
		t.Fatal(out)
	}
	before, _ := ctl(t, d, true, 0, "status")
	d.write(t, "invalid: true")
	ctl(t, d, true, 1, "config", "reload")
	after, _ := ctl(t, d, true, 0, "status")
	if object(t, before)["generation"] != object(t, after)["generation"] {
		t.Fatal("invalid reload changed generation")
	}
	d.write(t, d.text)
	ctl(t, d, true, 0, "config", "reload")
}

func TestOptionFailuresAreOfflineAndDoNotExposeValues(t *testing.T) {
	for _, args := range [][]string{{}, {"--unknown", "--json"}, {"--json", "--json", "status"}, {"action", "run", "a", "--arg", "x=secret", "--arg", "x=other"}, {"status", "--async"}, {"workflow", "run", "a", "--arg", "x=secret"}, {"config", "reload", "--config", "secret"}, {"config", "validate", "--socket", "secret"}, {"status", "--timeout", "1s"}, {"status", "--request-timeout", "secret"}, {"status", "--socket"}, {"status", "--max-response-bytes", "1"}, {"--version", "status"}, {"--version", "--socket", "secret"}, {"job", "show", "../secret"}, {"status", "--confirm=secret"}} {
		var out, diagnostic bytes.Buffer
		code := Run("deckctl", append([]string{"--json"}, args...), &out, &diagnostic)
		if code != 2 || !json.Valid(out.Bytes()) || strings.Contains(out.String()+diagnostic.String(), "secret") {
			t.Fatal(args, code, out.String(), diagnostic.String())
		}
	}
	o, cmd, err := parseOptions([]string{"param", "set", "text", "--", "--json"})
	if err != nil || o.json || cmd[3] != "--json" {
		t.Fatal(o, cmd, err)
	}
	if Run("deckctl", []string{"--help"}, failedWriter{}, io.Discard) != 1 {
		t.Fatal("help output failure")
	}
}

func TestCLIProcessHelper(t *testing.T) {
	payload := os.Getenv("PATCHBAY_CLI_TEST_ARGS")
	if payload == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(payload), &args); err != nil {
		os.Exit(99)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := RunContext(ctx, "deckctl", args, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func TestIncompleteCommandsFailBeforeDispatch(t *testing.T) {
	for _, command := range [][]string{
		{"status"}, {"project", "list"}, {"project", "current"}, {"project", "use", "demo"},
		{"context", "show"}, {"context", "set", "mode", "review"},
		{"action", "list"}, {"action", "run", "echo"}, {"workflow", "list"}, {"workflow", "run", "validate"},
		{"job", "list"}, {"job", "show", "test"}, {"job", "cancel", "test"},
		{"param", "list"}, {"param", "get", "count"}, {"param", "set", "count", "1"},
		{"config", "validate"}, {"config", "reload"},
	} {
		for n := 0; n < len(command); n++ {
			var out, diagnostic bytes.Buffer
			args := append([]string{"--json", "--socket", filepath.Join(t.TempDir(), "absent")}, command[:n]...)
			if code := Run("deckctl", args, &out, &diagnostic); code != 2 || !json.Valid(out.Bytes()) {
				t.Fatal(command[:n], code, out.String(), diagnostic.String())
			}
		}
	}
}
func TestCtrlCCancelsOnlyTheAdmittedJob(t *testing.T) {
	d := newDaemon(t)
	exe, _ := os.Executable()
	payload, _ := json.Marshal([]string{"--socket", d.socket, "--json", "action", "run", "slow"})
	cmd := exec.Command(exe, "-test.run=^TestCLIProcessHelper$")
	cmd.Env = append(os.Environ(), "PATCHBAY_CLI_TEST_ARGS="+string(payload))
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	id := ""
	for id == "" {
		select {
		case <-deadline.C:
			t.Fatal("CLI did not admit job")
		case <-ticker.C:
			text, _ := ctl(t, d, true, 0, "job", "list")
			jobs := object(t, text)["jobs"].([]any)
			if len(jobs) > 0 {
				id = jobs[0].(map[string]any)["id"].(string)
			}
		}
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil || cmd.ProcessState.ExitCode() != 130 {
			t.Fatal(err, out.String(), diagnostic.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CLI cancellation hung")
	}
	response := object(t, out.String())
	if response["job_id"] != id || response["error"].(map[string]any)["code"] != "cancelled" {
		t.Fatal(response)
	}
	for {
		text, _ := ctl(t, d, true, 0, "job", "show", id)
		if object(t, text)["state"] == "cancelled" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("daemon did not cancel job")
		case <-ticker.C:
		}
	}
}
