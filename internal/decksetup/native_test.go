package decksetup

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"patchbay/internal/daemon"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Keep the actual native service checks and client construction. Only launchctl
// is substituted: it owns a real daemon in this fixture's private directories.
type nativeServicePlatform struct {
	*fixturePlatform
	native *native
}

func (p *nativeServicePlatform) StopService(ctx context.Context, path string) error {
	return p.native.StopService(ctx, path)
}
func (p *nativeServicePlatform) StartService(ctx context.Context, path, socket string) error {
	return p.native.StartService(ctx, path, socket)
}

func useNativeServices(t *testing.T, m *Manager, f *fixturePlatform) {
	t.Helper()
	missing := exec.Command("/bin/sh", "-c", "exit 113").Run()
	var cancel context.CancelFunc
	var done chan error
	stop := func() error {
		if cancel == nil {
			return nil
		}
		cancel()
		cancel = nil
		select {
		case err := <-done:
			f.service = false
			return err
		case <-time.After(5 * time.Second):
			return errors.New("fixture daemon did not stop")
		}
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Error(err)
		}
	})
	n := &native{paths: m.paths}
	n.command = func(_ context.Context, path string, args ...string) ([]byte, error) {
		if path != "/bin/launchctl" || len(args) < 2 || args[1] != n.domain() && args[1] != n.domain()+"/"+serviceID {
			return nil, errors.New("unexpected native command")
		}
		switch args[0] {
		case "print":
			if cancel == nil {
				return nil, missing
			}
		case "bootstrap":
			if cancel != nil || len(args) != 3 || args[2] != m.agent() {
				return nil, errors.New("unexpected bootstrap")
			}
			if f.failStart {
				f.failStart = false
				return nil, errors.New("injected bootstrap failure")
			}
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			done = make(chan error, 1)
			finished := done
			go func() { finished <- daemon.Run(ctx, filepath.Join(m.paths.Root, "config.yaml"), io.Discard) }()
			f.service = true
		case "bootout":
			return nil, stop()
		default:
			return nil, errors.New("unexpected launchctl operation")
		}
		return nil, nil
	}
	m.platform = &nativeServicePlatform{f, n}
}

func TestNativeServiceSwitchAndRestore(t *testing.T) {
	m, f := fixture(t)
	useNativeServices(t, m, f)
	first, err := m.Use(context.Background(), "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if !f.service || !f.running {
		t.Fatal("native startup did not become ready")
	}
	if _, err = m.Use(context.Background(), "benchmark", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Restore(context.Background(), first.Backup); err != nil {
		t.Fatal(err)
	}
	assertOriginal(t, m, f)
	if !f.running {
		t.Fatal("app not reopened after restoring original")
	}
}

func TestNativeServiceFailureRecoversPriorRunningSetup(t *testing.T) {
	m, f := fixture(t)
	useNativeServices(t, m, f)
	if _, err := m.Use(context.Background(), "demo", ""); err != nil {
		t.Fatal(err)
	}
	f.failStart = true
	result, err := m.Use(context.Background(), "benchmark", "")
	if err == nil || !strings.Contains(err.Error(), "previous setup restored") {
		t.Fatal("startup failure did not recover", result, err)
	}
	if m.active().Preset != "demo" || !f.service || !f.running {
		t.Fatal("prior setup did not restart through native readiness check")
	}
	if _, err = m.loadBackup(result.Backup, true); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAppQuitFailureGivesManualRecovery(t *testing.T) {
	m, _ := fixture(t)
	calls := 0
	n := native{paths: m.paths, command: func(_ context.Context, path string, args ...string) ([]byte, error) {
		calls++
		if path == "/usr/bin/pgrep" {
			return nil, nil
		}
		if path == "/usr/bin/osascript" {
			return nil, errors.New("private native command diagnostic")
		}
		return nil, errors.New("unexpected native command")
	}}
	running, err := n.StopApp(context.Background())
	if !running || err == nil || calls != 2 || !strings.Contains(err.Error(), "quit it from its menu") || !strings.Contains(err.Error(), "Automation access") || strings.Contains(err.Error(), "private native") {
		t.Fatal("missing manual quit guidance or exposed diagnostic", running, err, calls)
	}
}
