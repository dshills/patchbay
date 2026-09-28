package patching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"patchbay/internal/evidence"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/localfs"
	"patchbay/pkg/protocol"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
)

const MaxOperations = 64
const MaxStorage = 64 << 20
const MaxRecord = 8 << 20

type Record struct {
	ID      string                      `json:"id"`
	Run     string                      `json:"run"`
	Project string                      `json:"project"`
	State   string                      `json:"state"`
	Message string                      `json:"message,omitempty"`
	Plan    Plan                        `json:"plan"`
	Files   []protocol.PatchFileOutcome `json:"files"`
}
type Store struct {
	execute chan struct{}
	sizes   map[string]int
	used    int
	mu      sync.Mutex
	root    *os.Root
	lock    *localfs.Lock
	records map[string]Record
	failed  bool
	Fault   func(string) error
}

var operationID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func Open(path string, fault func(string) error) (*Store, error) {
	if err := localfs.PrivateDir(path); err != nil {
		return nil, err
	}
	root, err := localfs.OpenDirectory(path)
	if err != nil {
		return nil, err
	}
	lock, err := localfs.Acquire(path + "/lock")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	s := &Store{execute: make(chan struct{}, 1), root: root, lock: lock, records: map[string]Record{}, sizes: map[string]int{}, Fault: fault}
	fail := func(err error) (*Store, error) { _ = s.Close(); return nil, err }
	dir, err := root.Open(".")
	if err != nil {
		return fail(err)
	}
	entries, err := dir.ReadDir(2*MaxOperations + 4)
	_ = dir.Close()
	if err != nil {
		return fail(err)
	}
	if len(entries) > MaxOperations+2 {
		return fail(errors.New("patch journal directory quota"))
	}
	used := 0
	for _, entry := range entries {
		name := entry.Name()
		if name == "lock" {
			continue
		}
		if name == "journal.pending" {
			if _, err = s.readFile(name, MaxStorage); err != nil {
				return fail(err)
			}
			if err = root.Remove(name); err != nil {
				return fail(err)
			}
			continue
		}
		if !strings.HasPrefix(name, "operation-") || !strings.HasSuffix(name, ".json") {
			return fail(errors.New("unknown patch journal file"))
		}
		data, err := s.readFile(name, MaxRecord)
		if err != nil {
			return fail(err)
		}
		used += len(data)
		if used > MaxStorage {
			return fail(errors.New("patch storage quota"))
		}
		var record Record
		if jsonstrict.Decode(data, &record) != nil || !validRecord(record) || name != "operation-"+record.ID+".json" {
			return fail(errors.New("invalid patch journal"))
		}
		s.records[record.ID] = record
		s.sizes[record.ID] = len(data)
		s.used += len(data)
	}
	if len(s.records) > MaxOperations {
		return fail(errors.New("patch operation quota"))
	}
	// Reconcile only recorded paths. Recovery never dispatches a write or reverts.
	for _, record := range s.records {
		if record.State == "intent" || record.State == "staging" || record.State == "ready" || record.State == "applying" {
			record.State = "interrupted"
			record.Message = "Daemon restarted; inspect each current hash. No write was replayed."
		}
		s.classify(&record)
		s.cleanup(&record)
		if err = s.save(record, false); err != nil {
			return fail(err)
		}
	}
	return s, nil
}
func (s *Store) Close() error { return errors.Join(s.root.Close(), s.lock.Close()) }
func (s *Store) readFile(name string, limit int) ([]byte, error) {
	f, err := s.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > int64(limit) {
		return nil, errors.New("invalid private patch journal")
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(b) > limit {
		return nil, errors.New("patch journal too large")
	}
	return b, err
}
func validRecord(r Record) bool {
	if !operationID.MatchString(r.ID) || !filepath.IsAbs(r.Plan.Root) || filepath.Clean(r.Plan.Root) != r.Plan.Root || len(r.Plan.Files) == 0 || len(r.Plan.Files) > 10 || len(r.Files) != len(r.Plan.Files) || len(r.Plan.Preview.Files) != len(r.Plan.Files) || len(r.Plan.Preview.Diff) > MaxDiff || r.Plan.Preview.Digest != evidence.Digest([]byte(r.Plan.Preview.Diff)) {
		return false
	}
	total := 0
	seen := map[string]bool{}
	for i, f := range r.Plan.Files {
		total += len(f.BeforeText) + len(f.AfterText)
		if r.Files[i].Path != f.Path || r.Plan.Preview.Files[i] != f.PatchFile || !filepath.IsLocal(f.Path) || filepath.Clean(f.Path) != f.Path || seen[f.Path] || f.Stage != stageName(r.ID, i) || len(f.BeforeText) > MaxFile || len(f.AfterText) > MaxFile || f.Mode&^0777 != 0 || f.Before != evidence.Digest([]byte(f.BeforeText)) || f.After != evidence.Digest([]byte(f.AfterText)) {
			return false
		}
		seen[f.Path] = true
	}
	return total <= MaxContent
}
func stageName(id string, index int) string { return fmt.Sprintf(".patchbay-%s-%d.pending", id, index) }
func (s *Store) inject(phase string) error {
	if s.Fault != nil {
		return s.Fault(phase)
	}
	return nil
}
func syncRoot(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// save is called under the store lock (or during single-threaded opening).
func (s *Store) save(record Record, inject bool) error {
	if s.failed {
		return errors.New("patch journal is read-only")
	}
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(b) > MaxRecord {
		return errors.New("patch record quota reached")
	}
	used := s.used - s.sizes[record.ID] + len(b)
	if used > MaxStorage {
		return errors.New("patch journal quota reached")
	}
	if inject {
		if err = s.inject("journal_write"); err != nil {
			return err
		}
	}
	f, err := s.root.OpenFile("journal.pending", os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	err = errors.Join(err, closeErr)
	if err == nil {
		err = s.root.Rename("journal.pending", "operation-"+record.ID+".json")
	}
	if err != nil {
		_ = s.root.Remove("journal.pending")
		return err
	}
	s.records[record.ID] = evidence.Clone(record)
	s.used = used
	s.sizes[record.ID] = len(b)
	if err = syncRoot(s.root); err != nil {
		s.failed = true
		return err
	}
	if inject {
		if err = s.inject("journal_synced"); err != nil {
			s.failed = true
			return err
		}
	}
	return nil
}
func (s *Store) Begin(id, run, project string, plan Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || len(s.records) >= MaxOperations {
		return errors.New("patch journal unavailable or full")
	}
	if _, exists := s.records[id]; exists {
		return errors.New("patch operation already reserved")
	}
	// Break every shared slice before assigning daemon-owned staging paths.
	b, _ := json.Marshal(plan)
	var frozen Plan
	if err := json.Unmarshal(b, &frozen); err != nil {
		return err
	}
	record := Record{ID: id, Run: run, Project: project, State: "intent", Plan: frozen, Files: []protocol.PatchFileOutcome{}}
	for i := range record.Plan.Files {
		record.Plan.Files[i].Stage = stageName(id, i)
		record.Files = append(record.Files, protocol.PatchFileOutcome{Path: record.Plan.Files[i].Path, State: "unapplied"})
	}
	if !validRecord(record) {
		return errors.New("invalid patch intent")
	}
	return s.save(record, true)
}
func (s *Store) outcome(r Record) protocol.PatchOutcome {
	return protocol.PatchOutcome{Operation: r.ID, Run: r.Run, Project: r.Project, State: r.State, Message: r.Message, Files: append([]protocol.PatchFileOutcome(nil), r.Files...)}
}
func (s *Store) List(project string) []protocol.PatchOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []protocol.PatchOutcome{}
	for _, r := range s.records {
		if project == "" || r.Project == project {
			out = append(out, s.outcome(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Operation < out[j].Operation })
	return out
}
func (s *Store) Get(id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return r, os.ErrNotExist
	}
	b, _ := json.Marshal(r)
	var copy Record
	err := json.Unmarshal(b, &copy)
	return copy, err
}
func (s *Store) classify(r *Record) {
	dev, ino, err := directoryIdentity(r.Plan.Root)
	for i, f := range r.Plan.Files {
		state := "conflicted"
		if err == nil && dev == r.Plan.Device && ino == r.Plan.Inode {
			data, mode, e := read(r.Plan.Root, f.Path)
			if e == nil && mode == os.FileMode(f.Mode) {
				switch evidence.Digest(data) {
				case f.Before:
					state = "unapplied"
				case f.After:
					state = "applied"
				}
			}
		}
		r.Files[i].State = state
	}
}
func (s *Store) cleanup(r *Record) {
	dev, ino, err := directoryIdentity(r.Plan.Root)
	if err != nil || dev != r.Plan.Device || ino != r.Plan.Inode {
		for i := range r.Files {
			r.Files[i].Cleanup = "unverified project directory"
		}
		return
	}
	for i, f := range r.Plan.Files {
		parent, err := localfs.OpenDirectory(filepath.Join(r.Plan.Root, filepath.Dir(f.Path)))
		if err != nil {
			r.Files[i].Cleanup = "unverified parent"
			continue
		}
		func() {
			defer func() { _ = parent.Close() }()
			info, err := parent.Lstat(f.Stage)
			if errors.Is(err, os.ErrNotExist) {
				r.Files[i].Cleanup = ""
				return
			}
			if err != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Size() != int64(len(f.AfterText)) {
				r.Files[i].Cleanup = "unverified staging file retained"
				return
			}
			dev, ino := inodeIdentity(info)
			if f.StageInode != 0 && (dev != f.StageDevice || ino != f.StageInode) {
				r.Files[i].Cleanup = "replaced staging file retained"
				return
			}
			file, err := parent.OpenFile(f.Stage, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				r.Files[i].Cleanup = "unreadable staging file retained"
				return
			}
			opened, statErr := file.Stat()
			data, readErr := io.ReadAll(io.LimitReader(file, MaxFile+1))
			_ = file.Close()
			current, currentErr := parent.Lstat(f.Stage)
			if statErr != nil || readErr != nil || currentErr != nil || !os.SameFile(info, opened) || !os.SameFile(opened, current) || evidence.Digest(data) != f.After {
				r.Files[i].Cleanup = "changed staging file retained"
				return
			}
			if err = parent.Remove(f.Stage); err == nil {
				err = syncRoot(parent)
			}
			if err != nil {
				r.Files[i].Cleanup = "staging cleanup failed"
			} else {
				r.Files[i].Cleanup = ""
			}
		}()
	}
}

// Apply serializes Patchbay patches, stages every file, then replaces each exact
// preimage. External editors do not share this lock; every rename is rechecked.
func (s *Store) Apply(ctx context.Context, id string) (protocol.PatchOutcome, error) {
	select {
	case s.execute <- struct{}{}:
		defer func() { <-s.execute }()
	case <-ctx.Done():
		return protocol.PatchOutcome{}, ctx.Err()
	}
	s.mu.Lock()
	record, ok := s.records[id]
	if !ok {
		s.mu.Unlock()
		return protocol.PatchOutcome{}, os.ErrNotExist
	}
	if s.failed || record.State != "intent" {
		s.mu.Unlock()
		return s.outcome(record), errors.New("patch cannot be replayed")
	}
	record = evidence.Clone(record)
	record.State = "staging"
	claimErr := s.save(record, true)
	s.mu.Unlock()
	if claimErr != nil {
		return s.outcome(record), claimErr
	}
	finish := func(cause error) (protocol.PatchOutcome, error) {
		if cause != nil {
			record.State = "failed"
			record.Message = cause.Error()
			if ctx.Err() != nil {
				record.State = "cancelled"
			}
		} else {
			record.State = "applied"
		}
		s.classify(&record)
		s.cleanup(&record)
		if err := s.persist(record); err != nil {
			s.mu.Lock()
			s.failed = true
			record.State = "recording_failed"
			record.Message = "Patch journal write failed; inspect recovery before further changes."
			s.records[id] = evidence.Clone(record)
			s.mu.Unlock()
			cause = errors.Join(cause, err)
		}
		return s.outcome(record), cause
	}
	projectRoot, err := localfs.OpenDirectory(record.Plan.Root)
	if err != nil {
		return finish(err)
	}
	defer func() { _ = projectRoot.Close() }()
	projectLock, err := projectRoot.Open(".")
	if err != nil {
		return finish(err)
	}
	defer func() { _ = projectLock.Close() }()
	if err = syscall.Flock(int(projectLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return finish(errors.New("another Patchbay patch holds the project write lock"))
	}
	if err := Validate(ctx, record.Plan); err != nil {
		return finish(err)
	}
	for i := range record.Plan.Files {
		f := &record.Plan.Files[i]
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		parent, err := localfs.OpenDirectory(filepath.Join(record.Plan.Root, filepath.Dir(f.Path)))
		if err != nil {
			return finish(err)
		}
		err = func() error {
			defer func() { _ = parent.Close() }()
			stage, err := parent.OpenFile(f.Stage, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
			if err != nil {
				return err
			}
			defer func() { _ = stage.Close() }()
			info, err := stage.Stat()
			if err != nil {
				return err
			}
			f.StageDevice, f.StageInode = inodeIdentity(info)
			if err = s.persist(record); err != nil {
				return err
			}
			if err = s.inject("stage_created"); err != nil {
				return err
			}
			if _, err = stage.WriteString(f.AfterText); err != nil {
				return err
			}
			if err = stage.Chmod(os.FileMode(f.Mode)); err != nil {
				return err
			}
			if err = stage.Sync(); err != nil {
				return err
			}
			if err = syncRoot(parent); err != nil {
				return err
			}
			if _, err = stage.Seek(0, 0); err != nil {
				return err
			}
			data, err := io.ReadAll(io.LimitReader(stage, MaxFile+1))
			if err != nil || evidence.Digest(data) != f.After {
				return errors.New("staged after-image verification failed")
			}
			return s.inject("staged")
		}()
		if err != nil {
			return finish(err)
		}
	}
	record.State = "ready"
	if err := s.persist(record); err != nil {
		return finish(err)
	}
	if err := s.inject("ready"); err != nil {
		return finish(err)
	}
	for i, f := range record.Plan.Files {
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		current, err := head(ctx, record.Plan.Root)
		if err != nil || current != record.Plan.Preview.Head {
			return finish(errors.New("Git HEAD changed before replacement"))
		}
		if err = tracked(ctx, record.Plan.Root, f.Path); err != nil {
			return finish(err)
		}
		dev, ino, err := directoryIdentity(record.Plan.Root)
		if err != nil || dev != record.Plan.Device || ino != record.Plan.Inode {
			return finish(errors.New("project directory changed"))
		}
		parent, err := localfs.OpenDirectory(filepath.Join(record.Plan.Root, filepath.Dir(f.Path)))
		if err != nil {
			return finish(err)
		}
		err = func() error {
			defer func() { _ = parent.Close() }()
			// Both reads are bound to the same parent used for the atomic replacement.
			data, mode, err := readAt(parent, filepath.Base(f.Path))
			if err != nil || mode != os.FileMode(f.Mode) || evidence.Digest(data) != f.Before {
				return errors.New("preimage changed immediately before replacement")
			}
			stage, err := parent.OpenFile(f.Stage, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			info, statErr := stage.Stat()
			bytes, readErr := io.ReadAll(io.LimitReader(stage, MaxFile+1))
			_ = stage.Close()
			if statErr != nil || readErr != nil || !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm() != os.FileMode(f.Mode) || !localfs.Owned(info) || evidence.Digest(bytes) != f.After {
				return errors.New("staging changed before replacement")
			}
			dev, ino := inodeIdentity(info)
			if dev != f.StageDevice || ino != f.StageInode {
				return errors.New("staging identity changed")
			}
			if err = s.inject("before_replace"); err != nil {
				return err
			}
			// Recheck target after the fault/cancellation injection boundary as well.
			if err = ctx.Err(); err != nil {
				return err
			}
			data, mode, err = readAt(parent, filepath.Base(f.Path))
			if err != nil || mode != os.FileMode(f.Mode) || evidence.Digest(data) != f.Before {
				return errors.New("preimage changed before rename")
			}
			rootDev, rootIno, rootErr := directoryIdentity(record.Plan.Root)
			if rootErr != nil || rootDev != record.Plan.Device || rootIno != record.Plan.Inode {
				return errors.New("project replaced at rename boundary")
			}
			parentInfo, parentErr := parent.Stat(".")
			if parentErr != nil {
				return parentErr
			}
			parentDev, parentIno := inodeIdentity(parentInfo)
			currentDev, currentIno, parentErr := directoryIdentity(parent.Name())
			if parentErr != nil || currentDev != parentDev || currentIno != parentIno {
				return errors.New("parent directory replaced at rename boundary")
			}
			verified, err := parent.OpenFile(f.Stage, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			finalInfo, finalErr := verified.Stat()
			finalData, finalReadErr := io.ReadAll(io.LimitReader(verified, MaxFile+1))
			_ = verified.Close()
			if finalErr != nil || finalReadErr != nil || !os.SameFile(info, finalInfo) || finalInfo.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || finalInfo.Mode().Perm() != os.FileMode(f.Mode) || evidence.Digest(finalData) != f.After {
				return errors.New("staged content changed at rename boundary")
			}
			currentStage, err := parent.Lstat(f.Stage)
			if err != nil || !os.SameFile(finalInfo, currentStage) {
				return errors.New("staging path replaced at rename boundary")
			}
			if err = parent.Rename(f.Stage, filepath.Base(f.Path)); err != nil {
				return err
			}
			if err = syncRoot(parent); err != nil {
				return err
			}
			record.Files[i].State = "applied"
			record.State = "applying"
			if err = s.inject("replaced"); err != nil {
				return err
			}
			return s.persist(record)
		}()
		if err != nil {
			return finish(err)
		}
	}
	return finish(nil)
}
func (s *Store) Forget(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return os.ErrNotExist
	}
	if r.State == "intent" || r.State == "staging" || r.State == "ready" || r.State == "applying" || s.failed {
		return errors.New("active or uncertain patch cannot be forgotten")
	}
	s.cleanup(&r)
	for _, f := range r.Files {
		if f.Cleanup != "" {
			return errors.New("unverified staging files need manual recovery")
		}
	}
	if err := s.root.Remove("operation-" + id + ".json"); err != nil {
		return err
	}
	delete(s.records, id)
	s.used -= s.sizes[id]
	delete(s.sizes, id)
	if err := syncRoot(s.root); err != nil {
		s.failed = true
		return err
	}
	return nil
}

// Restoration is a new complete diff against current approved after-images.
func (s *Store) Restoration(ctx context.Context, id string) (string, error) {
	record, err := s.Get(id)
	if err != nil {
		return "", err
	}
	if record.State == "intent" || record.State == "staging" || record.State == "ready" || record.State == "applying" {
		return "", errors.New("patch is still active")
	}
	var diff strings.Builder
	for _, f := range record.Plan.Files {
		data, mode, err := read(record.Plan.Root, f.Path)
		if err != nil || mode != os.FileMode(f.Mode) {
			return "", errors.New("restoration target changed")
		}
		hash := evidence.Digest(data)
		if hash == f.Before {
			continue
		}
		if hash != f.After {
			return "", errors.New("conflicted file requires manual recovery")
		}
		diff.WriteString(fullDiff(f.Path, f.AfterText, f.BeforeText))
	}
	if diff.Len() == 0 || diff.Len() > MaxDiff {
		return "", errors.New("restoration is empty or exceeds full review limit")
	}
	return diff.String(), ctx.Err()
}
func fullDiff(name, before, after string) string {
	a, b := lines(before), lines(after)
	startA, startB := 1, 1
	if len(a) == 0 {
		startA = 0
	}
	if len(b) == 0 {
		startB = 0
	}
	out := fmt.Sprintf("--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n", name, name, startA, len(a), startB, len(b))
	for _, group := range []struct {
		prefix string
		lines  []string
	}{{"-", a}, {"+", b}} {
		for _, line := range group.lines {
			out += group.prefix + line
			if !strings.HasSuffix(line, "\n") {
				out += "\n\\ No newline at end of file\n"
			}
		}
	}
	return out
}

// FinalizePending records cancellation/admission failure before the worker started.
func (s *Store) FinalizePending(id, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok || r.State != "intent" {
		return
	}
	r.State = "unapplied"
	r.Message = message
	s.classify(&r)
	s.cleanup(&r)
	if err := s.save(r, true); err != nil {
		s.failed = true
	}
}

func (s *Store) Writable() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.failed }

// persist holds only the metadata transaction lock, never the full execution lock.
func (s *Store) persist(record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(record, true)
}
