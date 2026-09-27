// Package fault normalizes execution errors to safe, shared protocol categories.
package fault

import (
	"context"
	"errors"
	"patchbay/pkg/protocol"
)

func New(code protocol.Code, message string) *protocol.Error {
	return &protocol.Error{Code: code, Message: message}
}

// Safe never copies arbitrary provider/OS error messages into responses or logs.
func Safe(err error) *protocol.Error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return New(protocol.Timeout, "Execution timed out.")
	}
	if errors.Is(err, context.Canceled) {
		return New(protocol.Cancelled, "Execution was cancelled.")
	}
	var known *protocol.Error
	if errors.As(err, &known) && known != nil && known.Code.Valid() {
		copy := *known
		return &copy
	}
	return New(protocol.Internal, "An internal operation failed.")
}
