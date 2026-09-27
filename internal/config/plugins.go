package config

import (
	"strings"

	"patchbay/internal/permission"
)

func (v *validator) plugins(c *Config, baseDir, home string) error {
	if len(c.Plugins) > 16 {
		return v.fail("plugins", "at most 16 plugins are supported")
	}
	for _, id := range keys(c.Plugins) {
		p := c.Plugins[id]
		path := "plugins." + id
		if p.Command == "" || !strings.Contains(p.Command, "/") {
			return v.fail(path, "plugin command must be an explicit executable path")
		}
		var err error
		p.Command, err = resolvePath(p.Command, baseDir, home)
		if err != nil {
			return v.fail(path, "invalid plugin executable path")
		}
		if p.Cwd == "" {
			p.Cwd = baseDir
		}
		p.Cwd, err = resolvePath(p.Cwd, baseDir, home)
		if err != nil {
			return v.fail(path, "invalid plugin working directory")
		}
		if err = p.Normalize(); err != nil {
			return v.fail(path, err.Error())
		}
		c.Plugins[id] = p
	}
	return nil
}
func (v *validator) pluginAction(path string, a *Action, c *Config) error {
	p, ok := c.Plugins[a.Plugin]
	floor, allowed := p.Operations[a.Operation]
	if !ok || !allowed {
		return v.fail(path, "plugin action requires an explicitly allowed plugin and operation")
	}
	if a.Command != "" || len(a.Args) != 0 || a.Cwd != "" || len(a.Environment) != 0 || a.Target != "" || a.Workflow != "" {
		return v.fail(path, "plugin actions accept plugin, operation, typed inputs, safety and timeout only")
	}
	a.Safety = permission.Strongest(a.Safety, permission.Strongest(floor, permission.Confirm))
	if a.Timeout == "" {
		a.Timeout = p.ExecutionTimeout
	}
	return nil
}
