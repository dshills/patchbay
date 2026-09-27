package state

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	runtimecontext "patchbay/internal/context"
	"patchbay/internal/identity"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/localfs"
)

const MaxBytes = 8 << 20

var ErrRecovered = errors.New("corrupt state preserved; using configured defaults")

type Snapshot struct {
	Version    int                           `json:"version"`
	Context    runtimecontext.RuntimeContext `json:"context"`
	Parameters map[string]any                `json:"parameters"`
}

// Read preserves malformed/incompatible state before permitting fresh writes.
// It returns a warning alongside defaults for a recoverable corrupt file.
func Read(path string) (*Snapshot, error) {
	previous, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !previous.Mode().IsRegular() || !localfs.Owned(previous) || previous.Mode().Perm()&0077 != 0 {
		return nil, errors.New("state must be a private regular file owned by this user")
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot read state file")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(previous, info) {
		return nil, errors.New("state file changed during load")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, errors.New("cannot read state file")
	}
	var snap Snapshot
	err = jsonstrict.Decode(data, &snap)
	if err != nil || len(data) > MaxBytes || snap.Version != 1 {
		if err := os.Rename(path, path+".corrupt-"+identity.New()); err != nil {
			return nil, errors.New("cannot preserve corrupt state file")
		}
		return nil, ErrRecovered
	}
	return &snap, nil
}

// Write leaves the prior file intact if preparation fails. Temp files and state
// are mode 0600. The directory is synced after the atomic rename.
func Write(path string, data []byte) error {
	if len(data) > MaxBytes {
		return errors.New("state exceeds size limit")
	}
	dir := filepath.Dir(path)
	if err := localfs.PrivateDir(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".deckd-state-*")
	if err != nil {
		return errors.New("cannot create temporary state file")
	}
	name := f.Name()
	defer func() { _ = f.Close(); _ = os.Remove(name) }()
	if _, err := f.Write(data); err != nil {
		return errors.New("cannot write state file")
	}
	if err := f.Sync(); err != nil {
		return errors.New("cannot sync state file")
	}
	if err := f.Close(); err != nil {
		return errors.New("cannot close state file")
	}
	if err := os.Rename(name, path); err != nil {
		return errors.New("cannot replace state file")
	}
	directory, err := os.Open(dir)
	if err != nil {
		return errors.New("cannot open state directory")
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return errors.New("cannot sync state directory")
	}
	return nil
}

// Writer owns one goroutine, started by NewWriter and joined by Close. It only
// wakes when dirty and retains dirty data after failure for bounded retries.
type Writer struct {
	mu       sync.Mutex
	data     []byte
	revision uint64
	written  uint64
	lastErr  error
	closed   bool
	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	path     string
	interval time.Duration
	warn     func(error)
}

func NewWriter(path string, interval time.Duration, warn func(error)) *Writer {
	w := &Writer{path: path, interval: interval, warn: warn, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go w.run()
	return w
}

func (w *Writer) Submit(snapshot Snapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > MaxBytes {
		return errors.New("state is not serializable within its size limit")
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return errors.New("state writer is closed")
	}
	w.data, w.revision = data, w.revision+1
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return nil
}

func (w *Writer) flush() bool {
	w.mu.Lock()
	data, revision, written := w.data, w.revision, w.written
	w.mu.Unlock()
	if revision == written {
		return false
	}
	err := Write(w.path, data)
	w.mu.Lock()
	if err == nil {
		w.written = revision
	}
	w.lastErr = err
	dirty := w.revision != w.written
	w.mu.Unlock()
	if err != nil && w.warn != nil {
		w.warn(err)
	}
	return dirty
}

func (w *Writer) run() {
	defer close(w.done)
	for {
		select {
		case <-w.stop:
			w.flush()
			return
		case <-w.wake:
		}
		timer := time.NewTimer(w.interval)
		select {
		case <-w.stop:
			timer.Stop()
			w.flush()
			return
		case <-timer.C:
		}
		if w.flush() {
			select {
			case w.wake <- struct{}{}:
			default:
			}
		}
	}
}

func (w *Writer) Close(ctx context.Context) error {
	w.once.Do(func() { w.mu.Lock(); w.closed = true; w.mu.Unlock(); close(w.stop) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

func (w *Writer) Done() <-chan struct{} { return w.done }
