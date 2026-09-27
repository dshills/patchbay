package daemon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"patchbay/internal/state"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDaemonChild(t *testing.T) {
	path := os.Getenv("PATCHBAY_DAEMON_TEST_CONFIG")
	if path == "" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, path, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestSignalShutdownFlushesStateAndCleansSocket(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "pb-daemon-")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(dir) }()
			path, socket, statePath := filepath.Join(dir, "config.yaml"), filepath.Join(dir, "sock"), filepath.Join(dir, "state.json")
			text := fmt.Sprintf("version: 1\nserver: {socket: %q, shutdown_grace: 2s}\nstate: {path: %q, flush_interval: 1h}\nactions:\n  slow: {type: exec, safety: safe, command: /bin/sleep, args: ['30']}\n", socket, statePath)
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			exe, _ := os.Executable()
			cmd := exec.Command(exe, "-test.run=^TestDaemonChild$")
			cmd.Env = append(os.Environ(), "PATCHBAY_DAEMON_TEST_CONFIG="+path)
			cmd.Stderr = os.Stderr
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill() }()
			ready := make(chan struct{}, 1)
			scanDone := make(chan struct{})
			go func() {
				defer close(scanDone)
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					if strings.Contains(scanner.Text(), `"outcome":"ready"`) {
						select {
						case ready <- struct{}{}:
						default:
						}
					}
				}
			}()
			select {
			case <-ready:
			case <-time.After(3 * time.Second):
				t.Fatal("daemon did not become ready")
			}
			transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: time.Second}
			for _, reqData := range []struct {
				method, path, body string
				status             int
			}{{"PATCH", "context", `{"mode":"persisted"}`, 200}, {"POST", "actions/slow", `{}`, 202}} {
				req, _ := http.NewRequest(reqData.method, "http://deckd/v1/"+reqData.path, strings.NewReader(reqData.body))
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
				if res.StatusCode != reqData.status {
					t.Fatal(res.StatusCode)
				}
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown exceeded grace")
			}
			<-scanDone
			if _, err := os.Stat(socket); !os.IsNotExist(err) {
				t.Fatal("socket remains", err)
			}
			saved, err := state.Read(statePath)
			if err != nil || saved == nil || saved.Context.Mode != "persisted" {
				t.Fatal(saved, err)
			}
		})
	}
}
