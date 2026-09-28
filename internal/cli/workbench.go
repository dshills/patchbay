package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"patchbay/internal/workbench"
)

func runWorkbench(ctx context.Context, socket string, stdout, stderr io.Writer) int {
	server, err := workbench.Start(socket)
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
	_, _ = fmt.Fprintln(stdout, "Patchbay workbench opened. Keep this process running; Ctrl-C closes browser access. Daemon jobs continue running.")
	select {
	case <-ctx.Done():
	case <-server.Done():
	}
	return 0
}
