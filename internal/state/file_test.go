package state

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	runtimecontext "patchbay/internal/context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func snapshot(n int64) Snapshot {
	return Snapshot{Version: 1, Context: runtimecontext.RuntimeContext{Mode: "dev"}, Parameters: map[string]any{"n": n}}
}
func TestRoundTripAndCorruptPreservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "state.json")
	if got, err := Read(path); got != nil || err != nil {
		t.Fatal(got, err)
	}
	data, _ := json.Marshal(snapshot(math.MaxInt64))
	if err := Write(path, data); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	got, err := Read(path)
	if err != nil || got.Parameters["n"] != json.Number("9223372036854775807") {
		t.Fatal(got, err)
	}
	for _, bad := range []string{`{broken`, `{"version":2}`, `{"version":1,"version":1}`} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path); !errors.Is(err, ErrRecovered) {
			t.Fatal(err)
		}
		matches, _ := filepath.Glob(path + ".corrupt-*")
		found := false
		for _, match := range matches {
			data, _ := os.ReadFile(match)
			found = found || string(data) == bad
		}
		if !found {
			t.Fatal("original corrupt bytes not preserved")
		}
	}
}

func TestStateRejectsUnsafeFilesAndKeepsPriorOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("public state accepted")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new")); err == nil {
		t.Fatal("public dir accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old" {
		t.Fatal("old state changed")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := Write(path, make([]byte, MaxBytes+1)); err == nil {
		t.Fatal("oversized write")
	}
	if err := Write(dir, []byte("new")); err == nil {
		t.Fatal("directory replaced")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".deckd-state-*"))
	if len(matches) != 0 {
		t.Fatal("temporary file leaked")
	}
}

func TestWriterRetryCoalescingAndClose(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	} // force atomic rename failure
	warnings := make(chan error, 10)
	w := NewWriter(path, 5*time.Millisecond, func(err error) {
		select {
		case warnings <- err:
		default:
		}
	})
	defer func() { _ = w.Close(context.Background()) }()
	if err := w.Submit(snapshot(1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-warnings:
	case <-time.After(time.Second):
		t.Fatal("missing write warning")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := range 20 {
		wg.Go(func() {
			if err := w.Submit(snapshot(int64(n))); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := w.Submit(snapshot(99)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil || got.Parameters["n"] != json.Number("99") {
		t.Fatal(got, err)
	}
	if err := w.Submit(snapshot(100)); err == nil {
		t.Fatal("submit after close")
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
