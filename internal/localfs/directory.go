package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// OpenDirectory opens each absolute path component from a held root and rejects
// links/replacements. Subsequent relative writes remain bound to this directory.
func OpenDirectory(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("expected a clean absolute directory")
	}
	current, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		before, err := current.Lstat(part)
		if err != nil || !before.IsDir() {
			_ = current.Close()
			return nil, errors.New("directory component is missing or linked")
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			_ = current.Close()
			return nil, err
		}
		after, err := current.Lstat(part)
		opened, statErr := next.Stat(".")
		_ = current.Close()
		if err != nil || statErr != nil || !after.IsDir() || !os.SameFile(before, after) || !os.SameFile(before, opened) {
			_ = next.Close()
			return nil, errors.New("directory changed while opening")
		}
		current = next
	}
	return current, nil
}
