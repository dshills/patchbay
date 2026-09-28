package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"patchbay/internal/firstuse"
	"path/filepath"

	"patchbay/internal/workbench"
)

func runWorkbench(ctx context.Context, socket string, stdout, stderr io.Writer) int {
	return runWorkbenchOwned(ctx, socket, false, stdout, stderr)
}
func runWorkbenchOwned(ctx context.Context, socket string, owned bool, stdout, stderr io.Writer) int {
	server, err := workbench.StartOwned(socket, owned)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "Cannot start workbench:", err)
		return 1
	}
	defer func() { _ = server.Close() }()
	// The fragment is passed only to the browser opener, never printed or logged.
	if err := exec.CommandContext(ctx, "/usr/bin/open", server.URL()).Run(); err != nil {
		_, _ = fmt.Fprintln(stderr, "Cannot open the browser. Check that a default browser is configured.")
		return 1
	}
	message := "Patchbay workbench opened. Keep this process running; Ctrl-C closes browser access. Daemon jobs continue running."
	if owned {
		message = "Patchbay demo opened. Keep this process running. Quit demo or Ctrl-C stops this session’s daemon and cancels its active jobs. Saved results remain in the demo workspace."
	}
	_, _ = fmt.Fprintln(stdout, message)
	select {
	case <-ctx.Done():
	case <-server.Done():
	}
	return 0
}

func runDemo(ctx context.Context, directory string, stdout, stderr io.Writer) int {
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "Cannot locate the Patchbay bundle.")
		return 1
	}
	session, err := firstuse.Start(ctx, directory, filepath.Dir(executable))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "Cannot start the demo:", err)
		return 1
	}
	code := runWorkbenchOwned(ctx, session.Socket, session.Owned, stdout, stderr)
	if err := session.Close(); err != nil {
		_, _ = fmt.Fprintln(stderr, "Demo shutdown:", err)
		return 1
	}
	return code
}
