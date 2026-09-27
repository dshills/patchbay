package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"patchbay/internal/action"
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The Go test binary is the compiled subprocess fixture, including its child.
func TestProcessHelper(t *testing.T) {
	if os.Getenv("PATCHBAY_PROCESS_HELPER") != "1" {
		return
	}
	mode := os.Getenv("PATCHBAY_HELPER_MODE")
	switch mode {
	case "output":
		fmt.Print(strings.Repeat("o", 200000))
		fmt.Fprint(os.Stderr, strings.Repeat("e", 200000))
	case "fail":
		fmt.Fprint(os.Stderr, "planted-secret")
		os.Exit(7)
	case "args":
		fmt.Print(os.Args[len(os.Args)-1])
		fmt.Fprint(os.Stderr, os.Getenv("PATCHBAY_TEST_VALUE"))
	case "child":
		time.Sleep(time.Hour)
	case "tree", "orphan":
		executable, _ := os.Executable()
		cmd := exec.Command(executable, "-test.run=^TestProcessHelper$")
		cmd.Env = append(os.Environ(), "PATCHBAY_HELPER_MODE=child")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("PATCHBAY_PID_FILE"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
			os.Exit(3)
		}
		if mode == "tree" {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
}

func helper(t *testing.T, mode string) Command {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Path: path, Args: []string{"-test.run=^TestProcessHelper$"}, Dir: t.TempDir(), Env: append(os.Environ(), "PATCHBAY_PROCESS_HELPER=1", "PATCHBAY_HELPER_MODE="+mode)}
}
func TestProcessOutputExitAndArguments(t *testing.T) {
	runner := ProcessRunner{}
	budget := NewBudget(1024)
	result, err := runner.Run(context.Background(), helper(t, "output"), budget)
	if err != nil || result.Status != action.Success || len(result.Data["stdout"].(string))+len(result.Data["stderr"].(string)) != 1024 || result.Data["truncated"] != true {
		t.Fatal(result, err)
	}
	result, err = runner.Run(context.Background(), helper(t, "output"), budget)
	if err != nil || result.Data["stdout"] != "" || result.Data["stderr"] != "" {
		t.Fatal("workflow budget not shared", result, err)
	}
	result, err = runner.Run(context.Background(), helper(t, "fail"), NewBudget(100))
	if err == nil || fault.Safe(err).Code != protocol.ExecutionFailed || strings.Contains(err.Error(), "planted-secret") || result.Data["exit_code"] != 7 {
		t.Fatal(result, err)
	}
	c := helper(t, "args")
	argument := `$(touch never); spaces "quotes"`
	c.Args = append(c.Args, "--", argument)
	c.Env = append(c.Env, "PATCHBAY_TEST_VALUE=overlay")
	result, err = runner.Run(context.Background(), c, NewBudget(1000))
	if err != nil || result.Data["stdout"] != argument || result.Data["stderr"] != "overlay" {
		t.Fatal(result, err)
	}
	c.Path = "/nonexistent/deckd-executable"
	if _, err = runner.Run(context.Background(), c, NewBudget(100)); err == nil || fault.Safe(err).Code != protocol.ProviderUnavailable {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = runner.Run(ctx, c, NewBudget(100)); err == nil || fault.Safe(err).Code != protocol.Cancelled {
		t.Fatal(err)
	}
}

func TestProcessGroupCancellationAndInheritedPipes(t *testing.T) {
	for _, mode := range []string{"tree", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			c := helper(t, mode)
			pidfile := filepath.Join(c.Dir, "pid")
			c.Env = append(c.Env, "PATCHBAY_PID_FILE="+pidfile)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := (ProcessRunner{}).Run(ctx, c, NewBudget(100)); done <- err }()
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			var pid int
			for pid == 0 {
				select {
				case <-deadline.C:
					t.Fatal("child did not start")
				case <-tick.C:
					data, _ := os.ReadFile(pidfile)
					pid, _ = strconv.Atoi(string(data))
				}
			}
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			if mode == "tree" {
				cancel()
			}
			select {
			case err := <-done:
				want := protocol.ExecutionFailed
				if mode == "tree" {
					want = protocol.Cancelled
				}
				if err == nil || fault.Safe(err).Code != want {
					t.Fatal(err)
				}
			case <-deadline.C:
				t.Fatal("process/pipe cleanup hung")
			}
			for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				select {
				case <-deadline.C:
					t.Fatal("descendant survived", pid)
				case <-tick.C:
				}
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := (ProcessRunner{}).Run(ctx, helper(t, "child"), NewBudget(10))
	if err == nil || fault.Safe(err).Code != protocol.Timeout {
		t.Fatal(err)
	}
}

func TestResolveExecutableUsesPreparedPATH(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "tool")
	if err := os.WriteFile(tool, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tool", "./tool", tool} {
		got, err := ResolveExecutable(name, dir, []string{"PATH=."})
		if err != nil || got != tool {
			t.Fatal(got, err)
		}
	}
	if _, err := ResolveExecutable("missing", dir, []string{"PATH=."}); err == nil {
		t.Fatal("missing")
	}
	if _, err := ResolveExecutable("", dir, nil); err == nil {
		t.Fatal("empty")
	}
	if err := os.Chmod(tool, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExecutable(tool, dir, nil); err == nil {
		t.Fatal("not executable")
	}
}
