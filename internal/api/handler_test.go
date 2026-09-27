package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	runtimecore "patchbay/internal/runtime"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const apiConfig = `version: 1
server: {socket: %q, max_request_bytes: 1024}
state: {path: %q, flush_interval: 5ms}
jobs: {concurrency: 1, queue_capacity: 1, history_limit: 10}
projects: {p: {name: Demo, path: .}}
actions:
  echo: {type: exec, safety: safe, command: /bin/echo, args: [hello]}
  slow: {type: exec, safety: safe, command: /bin/sleep, args: ['30']}
  fail: {type: exec, safety: safe, command: /usr/bin/false}
  confirm: {type: exec, safety: confirm, command: /bin/echo}
  danger: {type: exec, safety: dangerous, command: /bin/echo}
  absent: {type: exec, safety: safe, command: /not/a/program}
workflows: {two: {steps: [{action: echo}, {action: echo}]}}
parameters: {level: {type: integer, value: 1, min: 0, max: 10, persistent: true}}
bindings:
  - control: dial
    rotate: {parameter: level}
  - control: key
    press: {action: echo}
`

func shortDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type fixtureAPI struct {
	client     *http.Client
	runtime    *runtimecore.Runtime
	path, text string
}

func newAPI(t testing.TB) fixtureAPI {
	t.Helper()
	dir := shortDir(t)
	socket := filepath.Join(dir, "sock")
	path := filepath.Join(dir, "config.yaml")
	text := fmt.Sprintf(apiConfig, socket, filepath.Join(dir, "state.json"))
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := runtimecore.New(path, runtimecore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(socket)
	if err != nil {
		_ = r.Close(context.Background())
		t.Fatal(err)
	}
	server := &http.Server{Handler: NewHandler(r), ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := r.Close(ctx); err != nil {
			t.Error(err)
		}
		_ = server.Close()
		<-done
	})
	return fixtureAPI{client, r, path, text}
}
func request(t testing.TB, f fixtureAPI, method, path, body string, want int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(method, "http://deckd"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s: %d want %d: %s", method, path, res.StatusCode, want, data)
	}
	if res.Header.Get("Content-Type") != "application/json" {
		t.Fatal(res.Header)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err, string(data))
	}
	return value
}

func TestUnixAPIAllRoutes(t *testing.T) {
	f := newAPI(t)
	for _, path := range []string{"status", "context", "projects", "projects/p", "actions", "actions/echo", "workflows", "jobs", "parameters", "parameters/level"} {
		request(t, f, "GET", "/v1/"+path, "", 200)
	}
	request(t, f, "PATCH", "/v1/context", `{"mode":"review","values":{"tag":"main"}}`, 200)
	request(t, f, "PUT", "/v1/context/project", `{"project":"p"}`, 200)
	request(t, f, "PUT", "/v1/parameters/level", `{"value":5}`, 200)
	result := request(t, f, "POST", "/v1/actions/echo", `{"mode":"sync"}`, 200)
	id := result["job_id"].(string)
	request(t, f, "GET", "/v1/jobs/"+id, "", 200)
	request(t, f, "DELETE", "/v1/jobs/"+id, "", 200)
	request(t, f, "POST", "/v1/workflows/two", `{"mode":"sync"}`, 200)
	request(t, f, "POST", "/v1/events", `{"type":"control.rotated","source":"test","payload":{"control":"dial","delta":1}}`, 200)
	result = request(t, f, "POST", "/v1/events", `{"type":"control.pressed","source":"test","payload":{"control":"key"}}`, 202)
	if _, err := f.runtime.Jobs().Wait(context.Background(), result["job_id"].(string)); err != nil {
		t.Fatal(err)
	}
	request(t, f, "POST", "/v1/config/reload", `{}`, 200)
	if f.runtime.Status().Generation != 2 {
		t.Fatal("reload")
	}
	result = request(t, f, "POST", "/v1/actions/slow", `{}`, 202)
	running := result["job_id"].(string)
	result = request(t, f, "POST", "/v1/actions/echo", `{}`, 202)
	queued := result["job_id"].(string)
	request(t, f, "POST", "/v1/actions/echo", `{}`, 503)
	request(t, f, "DELETE", "/v1/jobs/"+queued, "", 200)
	request(t, f, "DELETE", "/v1/jobs/"+running, "", 202)
	if _, err := f.runtime.Jobs().Wait(context.Background(), running); err != nil {
		t.Fatal(err)
	}
	request(t, f, "POST", "/v1/actions/slow", `{"mode":"sync","timeout_ms":10}`, 504)
	request(t, f, "POST", "/v1/actions/fail", `{"mode":"sync"}`, 422)
}

func TestAPIRejectsMalformedAndUnauthorizedRequests(t *testing.T) {
	f := newAPI(t)
	for _, c := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/missing", "", 404}, {"POST", "/v1/status", "{}", 405}, {"GET", "/v1/status", "{}", 400},
		{"GET", "/v1/projects/missing", "", 404}, {"GET", "/v1/jobs/missing", "", 404}, {"DELETE", "/v1/jobs/missing", "", 404},
		{"GET", "/v1/parameters/missing", "", 404}, {"POST", "/v1/actions/missing", "{}", 404}, {"POST", "/v1/workflows/missing", "{}", 404},
		{"GET", "/v1/actions/missing", "", 404}, {"GET", "/v1/actions/echo", "{}", 400}, {"PUT", "/v1/actions/echo", "{}", 405},
		{"PUT", "/v1/context/project", "{}", 400}, {"PUT", "/v1/context/project", `{"project":"absent"}`, 404},
		{"PATCH", "/v1/context", `{"mode":null}`, 400}, {"PATCH", "/v1/context", `{"mode":"x","mode":"y"}`, 400},
		{"PATCH", "/v1/context", `{"values":{"tag":"x","tag":"y"}}`, 400}, {"PATCH", "/v1/context", `{"mode":"x"} {}`, 400},
		{"PATCH", "/v1/context", `{"values":{"bad.key":"x"}}`, 400},
		{"PUT", "/v1/parameters/level", `{"value":1.5}`, 400}, {"PUT", "/v1/parameters/level", `{"value":11}`, 400}, {"PUT", "/v1/parameters/level", `{}`, 400},
		{"POST", "/v1/actions/echo", `{"context":{"mode":"admin"}}`, 400}, {"POST", "/v1/actions/echo", `{"timeout_ms":0}`, 400},
		{"POST", "/v1/actions/echo", `{"mode":"later"}`, 400}, {"POST", "/v1/actions/echo", `{"args":{"command":"rm"}}`, 400},
		{"POST", "/v1/actions/echo", `{"args":null}`, 400}, {"POST", "/v1/actions/echo", "", 400}, {"POST", "/v1/actions/echo", strings.Repeat(" ", 1025), 413},
		{"POST", "/v1/actions/confirm", "{}", 409}, {"POST", "/v1/actions/danger", `{"confirmed":true}`, 403}, {"POST", "/v1/actions/absent", "{}", 503},
		{"POST", "/v1/events", `{"type":"job.finished","source":"test","payload":{}}`, 400},
		{"POST", "/v1/events", `{"type":"control.rotated","source":"test","payload":{"control":"dial","delta":1.5}}`, 400},
		{"POST", "/v1/config/reload", `{"extra":1}`, 400},
	} {
		result := request(t, f, c.method, c.path, c.body, c.status)
		if result["error"] == nil {
			t.Fatal("missing stable error envelope", result)
		}
	}
	if err := os.WriteFile(f.path, []byte("invalid: true"), 0600); err != nil {
		t.Fatal(err)
	}
	request(t, f, "POST", "/v1/config/reload", `{}`, 400)
	request(t, f, "GET", "/v1/status", "", 200)
	if f.runtime.Status().Generation != 1 {
		t.Fatal("invalid reload applied")
	}
}

func BenchmarkUnixDispatch(b *testing.B) {
	f := newAPI(b)
	b.ReportAllocs()
	for b.Loop() {
		request(b, f, "GET", "/v1/context", "", 200)
	}
}
