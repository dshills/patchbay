package provider

import (
	"context"
	"os"
	"os/exec"
	"patchbay/internal/permission"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitSemanticValidation(t *testing.T) {
	for _, c := range []struct {
		op   string
		args map[string]any
	}{
		{"raw", nil}, {"status", map[string]any{"flags": "--help"}},
		{"log", map[string]any{"limit": int64(0)}}, {"log", map[string]any{"limit": "2"}},
		{"pull", map[string]any{"remote": "--upload-pack=bad"}}, {"push", map[string]any{"branch": "HEAD:main"}},
		{"branch", map[string]any{"mode": "force"}}, {"branch", map[string]any{"mode": "list", "name": "x"}},
		{"stash-pop", map[string]any{"index": int64(-1)}}, {"stash", map[string]any{"message": strings.Repeat("x", 4097)}},
		{"diff", map[string]any{"path": "x\x00y"}}, {"diff", map[string]any{"staged": "true"}},
	} {
		if _, _, err := GitCommand(c.op, c.args); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	for _, ref := range []string{"", "-bad", "a..b", "a@{b}", "a//b", ".hidden", "a.lock", "a b", "a~1", "a:", "@", "a/", "a."} {
		if validRef(ref) {
			t.Error("accepted ref", ref)
		}
	}
}

func TestGitLocalRepositoryOperations(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	repo, remote, peer := filepath.Join(root, "repo"), filepath.Join(root, "remote.git"), filepath.Join(root, "peer")
	// Isolate user/global Git configuration, hooks, and identity.
	env := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_TERMINAL_PROMPT=0")
	raw := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir, cmd.Env = dir, env
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, data, err)
		}
		return string(data)
	}
	raw(root, "init", "--bare", remote)
	raw(root, "init", "-b", "main", repo)
	write := func(dir, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "file"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(repo, "initial\n")
	raw(repo, "add", "file")
	raw(repo, "commit", "-m", "initial")
	raw(repo, "remote", "add", "origin", remote)
	run := func(op string, args map[string]any, wantRisk permission.Permission) string {
		t.Helper()
		argv, risk, err := GitCommand(op, args)
		if err != nil || risk != wantRisk {
			t.Fatal(argv, risk, err)
		}
		result, err := (ProcessRunner{}).Run(context.Background(), Command{Path: git, Args: argv, Dir: repo, Env: env}, NewBudget(100000))
		if err != nil {
			t.Fatalf("%s: %+v %v", op, result, err)
		}
		return result.Data["stdout"].(string)
	}
	run("push", map[string]any{"branch": "main"}, permission.Confirm)
	raw(root, "clone", "-b", "main", remote, peer)
	if out := run("status", nil, permission.Safe); out != "" {
		t.Fatal(out)
	}
	if out := run("log", map[string]any{"limit": int64(1)}, permission.Safe); !strings.Contains(out, "initial") {
		t.Fatal(out)
	}
	write(repo, "dirty\n")
	if out := run("diff", nil, permission.Safe); !strings.Contains(out, "dirty") {
		t.Fatal(out)
	}
	raw(repo, "add", "file")
	if out := run("diff", map[string]any{"staged": true, "path": "file"}, permission.Safe); !strings.Contains(out, "dirty") {
		t.Fatal(out)
	}
	run("stash", map[string]any{"message": "test", "untracked": true}, permission.Confirm)
	run("stash-pop", map[string]any{"index": int64(0)}, permission.Confirm)
	raw(repo, "restore", "--staged", "file")
	raw(repo, "restore", "file")
	run("branch", nil, permission.Safe)
	run("branch", map[string]any{"mode": "create", "name": "feature"}, permission.Confirm)
	run("branch", map[string]any{"mode": "switch", "name": "feature"}, permission.Confirm)
	run("branch", map[string]any{"mode": "switch", "name": "main"}, permission.Confirm)
	run("branch", map[string]any{"mode": "delete", "name": "feature"}, permission.Confirm)
	write(peer, "remote\n")
	raw(peer, "add", "file")
	raw(peer, "commit", "-m", "remote")
	raw(peer, "push", "origin", "main")
	run("pull", map[string]any{"branch": "main"}, permission.Confirm)
	write(repo, "local\n")
	raw(repo, "add", "file")
	raw(repo, "commit", "-m", "local")
	write(peer, "diverged\n")
	raw(peer, "add", "file")
	raw(peer, "commit", "-m", "diverged")
	raw(peer, "push", "origin", "main")
	argv, _, _ := GitCommand("pull", map[string]any{"branch": "main"})
	result, err := (ProcessRunner{}).Run(context.Background(), Command{Path: git, Args: argv, Dir: repo, Env: env}, NewBudget(100000))
	if err == nil {
		t.Fatal("divergent pull should fail", result)
	}
	if got := raw(repo, "status", "--porcelain"); got != "" {
		t.Fatal("failed pull changed index", got)
	}
}
