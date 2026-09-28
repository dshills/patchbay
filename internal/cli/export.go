package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"patchbay/internal/client"
	"patchbay/internal/evidence"
	"patchbay/pkg/protocol"
)

func exportFile(ctx context.Context, c *client.Client, command []string) (any, error) {
	if command[4] != "html" && command[4] != "json" {
		return nil, usage("Export format must be html or json.")
	}
	// Inspect the destination before consuming the export preview. Never overwrite.
	path := command[5]
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return nil, usage("Export paths cannot traverse parent directories.")
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, usage("Invalid export path.")
	}
	dir := filepath.Dir(absolute)
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil || canonical != dir {
		return nil, usage("Export directory must exist and contain no symlinks.")
	}
	if _, err := os.Lstat(absolute); !errors.Is(err, os.ErrNotExist) {
		return nil, usage("Export destination must be a new file.")
	}
	file, err := call[protocol.ExportFile](ctx, c, "POST", protocol.ExportRequest{Preparation: command[2], Digest: command[3], Format: command[4]}, "exports")
	if err != nil {
		return nil, err
	}
	if len(file.Data) > 16<<20 || evidence.Digest(file.Data) != file.SHA256 {
		return nil, &client.Error{Code: "invalid_response", Message: "Export integrity check failed."}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, usage("Cannot open export directory.")
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(absolute)
	out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, usage("Cannot create export file without overwriting an existing entry.")
	}
	success := false
	defer func() {
		_ = out.Close()
		if !success {
			_ = root.Remove(name)
		}
	}()
	if _, err := out.Write(file.Data); err != nil {
		return nil, err
	}
	if err := out.Sync(); err != nil {
		return nil, err
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	parent, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	if err := parent.Sync(); err != nil {
		return nil, err
	}
	success = true
	return map[string]string{"path": absolute, "sha256": file.SHA256}, nil
}
