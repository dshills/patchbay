package streamdeck

import (
	"errors"
	"io"
	"os"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/localfs"
	"path/filepath"
	"syscall"
)

// SetupSocket is only present in a plugin explicitly installed by deckctl.
func SetupSocket(executable string) (string, error) {
	path := filepath.Join(filepath.Dir(filepath.Dir(executable)), "patchbay-setup.json")
	parent, err := localfs.OpenDirectory(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer func() { _ = parent.Close() }()
	f, err := parent.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return "", errors.New("invalid managed Stream Deck setup")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", err
	}
	var setup struct {
		Socket string `json:"socket"`
	}
	if jsonstrict.Decode(b, &setup) != nil || !filepath.IsAbs(setup.Socket) || filepath.Clean(setup.Socket) != setup.Socket || len(setup.Socket) > 100 {
		return "", errors.New("invalid managed Stream Deck socket")
	}
	return setup.Socket, nil
}
