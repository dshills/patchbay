package workbench

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"patchbay/internal/recipe"
	"syscall"
	"testing"
)

// TestServeBrowser is a test-only launcher. Descriptor 3 is private IPC to the
// browser harness; session credentials never go to stdout, logs, or disk.
func TestServeBrowser(t *testing.T) {
	socket := os.Getenv("PATCHBAY_BROWSER_TEST_SOCKET")
	if socket == "" {
		t.Skip("browser harness only")
	}
	if output := os.Getenv("PATCHBAY_BROWSER_TEST_RECIPE_OUT"); output != "" {
		p, err := recipe.Inspect(context.Background(), "recipes/benchmark")
		if err != nil {
			t.Fatal(err)
		}
		p.Files["README.md"] = append(p.Files["README.md"], []byte("\n<script>window.recipeInjected=true</script><img src='https://invalid.example/pixel'>")...)
		delete(p.Files, "recipe.yaml")
		p, err = recipe.Build(p.Manifest, p.Files)
		if err != nil {
			t.Fatal(err)
		}
		data, err := recipe.ZIP(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	server, err := Start(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	ready := os.NewFile(3, "browser-ready")
	if ready == nil {
		t.Fatal("missing harness IPC")
	}
	if _, err := fmt.Fprintln(ready, server.URL()); err != nil {
		t.Fatal(err)
	}
	_ = ready.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-server.Done():
	}
}
