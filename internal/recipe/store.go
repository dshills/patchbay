package recipe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/identity"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/localfs"
	"patchbay/pkg/protocol"
)

const MaxStoreBytes = 200 << 20
const maxStoreMetadata = 4 << 20

var ErrUncertain = errors.New("recipe selection durability is uncertain; restart before admitting more work")

type Mapping struct {
	Project string `json:"project,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Action  string `json:"action,omitempty"`
}
type Assignment struct {
	Device  string `json:"device,omitempty"`
	Control string `json:"control"`
	Gesture string `json:"gesture"`
}
type Installation struct {
	ID          string                `json:"id"`
	Alias       string                `json:"alias"`
	DeclaredID  string                `json:"declared_id"`
	Content     string                `json:"content"`
	Candidate   string                `json:"candidate,omitempty"`
	Previous    string                `json:"previous,omitempty"`
	Active      bool                  `json:"active"`
	BaseDigest  string                `json:"base_digest,omitempty"`
	Mappings    map[string]Mapping    `json:"mappings"`
	Assignments map[string]Assignment `json:"assignments"`
}
type ManagementResult struct {
	ID        string `json:"id"`
	Revision  uint64 `json:"revision"`
	Operation string `json:"operation"`
	Content   string `json:"content,omitempty"`
}
type Receipt struct {
	Digest string           `json:"digest"`
	At     time.Time        `json:"at"`
	Result ManagementResult `json:"result"`
}
type Selection struct {
	SchemaVersion int                `json:"schema_version"`
	Revision      uint64             `json:"revision"`
	Clock         time.Time          `json:"clock"`
	Installations []Installation     `json:"installations"`
	Receipts      map[string]Receipt `json:"receipts"`
}
type StoreOptions struct {
	Fault func(operation, name string) error
}
type Store struct {
	mu               sync.Mutex
	root             *os.Root
	lock             *localfs.Lock
	selected         Selection
	closed, readOnly bool
	options          StoreOptions
}

func OpenStore(directory string, options StoreOptions) (*Store, error) {
	lock, err := localfs.Acquire(filepath.Join(directory, ".lock"))
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	s := &Store{root: root, lock: lock, options: options, selected: Selection{SchemaVersion: 1, Installations: []Installation{}, Receipts: map[string]Receipt{}}}
	data, err := s.read("selection.json", maxStoreMetadata)
	if err == nil {
		err = jsonstrict.Decode(data, &s.selected)
		if err == nil {
			err = validSelection(s.selected)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = s.Close()
		return nil, fail("store", "invalid or unsupported selection; original files preserved")
	}
	if time.Now().Before(s.selected.Clock) {
		s.readOnly = true
	}
	// selection.json is authoritative. An intent never promotes itself on restart.
	// Incomplete intent/stage files stay charged and available for offline diagnosis.
	if _, err := s.used(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}
func validSelection(s Selection) error {
	if s.SchemaVersion != 1 || len(s.Installations) > 50 || len(s.Receipts) > 10000 || s.Receipts == nil {
		return fail("store", "unsupported schema or quota")
	}
	ids, aliases := map[string]bool{}, map[string]bool{}
	for _, entry := range s.Installations {
		if !config.ValidName(entry.ID) || !config.ValidName(entry.DeclaredID) || len(entry.Mappings) > 64 || len(entry.Assignments) > 32 || !config.ValidName(entry.Alias) || ids[entry.ID] || aliases[strings.ToLower(entry.Alias)] || !hexDigest.MatchString(entry.Content) || entry.Candidate != "" && !hexDigest.MatchString(entry.Candidate) || entry.Previous != "" && !hexDigest.MatchString(entry.Previous) || entry.Active && !hexDigest.MatchString(entry.BaseDigest) {
			return fail("store", "invalid installation identity or selection")
		}
		ids[entry.ID] = true
		aliases[strings.ToLower(entry.Alias)] = true
	}
	for id, receipt := range s.Receipts {
		if _, err := evidence.RequestTime(id); err != nil || !hexDigest.MatchString(receipt.Digest) || receipt.At.IsZero() {
			return fail("store", "invalid management receipt")
		}
	}
	return nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.root.Close(), s.lock.Close())
}
func (s *Store) Snapshot() Selection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return evidence.Clone(s.selected)
}
func (s *Store) Writable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && !s.readOnly && !time.Now().Before(s.selected.Clock)
}
func (s *Store) check() error {
	if s.closed || s.readOnly || time.Now().Before(s.selected.Clock) {
		return fail("store", "store is closed or read-only")
	}
	return nil
}
func (s *Store) Package(ctx context.Context, digest string) (*Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !hexDigest.MatchString(digest) || s.closed {
		return nil, fail("store", "invalid content reference")
	}
	data, err := s.read(digest+".zip", MaxPackage)
	if err != nil {
		return nil, err
	}
	p, err := InspectZIP(ctx, data)
	if err != nil {
		return nil, err
	}
	if p.Digest != digest {
		return nil, fail("store", "stored package digest mismatch")
	}
	return p, nil
}
func (s *Store) Import(ctx context.Context, p *Package, target, alias string) (Installation, error) {
	data, err := ZIP(ctx, p)
	if err != nil {
		return Installation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return Installation{}, err
	}
	next := evidence.Clone(s.selected)
	index := -1
	for i, entry := range next.Installations {
		if target == "" && (entry.Content == p.Digest || entry.Candidate == p.Digest) {
			return entry, nil
		}
		if entry.ID == target {
			index = i
		}
	}
	if target != "" && index < 0 {
		return Installation{}, fail("installation", "explicit update target not found")
	}
	if index < 0 {
		if len(next.Installations) >= 50 {
			return Installation{}, fail("store", "50 installation limit reached")
		}
		id := "r" + strings.ToLower(identity.New())
		if alias == "" {
			alias = p.Manifest.ID
		}
		if !config.ValidName(alias) {
			return Installation{}, fail("alias", "expected a portable identifier")
		}
		usedAliases := map[string]bool{}
		for _, entry := range next.Installations {
			usedAliases[strings.ToLower(entry.Alias)] = true
		}
		base := alias
		for suffix := 0; usedAliases[strings.ToLower(alias)]; suffix++ {
			tail := fmt.Sprintf("-%s-%d", id[1:7], suffix)
			alias = base[:min(len(base), 128-len(tail))] + tail
		}
		next.Installations = append(next.Installations, Installation{ID: id, Alias: alias, DeclaredID: p.Manifest.ID, Content: p.Digest, Mappings: map[string]Mapping{}, Assignments: map[string]Assignment{}})
		index = len(next.Installations) - 1
	} else {
		next.Installations[index].Candidate = p.Digest
	}
	if existing, err := s.read(p.Digest+".zip", MaxPackage); errors.Is(err, os.ErrNotExist) {
		used, err := s.used()
		if err != nil {
			return Installation{}, err
		}
		if used+int64(len(data))+16<<20 > MaxStoreBytes {
			return Installation{}, fail("store", "200 MiB quota reached; remove unused content explicitly")
		}
		if _, err = s.write(p.Digest+".zip", data); err != nil {
			return Installation{}, err
		}
	} else if err != nil {
		return Installation{}, err
	} else if !bytes.Equal(existing, data) {
		return Installation{}, fail("store", "existing immutable content differs; inspect store corruption")
	}
	if err := ctx.Err(); err != nil {
		return Installation{}, err
	}
	next.Revision++
	next.Clock = time.Now().UTC()
	if err := s.selectState(next); err != nil {
		return Installation{}, err
	}
	return evidence.Clone(next.Installations[index]), nil
}
func (s *Store) Lookup(id, digest string) (ManagementResult, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if receipt, ok := s.selected.Receipts[id]; ok {
		if receipt.Digest != digest {
			return ManagementResult{}, true, &protocol.Error{Code: protocol.RequestConflict, Message: "Management request ID was already used with different content."}
		}
		return receipt.Result, true, nil
	}
	return ManagementResult{}, false, nil
}
func (s *Store) Commit(expected uint64, next Selection, id, digest string, result ManagementResult) (ManagementResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return ManagementResult{}, err
	}
	if expected != s.selected.Revision {
		return ManagementResult{}, &protocol.Error{Code: protocol.StalePreparation, Message: "Recipe state changed; review again."}
	}
	now := time.Now().UTC()
	created, err := evidence.RequestTime(id)
	if err != nil || created.Before(now.Add(-24*time.Hour)) || created.After(now.Add(5*time.Minute)) {
		return ManagementResult{}, fail("request_id", "unknown request ID is outside its admission window")
	}
	if _, exists := s.selected.Receipts[id]; exists {
		return ManagementResult{}, fail("request_id", "duplicate request must use receipt lookup")
	}
	next.Receipts = evidence.Clone(s.selected.Receipts)
	for key, receipt := range next.Receipts {
		if now.After(receipt.At.Add(25 * time.Hour)) {
			delete(next.Receipts, key)
		}
	}
	if len(next.Receipts) >= 10000 {
		return ManagementResult{}, fail("store", "management receipt quota reached")
	}
	next.SchemaVersion = 1
	next.Revision = expected + 1
	next.Clock = now
	result.Revision = next.Revision
	next.Receipts[id] = Receipt{Digest: digest, At: now, Result: result}
	if err := validSelection(next); err != nil {
		return ManagementResult{}, err
	}
	if err := s.selectState(next); err != nil {
		return ManagementResult{}, err
	}
	return result, nil
}
func (s *Store) selectState(next Selection) error {
	if err := validSelection(next); err != nil {
		return err
	}
	data, err := json.Marshal(next)
	if err != nil || len(data) > maxStoreMetadata {
		return fail("store", "selection exceeds metadata limit")
	}
	if _, err := s.write("intent.json", data); err != nil {
		return err
	}
	renamed, err := s.write("selection.json", data)
	if err != nil {
		if renamed {
			s.readOnly = true
			return fmt.Errorf("%w: %v", ErrUncertain, err)
		}
		return err
	}
	s.selected = next
	// The committed selection and receipt are sufficient for recovery; an old intent
	// is inert and is replaced by the next management transaction.
	return nil
}
func (s *Store) read(name string, limit int) ([]byte, error) {
	f, err := s.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > int64(limit) {
		return nil, fail("store", "expected a bounded private regular file")
	}
	return readBounded(context.Background(), f, limit)
}
func (s *Store) used() (int64, error) {
	dir, err := s.root.Open(".")
	if err != nil {
		return 0, err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(1025)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if len(entries) > 1024 {
		return 0, fail("store", "too many stored files")
	}
	var total int64
	for _, entry := range entries {
		info, err := s.root.Lstat(entry.Name())
		if err != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0077 != 0 {
			return 0, fail("store", "unsafe store entry")
		}
		total += info.Size()
	}
	if total > MaxStoreBytes {
		return 0, fail("store", "stored bytes exceed quota")
	}
	return total, nil
}
func (s *Store) write(name string, data []byte) (bool, error) {
	used, err := s.used()
	if err != nil {
		return false, err
	}
	if used+int64(len(data)) > MaxStoreBytes {
		return false, fail("store", "insufficient staging space")
	}
	fault := func(op string) error {
		if s.options.Fault != nil {
			return s.options.Fault(op, name)
		}
		return nil
	}
	temp := ".stage-" + identity.New()
	renamed := false
	defer func() {
		if !renamed {
			_ = s.root.Remove(temp)
		}
	}()
	f, err := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	if err = fault("write"); err == nil {
		_, err = f.Write(data)
	}
	if err != nil {
		return false, err
	}
	if err = fault("sync"); err == nil {
		err = f.Sync()
	}
	if err != nil {
		return false, err
	}
	if err = f.Close(); err != nil {
		return false, err
	}
	if err = fault("rename"); err == nil {
		err = s.root.Rename(temp, name)
	}
	if err != nil {
		return false, err
	}
	renamed = true
	if err = fault("dirsync"); err != nil {
		return true, err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return true, err
	}
	defer func() { _ = dir.Close() }()
	return true, dir.Sync()
}

// Unused lists content outside current/previous/staged selections. Jobs own their
// prepared definitions and never reopen these archives. Orphaned staging files
// remain charged until this explicit cleanup is reviewed.
func (s *Store) Unused() ([]File, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	used, err := s.used()
	if err != nil {
		return nil, 0, err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, 0, err
	}
	live := s.references()
	out := []File{}
	for _, entry := range entries {
		name := entry.Name()
		if !cleanupName(name) || live[name] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, 0, err
		}
		out = append(out, File{Path: name, Size: info.Size()})
	}
	return out, used, nil
}
func cleanupName(name string) bool {
	return strings.HasPrefix(name, ".stage-") || strings.HasSuffix(name, ".zip") && hexDigest.MatchString(strings.TrimSuffix(name, ".zip"))
}
func (s *Store) references() map[string]bool {
	live := map[string]bool{}
	for _, entry := range s.selected.Installations {
		for _, digest := range []string{entry.Content, entry.Previous, entry.Candidate} {
			if digest != "" {
				live[digest+".zip"] = true
			}
		}
	}
	return live
}

// Cleanup is called only after the management receipt has committed. Repeating
// deletion of the same reviewed set is safe; new files are never swept implicitly.
func (s *Store) Cleanup(files []File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	live := s.references()
	for _, file := range files {
		if !cleanupName(file.Path) || live[file.Path] {
			return fail("cleanup", "content remains selected")
		}
		if err := s.root.Remove(file.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
