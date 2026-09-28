package supervisor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"patchbay/internal/localfs"
)

// Protected is deliberately independent of model output and user-facing labels.
func Protected(name string, extra []string) bool {
	if !filepath.IsLocal(name) || filepath.Clean(name) != name || strings.Contains(name, "\\") {
		return true
	}
	for _, part := range strings.Split(strings.ToLower(filepath.ToSlash(name)), "/") {
		if part == ".git" || part == ".patchbay" || part == ".agents" || part == ".codex" || part == ".ssh" || part == ".aws" || part == ".gnupg" || part == "agents.md" || strings.HasPrefix(part, ".env") || strings.Contains(part, "credential") || strings.Contains(part, "secret") || strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") || part == "id_rsa" || part == "id_ed25519" {
			return true
		}
	}
	for _, p := range extra {
		if name == p || strings.HasPrefix(name, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ReadText binds every directory component and the regular leaf without following links.
func ReadText(project, name string, limit int) ([]byte, error) {
	fail := errors.New("selected file must be bounded regular UTF-8 text with no linked path components")
	if Protected(name, nil) {
		return nil, fail
	}
	root, err := localfs.OpenDirectory(filepath.Join(project, filepath.Dir(name)))
	if err != nil {
		return nil, fail
	}
	defer func() { _ = root.Close() }()
	leaf := filepath.Base(name)
	before, err := root.Lstat(leaf)
	if err != nil || !before.Mode().IsRegular() || before.Size() > int64(limit) {
		return nil, fail
	}
	f, err := root.OpenFile(leaf, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fail
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fail
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(data) > limit || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, fail
	}
	after, err := root.Lstat(leaf)
	if err != nil || !os.SameFile(opened, after) {
		return nil, fail
	}
	return data, nil
}
