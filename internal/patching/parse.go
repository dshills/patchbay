// Package patching implements bounded exact text patches without invoking git apply.
package patching

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"patchbay/internal/evidence"
	"patchbay/internal/localfs"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const MaxDiff = 64 << 10
const MaxFile = 256 << 10
const MaxContent = 1 << 20

type File struct {
	protocol.PatchFile `json:"file"`
	BeforeText         string `json:"before_text"`
	AfterText          string `json:"after_text"`
	Stage              string `json:"stage"`
	StageDevice        uint64 `json:"stage_device"`
	StageInode         uint64 `json:"stage_inode"`
}
type Plan struct {
	Preview protocol.PatchPreview `json:"preview"`
	Root    string                `json:"root"`
	Device  uint64                `json:"device"`
	Inode   uint64                `json:"inode"`
	Files   []File                `json:"files"`
}

var hunk = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@$`)

func invalid() error {
	return errors.New("patch requires exact bounded unified hunks for allowed existing tracked UTF-8 files")
}
func Git(ctx context.Context, root string, args ...string) (string, error) {
	child, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(child, "git", append([]string{"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-C", root}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat"}
	out := &boundedWriter{}
	cmd.Stdout = out
	cmd.Stderr = &boundedWriter{}
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return string(out.data), nil
}

type boundedWriter struct{ data []byte }

func (w *boundedWriter) Write(b []byte) (int, error) {
	if len(w.data)+len(b) > 64<<10 {
		return 0, errors.New("git output limit")
	}
	w.data = append(w.data, b...)
	return len(b), nil
}
func head(ctx context.Context, root string) (string, error) {
	out, err := Git(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if !regexp.MustCompile(`^[0-9a-f]{40,64}$`).MatchString(out) {
		return "", invalid()
	}
	return out, nil
}
func inodeIdentity(info os.FileInfo) (uint64, uint64) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	return uint64(stat.Dev), stat.Ino
}
func directoryIdentity(root string) (uint64, uint64, error) {
	dir, err := localfs.OpenDirectory(root)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = dir.Close() }()
	info, err := dir.Stat(".")
	if err != nil {
		return 0, 0, err
	}
	dev, ino := inodeIdentity(info)
	return dev, ino, nil
}
func read(root, name string) ([]byte, os.FileMode, error) {
	parent, err := localfs.OpenDirectory(filepath.Join(root, filepath.Dir(name)))
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = parent.Close() }()
	return readAt(parent, filepath.Base(name))
}
func readAt(parent *os.Root, name string) ([]byte, os.FileMode, error) {
	before, err := parent.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || !localfs.Owned(before) || before.Size() > MaxFile || before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, 0, invalid()
	}
	f, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !os.SameFile(before, info) {
		return nil, 0, invalid()
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFile+1))
	after, statErr := parent.Lstat(name)
	if err != nil || statErr != nil || !os.SameFile(info, after) || len(data) > MaxFile || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, 0, invalid()
	}
	return data, info.Mode().Perm(), nil
}
func tracked(ctx context.Context, root, name string) error {
	out, err := Git(ctx, root, "ls-files", "--stage", "-z", "--", name)
	if err != nil {
		return err
	}
	records := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if len(records) != 1 {
		return invalid()
	}
	parts := strings.SplitN(records[0], "\t", 2)
	if len(parts) != 2 || parts[1] != name {
		return invalid()
	}
	fields := strings.Fields(parts[0])
	if len(fields) != 3 || (fields[0] != "100644" && fields[0] != "100755") || fields[2] != "0" {
		return invalid()
	}
	return nil
}
func lines(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.SplitAfter(s, "\n")
	if out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// Prepare supports plain ---/+++ unified text diffs. Metadata extensions are rejected.
func Prepare(ctx context.Context, root, diff string, allowed []string, protected func(string) bool) (Plan, error) {
	p := Plan{Root: root, Preview: protocol.PatchPreview{Diff: diff, Digest: evidence.Digest([]byte(diff)), Files: []protocol.PatchFile{}}, Files: []File{}}
	if len(diff) == 0 || len(diff) > MaxDiff || !utf8.ValidString(diff) || strings.ContainsRune(diff, 0) || !strings.HasSuffix(diff, "\n") {
		return p, invalid()
	}
	var err error
	p.Device, p.Inode, err = directoryIdentity(root)
	if err != nil {
		return p, err
	}
	p.Preview.Head, err = head(ctx, root)
	if err != nil {
		return p, err
	}
	patch := lines(diff)
	seen := map[string]bool{}
	total := 0
	for i := 0; i < len(patch); {
		if len(p.Files) >= 10 || !strings.HasPrefix(patch[i], "--- a/") {
			return p, invalid()
		}
		name := strings.TrimSuffix(strings.TrimPrefix(patch[i], "--- a/"), "\n")
		i++
		if i >= len(patch) || patch[i] != "+++ b/"+name+"\n" || seen[name] || supervisor.Protected(name, nil) || protected != nil && protected(name) || !slices.Contains(allowed, name) {
			return p, invalid()
		}
		i++
		seen[name] = true
		if err = tracked(ctx, root, name); err != nil {
			return p, err
		}
		before, mode, err := read(root, name)
		if err != nil {
			return p, err
		}
		source := lines(string(before))
		after := []string{}
		cursor := 0
		hunks := 0
		for i < len(patch) && strings.HasPrefix(patch[i], "@@ ") {
			m := hunk.FindStringSubmatch(strings.TrimSuffix(patch[i], "\n"))
			if m == nil {
				return p, invalid()
			}
			i++
			hunks++
			values := []int{}
			for j := 1; j <= 4; j++ {
				v := 1
				if m[j] != "" {
					v, err = strconv.Atoi(m[j])
					if err != nil || v > MaxFile {
						return p, invalid()
					}
				}
				values = append(values, v)
			}
			oldStart, oldCount, newStart, newCount := values[0], values[1], values[2], values[3]
			oldIndex := oldStart - 1
			if oldCount == 0 {
				oldIndex = oldStart
			}
			if oldIndex < cursor || oldIndex > len(source) {
				return p, invalid()
			}
			after = append(after, source[cursor:oldIndex]...)
			cursor = oldIndex
			newIndex := newStart - 1
			if newCount == 0 {
				newIndex = newStart
			}
			if newIndex != len(after) {
				return p, invalid()
			}
			oldUsed, newUsed := 0, 0
			for oldUsed < oldCount || newUsed < newCount {
				if i >= len(patch) || len(patch[i]) < 2 {
					return p, invalid()
				}
				line := patch[i]
				i++
				kind := line[0]
				body := line[1:]
				if kind != ' ' && kind != '-' && kind != '+' {
					return p, invalid()
				}
				if i < len(patch) && patch[i] == "\\ No newline at end of file\n" {
					body = strings.TrimSuffix(body, "\n")
					i++
				}
				if kind != '+' {
					if cursor >= len(source) || source[cursor] != body {
						return p, invalid()
					}
					cursor++
					oldUsed++
				}
				if kind != '-' {
					after = append(after, body)
					newUsed++
				}
				if oldUsed > oldCount || newUsed > newCount {
					return p, invalid()
				}
			}
		}
		if hunks == 0 {
			return p, invalid()
		}
		after = append(after, source[cursor:]...)
		for j, line := range after {
			if j < len(after)-1 && !strings.HasSuffix(line, "\n") {
				return p, invalid()
			}
		}
		output := strings.Join(after, "")
		if len(output) > MaxFile || !utf8.ValidString(output) || strings.ContainsRune(output, 0) || output == string(before) {
			return p, invalid()
		}
		// Retain the file's existing newline convention; mixed conventions are unsupported.
		if !sameEndings(string(before), output) {
			return p, errors.New("patch changes or mixes line endings")
		}
		total += len(before) + len(output)
		if total > MaxContent {
			return p, invalid()
		}
		f := File{PatchFile: protocol.PatchFile{Path: name, Before: evidence.Digest(before), After: evidence.Digest([]byte(output)), BeforeBytes: len(before), AfterBytes: len(output), Mode: uint32(mode)}, BeforeText: string(before), AfterText: output}
		p.Files = append(p.Files, f)
		p.Preview.Files = append(p.Preview.Files, f.PatchFile)
	}
	if len(p.Files) == 0 {
		return p, invalid()
	}
	return p, nil
}
func sameEndings(before, after string) bool {
	style := func(s string) int {
		lf := strings.Count(s, "\n")
		crlf := strings.Count(s, "\r\n")
		if strings.Count(s, "\r") != crlf || crlf > 0 && lf != crlf {
			return -1
		}
		if crlf > 0 {
			return 2
		}
		if lf > 0 {
			return 1
		}
		return 0
	}
	a, b := style(before), style(after)
	return a >= 0 && b >= 0 && (a == 0 || b == 0 || a == b)
}
func Validate(ctx context.Context, p Plan) error {
	dev, ino, err := directoryIdentity(p.Root)
	if err != nil || dev != p.Device || ino != p.Inode {
		return errors.New("project directory changed")
	}
	current, err := head(ctx, p.Root)
	if err != nil || current != p.Preview.Head {
		return errors.New("Git HEAD changed")
	}
	for _, f := range p.Files {
		if err = tracked(ctx, p.Root, f.Path); err != nil {
			return err
		}
		data, mode, err := read(p.Root, f.Path)
		if err != nil || mode != os.FileMode(f.Mode) || !bytes.Equal(data, []byte(f.BeforeText)) {
			return fmt.Errorf("patch preimage changed: %s", f.Path)
		}
	}
	return nil
}
