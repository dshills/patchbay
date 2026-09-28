package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

type agentTransport func(*http.Request) (*http.Response, error)

func (f agentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func agentFixture(t *testing.T) AgentRequest {
	t.Helper()
	root := t.TempDir()
	return AgentRequest{Model: "test-model", Prompt: "Review selected context.", Project: root, Dir: root, MaxOutputTokens: 256}
}
func sse(value any) string { data, _ := json.Marshal(value); return "data: " + string(data) + "\n\n" }
func delta(text string) string {
	return sse(map[string]any{"type": "response.output_text.delta", "delta": text})
}
func completed() string {
	return sse(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []map[string]string{{"type": "message"}}}})
}
func fakeCodex(body string, check func(*http.Request)) *Codex {
	return &Codex{key: func() string { return "TEST_AGENT_SECRET" }, client: &http.Client{Transport: agentTransport(func(r *http.Request) (*http.Response, error) {
		if check != nil {
			check(r)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
}

func TestAgentRequestAndStreamBoundaries(t *testing.T) {
	request := agentFixture(t)
	request.Files = []string{"main.go"}
	if err := os.WriteFile(filepath.Join(request.Dir, "main.go"), []byte("package example"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	agent := fakeCodex(": keepalive\n\n"+delta("A useful ")+delta("result.")+completed(), func(r *http.Request) {
		calls++
		if r.URL.String() != "https://api.openai.com/v1/responses" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer TEST_AGENT_SECRET" || r.GetBody != nil {
			t.Fatal("invalid wire contract")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] != true || body["store"] != false || body["tool_choice"] != "none" || len(body["tools"].([]any)) != 0 || body["max_output_tokens"] != float64(256) {
			t.Fatal(body)
		}
		if !strings.Contains(body["input"].(string), "package example") || strings.Contains(body["input"].(string), "TEST_AGENT_SECRET") {
			t.Fatal("context or credential handling")
		}
	})
	partial := []action.Result{}
	result, err := agent.Run(context.Background(), request, NewBudget(12), func(r action.Result) { partial = append(partial, r) })
	if err != nil || calls != 1 || result.Status != action.Success || result.Data["stdout"] != "A useful res" || result.Data["truncated"] != true || len(partial) == 0 {
		t.Fatal(result, err, calls, partial)
	}
	if partial[0].Data["stdout"] != "A useful " {
		t.Fatal("partial snapshots changed", partial)
	}
}

func TestAgentStreamRejectsFailuresAndToolCalls(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       protocol.Code
	}{
		{"malformed", "data: {bad}\n\n", protocol.ExecutionFailed},
		{"missing delta", sse(map[string]any{"type": "response.output_text.delta"}), protocol.ExecutionFailed},
		{"missing terminal", delta("partial"), protocol.ExecutionFailed},
		{"empty", completed(), protocol.ExecutionFailed},
		{"failed", sse(map[string]any{"type": "response.failed"}), protocol.ExecutionFailed},
		{"incomplete", sse(map[string]any{"type": "response.incomplete"}), protocol.ExecutionFailed},
		{"refused", sse(map[string]any{"type": "response.refusal.delta"}), protocol.ExecutionFailed},
		{"error", sse(map[string]any{"type": "error", "message": "TEST_AGENT_SECRET"}), protocol.ExecutionFailed},
		{"tool", sse(map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "function_call"}}), protocol.PermissionDenied},
		{"completed tool", delta("x") + sse(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []map[string]string{{"type": "shell_call"}}}}), protocol.PermissionDenied},
		{"oversized", "data: " + strings.Repeat("x", maxAgentEvent+1) + "\n\n", protocol.ExecutionFailed},
		{"sequence", sse(map[string]any{"type": "response.created", "sequence_number": 2}) + sse(map[string]any{"type": "response.in_progress", "sequence_number": 1}), protocol.ExecutionFailed},
		{"total limit", strings.Repeat(": padding\n\n", maxAgentStream/10+1), protocol.ExecutionFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := fakeCodex(tc.body, nil).Run(context.Background(), agentFixture(t), NewBudget(100), nil)
			if err == nil || fault.Safe(err).Code != tc.code || strings.Contains(err.Error(), "TEST_AGENT_SECRET") || result.Status == action.Success {
				t.Fatal(result, err)
			}
		})
	}
}

func TestAgentRedactsSplitCredential(t *testing.T) {
	result, err := fakeCodex(delta("hello TEST_AGENT_")+delta("SECRET world")+completed(), nil).Run(context.Background(), agentFixture(t), NewBudget(100), func(r action.Result) {
		if strings.Contains(r.Data["stdout"].(string), "TEST_AGENT_") {
			t.Fatal("leaked key prefix")
		}
	})
	if err != nil || result.Data["stdout"] != "hello [redacted] world" {
		t.Fatal(result, err)
	}
	t.Run("one byte per event", func(t *testing.T) {
		var body strings.Builder
		for _, part := range []byte("hello TEST_AGENT_SECRETTEST_AGENT_SECRET world") {
			body.WriteString(delta(string(part)))
		}
		body.WriteString(completed())
		result, err := fakeCodex(body.String(), nil).Run(context.Background(), agentFixture(t), NewBudget(100), func(r action.Result) {
			if strings.ContainsAny(r.Data["stdout"].(string), "TEST_AGENT_SECRET") {
				t.Fatal("leaked credential fragment")
			}
		})
		if err != nil || result.Data["stdout"] != "hello [redacted][redacted] world" {
			t.Fatal(result, err)
		}
	})
	t.Run("short credentials remain secret", func(t *testing.T) {
		c := fakeCodex(delta("hello k")+delta("y world")+completed(), nil)
		c.key = func() string { return "ky" }
		result, err := c.Run(context.Background(), agentFixture(t), NewBudget(100), nil)
		if err != nil || result.Data["stdout"] != "hello [redacted] world" {
			t.Fatal(result, err)
		}
	})
}

func TestAgentMissingCredentialsTransportAndHTTPFailures(t *testing.T) {
	for _, status := range []int{301, 400, 401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			c := fakeCodex("", nil)
			c.client.Transport = agentTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("TEST_AGENT_SECRET"))}, nil
			})
			_, err := c.Run(context.Background(), agentFixture(t), NewBudget(100), nil)
			if err == nil || calls != 1 || strings.Contains(err.Error(), "TEST_AGENT_SECRET") {
				t.Fatal(err, calls)
			}
		})
	}
	c := fakeCodex("", nil)
	c.key = func() string { return "" }
	if c.Health(context.Background()).Available {
		t.Fatal("missing credential healthy")
	}
	if _, err := c.Run(context.Background(), agentFixture(t), NewBudget(100), nil); fault.Safe(err).Code != protocol.ProviderUnavailable {
		t.Fatal(err)
	}
	c.key = func() string { return "TEST_AGENT_SECRET" }
	c.client.Transport = agentTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("TEST_AGENT_SECRET transport failure")
	})
	if _, err := c.Run(context.Background(), agentFixture(t), NewBudget(100), nil); err == nil || strings.Contains(err.Error(), "TEST_AGENT_SECRET") {
		t.Fatal(err)
	}
	c.client.Transport = agentTransport(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	if _, err := c.Run(context.Background(), agentFixture(t), NewBudget(100), nil); fault.Safe(err).Code != protocol.Timeout {
		t.Fatal(err)
	}
	c.client.Transport = agentTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	if _, err := c.Run(context.Background(), agentFixture(t), NewBudget(100), nil); err == nil {
		t.Fatal("accepted non-stream")
	}
	real := NewCodex()
	defer func() { _ = real.Close() }()
	if real.client.Transport.(*http.Transport).Proxy != nil || real.client.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse {
		t.Fatal("ambient proxy or redirects enabled")
	}
}

func TestAgentCancellationClosesStreamingBody(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint(timeout), func(t *testing.T) {
			c := fakeCodex("", nil)
			started := make(chan struct{})
			c.client.Transport = agentTransport(func(r *http.Request) (*http.Response, error) {
				reader, writer := io.Pipe()
				go func() { close(started); <-r.Context().Done(); _ = writer.CloseWithError(r.Context().Err()) }()
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			if timeout {
				ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
			}
			defer cancel()
			done := make(chan error, 1)
			request := agentFixture(t)
			go func() { _, err := c.Run(ctx, request, NewBudget(100), nil); done <- err }()
			<-started
			if !timeout {
				cancel()
			}
			select {
			case err := <-done:
				want := protocol.Cancelled
				if timeout {
					want = protocol.Timeout
				}
				if fault.Safe(err).Code != want {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("stream did not stop")
			}
		})
	}
}

func TestAgentContextCannotEscapeOrBlock(t *testing.T) {
	r := agentFixture(t)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.Dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(r.Dir, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "huge"), make([]byte, 128<<10+1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "binary"), []byte{0, 255}, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"escape", "fifo", "huge", "binary", ".", "../outside", "missing"} {
		r.Files = []string{name}
		if _, err := agentInput(context.Background(), r); err == nil {
			t.Fatal("accepted", name)
		}
	}
	r.Files = nil
	r.Dir = filepath.Dir(r.Project)
	if _, err := agentInput(context.Background(), r); err == nil {
		t.Fatal("escaped working directory")
	}
}

func TestCodexLiveSmoke(t *testing.T) {
	if os.Getenv("PATCHBAY_CODEX_SMOKE") != "1" {
		t.Skip("opt-in billable provider check")
	}
	model := os.Getenv("PATCHBAY_CODEX_MODEL")
	if model == "" {
		t.Fatal("PATCHBAY_CODEX_MODEL is required")
	}
	c := NewCodex()
	defer func() { _ = c.Close() }()
	r := agentFixture(t)
	r.Model = model
	r.Prompt = "Reply with the single word ready."
	r.MaxOutputTokens = 1024
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := c.Run(ctx, r, NewBudget(1024), nil)
	if err != nil || result.Status != action.Success {
		t.Fatal("live provider smoke failed", err)
	}
	t.Log("Live provider returned a completed bounded text response; no project files sent.")
}

func TestFrozenAgentInputAndActualUsage(t *testing.T) {
	request := AgentRequest{Model: "test-model", FrozenInput: "exact consented input", MaxOutputTokens: 64}
	calls := 0
	c := &Codex{key: func() string { return "test-credential" }, client: &http.Client{Transport: agentTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		var sent map[string]any
		if err := json.NewDecoder(req.Body).Decode(&sent); err != nil {
			t.Fatal(err)
		}
		if sent["input"] != request.FrozenInput || sent["store"] != false || sent["tool_choice"] != "none" || req.GetBody != nil {
			t.Fatal(sent)
		}
		stream := sse(map[string]any{"type": "response.output_text.delta", "delta": "complete"}) + sse(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "usage": map[string]int{"input_tokens": 9, "output_tokens": 2, "total_tokens": 11}}})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
	})}}
	result, err := c.Run(context.Background(), request, NewBudget(100), nil)
	if err != nil || calls != 1 || result.Data["usage"].(map[string]int64)["total_tokens"] != 11 {
		t.Fatal(result, err)
	}
	request.Files = []string{"unexpected"}
	if _, err := agentInput(context.Background(), request); err == nil {
		t.Fatal("mixed frozen and filesystem context accepted")
	}
}
