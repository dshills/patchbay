package evidence

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

func testStore(t *testing.T, options Options) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runs")
	s, err := Open(path, DefaultLimits(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}
func draft() protocol.Run {
	return protocol.Run{Experiment: protocol.Experiment{SchemaVersion: 1, ID: "bench", Title: "Bench", Collectors: []protocol.Collector{}}, ExperimentDigest: Digest([]byte("experiment")), PlanDigest: Digest([]byte("plan")), Project: "test", RequestID: NewRequestID(time.Now()), RequestDigest: Digest([]byte("request")), Steps: []protocol.PreparedStep{}}
}
func reserve(t *testing.T, s *Store) protocol.Run {
	t.Helper()
	response, duplicate, err := s.Reserve(draft())
	if err != nil || duplicate {
		t.Fatalf("reserve: %#v %v", response, err)
	}
	r, err := s.Get(response.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func complete(t *testing.T, s *Store, r protocol.Run) protocol.Run {
	t.Helper()
	now := time.Now().UTC()
	r.State, r.FinishedAt = "success", &now
	if err := s.Update(r); err != nil {
		t.Fatal(err)
	}
	return r
}
func code(t *testing.T, err error, want protocol.Code) {
	t.Helper()
	if err == nil || fault.Safe(err).Code != want {
		t.Fatalf("error=%v want=%s", err, want)
	}
}

func TestReserveRecoverAndDeduplicate(t *testing.T) {
	s, path := testStore(t, Options{})
	r := reserve(t, s)
	a, err := s.AddArtifact(r.ID, "trace", "application/json", []byte(`{"schema_version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path, DefaultLimits(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	run, err := restored.Get(r.ID)
	if err != nil || run.State != "interrupted" || run.FinishedAt == nil || len(run.Artifacts) != 1 {
		t.Fatalf("recovered=%#v %v", run, err)
	}
	got, found, err := restored.Reserve(r)
	if err != nil || !found || got.RunID != r.ID {
		t.Fatalf("duplicate=%#v %t %v", got, found, err)
	}
	r.RequestDigest = Digest([]byte("changed"))
	_, _, err = restored.Reserve(r)
	code(t, err, protocol.RequestConflict)
	data, _, err := restored.Artifact(run.ID, a.ID)
	if err != nil || string(data) != `{"schema_version":1}` {
		t.Fatalf("artifact: %s %v", data, err)
	}
	for _, name := range []string{"store.json", runFile(run.ID), artifactFile(run.ID, a.ID)} {
		info, err := os.Stat(filepath.Join(path, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private mode %s: %v", name, err)
		}
	}
	code(t, restored.Update(run), protocol.RequestConflict)
}

func TestFaultBoundariesNeverReplay(t *testing.T) {
	for _, operation := range []string{"write", "sync", "rename", "directory_sync"} {
		t.Run(operation, func(t *testing.T) {
			fail := false
			s, path := testStore(t, Options{Fault: func(op, name string) error {
				if fail && op == operation && strings.HasPrefix(name, "run-") {
					return errors.New("injected")
				}
				return nil
			}})
			r := draft()
			fail = true
			_, _, err := s.Reserve(r)
			code(t, err, protocol.RecordingFailed)
			if !s.Status().ReadOnly {
				t.Fatal("uncertain commit remained writable")
			}
			_ = s.Close()
			restored, err := Open(path, DefaultLimits(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = restored.Close() }()
			_, found, err := restored.Lookup(r.RequestID, r.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			if found != (operation == "directory_sync") {
				t.Fatalf("durable receipt=%t for %s", found, operation)
			}
			if found {
				list, err := restored.List("", "", "", 10)
				if err != nil || list.Runs[0].State != "interrupted" {
					t.Fatal("admission was replayed or lost")
				}
			}
		})
	}
}

func TestQuotasConcurrentAdmissionAndAnnotationCAS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs")
	limits := DefaultLimits()
	limits.MaxRuns = 2
	s, err := Open(path, limits, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var wg sync.WaitGroup
	outcomes := make(chan error, 20)
	for range 20 {
		wg.Go(func() { _, _, err := s.Reserve(draft()); outcomes <- err })
	}
	wg.Wait()
	close(outcomes)
	successes := 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else {
			code(t, err, protocol.StorageFull)
		}
	}
	if successes != 2 {
		t.Fatalf("admitted %d", successes)
	}
	list, err := s.List("", "", "", 1)
	if err != nil || len(list.Runs) != 1 || list.NextCursor == "" {
		t.Fatalf("page %#v %v", list, err)
	}
	id := list.Runs[0].ID
	edits := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := s.Annotate(id, protocol.AnnotationUpdate{Title: "Edited", Revision: 0})
			edits <- err
		})
	}
	wg.Wait()
	close(edits)
	successes = 0
	for err := range edits {
		if err == nil {
			successes++
		} else {
			code(t, err, protocol.RequestConflict)
		}
	}
	if successes != 1 {
		t.Fatal("lost annotation update")
	}
	other, err := s.List("", "", list.NextCursor, 1)
	if err != nil || len(other.Runs) != 1 || other.Runs[0].ID == id {
		t.Fatalf("cursor %#v %v", other, err)
	}
	_, err = s.List("different", "", list.NextCursor, 1)
	code(t, err, protocol.InvalidRequest)
}

func TestDeletionReferencesBaselineAndTombstone(t *testing.T) {
	s, path := testStore(t, Options{})
	r := complete(t, s, reserve(t, s))
	baseline, err := s.SetBaseline("test", "bench", protocol.BaselineUpdate{RunID: r.ID})
	if err != nil || baseline.Revision != 1 {
		t.Fatal(err)
	}
	_, err = s.SetBaseline("test", "bench", protocol.BaselineUpdate{RunID: r.ID})
	code(t, err, protocol.RequestConflict)
	code(t, s.Delete(r.ID, false), protocol.ConfirmationRequired)
	release, err := s.Hold(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	code(t, s.Delete(r.ID, true), protocol.RequestConflict)
	release()
	release()
	annotation, err := s.Annotate(r.ID, protocol.AnnotationUpdate{Pinned: true})
	if err != nil {
		t.Fatal(err)
	}
	code(t, s.Delete(r.ID, true), protocol.RequestConflict)
	_, err = s.Annotate(r.ID, protocol.AnnotationUpdate{Revision: annotation.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(r.ID, true); err != nil {
		t.Fatal(err)
	}
	if s.Baseline("test", "bench").RunID != "" {
		t.Fatal("dangling baseline")
	}
	_ = s.Close()
	restored, err := Open(path, DefaultLimits(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	response, duplicate, err := restored.Reserve(r)
	if err != nil || !duplicate || !response.Deleted || response.RunID != r.ID {
		t.Fatalf("deleted admission replay: %#v %v", response, err)
	}
	_, err = restored.Get(r.ID)
	code(t, err, protocol.NotFound)
}

func TestCorruptionSymlinksAndNewerSchemaAreQuarantined(t *testing.T) {
	for _, kind := range []string{"run", "metadata", "missing_metadata", "symlink", "artifact"} {
		t.Run(kind, func(t *testing.T) {
			s, path := testStore(t, Options{})
			r := reserve(t, s)
			a, err := s.AddArtifact(r.ID, "log", "text/plain", []byte("data"))
			if err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			switch kind {
			case "run":
				err = os.WriteFile(filepath.Join(path, runFile(r.ID)), []byte(`{"schema_version":999}`), 0600)
			case "metadata":
				err = os.WriteFile(filepath.Join(path, "store.json"), []byte(`{"version":999}`), 0600)
			case "missing_metadata":
				err = os.Remove(filepath.Join(path, "store.json"))
			case "symlink":
				err = os.Symlink("/etc/passwd", filepath.Join(path, "unexpected"))
			case "artifact":
				err = os.WriteFile(filepath.Join(path, artifactFile(r.ID, a.ID)), []byte("bad"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			restored, err := Open(path, DefaultLimits(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = restored.Close() }()
			if !restored.Status().ReadOnly {
				t.Fatal("unsafe store writable")
			}
			_, _, err = restored.Reserve(draft())
			code(t, err, protocol.RecordingFailed)
			_, _, err = restored.Artifact(r.ID, "../../etc/passwd")
			code(t, err, protocol.NotFound)
		})
	}
}

func TestRequestWindowAndClockRollback(t *testing.T) {
	now := time.Now().UTC()
	s, _ := testStore(t, Options{Now: func() time.Time { return now }})
	for _, stamp := range []time.Time{now.Add(-25 * time.Hour), now.Add(6 * time.Minute)} {
		r := draft()
		r.RequestID = NewRequestID(stamp)
		_, _, err := s.Reserve(r)
		code(t, err, protocol.InvalidRequest)
	}
	r := reserve(t, s)
	now = now.Add(-time.Minute)
	_, _, err := s.Reserve(draft())
	code(t, err, protocol.RecordingFailed)
	_, found, err := s.Lookup(r.RequestID, r.RequestDigest)
	if err != nil || !found {
		t.Fatal("clock rollback discarded a known receipt")
	}
}

func TestImmutableSnapshotAndSeries(t *testing.T) {
	s, _ := testStore(t, Options{})
	r := reserve(t, s)
	r.Experiment.Title = "changed"
	code(t, s.Update(r), protocol.RequestConflict)
	unchanged, err := s.Get(r.ID)
	if err != nil || unchanged.Experiment.Title != "Bench" {
		t.Fatal("caller aliased stored snapshot")
	}
	valid := protocol.Series{SchemaVersion: 1, Name: "trace", X: []float64{0, 1}, Y: []float64{1, 2}, XUnit: "s", YUnit: "V", Quality: "valid"}
	if err := ValidSeries(valid); err != nil {
		t.Fatal(err)
	}
	valid.X[1] = 0
	code(t, ValidSeries(valid), protocol.InvalidRequest)
}
