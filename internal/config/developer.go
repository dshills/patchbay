package config

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"patchbay/internal/permission"
)

var conventionDefaults = map[string]map[string]ConventionCommand{
	"go":     {"validate": {"go", []string{"vet", "./..."}}, "test": {"go", []string{"test", "./..."}}, "build": {"go", []string{"build", "./..."}}},
	"node":   {"validate": {"npm", []string{"run", "lint"}}, "test": {"npm", []string{"test"}}, "build": {"npm", []string{"run", "build"}}},
	"python": {"validate": {"python3", []string{"-m", "compileall", "."}}, "test": {"python3", []string{"-m", "unittest", "discover"}}, "build": {"python3", []string{"-m", "build"}}},
}

func (v *validator) developerConfig(c *Config, baseDir, home string) error {
	for name, prompt := range c.Prompts {
		if strings.TrimSpace(prompt) == "" || len(prompt) > 64<<10 || strings.ContainsRune(prompt, 0) || !utf8.ValidString(prompt) {
			return v.fail("prompts."+name, "prompt must be nonempty UTF-8 text of at most 64 KiB without NUL")
		}
		// Validate even unused prompts. Referencing actions validate argument names.
		inputs := map[string]Input{}
		for text := prompt; ; {
			start := strings.Index(text, "{{")
			if start < 0 {
				break
			}
			end := strings.Index(text[start+2:], "}}")
			if end < 0 {
				break
			}
			token := strings.TrimSpace(text[start+2 : start+2+end])
			if key, ok := strings.CutPrefix(token, ".args."); ok {
				inputs[key] = Input{}
			}
			text = text[start+2+end+2:]
		}
		if err := ValidateTemplate(prompt, inputs); err != nil {
			return v.fail("prompts."+name, "invalid prompt substitution")
		}
	}
	if c.Agents.Proposals.Enabled && !c.Agents.Proposals.Demo && c.Agents.Codex.Model == "" {
		return v.fail("agents.proposals", "proposal mode requires an explicit codex model")
	}
	if len(c.Agents.Proposals.ProtectedPaths) > 100 {
		return v.fail("agents.proposals.protected_paths", "at most 100 paths")
	}
	for _, p := range c.Agents.Proposals.ProtectedPaths {
		if !filepath.IsLocal(p) || filepath.Clean(p) != p {
			return v.fail("agents.proposals.protected_paths", "expected clean relative paths")
		}
	}
	if c.Agents.Codex.Model != "" && !ValidName(c.Agents.Codex.Model) {
		return v.fail("agents.codex.model", "expected a literal model identifier")
	}
	if c.Agents.Codex.MaxOutputTokens == 0 {
		c.Agents.Codex.MaxOutputTokens = 4096
	}
	if c.Agents.Codex.MaxOutputTokens < 16 || c.Agents.Codex.MaxOutputTokens > 32768 {
		return v.fail("agents.codex.max_output_tokens", "expected 16 through 32768 tokens")
	}
	for _, project := range c.Projects {
		if len(project.AgentGrants) > 128 {
			return v.fail("projects.agent_grants", "at most 128 grants per project")
		}
		seen := map[string]bool{}
		for _, grant := range project.AgentGrants {
			key := grant.Kind + ":" + grant.Target
			if (grant.Kind != "action" && grant.Kind != "workflow" && grant.Kind != "experiment") || !ValidName(grant.Target) || len(grant.Digest) != 64 || seen[key] || len(grant.Inputs) > 32 {
				return v.fail("projects.agent_grants", "expected unique bounded targets with exact 64-character digests")
			}
			seen[key] = true
			for _, ch := range grant.Digest {
				if !strings.ContainsRune("0123456789abcdef", ch) {
					return v.fail("projects.agent_grants.digest", "expected a hexadecimal digest")
				}
			}
			for name, input := range grant.Inputs {
				if !ValidName(name) || input.Sensitive || input.Default != nil {
					return v.fail("projects.agent_grants.inputs", "grant inputs cannot add defaults or sensitive values")
				}
				d := input.definition()
				if err := d.Normalize(); err != nil {
					return v.fail("projects.agent_grants.inputs."+name, err.Error())
				}
			}
		}
	}
	for language, commands := range c.Conventions {
		if _, ok := conventionDefaults[language]; !ok {
			return v.fail("conventions", "supported languages are go, node, and python")
		}
		for operation, command := range commands {
			if _, ok := conventionDefaults[language][operation]; !ok {
				return v.fail("conventions."+language, "supported operations are validate, test, and build")
			}
			a := Action{Type: "exec", Safety: permission.Confirm, Command: command.Command, Args: command.Args}
			if err := v.action("conventions."+language+"."+operation, &a, c, baseDir, home); err != nil {
				return err
			}
			commands[operation] = ConventionCommand{Command: a.Command, Args: a.Args}
		}
	}
	return nil
}

func (v *validator) projectConventions(path string, p Project, c *Config) (map[string]Action, error) {
	result := map[string]Action{}
	if len(p.Conventions) == 0 {
		return result, nil
	}
	language := strings.ToLower(p.Language)
	if language == "javascript" || language == "typescript" {
		language = "node"
	}
	commands, ok := conventionDefaults[language]
	if !ok {
		return nil, v.fail(path+".conventions", "conventions require a supported project language: go, node, or python")
	}
	for _, operation := range p.Conventions {
		command, ok := commands[operation]
		name := "project." + operation
		if !ok || result[name].Type != "" {
			return nil, v.fail(path+".conventions", "choose each of validate, test, and build at most once")
		}
		if override, ok := c.Conventions[language][operation]; ok {
			command = override
		}
		result[name] = Action{Type: "exec", Safety: permission.Confirm, Command: command.Command, Args: command.Args, Cwd: "{{ .project.path }}", Origin: "convention:" + language}
	}
	return result, nil
}

func (c *Config) conventionAction(name string) bool {
	for _, p := range c.Projects {
		if strings.HasPrefix(p.Actions[name].Origin, "convention:") {
			return true
		}
	}
	return false
}
