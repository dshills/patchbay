package decksetup

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"patchbay/internal/client"
	"patchbay/internal/localfs"
	"patchbay/internal/version"
	"patchbay/pkg/protocol"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func Default() (*Manager, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("deck setup currently requires macOS")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	paths := Paths{home, filepath.Join(home, ".deckd", "deck"), filepath.Join(home, "Library", "Application Support", "com.elgato.StreamDeck"), filepath.Dir(executable)}
	return New(paths, &native{paths: paths})
}

type native struct {
	paths   Paths
	command func(context.Context, string, ...string) ([]byte, error)
}

func (n *native) run(ctx context.Context, path string, args ...string) ([]byte, error) {
	if n.command != nil {
		return n.command(ctx, path, args...)
	}
	return run(ctx, path, args...)
}

type limitedOutput struct{ bytes.Buffer }

func (w *limitedOutput) Write(b []byte) (int, error) {
	if w.Len()+len(b) > 16<<20 {
		return 0, errors.New("setup command output too large")
	}
	return w.Buffer.Write(b)
}
func run(ctx context.Context, path string, args ...string) ([]byte, error) {
	child, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(child, path, args...)
	out := &limitedOutput{}
	cmd.Stdout = out
	cmd.Stderr = &limitedOutput{}
	err := cmd.Run()
	return out.Bytes(), err
}
func (n *native) appRunning(ctx context.Context) (bool, error) {
	_, err := n.run(ctx, "/usr/bin/pgrep", "-u", strconv.Itoa(os.Getuid()), "-x", "Stream Deck")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}
func (n *native) StopApp(ctx context.Context) (bool, error) {
	running, err := n.appRunning(ctx)
	if err != nil || !running {
		return running, err
	}
	if _, err = n.run(ctx, "/usr/bin/osascript", "-e", `tell application id "com.elgato.StreamDeck" to quit`); err != nil {
		return true, errors.New("could not close Stream Deck; close it and try again")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		running, err = n.appRunning(ctx)
		if err != nil {
			return true, err
		}
		if !running {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return true, errors.New("the Stream Deck app is still running; nothing was changed")
}
func (n *native) StartApp(ctx context.Context) error {
	_, err := n.run(ctx, "/usr/bin/open", "-b", "com.elgato.StreamDeck")
	if err != nil {
		return errors.New("setup saved, but Stream Deck could not reopen")
	}
	return nil
}
func (n *native) ReadPreferences(ctx context.Context) ([]byte, error) {
	b, err := n.run(ctx, "/usr/bin/defaults", "export", "com.elgato.StreamDeck", "-")
	if err != nil {
		return nil, errors.New("open the Stream Deck app once; its settings are unavailable")
	}
	return b, nil
}
func (n *native) WritePreferences(ctx context.Context, data []byte) error {
	if _, err := parsePlist(data); err != nil {
		return err
	}
	path := filepath.Join(n.paths.Root, ".preferences-import.xml")
	if err := writeFile(path, data, 0600); err != nil {
		return err
	}
	defer func() { _ = removeTarget(path) }()
	if _, err := n.run(ctx, "/usr/bin/defaults", "import", "com.elgato.StreamDeck", path); err != nil {
		return errors.New("could not restore Stream Deck preferences")
	}
	return nil
}
func (n *native) domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }
func (n *native) ownedAgent(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	data, err := readFile(path, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := parsePlist(data)
	if err != nil {
		return err
	}
	args := p.get("ProgramArguments")
	if p.get("Label").string() != serviceID || args == nil || args.Kind != "array" || len(args.Children) != 3 || args.Children[0].string() != filepath.Join(n.paths.Root, "bin", "deckd") || args.Children[1].string() != "--config" || args.Children[2].string() != filepath.Join(n.paths.Root, "config.yaml") {
		return errors.New("existing setup service belongs to another configuration; it was preserved")
	}
	return nil
}
func (n *native) StopService(ctx context.Context, path string) error {
	if err := n.ownedAgent(path); err != nil {
		return err
	}
	if _, err := n.run(ctx, "/bin/launchctl", "print", n.domain()+"/"+serviceID); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 113 {
			return nil
		}
		return errors.New("could not inspect the setup service; no configuration was changed")
	}
	if _, err := os.Lstat(path); err != nil {
		return errors.New("loaded setup service has no owned LaunchAgent; stop it before changing the setup")
	}
	if _, err := n.run(ctx, "/bin/launchctl", "bootout", n.domain()+"/"+serviceID); err != nil {
		return errors.New("could not stop the Patchbay setup service")
	}
	// Wait for its socket to stop serving before replacing the configuration.
	c, err := client.New(client.Options{Socket: filepath.Join(n.paths.Root, "deckd.sock"), Timeout: 200 * time.Millisecond})
	if err != nil {
		return err
	}
	defer c.Close()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var status protocol.Status
		if err = c.Call(ctx, "GET", []string{"status"}, nil, &status); err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("the Patchbay setup service is still shutting down; setup was preserved")
}
func (n *native) StartService(ctx context.Context, path, socket string) error {
	if err := n.ownedAgent(path); err != nil {
		return err
	}
	if _, err := n.run(ctx, "/bin/launchctl", "bootstrap", n.domain(), path); err != nil {
		return errors.New("could not start Patchbay; the previous setup will be restored")
	}
	c, err := client.New(client.Options{Socket: socket, Timeout: 300 * time.Millisecond})
	if err != nil {
		return err
	}
	defer c.Close()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var status protocol.Status
		if c.Call(ctx, "GET", []string{"status"}, nil, &status) == nil && status.ConfigPath == filepath.Join(n.paths.Root, "config.yaml") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("the Patchbay setup service did not become ready; the previous setup will be restored")
}
func (n *native) VerifyBinaries(ctx context.Context, path string) error {
	for _, name := range []string{"deckd", "deckdemo", "decksd"} {
		file := filepath.Join(path, name)
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("%s is missing; use bin/deckctl from a complete Patchbay bundle, or run make build", name)
		}
		args := []string{"--version", "--json"}
		if name == "decksd" {
			args = args[:1]
		}
		b, err := n.run(ctx, file, args...)
		var v version.Info
		if err != nil || json.Unmarshal(b, &v) != nil || v != version.Current() {
			return errors.New("use Patchbay binaries from the same bundle; their versions do not match")
		}
	}
	return n.ownedAgent(filepath.Join(n.paths.Home, "Library", "LaunchAgents", serviceID+".plist"))
}
func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func (m *Manager) launchAgent() []byte {
	args := []string{filepath.Join(m.paths.Root, "bin", "deckd"), "--config", filepath.Join(m.paths.Root, "config.yaml")}
	var a strings.Builder
	for _, arg := range args {
		fmt.Fprintf(&a, "<string>%s</string>", escape(arg))
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array>%s</array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/bin:/bin:/usr/sbin:/sbin</string></dict>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, serviceID, a.String(), escape(filepath.Join(m.paths.Root, "service.log"))))
}
