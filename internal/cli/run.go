// Package cli provides daemon startup and the current deckctl command surface.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"patchbay/internal/config"
	"patchbay/internal/daemon"
	"patchbay/internal/logging"
	"patchbay/internal/version"
	"patchbay/pkg/protocol"
)

// Run returns a process exit code. Daemon callers should use RunContext.
func Run(program string, args []string, stdout, stderr io.Writer) int {
	return RunContext(context.Background(), program, args, stdout, stderr)
}

func RunContext(ctx context.Context, program string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(program, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var versionFlag, validateFlag, jsonFlag bool
	var configPath string
	flags.BoolVar(&versionFlag, "version", false, "print build version")
	flags.BoolVar(&jsonFlag, "json", false, "emit JSON to stdout")
	flags.StringVar(&configPath, "config", config.DefaultPath, "configuration file")
	if program == "deckd" {
		flags.BoolVar(&validateFlag, "validate", false, "validate configuration and exit")
	}
	flags.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "%s: Deckd local automation runtime\n", program)
		if program == "deckctl" {
			_, _ = fmt.Fprintln(stderr, "Usage: deckctl [flags] config validate [flags]")
		} else {
			_, _ = fmt.Fprintln(stderr, "Usage: deckd [flags] [--validate]")
		}
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := flags.Args()
	if program == "deckctl" && len(rest) >= 2 && rest[0] == "config" && rest[1] == "validate" {
		validateFlag = true
		if err := flags.Parse(rest[2:]); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		rest = flags.Args()
	}
	if len(rest) != 0 || versionFlag && validateFlag {
		flags.Usage()
		return 2
	}
	if versionFlag {
		if jsonFlag {
			return writeJSON(stdout, version.Current())
		}
		if _, err := fmt.Fprintf(stdout, "%s %s\n", program, version.Current()); err != nil {
			return 1
		}
		return 0
	}
	if !validateFlag {
		if program == "deckd" {
			if err := daemon.Run(ctx, configPath, stderr); err != nil {
				logging.New(stderr).Operation(context.Background(), logging.Record{Component: "daemon", Outcome: "failed", Err: err})
				_, _ = fmt.Fprintln(stderr, "Daemon could not start or shut down cleanly; check configuration and private socket/state paths.")
				return 1
			}
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "Runtime commands are available in later phases. Use --help for offline foundation commands.")
		return 2
	}
	start := time.Now()
	_, err := config.Load(configPath)
	if err != nil {
		apiError := &protocol.Error{Code: protocol.InvalidConfig, Message: err.Error()}
		if program == "deckd" {
			logging.New(stderr).Operation(context.Background(), logging.Record{Component: "config", Duration: time.Since(start), Outcome: "failed", Err: apiError})
		}
		if jsonFlag {
			_ = writeJSON(stdout, protocol.ErrorResponse{Error: *apiError})
		} else {
			_, _ = fmt.Fprintln(stderr, err)
		}
		return 1
	}
	if jsonFlag {
		return writeJSON(stdout, struct {
			Valid bool `json:"valid"`
		}{Valid: true})
	}
	if _, err := fmt.Fprintln(stdout, "Configuration is valid."); err != nil {
		return 1
	}
	return 0
}

func writeJSON(writer io.Writer, value any) int {
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return 1
	}
	return 0
}
