package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"patchbay/internal/action"
	"patchbay/internal/api"
	"patchbay/internal/config"
	"patchbay/internal/provider"
	"patchbay/internal/recipe"
	runtimecore "patchbay/internal/runtime"
	"patchbay/internal/supervisor"
	"syscall"
	"testing"
	"time"
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

type browserAgent struct{}

func (browserAgent) Health(context.Context) provider.Health { return provider.Health{Available: true} }
func (browserAgent) Run(_ context.Context, request provider.AgentRequest, _ *provider.Budget, publish func(action.Result)) (action.Result, error) {
	var input struct {
		Items []supervisor.Item `json:"selected_context"`
	}
	_ = json.Unmarshal([]byte(request.FrozenInput), &input)
	refs := []string{"unsupported"}
	for _, item := range input.Items {
		refs = append(refs, item.ID)
	}
	out, _ := json.Marshal(supervisor.Output{SchemaVersion: 1, Summary: "<script>window.agentInjected=true</script> Model interpretation only.", ContextRefs: refs, Proposals: []supervisor.Suggestion{}})
	publish(action.Result{Status: "running", Data: map[string]any{"stdout": "partial JSON"}})
	return action.Result{Status: action.Success, Data: map[string]any{"stdout": string(out), "usage": map[string]int64{"input_tokens": 20, "output_tokens": 10, "total_tokens": 30}}}, nil
}
func TestServeAgentBrowser(t *testing.T) {
	path := os.Getenv("PATCHBAY_BROWSER_AGENT_CONFIG")
	if path == "" {
		t.Skip("browser harness only")
	}
	runtime, err := runtimecore.New(path, runtimecore.Options{Agent: browserAgent{}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Close(ctx)
	}()
	configuration, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	socket := configuration.Server.Socket
	listener, err := api.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: api.NewHandler(runtime), ReadHeaderTimeout: time.Second}
	go func() { _ = httpServer.Serve(listener) }()
	defer func() { _ = httpServer.Close() }()
	server, err := Start(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	ready := os.NewFile(3, "agent-browser-ready")
	if _, err := fmt.Fprintln(ready, server.URL()); err != nil {
		t.Fatal(err)
	}
	_ = ready.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
}
