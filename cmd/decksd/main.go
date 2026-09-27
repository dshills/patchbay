package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"patchbay/adapters/streamdeck"
	"patchbay/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
func run(ctx context.Context, args []string, out, diagnostic io.Writer) int {
	flags := flag.NewFlagSet("decksd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	launch := streamdeck.Launch{}
	var info string
	var capabilities, showVersion bool
	flags.IntVar(&launch.Port, "port", 0, "Stream Deck app port")
	flags.StringVar(&launch.UUID, "pluginUUID", "", "registration UUID")
	flags.StringVar(&launch.RegisterEvent, "registerEvent", "", "registration event")
	flags.StringVar(&info, "info", "", "host/device metadata")
	flags.StringVar(&launch.Socket, "socket", streamdeck.DefaultSocket, "private daemon socket")
	flags.BoolVar(&capabilities, "capabilities", false, "print supported capabilities")
	flags.BoolVar(&showVersion, "version", false, "print build version")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(diagnostic, "decksd: invalid arguments; launched by Stream Deck, or use --capabilities / --version")
		return 2
	}
	if showVersion {
		if json.NewEncoder(out).Encode(version.Current()) != nil {
			return 1
		}
		return 0
	}
	if capabilities {
		if json.NewEncoder(out).Encode(streamdeck.SupportedCapabilities()) != nil {
			return 1
		}
		return 0
	}
	var registration struct {
		Devices []streamdeck.Device `json:"devices"`
	}
	if len(info) > 64<<10 || json.Unmarshal([]byte(info), &registration) != nil {
		_, _ = fmt.Fprintln(diagnostic, "decksd: invalid registration metadata")
		return 2
	}
	launch.Devices = registration.Devices
	if len(launch.Devices) > streamdeck.MaxControls {
		_, _ = fmt.Fprintln(diagnostic, "decksd: too many devices")
		return 2
	}
	if err := streamdeck.Run(ctx, launch, diagnostic); err != nil {
		_, _ = fmt.Fprintln(diagnostic, "decksd: invalid launch configuration")
		return 2
	}
	return 0
}
