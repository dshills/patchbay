package provider

import (
	"errors"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"patchbay/internal/permission"
	wire "patchbay/pkg/plugin"
)

type PluginConfig struct {
	Command          string                           `yaml:"command"`
	Args             []string                         `yaml:"args,omitempty"`
	Cwd              string                           `yaml:"cwd,omitempty"`
	Environment      map[string]string                `yaml:"environment,omitempty"`
	Operations       map[string]permission.Permission `yaml:"operations"`
	StartupTimeout   string                           `yaml:"startup_timeout,omitempty"`
	ExecutionTimeout string                           `yaml:"execution_timeout,omitempty"`
	CancelTimeout    string                           `yaml:"cancel_timeout,omitempty"`
	ShutdownTimeout  string                           `yaml:"shutdown_timeout,omitempty"`
}

var pluginOperationName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,63}$`)
var pluginEnvironmentName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func (p *PluginConfig) Normalize() error {
	bad := func() error {
		return errors.New("plugin requires literal executable/cwd paths, bounded settings and explicit operation permissions")
	}
	literal := func(s string) bool {
		return len(s) <= 4096 && !strings.ContainsAny(s, "\x00\r\n") && !strings.Contains(s, "{{") && !strings.Contains(s, "}}")
	}
	if !filepath.IsAbs(p.Command) || !filepath.IsAbs(p.Cwd) || !literal(p.Command) || !literal(p.Cwd) || len(p.Args) > 64 || len(p.Environment) > 32 || len(p.Operations) == 0 || len(p.Operations) > wire.MaxOperations {
		return bad()
	}
	for _, a := range p.Args {
		if !literal(a) {
			return bad()
		}
	}
	for k, v := range p.Environment {
		if !pluginEnvironmentName.MatchString(k) || !literal(v) {
			return bad()
		}
	}
	for name, risk := range p.Operations {
		if !pluginOperationName.MatchString(name) || !risk.Valid() {
			return bad()
		}
		p.Operations[name] = permission.Strongest(risk, permission.Confirm)
	}
	for _, limit := range []struct {
		p     *string
		value string
		max   time.Duration
	}{{&p.StartupTimeout, "2s", 30 * time.Second}, {&p.ExecutionTimeout, "30s", 10 * time.Minute}, {&p.CancelTimeout, "500ms", 2 * time.Second}, {&p.ShutdownTimeout, "500ms", 2 * time.Second}} {
		if *limit.p == "" {
			*limit.p = limit.value
		}
		d, err := time.ParseDuration(*limit.p)
		if err != nil || d < 10*time.Millisecond || d > limit.max {
			return bad()
		}
	}
	return nil
}
func clonePluginConfig(p PluginConfig) PluginConfig {
	p.Args = slices.Clone(p.Args)
	p.Environment = maps.Clone(p.Environment)
	p.Operations = maps.Clone(p.Operations)
	return p
}
func pluginDuration(s string) time.Duration { d, _ := time.ParseDuration(s); return d }

type PluginRequest struct {
	Plugin, Operation         string
	Args                      map[string]any
	Context                   wire.Context
	AllowDangerous, Confirmed bool
	// Started is an optional conformance-test observer, after execute is written.
	Started func()
}
type PluginSnapshot struct {
	Health     Health
	Operations []wire.Operation
}
