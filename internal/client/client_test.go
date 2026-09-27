package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"patchbay/pkg/protocol"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serve(t *testing.T, handler http.HandlerFunc, timeout time.Duration, limit int64) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pb-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	c, err := New(Options{Socket: path, Timeout: timeout, MaxResponseBytes: limit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); _ = server.Close(); <-done })
	return c
}
func TestUnixTransportEscapingPrecisionAndNoProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "/v1/jobs/a%2Fb%3F%23" || r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Error(r.Method, r.RequestURI, r.Header)
		}
		var value map[string]any
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if err := d.Decode(&value); err != nil || value["n"] != json.Number("9223372036854775807") {
			t.Error(value, err)
		}
		_, _ = fmt.Fprint(w, `{"n":9223372036854775807}`)
	}, time.Second, 1024)
	var response map[string]any
	if err := c.Call(context.Background(), "POST", []string{"jobs", "a/b?#"}, map[string]any{"n": int64(9223372036854775807)}, &response); err != nil || response["n"] != json.Number("9223372036854775807") {
		t.Fatal(response, err)
	}
}
func TestResponseFailuresNeverEchoBodyOrRetry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
	}{
		{"malformed", 200, `secret-invalid-json`, "invalid_response"}, {"trailing", 200, `{} {}`, "invalid_response"}, {"null", 200, `null`, "invalid_response"},
		{"too-large", 200, `{"v":"` + strings.Repeat("s", 2048) + `"}`, "invalid_response"},
		{"redirect", 302, `{}`, "invalid_response"}, {"bad-error", 500, `{"error":{"code":"forged","message":"secret"}}`, "invalid_response"},
		{"known-error", 409, `{"error":{"code":"confirmation_required","message":"Confirm first."}}`, "confirmation_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := serve(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "http://remote.invalid/secret")
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}, time.Second, 1024)
			var response protocol.Job
			err := c.Call(context.Background(), "POST", []string{"actions", "test"}, struct{}{}, &response)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
			var wire *Error
			var apiError *protocol.Error
			got := ""
			if errors.As(err, &wire) {
				got = wire.Code
			} else if errors.As(err, &apiError) {
				got = string(apiError.Code)
			}
			if got != tc.code || calls.Load() != 1 {
				t.Fatal(got, calls.Load())
			}
		})
	}
}
func TestTransportTimeoutCancellationAndOptions(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, 10*time.Millisecond, 1024)
	var value protocol.Status
	if err := c.Call(context.Background(), "GET", []string{"status"}, nil, &value); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Call(ctx, "GET", []string{"status"}, nil, &value); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "POST", []string{"status"}, make(chan int), &value); err == nil {
		t.Fatal("unencodable body")
	}
	for _, o := range []Options{{Socket: "x"}, {Socket: "", Timeout: time.Second, MaxResponseBytes: 1024}, {Socket: "~user/x", Timeout: time.Second, MaxResponseBytes: 1024}, {Socket: "a\x00b", Timeout: time.Second, MaxResponseBytes: 1024}} {
		if _, err := New(o); err == nil {
			t.Fatal(o)
		}
	}
	c, err := New(Options{Socket: filepath.Join(t.TempDir(), "absent"), Timeout: time.Second, MaxResponseBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call(context.Background(), "GET", []string{"status"}, nil, &value); err == nil || !strings.Contains(err.Error(), "--socket") {
		t.Fatal(err)
	}
}

func TestMutationsUseFreshConnectionsAndDoNotReplayLostResponses(t *testing.T) {
	var lost atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && !r.Close {
			t.Error("mutation must close its connection")
		}
		if r.URL.Path == "/v1/lost" {
			lost.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		_, _ = fmt.Fprint(w, `{}`)
	}, time.Second, 1024)
	for i, method := range []string{"GET", "GET", "POST", "PUT", "PATCH", "DELETE"} {
		var reused atomic.Bool
		ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) }})
		var response map[string]any
		if err := c.Call(ctx, method, []string{"status"}, nil, &response); err != nil {
			t.Fatal(err)
		}
		if reused.Load() != (i == 1) {
			t.Fatalf("%s request %d: reused=%v", method, i, reused.Load())
		}
	}
	var response map[string]any
	if err := c.Call(context.Background(), "POST", []string{"lost"}, struct{}{}, &response); err == nil || lost.Load() != 1 {
		t.Fatal("lost response must fail without replay", err, lost.Load())
	}
}

func TestOptionErrorsIdentifyInvalidSetting(t *testing.T) {
	for _, tc := range []struct {
		timeout time.Duration
		limit   int64
		message string
	}{
		{0, DefaultMaxResponseBytes, "Request timeout"},
		{time.Second, MinResponseBytes - 1, "Response limit"},
		{time.Second, MaxResponseBytes + 1, "Response limit"},
	} {
		_, err := New(Options{Socket: "unused", Timeout: tc.timeout, MaxResponseBytes: tc.limit})
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Fatal(err)
		}
	}
}
