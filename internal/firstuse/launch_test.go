package firstuse

import (
	"context"
	"os"
	"os/exec"
	"patchbay/internal/client"
	"patchbay/pkg/protocol"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigNeverOverwritesOrFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := createConfig(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := createConfig(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := createConfig(path, []byte("changed")); err == nil {
		t.Fatal("overwrote existing config")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatal("configuration changed")
	}
	link := filepath.Join(root, "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := createConfig(link, []byte("original")); err == nil {
		t.Fatal("followed symlink")
	}
}
func TestEmbeddedDemoMatchesPublishedTemplate(t *testing.T) {
	embedded, err := resources.ReadFile("benchmark.yaml")
	if err != nil {
		t.Fatal(err)
	}
	published, err := os.ReadFile("../../configs/benchmark.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(embedded) != string(published) {
		t.Fatal("embedded demo and documented config differ")
	}
}

func TestOwnedDaemonReuseShutdownAndVersionMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("builds demo executable")
	}
	root, err := os.MkdirTemp("/tmp", "pb-demo-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binaries := filepath.Join(root, "bin")
	if err := os.Mkdir(binaries, 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(binaries, "deckdemo"), "../../cmd/deckdemo")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("demo build: %v %s", err, output)
	}
	owned, err := Start(context.Background(), filepath.Join(root, "workspace"), binaries)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owned.Close() }()
	if !owned.Owned {
		t.Fatal("launcher did not own its daemon")
	}
	reused, err := Start(context.Background(), filepath.Join(root, "workspace"), binaries)
	if err != nil || reused.Owned {
		t.Fatal("did not reuse compatible daemon", err)
	}
	if err := reused.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(client.Options{Socket: owned.Socket, Timeout: time.Second, MaxResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var status protocol.Status
	if err := c.Call(context.Background(), "GET", []string{"status"}, nil, &status); err != nil {
		t.Fatal("reuse session stopped someone else's daemon", err)
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "GET", []string{"status"}, nil, &status); err == nil {
		t.Fatal("owned daemon survived explicit quit")
	}
	if err := os.WriteFile(filepath.Join(binaries, "deckdemo"), []byte("#!/bin/sh\nprintf '%s' '{\"version\":\"wrong\",\"commit\":\"wrong\",\"build_time\":\"wrong\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), filepath.Join(root, "other"), binaries); err == nil {
		t.Fatal("mismatched demo binary accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "other", "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("wrote config before version verification")
	}
}
