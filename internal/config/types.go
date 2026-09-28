// Package config loads and validates declarative configuration without starting
// providers, opening sockets, or inspecting project contents.
package config

import (
	"patchbay/internal/binding"
	runtimecontext "patchbay/internal/context"
	"patchbay/internal/parameter"
	"patchbay/internal/permission"
	"patchbay/internal/provider"
	"patchbay/internal/workflow"
	"patchbay/pkg/protocol"
)

const (
	DefaultPath      = "~/.config/deckd/config.yaml"
	DefaultSocket    = "~/.deckd/deckd.sock"
	DefaultStatePath = "~/.deckd/state.json"
	MaxConfigBytes   = 1 << 20
)

type Config struct {
	Runs        RunSettings                             `yaml:"runs,omitempty"`
	Experiments map[string]protocol.Experiment          `yaml:"experiments,omitempty"`
	Version     int                                     `yaml:"version"`
	Server      Server                                  `yaml:"server,omitempty"`
	Context     Context                                 `yaml:"context,omitempty"`
	Security    Security                                `yaml:"security,omitempty"`
	Jobs        Jobs                                    `yaml:"jobs,omitempty"`
	Events      Events                                  `yaml:"events,omitempty"`
	State       State                                   `yaml:"state,omitempty"`
	Projects    map[string]Project                      `yaml:"projects,omitempty"`
	Actions     map[string]Action                       `yaml:"actions,omitempty"`
	Workflows   map[string]Workflow                     `yaml:"workflows,omitempty"`
	Parameters  map[string]parameter.Definition         `yaml:"parameters,omitempty"`
	Bindings    []binding.Binding                       `yaml:"bindings,omitempty"`
	Prompts     map[string]string                       `yaml:"prompts,omitempty"`
	Agents      Agents                                  `yaml:"agents,omitempty"`
	Conventions map[string]map[string]ConventionCommand `yaml:"conventions,omitempty"`
	Devices     map[string]provider.SCPIDevice          `yaml:"devices,omitempty"`
	Plugins     map[string]provider.PluginConfig        `yaml:"plugins,omitempty"`
}

type Agents struct {
	Codex Codex `yaml:"codex,omitempty"`
}
type Codex struct {
	Model           string `yaml:"model,omitempty"`
	MaxOutputTokens int    `yaml:"max_output_tokens,omitempty"`
}
type ConventionCommand struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args,omitempty"`
}

type Server struct {
	Socket          string `yaml:"socket"`
	MaxRequestBytes int64  `yaml:"max_request_bytes,omitempty"`
	ShutdownGrace   string `yaml:"shutdown_grace,omitempty"`
}

type Context struct {
	Defaults runtimecontext.RuntimeContext `yaml:"defaults,omitempty"`
}
type Security struct {
	AllowDangerousActions bool `yaml:"allow_dangerous_actions,omitempty"`
}
type Jobs struct {
	Concurrency      int   `yaml:"concurrency,omitempty"`
	QueueCapacity    int   `yaml:"queue_capacity,omitempty"`
	HistoryLimit     int   `yaml:"history_limit,omitempty"`
	OutputLimitBytes int64 `yaml:"output_limit_bytes,omitempty"`
}
type Events struct {
	SubscriberCapacity int `yaml:"subscriber_capacity,omitempty"`
}
type State struct {
	Path          string `yaml:"path,omitempty"`
	FlushInterval string `yaml:"flush_interval,omitempty"`
}

type RunSettings struct {
	Path             string `yaml:"path,omitempty"`
	MaxRuns          int    `yaml:"max_runs,omitempty"`
	MaxBytes         int64  `yaml:"max_bytes,omitempty"`
	MaxRunBytes      int64  `yaml:"max_run_bytes,omitempty"`
	MaxArtifactBytes int64  `yaml:"max_artifact_bytes,omitempty"`
	MaxReceipts      int    `yaml:"max_receipts,omitempty"`
}

type Project struct {
	ID          string            `yaml:"id,omitempty"`
	Name        string            `yaml:"name"`
	Path        string            `yaml:"path"`
	GitHub      string            `yaml:"github,omitempty"`
	Language    string            `yaml:"language,omitempty"`
	Engine      string            `yaml:"engine,omitempty"`
	Metadata    map[string]string `yaml:"metadata,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Actions     map[string]Action `yaml:"actions,omitempty"`
	Conventions []string          `yaml:"conventions,omitempty"`
}

type Action struct {
	Experiment  string                `yaml:"experiment,omitempty"`
	Type        string                `yaml:"type"`
	Safety      permission.Permission `yaml:"safety,omitempty"`
	Command     string                `yaml:"command,omitempty"`
	Args        []string              `yaml:"args,omitempty"`
	Cwd         string                `yaml:"cwd,omitempty"`
	Environment map[string]string     `yaml:"environment,omitempty"`
	Timeout     string                `yaml:"timeout,omitempty"`
	Target      string                `yaml:"target,omitempty"`
	Operation   string                `yaml:"operation,omitempty"`
	Workflow    string                `yaml:"workflow,omitempty"`
	Inputs      map[string]Input      `yaml:"inputs,omitempty"`
	Provider    string                `yaml:"provider,omitempty"`
	Prompt      string                `yaml:"prompt,omitempty"`
	Files       []string              `yaml:"files,omitempty"`
	Origin      string                `yaml:"-"`
	Device      string                `yaml:"device,omitempty"`
	Channel     int                   `yaml:"channel,omitempty"`
	Parameter   string                `yaml:"parameter,omitempty"`
	Plugin      string                `yaml:"plugin,omitempty"`
}

type Input struct {
	Sensitive bool           `yaml:"sensitive,omitempty"`
	Type      parameter.Type `yaml:"type"`
	Required  bool           `yaml:"required,omitempty"`
	Default   any            `yaml:"default,omitempty"`
	Min       any            `yaml:"min,omitempty"`
	Max       any            `yaml:"max,omitempty"`
	Enum      []string       `yaml:"enum,omitempty"`
}

type Workflow struct {
	StopOnError *bool                   `yaml:"stop_on_error,omitempty"`
	Steps       []workflow.WorkflowStep `yaml:"steps"`
}

func defaults() Config {
	return Config{
		Runs:   RunSettings{MaxRuns: 1000, MaxBytes: 1 << 30, MaxRunBytes: 16 << 20, MaxArtifactBytes: 4 << 20, MaxReceipts: 10000},
		Server: Server{Socket: DefaultSocket, MaxRequestBytes: 1 << 20, ShutdownGrace: "5s"},
		Jobs:   Jobs{Concurrency: 4, QueueCapacity: 64, HistoryLimit: 100, OutputLimitBytes: 1 << 20},
		Events: Events{SubscriberCapacity: 64},
		State:  State{Path: DefaultStatePath, FlushInterval: "250ms"},
	}
}
