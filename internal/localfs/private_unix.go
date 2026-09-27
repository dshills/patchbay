//go:build darwin || linux

// Package localfs provides private local directories and stable advisory locks.
package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

func Owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

// PrivateDir never chmods an existing directory or accepts a symlink as the leaf.
func PrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return errors.New("cannot create private directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || !Owned(info) || info.Mode().Perm()&0077 != 0 {
		return errors.New("directory must be owned by this user with mode 0700")
	}
	return nil
}

type Lock struct {
	file *os.File
	once sync.Once
}

func Acquire(path string) (*Lock, error) {
	if err := PrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errors.New("cannot open local lock")
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !Owned(info) || info.Mode().Perm()&0077 != 0 {
		_ = f.Close()
		return nil, errors.New("lock must be a private regular file owned by this user")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("another daemon holds the local lock")
	}
	return &Lock{file: f}, nil
}

func (l *Lock) Close() error {
	var err error
	l.once.Do(func() { err = l.file.Close() })
	return err
}
