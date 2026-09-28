package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

func privateRoot(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func testSession(id string) Session {
	return Session{ID: id, RequestID: evidence.NewRequestID(time.Now()), RequestDigest: Hash(id), State: "generating", Items: []Item{}, Source: &protocol.SourceObservation{Status: "unavailable"}, CreatedAt: time.Now().UTC()}
}
func TestDurableGenerationRecoveryAndForget(t *testing.T) {
	path := privateRoot(t)
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := testSession("session1")
	if _, err = s.Create(v); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Lookup(v.RequestID, "changed"); fault.Safe(err).Code != protocol.RequestConflict {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, known, err := s.Lookup(v.RequestID, v.RequestDigest)
	if err != nil || !known || got.State != "interrupted" {
		t.Fatal(got, known, err)
	}
	if err = s.Forget(v.ID); err != nil {
		t.Fatal(err)
	}
	if _, known, err = s.Lookup(v.RequestID, v.RequestDigest); !known || fault.Safe(err).Code != protocol.NotFound {
		t.Fatal(known, err)
	}
}
func TestAuditFailuresAndQuota(t *testing.T) {
	for _, op := range []string{"create", "write", "sync", "rename", "directory_sync"} {
		t.Run(op, func(t *testing.T) {
			path := privateRoot(t)
			failed := false
			s, err := Open(path, Options{Fault: func(at string) error {
				if at == op && !failed {
					failed = true
					return errors.New("injected")
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			v := testSession(op)
			if _, err = s.Create(v); err == nil {
				t.Fatal("failure accepted")
			}
			_ = s.Close()
			s, err = Open(path, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			got, known, err := s.Lookup(v.RequestID, v.RequestDigest)
			if op == "directory_sync" {
				if !known || err != nil || got.State != "interrupted" {
					t.Fatal(got, known, err)
				}
			} else if known {
				t.Fatal("uncommitted request survived")
			}
		})
	}
	s, err := Open(privateRoot(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for i := range MaxSessions {
		v := testSession(strings.Repeat("x", i+1))
		if _, err = s.Create(v); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err = s.Create(testSession("over")); fault.Safe(err).Code != protocol.StorageFull {
		t.Fatal(err)
	}
}
func TestStrictOutputAndConfinedContext(t *testing.T) {
	good := `{"schema_version":1,"summary":"An interpretation","context_refs":[],"proposals":[]}`
	if _, err := ParseOutput(good); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{strings.Replace(good, `"schema_version":1`, `"schema_version":2`, 1), strings.Replace(good, `"summary":`, `"confirmed":true,"summary":`, 1), strings.Replace(good, `"proposals":[]`, `"proposals":[{"kind":"action","target":"test","inputs":{"shell":{"command":"bad"}},"rationale":"r","expected_outcome":"e"}]`, 1), good + good, strings.Repeat("x", MaxOutput+1), `{"schema_version":1,"summary":"no lists"}`} {
		if _, err := ParseOutput(text); err == nil {
			t.Fatal("invalid final output accepted")
		}
	}
	root := privateRoot(t)
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ok.txt", filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", "AGENTS.md", "../outside", "linked"} {
		if _, err := ReadText(root, name, 100); err == nil {
			t.Fatal(name)
		}
	}
	if b, err := ReadText(root, "ok.txt", 100); err != nil || string(b) != "data" {
		t.Fatal(string(b), err)
	}
	if _, err := ReadText(root, "ok.txt", 2); err == nil {
		t.Fatal("oversize accepted")
	}
}
