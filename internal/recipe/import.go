package recipe

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"patchbay/internal/evidence"
)

var slots = make(chan struct{}, 2)

func withImport(ctx context.Context, work func(context.Context) (*Package, error)) (*Package, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		return nil, fail("$", "two imports already in progress")
	}
	p, err := work(ctx)
	if err == nil {
		err = ctx.Err()
	}
	return p, err
}

// Inspect copies a selected directory's inventoried files or a bounded archive
// into owned memory. Its returned bytes are independent of later source changes.
func Inspect(ctx context.Context, name string) (*Package, error) {
	return withImport(ctx, func(ctx context.Context) (*Package, error) {
		absolute, err := filepath.Abs(name)
		if err != nil {
			return nil, err
		}
		// Walk from an already open filesystem root. Every descendant is checked
		// against its opened descriptor, so swapping an absolute-path ancestor
		// cannot retarget the selected directory between check and use.
		filesystem, err := os.OpenRoot(string(filepath.Separator))
		if err != nil {
			return nil, err
		}
		defer func() { _ = filesystem.Close() }()
		parentName := strings.TrimPrefix(filepath.ToSlash(filepath.Dir(absolute)), "/")
		if parentName == "" {
			parentName = "."
		}
		parent, err := openDirectory(filesystem, parentName)
		if err != nil {
			return nil, err
		}
		defer func() { _ = parent.Close() }()
		info, err := parent.Lstat(filepath.Base(absolute))
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			data, err := readFile(ctx, parent, filepath.Base(absolute), MaxPackage)
			if err != nil {
				return nil, err
			}
			return readZIP(ctx, data)
		}
		root, err := openDirectory(parent, filepath.Base(absolute))
		if err != nil {
			return nil, err
		}
		defer func() { _ = root.Close() }()
		manifest, err := readFile(ctx, root, "recipe.yaml", MaxManifest)
		if err != nil {
			return nil, err
		}
		m, err := parseManifest(manifest)
		if err != nil {
			return nil, err
		}
		files := map[string][]byte{"recipe.yaml": manifest}
		allowed := map[string]bool{"recipe.yaml": true}
		directories := map[string]bool{".": true}
		for _, item := range m.Inventory {
			allowed[item.Path] = true
			for parent := path.Dir(item.Path); parent != "."; parent = path.Dir(parent) {
				directories[parent] = true
			}
		}
		// Enumerate only declared directories, rejecting unlisted content without
		// traversing unrelated trees or reading unrelated file contents.
		for _, directory := range sortedKeys(directories) {
			dir, err := openDirectory(root, directory)
			if err != nil {
				return nil, err
			}
			f, err := dir.Open(".")
			if err != nil {
				_ = dir.Close()
				return nil, err
			}
			entries, readErr := f.ReadDir(MaxDirectoryEntries + 1)
			_ = f.Close()
			_ = dir.Close()
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return nil, readErr
			}
			if len(entries) > MaxDirectoryEntries {
				return nil, fail(directory, "too many directory entries")
			}
			for _, entry := range entries {
				name := path.Join(directory, entry.Name())
				if (!entry.IsDir() && !entry.Type().IsRegular()) || !allowed[name] && !directories[name] || entry.IsDir() != directories[name] {
					return nil, fail(directory, "unlisted entry, link or type mismatch")
				}
			}
		}
		for _, item := range m.Inventory {
			data, err := readFile(ctx, root, item.Path, int(item.Size))
			if err != nil {
				return nil, err
			}
			files[item.Path] = data
		}
		return finish(m, files)
	})
}
func InspectZIP(ctx context.Context, data []byte) (*Package, error) {
	return withImport(ctx, func(ctx context.Context) (*Package, error) { return readZIP(ctx, data) })
}
func readZIP(ctx context.Context, data []byte) (*Package, error) {
	if len(data) > MaxPackage {
		return nil, fail("$", "compressed package exceeds 20 MiB")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fail("$", "invalid ZIP")
	}
	if len(reader.File) > MaxFiles || len(reader.File) < 3 {
		return nil, fail("$", "invalid archive file count")
	}
	files := map[string][]byte{}
	names := map[string]bool{}
	total := uint64(0)
	for _, entry := range reader.File {
		key := strings.ToLower(entry.Name)
		if !validPath(entry.Name) || names[key] || entry.Flags&1 != 0 || !entry.Mode().IsRegular() || entry.Mode().Perm()&0111 != 0 || entry.UncompressedSize64 > MaxFile || entry.CompressedSize64 > MaxPackage {
			return nil, fail("$", "unsafe, colliding, encrypted or oversized ZIP entry")
		}
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if names[parent] {
				return nil, fail("$", "file/directory collision")
			}
		}
		for prior := range names {
			if strings.HasPrefix(prior, key+"/") {
				return nil, fail("$", "file/directory collision")
			}
		}
		names[key] = true
		total += entry.UncompressedSize64
		if total > MaxPackage {
			return nil, fail("$", "expanded package exceeds 20 MiB")
		}
		limit := MaxFile
		if entry.Name == "recipe.yaml" {
			limit = MaxManifest
		}
		if entry.UncompressedSize64 > uint64(limit) {
			return nil, fail("$", "manifest exceeds 256 KiB")
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, fail("$", "invalid ZIP entry")
		}
		contents, err := readBounded(ctx, stream, limit)
		closeErr := stream.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil || uint64(len(contents)) != entry.UncompressedSize64 {
			return nil, fail("$", "invalid ZIP length or checksum")
		}
		files[entry.Name] = contents
	}
	manifest, ok := files["recipe.yaml"]
	if !ok {
		return nil, fail("$", "missing recipe.yaml")
	}
	m, err := parseManifest(manifest)
	if err != nil {
		return nil, err
	}
	if len(files) != len(m.Inventory)+1 {
		return nil, fail("inventory", "unlisted archive content")
	}
	return finish(m, files)
}
func finish(m Manifest, files map[string][]byte) (*Package, error) {
	samples, err := verifyFiles(m, files)
	if err != nil {
		return nil, err
	}
	// Comments, YAML formatting and map order do not affect portable identity.
	canonical, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	files["recipe.yaml"] = canonical
	inventory := append([]File{}, m.Inventory...)
	slices.SortFunc(inventory, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	data, _ := json.Marshal(struct {
		Manifest string `json:"manifest"`
		Files    []File `json:"files"`
	}{m.Digest, inventory})
	return &Package{Manifest: m, Digest: evidence.Digest(data), Files: files, Samples: samples}, nil
}
func readBounded(ctx context.Context, r io.Reader, limit int) ([]byte, error) {
	var out bytes.Buffer
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(buffer[:min(len(buffer), limit+1-out.Len())])
		if n > 0 {
			_, _ = out.Write(buffer[:n])
		}
		if out.Len() > limit {
			return nil, fail("$", "stream exceeds declared byte limit")
		}
		if errors.Is(err, io.EOF) {
			return out.Bytes(), nil
		}
		if err != nil {
			return nil, fail("$", "cannot read complete content")
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
}
func openDirectory(root *os.Root, name string) (*os.Root, error) {
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	if name == "." {
		return current, nil
	}
	for _, component := range strings.Split(name, "/") {
		before, err := current.Lstat(component)
		if err != nil || !before.IsDir() {
			_ = current.Close()
			return nil, fail(name, "directory component is missing, linked or not a directory")
		}
		next, err := current.OpenRoot(component)
		if err != nil {
			_ = current.Close()
			return nil, err
		}
		after, err := current.Lstat(component)
		opened, statErr := next.Stat(".")
		_ = current.Close()
		if err != nil || statErr != nil || !after.IsDir() || !os.SameFile(before, after) || !os.SameFile(before, opened) {
			_ = next.Close()
			return nil, fail(name, "directory changed while opening")
		}
		current = next
	}
	return current, nil
}
func readFile(ctx context.Context, root *os.Root, name string, limit int) ([]byte, error) {
	parent, err := openDirectory(root, path.Dir(name))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	before, err := parent.Lstat(path.Base(name))
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0111 != 0 || before.Size() > int64(limit) {
		return nil, fail(name, "payload must be a bounded regular non-executable file")
	}
	file, err := parent.OpenFile(path.Base(name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fail(name, "source changed while opening")
	}
	data, err := readBounded(ctx, file, limit)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	named, linkErr := parent.Lstat(path.Base(name))
	if err != nil || linkErr != nil || !named.Mode().IsRegular() || !os.SameFile(opened, named) || after.Size() != opened.Size() || after.ModTime() != opened.ModTime() {
		return nil, fail(name, "source changed while copying")
	}
	return data, nil
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
