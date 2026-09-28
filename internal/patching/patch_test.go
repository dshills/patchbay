package patching

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"patchbay/internal/identity"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err = os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, name), []byte(data), 0640); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	return root
}
func prepare(t *testing.T, root string, diff string, allowed ...string) Plan {
	t.Helper()
	p, err := Prepare(context.Background(), root, diff, allowed, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(journalPath(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestExactDiffFormsAndBounds(t *testing.T) {
	for _, pair := range [][2]string{{"old\n", "new\n"}, {"old\r\n", "nëw\r\n"}, {"old", "new"}, {"", "new\n"}, {"old\n", ""}, {"a\nb\nc\n", "a\nβ\nc\n"}} {
		t.Run(pair[0], func(t *testing.T) {
			root := repo(t, map[string]string{"file.txt": pair[0]})
			p := prepare(t, root, fullDiff("file.txt", pair[0], pair[1]), "file.txt")
			if p.Files[0].AfterText != pair[1] || p.Files[0].Mode != 0640 {
				t.Fatal(p)
			}
			if err := Validate(context.Background(), p); err != nil {
				t.Fatal(err)
			}
		})
	}
	root := repo(t, map[string]string{"file.txt": "old\n", ".env": "secret\n"})
	good := fullDiff("file.txt", "old\n", "new\n")
	for _, bad := range []string{strings.Replace(good, "-old", "-wrong", 1), strings.Replace(good, "+1,1", "+2,1", 1), "diff --git a/file.txt b/file.txt\n" + good, strings.Replace(good, "+++ b/file.txt", "+++ b/other", 1), strings.ReplaceAll(good, "file.txt", "../file.txt"), fullDiff(".env", "secret\n", "leak\n"), strings.Replace(good, "@@ -1,1 +1,1 @@", "@@ -1,2 +1,1 @@", 1), good + good, strings.Replace(good, "+new\n", "+new\r\n", 1), strings.Repeat("x", MaxDiff+1)} {
		if _, err := Prepare(context.Background(), root, bad, []string{"file.txt", ".env"}, nil); err == nil {
			t.Fatal("accepted malformed diff", bad)
		}
	}
	if _, err := Prepare(context.Background(), root, good, nil, nil); err == nil {
		t.Fatal("no allowlist")
	}
	if _, err := Prepare(context.Background(), root, good, []string{"file.txt"}, func(string) bool { return true }); err == nil {
		t.Fatal("protected override")
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), root, fullDiff("untracked.txt", "old\n", "new\n"), []string{"untracked.txt"}, nil); err == nil {
		t.Fatal("untracked")
	}
	if err := os.Remove(filepath.Join(root, "file.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("untracked.txt", filepath.Join(root, "file.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), root, good, []string{"file.txt"}, nil); err == nil {
		t.Fatal("symlink")
	}
}
func TestPatchStagesAllBeforeWritesAndPreservesExternalChanges(t *testing.T) {
	for _, phase := range []string{"stage_created", "staged", "ready", "before_replace", "replaced"} {
		t.Run(phase, func(t *testing.T) {
			root := repo(t, map[string]string{"a": "old\n", "b": "old\n"})
			p := prepare(t, root, fullDiff("a", "old\n", "new\n")+fullDiff("b", "old\n", "new\n"), "a", "b")
			s := openStore(t)
			id := identity.New()
			if err := s.Begin(id, "run", "project", p); err != nil {
				t.Fatal(err)
			}
			once := false
			s.Fault = func(op string) error {
				if op == phase && !once {
					once = true
					return errors.New("injected")
				}
				return nil
			}
			out, err := s.Apply(context.Background(), id)
			if err == nil || out.State != "failed" {
				t.Fatal(out, err)
			}
			want := []string{"unapplied", "unapplied"}
			if phase == "replaced" {
				want[0] = "applied"
			}
			for i, f := range out.Files {
				if f.State != want[i] {
					t.Fatal(out)
				}
			}
			if _, err = s.Apply(context.Background(), id); err == nil {
				t.Fatal("replayed")
			}
		})
	}
	root := repo(t, map[string]string{"a": "old\n", "b": "old\n"})
	p := prepare(t, root, fullDiff("a", "old\n", "new\n")+fullDiff("b", "old\n", "new\n"), "a", "b")
	s := openStore(t)
	id := identity.New()
	if err := s.Begin(id, "run", "project", p); err != nil {
		t.Fatal(err)
	}
	s.Fault = func(phase string) error {
		if phase == "replaced" {
			return os.WriteFile(filepath.Join(root, "b"), []byte("external\n"), 0640)
		}
		return nil
	}
	out, err := s.Apply(context.Background(), id)
	if err == nil || out.Files[0].State != "applied" || out.Files[1].State != "conflicted" {
		t.Fatal(out, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "b"))
	if string(data) != "external\n" {
		t.Fatal("overwrote external editor")
	}
	if _, err = s.Restoration(context.Background(), id); err == nil {
		t.Fatal("restored conflicted file")
	}
}
func TestPatchRestorationRequiresAnotherExplicitApply(t *testing.T) {
	root := repo(t, map[string]string{"a": "old\n"})
	s := openStore(t)
	p := prepare(t, root, fullDiff("a", "old\n", "new\n"), "a")
	id := identity.New()
	if err := s.Begin(id, "run", "project", p); err != nil {
		t.Fatal(err)
	}
	out, err := s.Apply(context.Background(), id)
	if err != nil || out.State != "applied" {
		t.Fatal(out, err)
	}
	diff, err := s.Restoration(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "a"))
	if string(data) != "new\n" {
		t.Fatal("restoration applied itself")
	}
	restore := prepare(t, root, diff, "a")
	next := identity.New()
	if err = s.Begin(next, "next-run", "project", restore); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(root, "a"))
	if string(data) != "old\n" {
		t.Fatal(string(data))
	}
}
func TestPatchCancellationAndStageReplacement(t *testing.T) {
	for _, change := range []string{"cancel", "target", "stage"} {
		t.Run(change, func(t *testing.T) {
			root := repo(t, map[string]string{"a": "old\n"})
			s := openStore(t)
			p := prepare(t, root, fullDiff("a", "old\n", "new\n"), "a")
			id := identity.New()
			if err := s.Begin(id, "run", "project", p); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.Fault = func(phase string) error {
				if phase != "before_replace" {
					return nil
				}
				switch change {
				case "cancel":
					cancel()
				case "target":
					return os.WriteFile(filepath.Join(root, "a"), []byte("external\n"), 0640)
				case "stage":
					return os.WriteFile(filepath.Join(root, stageName(id, 0)), []byte("hostile\n"), 0640)
				}
				return nil
			}
			out, err := s.Apply(ctx, id)
			if err == nil {
				t.Fatal(out)
			}
			data, _ := os.ReadFile(filepath.Join(root, "a"))
			if string(data) == "new\n" || string(data) == "hostile\n" {
				t.Fatal("unexpected write")
			}
			if change == "stage" && out.Files[0].Cleanup == "" {
				t.Fatal("deleted externally modified stage")
			}
		})
	}
}
func TestPatchCrashRecovery(t *testing.T) {
	if root := os.Getenv("PATCHBAY_PATCH_CRASH_ROOT"); root != "" {
		s, err := Open(os.Getenv("PATCHBAY_PATCH_CRASH_STORE"), nil)
		if err != nil {
			t.Fatal(err)
		}
		p := prepare(t, root, fullDiff("a", "old\n", "new\n")+fullDiff("b", "old\n", "new\n"), "a", "b")
		if err = s.Begin("crash-operation-0001", "run", "project", p); err != nil {
			t.Fatal(err)
		}
		s.Fault = func(phase string) error {
			if phase == os.Getenv("PATCHBAY_PATCH_CRASH_PHASE") {
				os.Exit(73)
			}
			return nil
		}
		_, _ = s.Apply(context.Background(), "crash-operation-0001")
		t.Fatal("did not crash")
	}
	for _, phase := range []string{"stage_created", "staged", "ready", "replaced"} {
		t.Run(phase, func(t *testing.T) {
			root := repo(t, map[string]string{"a": "old\n", "b": "old\n"})
			store := journalPath(t)
			cmd := exec.Command(os.Args[0], "-test.run=^TestPatchCrashRecovery$")
			cmd.Env = append(os.Environ(), "PATCHBAY_PATCH_CRASH_ROOT="+root, "PATCHBAY_PATCH_CRASH_STORE="+store, "PATCHBAY_PATCH_CRASH_PHASE="+phase)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatal(string(out), err)
			}
			s, err := Open(store, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			record, err := s.Get("crash-operation-0001")
			if err != nil {
				t.Fatal(err)
			}
			if record.State != "interrupted" {
				t.Fatal(record)
			}
			want := "unapplied"
			if phase == "replaced" {
				want = "applied"
			}
			if record.Files[0].State != want || record.Files[1].State != "unapplied" {
				t.Fatal(record.Files)
			}
			if _, err = s.Apply(context.Background(), record.ID); err == nil {
				t.Fatal("recovery replayed")
			}
		})
	}
}

func journalPath(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "journal")
}

func TestConcurrentPatchStoresRespectProjectLock(t *testing.T) {
	root := repo(t, map[string]string{"a": "old\n"})
	p := prepare(t, root, fullDiff("a", "old\n", "new\n"), "a")
	first, second := openStore(t), openStore(t)
	id1, id2 := identity.New(), identity.New()
	if err := first.Begin(id1, "r1", "p", p); err != nil {
		t.Fatal(err)
	}
	if err := second.Begin(id2, "r2", "p", p); err != nil {
		t.Fatal(err)
	}
	ready, release := make(chan struct{}), make(chan struct{})
	first.Fault = func(phase string) error {
		if phase == "ready" {
			close(ready)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := first.Apply(context.Background(), id1); done <- err }()
	<-ready
	out, err := second.Apply(context.Background(), id2)
	if err == nil || out.Files[0].State != "unapplied" {
		t.Error(out, err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPatchRecoveryDiscardsUncommittedMetadata(t *testing.T) {
	for _, scenario := range []string{"new-intent", "before-replacement", "after-replacement"} {
		t.Run(scenario, func(t *testing.T) {
			root := repo(t, map[string]string{"a": "old\n"})
			path := journalPath(t)
			s, err := Open(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			id := identity.New()
			p := prepare(t, root, fullDiff("a", "old\n", "new\n"), "a")
			if err = s.Begin(id, "run", "project", p); err != nil {
				t.Fatal(err)
			}
			record, err := s.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			// A fully encoded scratch file still has not crossed the
			// metadata rename boundary. It cannot assert success on recovery.
			record.State = "applied"
			record.Files[0].State = "applied"
			pending, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(path, "journal.pending"), pending, 0600); err != nil {
				t.Fatal(err)
			}
			if scenario == "new-intent" {
				if err = os.Remove(filepath.Join(path, "operation-"+id+".json")); err != nil {
					t.Fatal(err)
				}
			}
			wantData, wantState := "old\n", "unapplied"
			if scenario == "after-replacement" {
				wantData, wantState = "new\n", "applied"
				if err = os.WriteFile(filepath.Join(root, "a"), []byte(wantData), 0640); err != nil {
					t.Fatal(err)
				}
			}
			s, err = Open(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if scenario == "new-intent" {
				if len(s.List("project")) != 0 {
					t.Fatal("promoted uncommitted intent")
				}
			} else {
				recovered, err := s.Get(id)
				if err != nil || recovered.State != "interrupted" || recovered.Files[0].State != wantState {
					t.Fatal(recovered, err)
				}
			}
			data, err := os.ReadFile(filepath.Join(root, "a"))
			if err != nil || string(data) != wantData {
				t.Fatal("recovery changed project content", err)
			}
			if _, err = os.Stat(filepath.Join(path, "journal.pending")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("uncommitted scratch retained", err)
			}
		})
	}
}
