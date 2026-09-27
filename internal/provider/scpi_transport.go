package provider

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

type scpiWire struct {
	conn    net.Conn
	reader  *bufio.Reader
	ctx     context.Context
	timeout time.Duration
	limit   int
}

func (w *scpiWire) deadline() error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(w.timeout)
	if end, ok := w.ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	return w.conn.SetDeadline(deadline)
}
func (w *scpiWire) write(command string) error {
	if strings.ContainsAny(command, "\r\n\x00") || len(command) > 256 {
		return scpiInvalid()
	}
	if err := w.deadline(); err != nil {
		return err
	}
	_, err := io.WriteString(w.conn, command+"\n")
	return err
}
func (w *scpiWire) line() (string, error) {
	var b strings.Builder
	for b.Len() <= min(w.limit, 4096) {
		c, err := w.reader.ReadByte()
		if err != nil {
			return "", err
		}
		if c == '\n' {
			return strings.TrimSuffix(b.String(), "\r"), nil
		}
		if c == 0 {
			return "", scpiInvalid()
		}
		b.WriteByte(c)
	}
	return "", scpiInvalid()
}
func (w *scpiWire) query(command string) (string, error) {
	if err := w.write(command); err != nil {
		return "", err
	}
	text, err := w.line()
	return strings.TrimSpace(text), err
}
func (w *scpiWire) block(command string, maxBytes int) ([]byte, error) {
	if err := w.write(command); err != nil {
		return nil, err
	}
	header := make([]byte, 2)
	if _, err := io.ReadFull(w.reader, header); err != nil {
		return nil, err
	}
	if header[0] != '#' || header[1] < '1' || header[1] > '9' {
		return nil, scpiInvalid()
	}
	digits := make([]byte, int(header[1]-'0'))
	if _, err := io.ReadFull(w.reader, digits); err != nil {
		return nil, err
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return nil, scpiInvalid()
		}
	}
	n, err := strconv.Atoi(string(digits))
	if err != nil || n < 1 || n > min(w.limit, maxBytes) {
		return nil, scpiInvalid()
	}
	data := make([]byte, n)
	if _, err = io.ReadFull(w.reader, data); err != nil {
		return nil, err
	}
	ending, err := w.line()
	if err != nil {
		return nil, err
	}
	if ending != "" {
		return nil, scpiInvalid()
	}
	return data, nil
}
func scpiInvalid() error {
	return fault.New(protocol.ExecutionFailed, "Instrument returned an invalid or unsupported response.")
}
func scpiError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fault.Safe(ctx.Err())
	}
	var known *protocol.Error
	if errors.As(err, &known) {
		return known
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return fault.New(protocol.Timeout, "Instrument communication timed out; state may be unknown.")
	}
	return fault.New(protocol.ProviderUnavailable, "Instrument communication failed; state may be unknown.")
}
