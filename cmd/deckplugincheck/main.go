// deckplugincheck runs conformance probes against an explicitly configured plugin.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"patchbay/internal/config"
	"patchbay/internal/jsonstrict"
	wire "patchbay/pkg/plugin"
	"patchbay/pkg/pluginconform"
)

func main() { os.Exit(run()) }
func run() int {
	flags := flag.NewFlagSet("deckplugincheck", flag.ContinueOnError)
	path := flags.String("config", "configs/plugins.yaml", "configuration file (executes its configured plugin)")
	name := flags.String("plugin", "example", "configured plugin name")
	operation := flags.String("operation", "echo", "successful test operation")
	arguments := flags.String("args", `{"text":"conformance"}`, "JSON input object for successful operation")
	cancelOperation := flags.String("cancel-operation", "wait", "long-running operation that supports cancellation")
	cancelArguments := flags.String("cancel-args", `{"milliseconds":30000}`, "JSON input object for cancellation operation")
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 {
		return 2
	}
	c, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	p, ok := c.Plugins[*name]
	if !ok {
		fmt.Fprintln(os.Stderr, "Plugin is not configured.")
		return 2
	}
	s := pluginconform.Specification{Command: p.Command, Args: p.Args, Directory: p.Cwd, Environment: p.Environment, Operations: map[string]string{}, Execute: wire.Execute{Operation: *operation}, Cancel: wire.Execute{Operation: *cancelOperation}}
	s.AllowDangerous = c.Security.AllowDangerousActions
	for n, risk := range p.Operations {
		s.Operations[n] = string(risk)
	}
	if jsonstrict.Decode([]byte(*arguments), &s.Execute.Args) != nil || jsonstrict.Decode([]byte(*cancelArguments), &s.Cancel.Args) != nil {
		fmt.Fprintln(os.Stderr, "Arguments must be JSON objects.")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	report, err := pluginconform.Check(ctx, s)
	if e := json.NewEncoder(os.Stdout).Encode(report); e != nil {
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
