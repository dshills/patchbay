package provider

import (
	"fmt"
	"regexp"
	"strings"

	"patchbay/internal/fault"
	"patchbay/internal/permission"
	"patchbay/pkg/protocol"
)

// GitInputs is the complete semantic option surface. No raw flag array is accepted.
func GitInputs(operation string) map[string]string {
	switch operation {
	case "status":
		return map[string]string{}
	case "diff":
		return map[string]string{"path": "string", "staged": "boolean"}
	case "log":
		return map[string]string{"limit": "integer"}
	case "pull", "push":
		return map[string]string{"remote": "string", "branch": "string"}
	case "branch":
		return map[string]string{"mode": "enum", "name": "string"}
	case "stash":
		return map[string]string{"message": "string", "untracked": "boolean"}
	case "stash-pop":
		return map[string]string{"index": "integer"}
	default:
		return nil
	}
}

var remoteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func validRef(name string) bool {
	if name == "" || name == "@" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r == 127 || strings.ContainsRune(`~^:?*[\`, r) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func GitCommand(operation string, values map[string]any) ([]string, permission.Permission, error) {
	invalid := func() ([]string, permission.Permission, error) {
		return nil, permission.Dangerous, fault.New(protocol.InvalidRequest, "Invalid Git operation options.")
	}
	schema := GitInputs(operation)
	if schema == nil {
		return invalid()
	}
	for key, value := range values {
		switch schema[key] {
		case "string", "enum":
			if _, ok := value.(string); !ok {
				return invalid()
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return invalid()
			}
		case "integer":
			if _, ok := value.(int64); !ok {
				return invalid()
			}
		default:
			return invalid()
		}
	}
	text := func(key, fallback string) string {
		if value, ok := values[key].(string); ok {
			return value
		}
		return fallback
	}
	flag := func(key string) bool { value, _ := values[key].(bool); return value }
	number := func(key string, fallback int64) int64 {
		if value, ok := values[key].(int64); ok {
			return value
		}
		return fallback
	}
	base := []string{"--no-pager"}
	risk := permission.Safe
	var args []string
	switch operation {
	case "status":
		args = []string{"status", "--porcelain=v1", "-z"}
	case "diff":
		args = []string{"diff", "--no-ext-diff", "--no-textconv"}
		if flag("staged") {
			args = append(args, "--cached")
		}
		args = append(args, "--")
		if path := text("path", ""); path != "" {
			args = append(args, path)
		}
	case "log":
		limit := number("limit", 20)
		if limit < 1 || limit > 1000 {
			return invalid()
		}
		args = []string{"log", fmt.Sprintf("--max-count=%d", limit), "--format=%H%x00%s%x00", "--"}
	case "pull", "push":
		remote, branch := text("remote", "origin"), text("branch", "")
		if !remoteName.MatchString(remote) || branch != "" && !validRef(branch) {
			return invalid()
		}
		args = []string{operation}
		if operation == "pull" {
			args = append(args, "--ff-only", "--no-rebase")
		}
		args = append(args, "--", remote)
		if branch != "" {
			args = append(args, branch)
		}
		risk = permission.Confirm
	case "branch":
		mode, name := text("mode", "list"), text("name", "")
		switch mode {
		case "list":
			if name != "" {
				return invalid()
			}
			args = []string{"branch", "--list", "--format=%(refname:short)"}
		case "create", "delete", "switch":
			if !validRef(name) {
				return invalid()
			}
			if mode == "switch" {
				args = []string{"switch", "--", name}
			} else {
				args = []string{"branch"}
				if mode == "delete" {
					args = append(args, "--delete")
				}
				args = append(args, "--", name)
			}
			risk = permission.Confirm
		default:
			return invalid()
		}
	case "stash":
		message := text("message", "deckd stash")
		if len(message) > 4096 {
			return invalid()
		}
		args = []string{"stash", "push", "--message=" + message}
		if flag("untracked") {
			args = append(args, "--include-untracked")
		}
		args = append(args, "--")
		risk = permission.Confirm
	case "stash-pop":
		index := number("index", 0)
		if index < 0 || index > 1000 {
			return invalid()
		}
		args = []string{"stash", "pop", fmt.Sprintf("stash@{%d}", index)}
		risk = permission.Confirm
	}
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return invalid()
		}
	}
	return append(base, args...), risk, nil
}
