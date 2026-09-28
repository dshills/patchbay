package cli

import (
	"errors"
	"fmt"
	"patchbay/internal/client"
	"patchbay/internal/config"
	"strconv"
	"strings"
	"time"
)

type ctlOptions struct {
	socket, config, demoDir             string
	json, help, version, async, confirm bool
	requestTimeout, timeout             time.Duration
	maxResponse                         int64
	args                                map[string]string
	used                                map[string]bool
}

func parseOptions(args []string) (ctlOptions, []string, error) {
	o := ctlOptions{socket: config.DefaultSocket, config: config.DefaultPath, requestTimeout: 5 * time.Second, maxResponse: client.DefaultMaxResponseBytes, args: map[string]string{}, used: map[string]bool{}}
	var positional []string
	var first error
	fail := func(message string) {
		if first == nil {
			first = errors.New(message)
		}
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" || len(arg) > 1 && (arg[1] >= '0' && arg[1] <= '9' || arg[1] == '.') {
			positional = append(positional, arg)
			continue
		}
		key, value, inline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if arg == "-h" {
			key = "help"
		}
		boolean := key == "json" || key == "help" || key == "version" || key == "async" || key == "confirm"
		if !boolean && key != "socket" && key != "demo-dir" && key != "config" && key != "request-timeout" && key != "timeout" && key != "arg" && key != "max-response-bytes" {
			fail("Unknown option; use --help for supported options.")
			continue
		}
		if o.used[key] && key != "arg" {
			fail("Duplicate option --" + key + ".")
		}
		o.used[key] = true
		if !boolean && !inline {
			if i+1 == len(args) {
				fail("Option --" + key + " requires a value.")
				continue
			}
			i++
			value = args[i]
		}
		if boolean {
			if !inline {
				value = "true"
			}
			if value != "true" && value != "false" {
				fail("Option --" + key + " requires true or false.")
				continue
			}
			enabled := value == "true"
			switch key {
			case "json":
				o.json = enabled
			case "help":
				o.help = enabled
			case "version":
				o.version = enabled
			case "async":
				o.async = enabled
			case "confirm":
				o.confirm = enabled
			}
			continue
		}
		switch key {
		case "demo-dir":
			o.demoDir = value
		case "socket":
			o.socket = value
		case "config":
			o.config = value
		case "request-timeout", "timeout":
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				fail("Option --" + key + " requires a positive duration.")
				continue
			}
			if key == "timeout" {
				o.timeout = d
			} else {
				o.requestTimeout = d
			}
		case "max-response-bytes":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < client.MinResponseBytes || n > client.MaxResponseBytes {
				fail(fmt.Sprintf("Response limit must be between %d and %d bytes.", client.MinResponseBytes, client.MaxResponseBytes))
			} else {
				o.maxResponse = n
			}
		case "arg":
			name, text, ok := strings.Cut(value, "=")
			if !ok || name == "" {
				fail("Each --arg must be name=value.")
				continue
			}
			if _, exists := o.args[name]; exists {
				fail("Duplicate action argument.")
			}
			o.args[name] = text
		}
	}
	return o, positional, first
}

func (o ctlOptions) validate(command []string) error {
	if o.version {
		if len(command) != 0 {
			return usage("--version does not accept a command.")
		}
		for key := range o.used {
			if key != "json" && key != "version" {
				return usage("Unsupported option with --version.")
			}
		}
		return nil
	}
	if len(command) == 0 {
		return usage("A command is required; use --help.")
	}
	key := command[0]
	if len(command) > 1 {
		key += " " + command[1]
	}
	lengths := map[string]int{"recipe export-preview": 4, "recipe export-save": 6, "recipe inspect": 3, "recipe import": 3, "recipe stage": 4, "recipe list": 2, "recipe show": 3, "recipe prepare": 4, "recipe commit": 6, "capabilities": 1, "workbench": 1, "demo": 1, "sample list": 2, "experiment list": 2, "experiment prepare": 3, "experiment run": 3, "experiment capture": 5, "run compare": 4, "request-id": 1, "export prepare": 4, "export save": 6, "storage status": 2, "run list": 2, "run show": 3, "run page": 3, "run annotate": 4, "run delete": 3, "baseline show": 3, "baseline set": 4, "status": 1, "project list": 2, "project current": 2, "project use": 3, "context show": 2, "context set": 4, "action list": 2, "action run": 3, "workflow list": 2, "workflow run": 3, "job list": 2, "job show": 3, "job cancel": 3, "param list": 2, "param get": 3, "param set": 4, "config validate": 2, "config reload": 2}
	if n, ok := lengths[key]; !ok || n != len(command) {
		return usage("Unknown command or incorrect number of arguments; use --help.")
	}
	for flag := range o.used {
		if key == "demo" && flag != "demo-dir" && flag != "help" {
			return usage("Demo accepts only --demo-dir and --help.")
		}
		if flag == "demo-dir" && key != "demo" {
			return usage("--demo-dir requires demo.")
		}
		if key == "workbench" && flag != "socket" && flag != "help" {
			return usage("Workbench accepts only --socket and --help.")
		}
		switch flag {
		case "confirm":
			if key != "recipe export-save" && key != "recipe commit" && key != "action run" && key != "workflow run" && key != "run delete" && key != "experiment run" && key != "experiment capture" {
				return usage("--confirm requires an execution or deletion command.")
			}
		case "async", "timeout":
			if key != "action run" && key != "workflow run" && (key != "experiment run" || flag == "timeout") {
				return usage("Execution options require action run or workflow run.")
			}
		case "arg":
			if key != "action run" {
				return usage("--arg requires action run; workflows have no top-level arguments.")
			}
		case "config":
			if key != "config validate" {
				return usage("--config is only for offline config validate; reload uses the daemon's configuration.")
			}
		case "socket", "request-timeout", "max-response-bytes":
			if key == "config validate" || key == "recipe inspect" {
				return usage("Offline validation does not use socket or transport options.")
			}
		}
	}
	if len(command) > 2 && key != "recipe inspect" && key != "recipe import" && key != "context set" && key != "config validate" && key != "run page" && key != "run compare" && (key != "project use" || command[2] != "") && !config.ValidName(command[2]) {
		return usage("Resource names must be valid identifiers.")
	}
	return nil
}

const ctlHelp = `deckctl: Patchbay local automation client
Usage: deckctl [options] <command> [options]

  recipe inspect <directory-or-zip> [--json]
  recipe import <directory-or-zip> | stage <installation-id> <directory-or-zip>
  recipe list | show <id-or-alias> | prepare <id-or-alias> <JSON>
  recipe commit <installation-id> <preparation> <digest> <request-id> --confirm
  recipe export-preview <id-or-alias> <JSON>
  recipe export-save <installation-id> <preparation> <digest> <new.zip> --confirm
  demo [--demo-dir path]
  workbench [--socket path]
  status | capabilities
  experiment list | prepare <id> | run <id> [--confirm] [--async]
  experiment capture <preparation> <digest> <request-id> [--confirm]
  request-id
  export prepare <baseline-id> <candidate-id>
  export save <preparation> <digest> <html|json> <new-file>
  run compare <baseline-id> <candidate-id>
  sample list
  storage status
  run list | page <cursor> | show <id> | annotate <id> <JSON> | delete <id> [--confirm]
  baseline show <experiment> | set <experiment> <JSON>
  project list | current | use <id>
  context show | set <project|mode|values> <value>
  action list | run <name> [--arg name=value ...]
  workflow list | run <name>
  job list | show <id> | cancel <id>
  param list | get <name> | set <name> <value>
  config validate [--config path] | reload

Global: --socket path, --json, --request-timeout 5s,
        --max-response-bytes 134217728, --help, --version
Run:    --confirm, --timeout duration, --async

Runs wait by default. Ctrl-C requests cancellation. --async returns a job ID.
Action and parameter values use discovered types; strings are literal.
Context values takes a JSON object of strings and replaces the whole map.
--config only affects offline validation. No mutation is automatically retried.
Exit codes: 0 success, 1 config/internal/output, 2 usage/value/not-found,
            3 transport/unavailable, 4 permission, 5 execution, 124 timeout,
            130 cancellation. See specs/CLI.md for the complete contract.
`
