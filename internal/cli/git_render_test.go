package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"patchbay/internal/action"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
)

func TestStructuredGitRenderingThroughJSONTransport(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for _, tc := range []struct {
		operation, raw, want string
	}{
		{"status", "R  new name\x00old name\x00", `R  "new name" (from "old name")`},
		{"log", hash + "\x00A subject\x00\n", hash + " A subject"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			result := provider.GitResult(tc.operation, action.Result{Status: action.Success, Data: map[string]any{"stdout": tc.raw}})
			d := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/jobs/git-result" {
					t.Error("unexpected request", r.Method, r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(protocol.Job{ID: "git-result", State: "success", Result: &protocol.Result{Status: "success", Data: result.Data}})
			})
			out, _ := ctl(t, d, false, 0, "job", "show", "git-result")
			if !strings.Contains(out, tc.want) || strings.ContainsAny(out, "\x00") || strings.Contains(out, "stdout:") {
				t.Fatal(out)
			}
		})
	}
}

func TestGitRendererHandlesUnexpectedRecordTypes(t *testing.T) {
	for _, key := range []string{"git_status", "git_log"} {
		for _, value := range []any{nil, "unexpected", []any{nil, "unexpected"}} {
			var out strings.Builder
			renderData(&out, map[string]any{key: value, "stderr": "diagnostic"}, "")
			if !strings.Contains(out.String(), "diagnostic") {
				t.Fatal("lost diagnostic for malformed record", out.String())
			}
		}
	}
}
