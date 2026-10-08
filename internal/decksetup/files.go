package decksetup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"patchbay/internal/localfs"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const maxFiles = 20000
const maxFile = 128 << 20
const maxSnapshot = 1 << 30

type fileRecord struct {
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	Digest string `json:"sha256"`
}
type inventory map[string]fileRecord

func ensureDir(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("expected a clean absolute setup path")
	}
	r, err := os.OpenRoot("/")
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		info, err := r.Lstat(part)
		if errors.Is(err, os.ErrNotExist) {
			if err = r.Mkdir(part, 0700); err != nil {
				return err
			}
			info, err = r.Lstat(part)
		}
		if err != nil || !info.IsDir() {
			return errors.New("setup directory is missing or linked")
		}
		next, err := r.OpenRoot(part)
		if err != nil {
			return err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = next.Close()
			return errors.New("setup directory changed")
		}
		_ = r.Close()
		r = next
	}
	return nil
}

func readFile(path string, limit int64) ([]byte, error) {
	parent, err := localfs.OpenDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	f, err := parent.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Size() > limit {
		return nil, errors.New("setup file is not a bounded regular file owned by this user")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(b) > int(limit) {
		return nil, errors.New("setup file grew beyond its limit")
	}
	return b, err
}
func writeFile(path string, data []byte, mode os.FileMode) error {
	parent, err := localfs.OpenDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(path)
	next := "." + name + "-" + uuid() + ".pending"
	f, err := parent.OpenFile(next, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = parent.Rename(next, name)
	}
	if err != nil {
		_ = parent.Remove(next)
		return err
	}
	return syncDir(parent)
}
func syncDir(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
func syncTree(path string) error {
	r, err := localfs.OpenDirectory(path)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	var dirs []string
	err = fs.WalkDir(r.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, name := range dirs {
		f, err := r.Open(name)
		if err != nil {
			return err
		}
		err = errors.Join(f.Sync(), f.Close())
		if err != nil {
			return err
		}
	}
	return nil
}

// copyTree rejects links and special files; each read is relative to a held root.
// Backup modes are private while inventory retains each original file mode.
func copyTree(source, destination string, inv inventory) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	rootPath, entry := source, "."
	if !info.IsDir() {
		rootPath, entry = filepath.Dir(source), filepath.Base(source)
	}
	r, err := localfs.OpenDirectory(rootPath)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	var total int64
	count := 0
	for _, f := range inv {
		total += f.Size
		count++
	}
	err = fs.WalkDir(r.FS(), entry, func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > maxFiles {
			return errors.New("setup contains too many files")
		}
		before, err := r.Lstat(name)
		if err != nil {
			return err
		}
		if !localfs.Owned(before) || before.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return errors.New("setup contains a linked or unsafe file")
		}
		rel := "."
		if info.IsDir() {
			rel = name
		}
		target := destination
		if rel != "." {
			target = filepath.Join(destination, filepath.FromSlash(rel))
		}
		if before.IsDir() {
			return os.Mkdir(target, 0700)
		}
		if !before.Mode().IsRegular() || before.Size() > maxFile {
			return errors.New("setup contains a special or oversized file")
		}
		total += before.Size()
		if total > maxSnapshot {
			return errors.New("setup exceeds the backup size limit")
		}
		return func() error {
			parent, err := localfs.OpenDirectory(filepath.Join(rootPath, filepath.Dir(name)))
			if err != nil {
				return err
			}
			defer func() { _ = parent.Close() }()
			f, err := parent.OpenFile(filepath.Base(name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			opened, err := f.Stat()
			if err != nil || !os.SameFile(before, opened) {
				return errors.New("setup changed while opening a file")
			}
			mode := os.FileMode(0600)
			if before.Mode().Perm()&0111 != 0 {
				mode = 0700
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
			if err != nil {
				return err
			}
			h := sha256.New()
			n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, maxFile+1))
			if err == nil {
				err = out.Sync()
			}
			err = errors.Join(err, out.Close())
			if err != nil {
				return err
			}
			after, err := r.Lstat(name)
			if err != nil || !os.SameFile(opened, after) || n != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
				return errors.New("setup changed while copying a file")
			}
			if inv != nil {
				inv[filepath.ToSlash(target)] = fileRecord{n, uint32(before.Mode().Perm()), hex.EncodeToString(h.Sum(nil))}
			}
			return nil
		}()
	})
	if err != nil {
		return err
	}
	if info.IsDir() {
		return syncTree(destination)
	}
	return nil
}
func verifyTree(path string, inv inventory) error {
	count := 0
	var total int64
	root, err := localfs.OpenDirectory(path)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if name == "manifest.json" {
			return nil
		}
		count++
		rec, ok := inv[name]
		if !ok || count > maxFiles {
			return errors.New("backup inventory does not match")
		}
		b, err := readFile(filepath.Join(path, filepath.FromSlash(name)), maxFile)
		if err != nil {
			return err
		}
		total += int64(len(b))
		if total > maxSnapshot {
			return errors.New("backup exceeds size limit")
		}
		h := sha256.Sum256(b)
		if rec.Size != int64(len(b)) || rec.Digest != hex.EncodeToString(h[:]) || rec.Mode&^0777 != 0 {
			return fmt.Errorf("backup verification failed for %s", name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(inv) {
		return errors.New("backup is missing files")
	}
	return nil
}

func replaceTree(source, target string) error {
	if err := ensureDir(filepath.Dir(target)); err != nil {
		return err
	}
	parent, err := localfs.OpenDirectory(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	base := filepath.Base(target)
	next := "." + base + "-" + uuid() + ".next"
	old := "." + base + "-" + uuid() + ".previous"
	if err = copyTree(source, filepath.Join(filepath.Dir(target), next), nil); err != nil {
		return err
	}
	exists := false
	if info, e := parent.Lstat(base); e == nil {
		if info.Mode()&os.ModeSymlink != 0 || !localfs.Owned(info) {
			return errors.New("setup target is linked or owned by another user")
		}
		exists = true
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if exists {
		if err = parent.Rename(base, old); err != nil {
			return err
		}
	}
	if err = parent.Rename(next, base); err != nil {
		if exists {
			_ = parent.Rename(old, base)
		}
		return err
	}
	if err = syncDir(parent); err != nil {
		return err
	}
	if exists {
		if err = parent.RemoveAll(old); err != nil {
			return err
		}
	}
	return syncDir(parent)
}
func removeTarget(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	parent, err := localfs.OpenDirectory(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	info, err := parent.Lstat(filepath.Base(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !localfs.Owned(info) {
		return errors.New("refusing to remove a linked setup target")
	}
	if err = parent.RemoveAll(filepath.Base(path)); err != nil {
		return err
	}
	return syncDir(parent)
}
