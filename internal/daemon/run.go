// Package daemon assembles the local listener and runtime lifecycle.
package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"patchbay/internal/api"
	"patchbay/internal/config"
	"patchbay/internal/logging"
	runtimecore "patchbay/internal/runtime"
)

// Run owns the HTTP serving goroutine. Cancellation joins it and shuts down the
// runtime within one grace budget. Listener acquisition precedes state restore
// so a second daemon cannot race the active daemon's persistent state.
func Run(ctx context.Context, path string, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	listener, err := api.Listen(c.Server.Socket)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	runtime, err := runtimecore.NewConfigured(path, c, runtimecore.Options{Log: output})
	if err != nil {
		return err
	}
	server := &http.Server{Handler: api.NewHandler(runtime), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logging.New(output).Operation(ctx, logging.Record{Component: "daemon", Outcome: "ready"})
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-done:
	}
	grace, _ := time.ParseDuration(c.Server.ShutdownGrace)
	shutdown, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	runtimeErr := runtime.Close(shutdown)
	httpErr := server.Shutdown(shutdown)
	if httpErr != nil {
		_ = server.Close()
	}
	if serveErr == nil {
		select {
		case serveErr = <-done:
		case <-shutdown.Done():
			serveErr = shutdown.Err()
		}
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	logging.New(output).Operation(context.Background(), logging.Record{Component: "daemon", Outcome: "stopped", Err: errors.Join(runtimeErr, httpErr, serveErr)})
	return errors.Join(runtimeErr, httpErr, serveErr)
}
