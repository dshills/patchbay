package api

import (
	"errors"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"patchbay/internal/localfs"
)

type Listener struct {
	*net.UnixListener
	path     string
	identity os.FileInfo
	lock     *localfs.Lock
	once     sync.Once
}

func Listen(path string) (*Listener, error) {
	lock, err := localfs.Acquire(path + ".lock")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = lock.Close()
		}
	}()
	if previous, err := os.Lstat(path); err == nil {
		if previous.Mode()&os.ModeSocket == 0 || !localfs.Owned(previous) {
			return nil, errors.New("refusing to replace a non-socket or unowned socket")
		}
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return nil, errors.New("a listener already owns the socket")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, errors.New("cannot establish whether the socket is stale")
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(previous, current) {
			return nil, errors.New("socket changed during startup")
		}
		if err := os.Remove(path); err != nil {
			return nil, errors.New("cannot remove stale socket")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot inspect socket path")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, errors.New("cannot create Unix socket")
	}
	listener.SetUnlinkOnClose(false)
	info, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		return nil, errors.New("cannot inspect new socket")
	}
	result := &Listener{UnixListener: listener, path: path, identity: info, lock: lock}
	if err := os.Chmod(path, 0600); err != nil {
		_ = result.Close()
		return nil, errors.New("cannot protect Unix socket")
	}
	ok = true
	return result, nil
}

func (l *Listener) Close() error {
	var err error
	l.once.Do(func() {
		err = l.UnixListener.Close()
		if current, statErr := os.Lstat(l.path); statErr == nil && os.SameFile(current, l.identity) {
			_ = os.Remove(l.path)
		}
		_ = l.lock.Close()
	})
	return err
}
