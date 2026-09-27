// Package logging emits structured operation summaries without logging error
// messages, configuration values, request arguments, or subprocess output.
package logging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"patchbay/pkg/protocol"
)

type Logger struct{ logger *slog.Logger }

type Record struct {
	Component string
	EventID   string
	ActionID  string
	JobID     string
	Duration  time.Duration
	Outcome   string
	Err       error
}

func New(writer io.Writer) *Logger {
	return &Logger{logger: slog.New(slog.NewJSONHandler(writer, nil))}
}

// Operation accepts generated identifiers and fixed component/outcome labels.
// Err is deliberately reduced to a stable code. Callers must not put user data
// into identifiers or labels; raw slog access is not exposed by this wrapper.
func (l *Logger) Operation(ctx context.Context, r Record) {
	attrs := []slog.Attr{
		slog.String("component", r.Component), slog.String("event_id", r.EventID),
		slog.String("action_id", r.ActionID), slog.String("job_id", r.JobID),
		slog.Int64("duration_ms", r.Duration.Milliseconds()), slog.String("outcome", r.Outcome),
	}
	level := slog.LevelInfo
	if r.Err != nil {
		level = slog.LevelError
		code := protocol.Internal
		var apiError *protocol.Error
		if errors.As(r.Err, &apiError) && apiError != nil && apiError.Code.Valid() {
			code = apiError.Code
		}
		attrs = append(attrs, slog.String("error", string(code)))
	}
	l.logger.LogAttrs(ctx, level, "operation", attrs...)
}
