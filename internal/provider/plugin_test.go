package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"patchbay/internal/fault"
	"patchbay/internal/permission"
	"patchbay/internal/plugintest"
	wire "patchbay/pkg/plugin"
	"patchbay/pkg/protocol"
)

func TestPluginChild(t *testing.T) { plugintest.Child() }
func pluginFixture(t *testing.T, mode string) PluginConfig {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := PluginConfig{Command: executable, Args: []string{"-test.run=^TestPluginChild$"}, Cwd: t.TempDir(), Environment: map[string]string{"PATCHBAY_PLUGIN_FIXTURE": mode, "GORACE": "atexit_sleep_ms=0"}, Operations: map[string]permission.Permission{"echo": permission.Confirm, "wait": permission.Confirm}, StartupTimeout: "500ms", ExecutionTimeout: "3s", CancelTimeout: "200ms", ShutdownTimeout: "200ms"}
	if err = c.Normalize(); err != nil {
		t.Fatal(err)
	}
	return c
}
func pluginRequest() PluginRequest {
	return PluginRequest{Plugin: "example", Operation: "echo", Args: map[string]any{"text": "hello"}, Confirmed: true}
}
func TestPluginLifecycleDiscoveryAndBudget(t *testing.T) {
	h := NewPlugins(map[string]PluginConfig{"example": pluginFixture(t, "good")})
	if h.Snapshot("example").Health.Code != "not_checked" {
		t.Fatal(h.Snapshot("example"))
	}
	result, err := h.Run(context.Background(), pluginRequest(), NewBudget(3))
	if err != nil || result.Data["stdout"] != "hel" || result.Data["truncated"] != true || result.Data["shutdown_clean"] != true {
		t.Fatal(result, err)
	}
	snapshot := h.Snapshot("example")
	if !snapshot.Health.Available || len(snapshot.Operations) != 2 {
		t.Fatal(snapshot)
	}
	snapshot.Operations[0].Name = "mutated"
	if h.Snapshot("example").Operations[0].Name == "mutated" {
		t.Fatal("mutable snapshot")
	}
	if err = h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Run(context.Background(), pluginRequest(), NewBudget(100)); fault.Safe(err).Code != protocol.ShuttingDown {
		t.Fatal(err)
	}
}
func TestPluginFailureIsolation(t *testing.T) {
	for _, mode := range []string{"exit", "hang", "malformed", "oversize", "partial", "stderr", "wrong_id", "future", "unknown", "duplicate_key", "duplicate", "extra", "missing", "version", "unhealthy", "health_missing", "forge", "null", "early_exit", "refuse_shutdown", "shutdown_extra", "shutdown_exit", "failed"} {
		t.Run(mode, func(t *testing.T) {
			c := pluginFixture(t, mode)
			h := NewPlugins(map[string]PluginConfig{"example": c, "healthy": pluginFixture(t, "good")})
			started := time.Now()
			result, err := h.Run(context.Background(), pluginRequest(), NewBudget(1024))
			if err == nil || result.Status == "success" || time.Since(started) > 2*time.Second {
				t.Fatal(result, err, time.Since(started))
			}
			if strings.Contains(fmt.Sprint(result, err), "secret") {
				t.Fatal("stderr leaked")
			}
			r := pluginRequest()
			r.Plugin = "healthy"
			if _, err := h.Run(context.Background(), r, NewBudget(1024)); err != nil {
				t.Fatal("failure broke independent plugin", err)
			}
		})
	}
}
func TestPluginPermissionsAndInputRefuseBeforeSpawn(t *testing.T) {
	c := pluginFixture(t, "good")
	trace := filepath.Join(t.TempDir(), "trace")
	c.Environment["PATCHBAY_PLUGIN_TRACE"] = trace
	h := NewPlugins(map[string]PluginConfig{"example": c})
	r := pluginRequest()
	r.Confirmed = false
	if _, err := h.Run(context.Background(), r, NewBudget(100)); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal(err)
	}
	r.Confirmed = true
	r.Operation = "undeclared"
	if _, err := h.Run(context.Background(), r, NewBudget(100)); fault.Safe(err).Code != protocol.InvalidRequest {
		t.Fatal(err)
	}
	r = pluginRequest()
	r.Args["text"] = strings.Repeat("x", 70000)
	if _, err := h.Run(context.Background(), r, NewBudget(100)); fault.Safe(err).Code != protocol.InvalidRequest {
		t.Fatal(err)
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatal("denied invocation spawned plugin")
	}
	c = pluginFixture(t, "dangerous")
	c.Environment["PATCHBAY_PLUGIN_TRACE"] = trace
	h = NewPlugins(map[string]PluginConfig{"example": c})
	if _, err := h.Run(context.Background(), pluginRequest(), NewBudget(100)); fault.Safe(err).Code != protocol.PermissionDenied {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(trace)
	if strings.Contains(string(data), "execute") {
		t.Fatal(string(data))
	}
	r = pluginRequest()
	r.AllowDangerous = true
	if _, err := h.Run(context.Background(), r, NewBudget(100)); err != nil {
		t.Fatal(err)
	}
}
func TestPluginExplicitEnvironmentOnly(t *testing.T) {
	t.Setenv("PATCHBAY_PLUGIN_SECRET", "never-inherit")
	c := pluginFixture(t, "environment")
	c.Environment["EXPLICIT"] = "allowed"
	h := NewPlugins(map[string]PluginConfig{"example": c})
	result, err := h.Run(context.Background(), pluginRequest(), NewBudget(100))
	if err != nil || result.Data["stdout"] != "/allowed" {
		t.Fatal(result, err)
	}
}
func TestPluginCancellationSerializationAndClose(t *testing.T) {
	for _, mode := range []string{"good", "refuse_cancel", "cancel_result"} {
		t.Run(mode, func(t *testing.T) {
			c := pluginFixture(t, mode)
			h := NewPlugins(map[string]PluginConfig{"example": c})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			done := make(chan struct{})
			r := pluginRequest()
			r.Operation = "wait"
			r.Started = func() { close(started) }
			var resultStatus string
			var cooperative any
			var runErr error
			go func() {
				result, err := h.Run(ctx, r, NewBudget(100))
				resultStatus = string(result.Status)
				cooperative = result.Data["cooperative_cancel"]
				runErr = err
				close(done)
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("not started")
			}
			queued, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer stop()
			if _, err := h.Run(queued, pluginRequest(), NewBudget(100)); fault.Safe(err).Code != protocol.Timeout {
				t.Fatal(err)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancellation hung")
			}
			if fault.Safe(runErr).Code != protocol.Cancelled || resultStatus != "cancelled" || cooperative != (mode == "good") {
				t.Fatal(runErr, resultStatus, cooperative)
			}
			if _, err := h.Run(context.Background(), pluginRequest(), NewBudget(100)); err != nil {
				t.Fatal("slot not released", err)
			}
			if err := h.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPluginBlockedStdinAndHostShutdown(t *testing.T) {
	for _, mode := range []string{"no_read", "refuse_cancel"} {
		t.Run(mode, func(t *testing.T) {
			c := pluginFixture(t, mode)
			c.ExecutionTimeout = "200ms"
			h := NewPlugins(map[string]PluginConfig{"example": c})
			r := pluginRequest()
			r.Args["text"] = strings.Repeat("x", 60000)
			if mode == "refuse_cancel" {
				r.Operation = "wait"
			}
			done := make(chan error, 1)
			go func() { _, err := h.Run(context.Background(), r, NewBudget(100)); done <- err }()
			select {
			case err := <-done:
				if fault.Safe(err).Code != protocol.Timeout {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("hung child blocked completion")
			}
			if err := h.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPluginCloseCancelsActiveChildAndIndependentPluginRuns(t *testing.T) {
	h := NewPlugins(map[string]PluginConfig{"example": pluginFixture(t, "refuse_cancel"), "other": pluginFixture(t, "good")})
	started, done := make(chan struct{}), make(chan error, 1)
	r := pluginRequest()
	r.Operation = "wait"
	r.Started = func() { close(started) }
	go func() { _, err := h.Run(context.Background(), r, NewBudget(100)); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	other := pluginRequest()
	other.Plugin = "other"
	if _, err := h.Run(context.Background(), other, NewBudget(100)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; fault.Safe(err).Code != protocol.Cancelled {
		t.Fatal(err)
	}
}

func TestPluginCleanupKillsInheritedPipeDescendant(t *testing.T) {
	c := pluginFixture(t, "descendant")
	pidFile := filepath.Join(t.TempDir(), "pid")
	c.Environment["PATCHBAY_PLUGIN_DESCENDANT"] = pidFile
	h := NewPlugins(map[string]PluginConfig{"example": c})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := pluginRequest()
	r.Operation = "wait"
	done := make(chan error, 1)
	go func() { _, err := h.Run(ctx, r, NewBudget(100)); done <- err }()
	var pid int
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(string(data))
		if pid > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("descendant did not start")
	}
	cancel()
	select {
	case err := <-done:
		if fault.Safe(err).Code != protocol.Cancelled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("inherited pipe hung cleanup")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		output, err := exec.Command("/bin/ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err != nil || strings.HasPrefix(strings.TrimSpace(string(output)), "Z") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("descendant remained running")
}

func TestPluginConcurrentFailureCleanupIsSingleUse(t *testing.T) {
	p, err := startPlugin(pluginFixture(t, "hang"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { p.fail(pluginFailure()) })
	}
	wg.Wait()
	p.cleanup()
	select {
	case <-p.broken:
	default:
		t.Fatal("failure was not published")
	}
}

func TestPluginFailureInterruptsShutdownJoins(t *testing.T) {
	p, err := startPlugin(pluginFixture(t, "hang_after_shutdown"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var hello wire.HelloResponse
	if err = p.call(ctx, "hello", wire.HelloRequest{Versions: []int{1}}, &hello); err != nil {
		t.Fatal(err)
	}
	var health wire.Health
	if err = p.call(ctx, "health", struct{}{}, &health); err != nil {
		t.Fatal(err)
	}
	var result wire.Result
	if err = p.call(ctx, "execute", wire.Execute{Operation: "echo", Args: map[string]any{"text": "done"}}, &result); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.shutdown(ctx) }()
	deadline := time.Now().Add(time.Second)
	for !p.shutdownAck.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !p.shutdownAck.Load() {
		t.Fatal("shutdown reply did not arrive")
	}
	// The child acknowledged but is still alive. Failure must interrupt the joins
	// immediately, not wait for the five-second context deadline.
	p.fail(pluginFailure())
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("shutdown ignored failure")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("failure did not interrupt shutdown")
	}
}
