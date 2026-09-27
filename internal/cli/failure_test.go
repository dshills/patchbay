package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"patchbay/internal/client"
	"patchbay/pkg/protocol"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fakeAPI(t *testing.T, handler http.HandlerFunc) *cliDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pb-cli-wire-")
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
	t.Cleanup(func() { _ = server.Close(); <-done })
	return &cliDaemon{socket: path}
}
func TestAmbiguousAdmissionAndPollingFailures(t *testing.T) {
	var posted atomic.Int32
	d := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		posted.Add(1)
		_, _ = fmt.Fprint(w, `{"wrong":"SECRET_BODY"}`)
	})
	out, diagnostic := ctl(t, d, true, 3, "action", "run", "task")
	if posted.Load() != 1 || strings.Contains(out+diagnostic, "SECRET_BODY") || !strings.Contains(diagnostic, "before retrying") {
		t.Fatal(out, diagnostic, posted.Load())
	}
	for _, response := range []string{`{"id":"wrong","state":"running"}`, `{"id":"test","state":"unknown"}`, `invalid-secret`} {
		d := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				_, _ = fmt.Fprint(w, `{"job_id":"test"}`)
			} else {
				_, _ = fmt.Fprint(w, response)
			}
		})
		out, _ := ctl(t, d, true, 3, "action", "run", "task")
		if object(t, out)["job_id"] != "test" || strings.Contains(out, "secret") {
			t.Fatal(out)
		}
	}
}
func TestWaitingTransportTimeoutAndCancellationAcknowledgement(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(fmt.Sprint(acknowledged), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var deletes atomic.Int32
			d := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case "POST":
					_, _ = fmt.Fprint(w, `{"job_id":"test"}`)
				case "GET":
					cancel()
					<-r.Context().Done()
				case "DELETE":
					deletes.Add(1)
					if !acknowledged {
						<-r.Context().Done()
						return
					}
					_, _ = fmt.Fprint(w, `{"id":"test","state":"cancelled"}`)
				}
			})
			var out, diagnostic bytes.Buffer
			started := time.Now()
			code := RunContext(ctx, "deckctl", []string{"--socket", d.socket, "--json", "action", "run", "task"}, &out, &diagnostic)
			if code != 130 || deletes.Load() != 1 || time.Since(started) > 3*time.Second || !json.Valid(out.Bytes()) {
				t.Fatal(code, deletes.Load(), out.String(), diagnostic.String())
			}
			if !acknowledged && !strings.Contains(diagnostic.String(), "could not be acknowledged") {
				t.Fatal(diagnostic.String())
			}
		})
	}
	d := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			_, _ = fmt.Fprint(w, `{"job_id":"test"}`)
		} else {
			<-r.Context().Done()
		}
	})
	out, _ := ctl(t, d, true, 124, "action", "run", "task", "--request-timeout", "10ms")
	if object(t, out)["job_id"] != "test" {
		t.Fatal(out)
	}
}
func TestAdmissionNeverRetriesConfirmationOrChangedSchema(t *testing.T) {
	for _, code := range []string{"confirmation_required", "invalid_request"} {
		t.Run(code, func(t *testing.T) {
			var posts atomic.Int32
			d := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					if r.URL.Path != "/v1/actions/task" {
						t.Error("expected targeted action lookup", r.URL.Path)
					}
					_, _ = fmt.Fprint(w, `{"name":"task","inputs":{"n":{"type":"integer"}}}`)
					return
				}
				posts.Add(1)
				w.WriteHeader(400)
				_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":"Definition changed."}}`, code)
			})
			want := 2
			if code == "confirmation_required" {
				want = 4
			}
			ctl(t, d, true, want, "action", "run", "task", "--arg", "n=1")
			if posts.Load() != 1 {
				t.Fatal("mutation retried", posts.Load())
			}
		})
	}
}

func TestParameterReloadBetweenMetadataAndWriteRejectsStaleValue(t *testing.T) {
	for _, tc := range []struct {
		name, definition, kind string
		value                  any
	}{
		{"type", "count: {type: string, value: newer, persistent: true}", "string", "newer"},
		{"bounds", "count: {type: integer, value: 1, max: 5, persistent: true}", "integer", json.Number("1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daemon := newDaemon(t)
			upstream, err := client.New(client.Options{Socket: daemon.socket, Timeout: time.Second, MaxResponseBytes: 4096})
			if err != nil {
				t.Fatal(err)
			}
			defer upstream.Close()
			var puts atomic.Int32
			proxy := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				var metadata protocol.Parameter
				var err error
				switch r.Method {
				case "GET":
					err = upstream.Call(r.Context(), "GET", []string{"parameters", "count"}, nil, &metadata)
					if err == nil {
						reloaded := strings.Replace(daemon.text, "count: {type: integer, value: 1, persistent: true}", tc.definition, 1)
						reloaded = strings.Replace(reloaded, "rotate: {parameter: count}", "rotate: {parameter: level}", 1)
						err = os.WriteFile(daemon.path, []byte(reloaded), 0600)
					}
					if err == nil {
						var result protocol.ReloadResponse
						err = upstream.Call(r.Context(), "POST", []string{"config", "reload"}, struct{}{}, &result)
					}
				case "PUT":
					puts.Add(1)
					var write protocol.ParameterSet
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					err = decoder.Decode(&write)
					if err == nil {
						err = upstream.Call(r.Context(), "PUT", []string{"parameters", "count"}, write, &metadata)
					}
				default:
					t.Error("unexpected request", r.Method)
					w.WriteHeader(500)
					return
				}
				if err != nil {
					var apiError *protocol.Error
					if errors.As(err, &apiError) {
						w.WriteHeader(apiError.Code.HTTPStatus())
						_ = json.NewEncoder(w).Encode(protocol.ErrorResponse{Error: *apiError})
						return
					}
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				_ = json.NewEncoder(w).Encode(metadata)
			})
			ctl(t, proxy, true, 2, "param", "set", "count", "7")
			out, _ := ctl(t, daemon, true, 0, "param", "get", "count")
			current := object(t, out)
			if puts.Load() != 1 || current["type"] != tc.kind || current["value"] != tc.value {
				t.Fatal("stale write changed current parameter or retried", current, puts.Load())
			}
		})
	}
}
