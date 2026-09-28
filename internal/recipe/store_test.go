package recipe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"patchbay/internal/evidence"
)

func openTestStore(t *testing.T, path string, opts StoreOptions) *Store {
	t.Helper()
	s, err := OpenStore(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func variant(t *testing.T, p *Package, version string) *Package {
	t.Helper()
	files := rewrite(t, p, func(m *Manifest) { m.Version = version })
	out, err := InspectZIP(context.Background(), archive(t, files, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestStoreIdentityReceiptsAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recipes")
	s := openTestStore(t, path, StoreOptions{})
	p := benchmark(t)
	a, err := s.Import(ctx, p, "", strings.Repeat("a", 128))
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.Import(ctx, p, "", "")
	if err != nil || same.ID != a.ID {
		t.Fatal("duplicate content", err)
	}
	p2 := variant(t, p, "1.0.1")
	b, err := s.Import(ctx, p2, "", a.Alias)
	if err != nil || a.ID == b.ID || a.Alias == b.Alias || len(b.Alias) > 128 {
		t.Fatal("identity collision", err)
	}
	staged, err := s.Import(ctx, p2, a.ID, "")
	if err != nil || staged.Content != p.Digest || staged.Candidate != p2.Digest {
		t.Fatal("update replaced content", err)
	}
	next := s.Snapshot()
	next.Installations[0].Alias = "renamed"
	id := evidence.NewRequestID(time.Now())
	digest := evidence.Digest([]byte("request"))
	result, err := s.Commit(next.Revision, next, id, digest, ManagementResult{ID: a.ID, Operation: "rename"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Lookup(id, evidence.Digest([]byte("different"))); err == nil {
		t.Fatal("conflicting request accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path, StoreOptions{})
	again, known, err := s.Lookup(id, digest)
	if err != nil || !known || again != result {
		t.Fatal("receipt lost", err)
	}
	saved := s.Snapshot()
	if saved.Installations[0].ID != a.ID || saved.Installations[0].Alias != "renamed" || saved.Installations[1].ID != b.ID {
		t.Fatal("identity changed")
	}
	if _, err := s.Package(ctx, p.Digest); err != nil {
		t.Fatal(err)
	}
}
func TestSelectionFaultRecovery(t *testing.T) {
	for _, name := range []string{"intent.json", "selection.json"} {
		for _, op := range []string{"write", "sync", "rename", "dirsync"} {
			t.Run(name+"-"+op, func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "recipes")
				armed := false
				s := openTestStore(t, path, StoreOptions{Fault: func(operation, file string) error {
					if armed && file == name && operation == op {
						return errors.New("injected disk failure")
					}
					return nil
				}})
				entry, err := s.Import(ctx, benchmark(t), "", "")
				if err != nil {
					t.Fatal(err)
				}
				next := s.Snapshot()
				old := next.Revision
				next.Installations[0].Alias = "new-alias"
				id := evidence.NewRequestID(time.Now())
				digest := evidence.Digest([]byte("request"))
				armed = true
				_, err = s.Commit(old, next, id, digest, ManagementResult{ID: entry.ID, Operation: "rename"})
				if err == nil {
					t.Fatal("fault ignored")
				}
				uncertain := name == "selection.json" && op == "dirsync"
				if errors.Is(err, ErrUncertain) != uncertain {
					t.Fatalf("wrong uncertainty: %v", err)
				}
				if s.Snapshot().Revision != old {
					t.Fatal("failed publication changed memory")
				}
				if uncertain && s.Writable() {
					t.Fatal("uncertain store still admits writes")
				}
				_ = s.Close()
				restored := openTestStore(t, path, StoreOptions{})
				selected := restored.Snapshot()
				if uncertain {
					if selected.Revision != old+1 || selected.Installations[0].Alias != "new-alias" {
						t.Fatal("selected state lost")
					}
				} else if selected.Revision != old || selected.Installations[0].Alias != entry.Alias {
					t.Fatal("intent promoted itself")
				}
				_, known, err := restored.Lookup(id, digest)
				if err != nil || known != uncertain {
					t.Fatal("recovery receipt differs", err)
				}
			})
		}
	}
}
func TestStoreRejectsUnsafeQuotaAndFutureSchema(t *testing.T) {
	for _, kind := range []string{"symlink", "quota", "schema", "clock"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recipes")
			s := openTestStore(t, path, StoreOptions{})
			_, err := s.Import(context.Background(), benchmark(t), "", "")
			if err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			switch kind {
			case "symlink":
				err = os.Symlink("selection.json", filepath.Join(path, "evil"))
			case "quota":
				var f *os.File
				f, err = os.OpenFile(filepath.Join(path, "large"), os.O_CREATE|os.O_WRONLY, 0600)
				if err == nil {
					err = f.Truncate(MaxStoreBytes)
					_ = f.Close()
				}
			case "schema":
				err = os.WriteFile(filepath.Join(path, "selection.json"), []byte(`{"schema_version":99}`), 0600)
			case "clock":
				s = openTestStore(t, path, StoreOptions{})
				s.mu.Lock()
				next := s.selected
				next.Clock = time.Now().Add(time.Hour)
				err = s.selectState(next)
				s.mu.Unlock()
				_ = s.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			restored, err := OpenStore(path, StoreOptions{})
			if kind == "clock" {
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = restored.Close() }()
				if restored.Writable() {
					t.Fatal("clock rollback accepted")
				}
			} else if err == nil {
				_ = restored.Close()
				t.Fatal("unsafe store accepted")
			}
			if _, err := os.Lstat(filepath.Join(path, "selection.json")); err != nil {
				t.Fatal("original data removed")
			}
		})
	}
}

func TestRecipeStoreAbruptExitRecovery(t *testing.T) {
	if dir := os.Getenv("PATCHBAY_TEST_RECIPE_CRASH"); dir != "" {
		s, err := OpenStore(dir, StoreOptions{Fault: func(op, name string) error {
			if name == "selection.json" && op == os.Getenv("PATCHBAY_TEST_CRASH_AT") {
				os.Exit(79)
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		next := s.Snapshot()
		next.Installations[0].Alias = "crash-selected"
		_, _ = s.Commit(next.Revision, next, evidence.NewRequestID(time.Now()), evidence.Digest([]byte("crash")), ManagementResult{ID: next.Installations[0].ID, Operation: "rename"})
		t.Fatal("crash point not reached")
	}
	for _, boundary := range []string{"write", "dirsync"} {
		t.Run(boundary, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recipes")
			s := openTestStore(t, path, StoreOptions{})
			entry, err := s.Import(context.Background(), benchmark(t), "", "")
			if err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			process := exec.Command(os.Args[0], "-test.run=^TestRecipeStoreAbruptExitRecovery$")
			process.Env = append(os.Environ(), "PATCHBAY_TEST_RECIPE_CRASH="+path, "PATCHBAY_TEST_CRASH_AT="+boundary)
			result, err := process.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 79 {
				t.Fatalf("unexpected child result: %v %s", err, result)
			}
			restored := openTestStore(t, path, StoreOptions{})
			alias := restored.Snapshot().Installations[0].Alias
			if boundary == "write" && alias != entry.Alias || boundary == "dirsync" && alias != "crash-selected" {
				t.Fatal("wrong authoritative selection after abrupt exit", alias)
			}
		})
	}
}
