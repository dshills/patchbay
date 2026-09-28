package supervisor

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"sync"
	"syscall"
	"time"

	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/localfs"
	"patchbay/pkg/protocol"
)

type Options struct {
	Fault func(string) error
	Now   func() time.Time
}
type Receipt struct {
	Digest  string    `json:"digest"`
	Session string    `json:"session"`
	At      time.Time `json:"at"`
}
type saved struct {
	Key      string             `json:"key,omitempty"`
	Version  int                `json:"version"`
	Written  time.Time          `json:"written"`
	Sessions map[string]Session `json:"sessions"`
	Receipts map[string]Receipt `json:"receipts"`
}
type Store struct {
	proposalIndex map[string]string
	mu            sync.Mutex
	root          *os.Root
	lock          *localfs.Lock
	data          saved
	options       Options
	failed        bool
	boot          bool
}

func Open(path string, options Options) (*Store, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
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
	s := &Store{boot: true, root: root, lock: lock, options: options, data: saved{Version: 1, Sessions: map[string]Session{}, Receipts: map[string]Receipt{}}}
	// A single fixed staging leaf bounds crash leftovers. Never promote it.
	if info, err := root.Lstat("sessions.pending"); err == nil {
		if !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > MaxStorage {
			_ = s.Close()
			return nil, errors.New("invalid agent staging file")
		}
		if err := root.Remove("sessions.pending"); err != nil {
			_ = s.Close()
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = s.Close()
		return nil, err
	}
	f, err := root.OpenFile("sessions.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err == nil {
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > MaxStorage {
			_ = f.Close()
			_ = s.Close()
			return nil, errors.New("invalid private agent store")
		}
		b, readErr := io.ReadAll(io.LimitReader(f, MaxStorage+1))
		_ = f.Close()
		if readErr != nil || len(b) > MaxStorage || jsonstrict.Decode(b, &s.data) != nil || s.data.Version != 1 || s.data.Sessions == nil || s.data.Receipts == nil || len(s.data.Sessions) > MaxSessions || len(s.data.Receipts) > 10000 {
			_ = s.Close()
			return nil, errors.New("invalid agent metadata")
		}
		for id, v := range s.data.Sessions {
			if id != v.ID || v.RequestID == "" {
				_ = s.Close()
				return nil, errors.New("invalid session identity")
			}
			if v.State == "generating" || v.State == "executing" || v.State == "awaiting_review" {
				v.State = "interrupted"
				v.Error = &protocol.Error{Code: protocol.Cancelled, Message: "Daemon restarted. Requests and effects were not replayed."}
			}
			for i := range v.Proposals {
				if v.Proposals[i].State == "pending" {
					v.Proposals[i].State = "invalidated"
				}
			}
			s.data.Sessions[id] = v
		}
		if err := s.persist(s.data); err != nil {
			_ = s.Close()
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = s.Close()
		return nil, err
	}
	if s.data.Key == "" {
		s.data.Key = rand.Text() + rand.Text()
		if err := s.persist(s.data); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	s.boot = false
	return s, nil
}
func (s *Store) inject(op string) error {
	if s.options.Fault != nil && !s.boot {
		return s.options.Fault(op)
	}
	return nil
}
func (s *Store) persist(next saved) (result error) {
	defer func() {
		if result != nil && fault.Safe(result).Code == protocol.Internal {
			result = fault.New(protocol.RecordingFailed, "Agent audit could not be saved.")
		}
	}()
	if s.failed || s.options.Now().Before(s.data.Written) {
		s.failed = true
		return fault.New(protocol.RecordingFailed, "Agent store is read-only; restart and inspect local storage.")
	}
	next.Written = s.options.Now().UTC()
	b, err := json.Marshal(next)
	if err != nil || len(b) > MaxStorage {
		return fault.New(protocol.StorageFull, "Agent metadata quota exceeded; explicitly forget a terminal session.")
	}
	name := "sessions.pending"
	if err = s.inject("create"); err != nil {
		return err
	}
	f, err := s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = s.root.Remove(name) }()
	if err = s.inject("write"); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = s.inject("sync")
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fault.New(protocol.RecordingFailed, "Agent audit could not be saved; no new work was admitted.")
	}
	if err = s.inject("rename"); err == nil {
		err = s.root.Rename(name, "sessions.json")
	}
	if err != nil {
		return fault.New(protocol.RecordingFailed, "Agent audit rename failed.")
	}
	// After rename an uncertain directory sync must never permit another dispatch.
	s.data = next
	s.indexProposals()
	dir, err := s.root.Open(".")
	if err == nil {
		err = s.inject("directory_sync")
		if err == nil {
			err = dir.Sync()
		}
		_ = dir.Close()
	}
	if err != nil {
		s.failed = true
		return fault.New(protocol.RecordingFailed, "Agent audit durability is uncertain; restart before further agent work.")
	}
	return nil
}
func (s *Store) Lookup(request, digest string) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookup(request, digest)
}
func (s *Store) lookup(request, digest string) (Session, bool, error) {
	if r, ok := s.data.Receipts[request]; ok {
		if r.Digest != digest {
			return Session{}, true, fault.New(protocol.RequestConflict, "Request ID already refers to another decision.")
		}
		v, exists := s.data.Sessions[r.Session]
		if !exists {
			return Session{}, true, fault.New(protocol.NotFound, "Session was forgotten; its request cannot be replayed.")
		}
		return evidence.Clone(v), true, nil
	}
	return Session{}, false, nil
}
func (s *Store) Create(v Session) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if known, ok, err := s.lookup(v.RequestID, v.RequestDigest); ok {
		return known, err
	}
	now := s.options.Now()
	stamp, err := evidence.RequestTime(v.RequestID)
	if err != nil || stamp.Before(now.Add(-24*time.Hour)) || stamp.After(now.Add(5*time.Minute)) {
		return Session{}, fault.New(protocol.RequestConflict, "Generation request ID expired or invalid.")
	}
	if len(s.data.Sessions) >= MaxSessions {
		return Session{}, fault.New(protocol.StorageFull, "At most 100 sessions can be retained; forget a terminal session explicitly.")
	}
	next := evidence.Clone(s.data)
	for k, r := range next.Receipts {
		if r.At.Before(now.Add(-25 * time.Hour)) {
			delete(next.Receipts, k)
		}
	}
	if len(next.Receipts) >= 10000 {
		return Session{}, fault.New(protocol.StorageFull, "Agent request receipts are full.")
	}
	next.Sessions[v.ID] = v
	next.Receipts[v.RequestID] = Receipt{Digest: v.RequestDigest, Session: v.ID, At: now.UTC()}
	if err := s.persist(next); err != nil {
		return Session{}, err
	}
	return evidence.Clone(v), nil
}
func (s *Store) Get(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Sessions[id]
	if !ok {
		return Session{}, fault.New(protocol.NotFound, "Agent session not found.")
	}
	return evidence.Clone(v), nil
}
func (s *Store) Change(id string, fn func(*Session) error) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Sessions[id]
	if !ok {
		return Session{}, fault.New(protocol.NotFound, "Agent session not found.")
	}
	next := evidence.Clone(s.data)
	v = next.Sessions[id]
	if err := fn(&v); err != nil {
		return Session{}, err
	}
	next.Sessions[id] = v
	if err := s.persist(next); err != nil {
		return Session{}, err
	}
	return evidence.Clone(v), nil
}
func (s *Store) List(project, cursor string) List {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := List{Sessions: []Session{}}
	ids := []string{}
	for id, v := range s.data.Sessions {
		if (project == "" || v.Project == project) && id > cursor {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if len(out.Sessions) == 20 {
			out.NextCursor = out.Sessions[len(out.Sessions)-1].ID
			break
		}
		v := s.data.Sessions[id]
		v.Snapshot = ""
		v.Text = ""
		v.Output = nil
		v.Items = nil
		v.Proposals = nil
		out.Sessions = append(out.Sessions, evidence.Clone(v))
	}
	return out
}
func (s *Store) ActiveGenerations() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.data.Sessions {
		if v.State == "generating" {
			n++
		}
	}
	return n
}
func (s *Store) Forget(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Sessions[id]
	if !ok {
		return fault.New(protocol.NotFound, "Session not found.")
	}
	if v.State == "generating" || v.State == "executing" || v.State == "awaiting_review" {
		return fault.New(protocol.Busy, "Cancel or complete the session before forgetting it.")
	}
	next := evidence.Clone(s.data)
	delete(next.Sessions, id)
	return s.persist(next)
}
func (s *Store) Close() error { return errors.Join(s.root.Close(), s.lock.Close()) }

// RecordingFailure keeps an honest live diagnostic and closes further admission.
// Recovery uses the last durable record and never retries the provider.
func (s *Store) RecordingFailure(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = true
	if v, ok := s.data.Sessions[id]; ok {
		v.State = "failed"
		v.Error = fault.New(protocol.RecordingFailed, "Agent finished but its audit could not be saved. Restart and inspect storage; no retry was sent.")
		s.data.Sessions[id] = v
	}
}

// Sign makes definition digests stable locally without publishing hashes of secret values.
func (s *Store) Sign(value any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, []byte(s.data.Key))
	_, _ = mac.Write(b)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Store) Writable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.failed && !s.options.Now().Before(s.data.Written)
}

// ReconcileRuns batches recovery links in one transaction and copies each session
// only as part of the single bounded metadata snapshot.
func (s *Store) ReconcileRuns(links []evidence.AgentReservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(links) == 0 {
		return nil
	}
	next := evidence.Clone(s.data)
	changed := false
	for _, run := range links {
		session, ok := next.Sessions[run.Session]
		if !ok {
			continue
		}
		for i, p := range session.Proposals {
			if p.ID != run.Proposal || p.RunID == run.RunID && p.State == run.State {
				continue
			}
			session.Proposals[i].RunID = run.RunID
			session.Proposals[i].JobID = run.JobID
			session.Proposals[i].State = run.State
			session.Proposals[i].Message = "Recovered saved evidence; no operation was replayed."
			session.JobID = run.JobID
			if session.State == "executing" || session.State == "awaiting_review" {
				session.State = "interrupted"
			}
			changed = true
		}
		next.Sessions[run.Session] = session
	}
	if !changed {
		return nil
	}
	return s.persist(next)
}

func (s *Store) indexProposals() {
	s.proposalIndex = map[string]string{}
	for id, session := range s.data.Sessions {
		for _, p := range session.Proposals {
			s.proposalIndex[p.ID] = id
		}
	}
}
func (s *Store) FindProposal(id string) (Session, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID, ok := s.proposalIndex[id]
	if ok {
		session := s.data.Sessions[sessionID]
		for i, p := range session.Proposals {
			if p.ID == id {
				return evidence.Clone(session), i, nil
			}
		}
	}
	return Session{}, 0, fault.New(protocol.NotFound, "Proposal not found.")
}
