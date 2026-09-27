package runtime

import (
	"path/filepath"
	"slices"
	"strings"

	"patchbay/internal/config"
	"patchbay/internal/fault"
	"patchbay/internal/provider"
	"patchbay/pkg/protocol"
)

func (r *Runtime) prepareAgent(definition config.Action, args map[string]any) (*provider.AgentRequest, error) {
	p, ok := r.cfg.Projects[r.context.Project]
	if !ok {
		return nil, fault.New(protocol.InvalidRequest, "Select a project before running an agent action.")
	}
	vars := r.variables(args)
	prompt, err := config.Render(r.cfg.Prompts[definition.Prompt], vars)
	if err != nil || len(prompt) > 64<<10 || strings.ContainsRune(prompt, 0) {
		return nil, fault.New(protocol.InvalidRequest, "Prompt expansion is missing a value or exceeds 64 KiB.")
	}
	cwd, err := config.Render(definition.Cwd, vars)
	if err != nil {
		return nil, fault.New(protocol.InvalidRequest, "Agent working directory requires valid project context.")
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(p.Path, cwd)
	}
	relative, err := filepath.Rel(p.Path, cwd)
	if err != nil || !filepath.IsLocal(relative) {
		return nil, fault.New(protocol.PermissionDenied, "Agent working directory must remain within the selected project.")
	}
	return &provider.AgentRequest{Project: p.Path, Dir: filepath.Clean(cwd), Prompt: prompt, Files: slices.Clone(definition.Files), Model: r.cfg.Agents.Codex.Model, MaxOutputTokens: r.cfg.Agents.Codex.MaxOutputTokens}, nil
}
