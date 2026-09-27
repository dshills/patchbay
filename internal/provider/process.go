package provider

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

type Command struct {
	Path string
	Args []string
	Dir  string
	Env  []string
}

type Runner interface {
	Run(context.Context, Command, *Budget) (action.Result, error)
}
type ProcessRunner struct{}

// Budget is shared by all steps in a single job. Its mutex also serializes
// stdout/stderr writes to their separate buffers.
type Budget struct {
	mu        sync.Mutex
	remaining int64
}

func NewBudget(limit int64) *Budget { return &Budget{remaining: max(0, limit)} }

type capture struct {
	budget    *Budget
	buffer    bytes.Buffer
	truncated bool
}

func (w *capture) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	n := min(int64(len(p)), w.budget.remaining)
	_, _ = w.buffer.Write(p[:n])
	w.budget.remaining -= n
	w.truncated = w.truncated || n < int64(len(p))
	return len(p), nil
}

// Run owns the subprocess and os/exec's pipe readers until Wait returns.
func (ProcessRunner) Run(ctx context.Context, command Command, budget *Budget) (action.Result, error) {
	if err := ctx.Err(); err != nil {
		return action.Result{}, fault.Safe(err)
	}
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir, cmd.Env = command.Dir, command.Env
	cmd.WaitDelay = 250 * time.Millisecond
	configureProcessGroup(cmd)
	out, diagnostic := &capture{budget: budget}, &capture{budget: budget}
	cmd.Stdout, cmd.Stderr = out, diagnostic
	err := cmd.Run()
	if err != nil {
		killProcessGroup(cmd)
	}
	exit := -1
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	result := action.Result{Status: action.Success, Message: "Completed.", Data: map[string]any{
		"exit_code": exit, "stdout": out.buffer.String(), "stderr": diagnostic.buffer.String(), "truncated": out.truncated || diagnostic.truncated,
	}}
	if err == nil {
		return result, nil
	}
	result.Status, result.Message = action.Failed, "Command failed."
	if ctx.Err() != nil {
		return result, fault.Safe(ctx.Err())
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) || errors.Is(err, exec.ErrWaitDelay) {
		return result, fault.New(protocol.ExecutionFailed, "Command failed or left output pipes open.")
	}
	return result, fault.New(protocol.ProviderUnavailable, "Executable or working directory is unavailable.")
}

// ResolveExecutable uses the prepared environment's PATH, then pins an absolute
// executable path. Relative PATH entries are resolved against the prepared cwd.
func ResolveExecutable(name, cwd string, environment []string) (string, error) {
	check := func(path string) (string, bool) {
		info, err := os.Stat(path)
		return path, err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
	}
	if strings.ContainsRune(name, 0) || name == "" {
		return "", fault.New(protocol.InvalidRequest, "Invalid executable.")
	}
	if strings.Contains(name, "/") {
		if !filepath.IsAbs(name) {
			name = filepath.Join(cwd, name)
		}
		if path, ok := check(name); ok {
			return path, nil
		}
	} else {
		pathValue := ""
		for _, value := range environment {
			if strings.HasPrefix(value, "PATH=") {
				pathValue = strings.TrimPrefix(value, "PATH=")
			}
		}
		for _, dir := range filepath.SplitList(pathValue) {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(cwd, dir)
			}
			if path, ok := check(filepath.Join(dir, name)); ok {
				return path, nil
			}
		}
	}
	return "", fault.New(protocol.ProviderUnavailable, "Configured executable is unavailable.")
}
