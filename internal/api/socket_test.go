package api

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestSocketOwnershipAndStaleRecovery(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "sock")
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatal("duplicate daemon")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("socket not removed", err)
	}
	// An external listener has no deckd lock but must still be preserved.
	foreign, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	foreign.SetUnlinkOnClose(false)
	if _, err := Listen(path); err == nil {
		t.Fatal("live socket replaced")
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err = Listen(path)
	if err != nil {
		t.Fatal("stale recovery", err)
	}
	// Shutdown must never unlink a different file installed at the old pathname.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatal(data, err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatal("regular file replaced")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", path); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatal("symlink replaced")
	}
}

func TestSocketRejectsPublicDirectoryAndSymlinkLock(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "sock")
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatal("public parent accepted")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".lock"); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatal("symlink lock accepted")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "keep" {
		t.Fatal("lock target changed")
	}
}
