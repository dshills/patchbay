package provider

import (
	"fmt"
	"strings"

	"patchbay/internal/action"
)

// GitResult adds structured inspection without removing the original stdout.
// A truncated record is never presented as a complete path or a clean tree.
func GitResult(operation string, result action.Result) action.Result {
	if result.Data == nil {
		return result
	}
	result.Data["operation"] = operation
	if result.Status != action.Success {
		return result
	}
	stdout, _ := result.Data["stdout"].(string)
	truncated, _ := result.Data["truncated"].(bool)
	switch operation {
	case "status":
		entries := []map[string]string{}
		remaining := stdout
		for remaining != "" && len(entries) < 1000 {
			record, rest, complete := strings.Cut(remaining, "\x00")
			if !complete {
				result.Data["structured_incomplete"] = true
				break
			}
			remaining = rest
			if len(record) < 4 || record[2] != ' ' {
				result.Data["structured_incomplete"] = true
				continue
			}
			entry := map[string]string{"index": record[:1], "worktree": record[1:2], "path": record[3:]}
			if strings.ContainsAny(record[:2], "RC") {
				original, rest, complete := strings.Cut(remaining, "\x00")
				if !complete {
					result.Data["structured_incomplete"] = true
					break
				}
				entry["original_path"] = original
				remaining = rest
			}
			entries = append(entries, entry)
		}
		if remaining != "" {
			result.Data["structured_incomplete"] = true
		}
		result.Data["git_status"] = entries
		result.Message = fmt.Sprintf("Git status: %d complete paths.", len(entries))
		if stdout == "" && !truncated {
			result.Message = "Working tree is clean."
		}
	case "log":
		commits := []map[string]string{}
		remaining := stdout
		for remaining != "" && remaining != "\n" && len(commits) < 1000 {
			hash, rest, complete := strings.Cut(remaining, "\x00")
			hash = strings.TrimPrefix(hash, "\n")
			if !complete || len(hash) != 40 && len(hash) != 64 {
				result.Data["structured_incomplete"] = true
				break
			}
			subject, rest, complete := strings.Cut(rest, "\x00")
			if !complete {
				result.Data["structured_incomplete"] = true
				break
			}
			remaining = rest
			commits = append(commits, map[string]string{"commit": hash, "subject": subject})
		}
		if remaining != "" && remaining != "\n" {
			result.Data["structured_incomplete"] = true
		}
		result.Data["git_log"] = commits
		result.Message = fmt.Sprintf("Git log: %d complete commits.", len(commits))
	}
	return result
}
