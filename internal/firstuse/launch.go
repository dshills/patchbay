// Package firstuse manages a private, explicitly launched demo daemon. It never
// replaces user configuration or stops a daemon owned by another session.
package firstuse

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"patchbay/internal/provider"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"patchbay/internal/client"
	"patchbay/internal/daemon"
	"patchbay/internal/localfs"
	"patchbay/internal/version"
	"patchbay/pkg/protocol"
)

//go:embed benchmark.yaml
var resources embed.FS

type Session struct {
	once     sync.Once
	closeErr error
	Socket   string
	Owned    bool
	cancel   context.CancelFunc
	done     <-chan error
}

func (s *Session) Close() error {
	s.once.Do(func() {
		if !s.Owned {
			return
		}
		s.cancel()
		select {
		case err := <-s.done:
			s.closeErr = err
		case <-time.After(7 * time.Second):
			s.closeErr = errors.New("demo shutdown exceeded its deadline; inspect its process before relaunching")
		}
	})
	return s.closeErr
}

func defaultDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".deckd", "demo"), nil
}
func Start(ctx context.Context, directory, binaries string) (*Session, error) {
	var err error
	if directory == "" {
		directory, err = defaultDirectory()
		if err != nil {
			return nil, err
		}
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	lock, err := localfs.Acquire(filepath.Join(directory, ".launcher.lock"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	socket := filepath.Join(directory, "deckd.sock")
	configPath := filepath.Join(directory, "config.yaml")
	c, err := client.New(client.Options{Socket: socket, Timeout: time.Second, MaxResponseBytes: 1 << 20})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	check := func() error {
		var status protocol.Status
		if err := c.Call(ctx, "GET", []string{"status"}, nil, &status); err != nil {
			return err
		}
		var capabilities protocol.Capabilities
		if err := c.Call(ctx, "GET", []string{"capabilities"}, nil, &capabilities); err != nil {
			return errors.New("existing demo daemon lacks workbench capabilities")
		}
		if status.ConfigPath != configPath || status.Version != version.Current().Version || capabilities.Features["capture"] != 1 || capabilities.Features["export"] != 1 || capabilities.Schemas["experiment"] != 1 || capabilities.Schemas["run"] != 1 {
			return errors.New("existing daemon is incompatible with this demo; it was left running")
		}
		return nil
	}
	var status protocol.Status
	if err := c.Call(ctx, "GET", []string{"status"}, nil, &status); err == nil {
		if err := check(); err != nil {
			return nil, err
		}
		return &Session{Socket: socket}, nil
	}
	demo := filepath.Join(binaries, "deckdemo")
	info, err := os.Lstat(demo)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, errors.New("deckdemo is missing; keep it beside deckctl from the same bundle")
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := (provider.ProcessRunner{}).Run(verifyCtx, provider.Command{Path: demo, Args: []string{"--version", "--json"}, Dir: directory, Env: os.Environ()}, provider.NewBudget(4096))
	output, _ := result.Data["stdout"].(string)
	var binaryVersion version.Info
	if err != nil || result.Data["truncated"] == true || json.Unmarshal([]byte(output), &binaryVersion) != nil || binaryVersion != version.Current() {
		return nil, errors.New("deckdemo version does not match deckctl; use binaries from one verified bundle")
	}
	template, err := resources.ReadFile("benchmark.yaml")
	if err != nil {
		return nil, err
	}
	yamlString := func(value string) string { data, _ := json.Marshal(value); return string(data) }
	configuration := strings.ReplaceAll(string(template), "../bin/deckdemo", yamlString(demo))
	configuration = strings.ReplaceAll(configuration, "../.cache/benchmark/deckd.sock", yamlString(socket))
	configuration = strings.ReplaceAll(configuration, "../.cache/benchmark/state.json", yamlString(filepath.Join(directory, "state.json")))
	configuration = strings.Replace(configuration, "path: ..}", "path: "+yamlString(directory)+"}", 1)
	if err := createConfig(configPath, []byte(configuration)); err != nil {
		return nil, err
	}
	daemonCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- daemon.Run(daemonCtx, configPath, io.Discard) }()
	session := &Session{Socket: socket, Owned: true, cancel: stop, done: done}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			stop()
			return nil, fmt.Errorf("demo daemon could not start: %w", err)
		case <-ctx.Done():
			_ = session.Close()
			return nil, ctx.Err()
		case <-deadline.C:
			_ = session.Close()
			return nil, errors.New("demo daemon did not become ready within five seconds")
		case <-tick.C:
			if err := check(); err == nil {
				return session, nil
			}
		}
	}
}
func createConfig(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
			return errors.New("existing demo configuration is not a private regular file")
		}
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		opened, err := f.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return errors.New("demo configuration changed while reading")
		}
		existing, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		if err != nil {
			return err
		}
		if string(existing) != string(data) {
			return errors.New("demo configuration differs from this bundle; it was left unchanged. Use workbench with its socket, or choose a fresh --demo-dir")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
