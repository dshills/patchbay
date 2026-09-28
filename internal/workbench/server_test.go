package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"patchbay/internal/api"
	runtimecore "patchbay/internal/runtime"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pb-web-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "sock")
	path := filepath.Join(dir, "config.yaml")
	configuration := fmt.Sprintf("version: 1\nserver: {socket: %q}\nstate: {path: %q}\nactions: {echo: {type: exec, safety: safe, command: /bin/echo, args: [hello]}}\nexperiments:\n  echo: {schema_version: 1, title: Echo, action: echo, collectors: [{name: output, step: 0, action: echo, kind: text, source: native, path: [stdout]}]}\n", socket, filepath.Join(dir, "state.json"))
	if err := os.WriteFile(path, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimecore.New(path, runtimecore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := api.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: api.NewHandler(runtime), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	workbench, err := Start(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workbench.Close(); _ = server.Close(); _ = runtime.Close(context.Background()) })
	return workbench
}
func TestBrowserBoundary(t *testing.T) {
	s := testServer(t)
	for _, tc := range []struct {
		name, method, path, token, host, origin, site, body string
		want                                                int
	}{
		{name: "page", method: "GET", path: "/", want: 200},
		{name: "unauthorized read", method: "GET", path: "/api/status", want: 401},
		{name: "authorized read", method: "GET", path: "/api/status", token: s.token, want: 200},
		{name: "wrong token", method: "GET", path: "/api/status", token: "wrong", want: 401},
		{name: "wrong host", method: "GET", path: "/api/status", token: s.token, host: "evil.test", want: 403},
		{name: "foreign origin", method: "GET", path: "/api/status", token: s.token, origin: "https://evil.test", want: 403},
		{name: "cross-site", method: "GET", path: "/api/status", token: s.token, site: "cross-site", want: 403},
		{name: "no mutation origin", method: "POST", path: "/api/captures/prepare", token: s.token, body: `{"experiment":"echo"}`, want: 403},
		{name: "valid preparation", method: "POST", path: "/api/captures/prepare", token: s.token, origin: s.origin, body: `{"experiment":"echo"}`, want: 200},
		{name: "config excluded", method: "POST", path: "/api/config/reload", token: s.token, origin: s.origin, body: `{}`, want: 404},
		{name: "direct actions excluded", method: "POST", path: "/api/actions/echo", token: s.token, origin: s.origin, body: `{}`, want: 404},
		{name: "traversal", method: "GET", path: "/api/runs/../status", token: s.token, want: 404},
		{name: "preflight excluded", method: "OPTIONS", path: "/api/captures", token: s.token, origin: s.origin, want: 403},
		{name: "duplicate JSON", method: "POST", path: "/api/captures/prepare", token: s.token, origin: s.origin, body: `{"experiment":"echo","experiment":"echo"}`, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, s.origin+tc.path, strings.NewReader(tc.body))
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			out := httptest.NewRecorder()
			s.ServeHTTP(out, req)
			if out.Code != tc.want {
				t.Fatalf("status %d: %s", out.Code, out.Body.String())
			}
			if out.Header().Get("Access-Control-Allow-Origin") != "" || out.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("unsafe browser headers")
			}
		})
	}
}
func TestRealLoopbackAndNoMutationOnLoad(t *testing.T) {
	s := testServer(t)
	host, _, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil || host != "127.0.0.1" {
		t.Fatal("listener not loopback")
	}
	response, err := http.Get(s.origin + "/")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || strings.Contains(string(data), s.token) {
		t.Fatal("asset leaked session token")
	}
	if !strings.Contains(string(data), "Review capture") {
		t.Fatal("missing workbench")
	}
	request, err := http.NewRequest("GET", s.origin+"/api/runs", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Runs []any `json:"runs"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || len(body.Runs) != 0 {
		t.Fatal("page load mutated run history")
	}
	if len(s.token) != 43 || !strings.HasSuffix(s.URL(), "/#"+s.token) {
		t.Fatal("invalid launch token")
	}
}
