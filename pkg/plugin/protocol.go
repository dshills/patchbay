// Package plugin defines the Deckd executable plugin protocol, version 1.
package plugin

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"

	"patchbay/internal/jsonstrict"
)

const Version = 1
const MaxFrameBytes = 64 << 10
const MaxOperations = 64

var ErrProtocol = errors.New("invalid plugin protocol message")

type Frame struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}
type HelloRequest struct {
	Versions []int `json:"versions"`
}
type Operation struct {
	Name   string `json:"name"`
	Safety string `json:"safety"`
}
type HelloResponse struct {
	SelectedVersion int         `json:"selected_version"`
	Operations      []Operation `json:"operations"`
}
type Health struct {
	Available *bool `json:"available"`
}
type Context struct {
	Project string            `json:"project,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	Values  map[string]string `json:"values,omitempty"`
}
type Execute struct {
	Operation string         `json:"operation"`
	Args      map[string]any `json:"args"`
	Context   Context        `json:"context"`
}
type Result struct {
	Status string  `json:"status"`
	Text   *string `json:"text"`
}

func NewFrame(id, kind string, payload any) (Frame, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Frame{}, ErrProtocol
	}
	return Frame{Version: Version, ID: id, Type: kind, Payload: data}, nil
}
func Decode(data []byte, destination any) error {
	if len(data) > MaxFrameBytes || !utf8.Valid(data) || jsonstrict.Decode(data, destination) != nil {
		return ErrProtocol
	}
	return nil
}
func validFrame(f Frame) bool {
	id, err := strconv.ParseUint(f.ID, 10, 64)
	if f.Version != Version || err != nil || id == 0 || len(f.ID) > 16 || strconv.FormatUint(id, 10) != f.ID || len(f.Payload) == 0 {
		return false
	}
	switch f.Type {
	case "hello", "health", "execute", "result", "cancel", "cancelled", "shutdown":
		return true
	}
	return false
}

type Reader struct{ reader *bufio.Reader }

func NewReader(r io.Reader) *Reader { return &Reader{bufio.NewReaderSize(r, MaxFrameBytes+1)} }
func (r *Reader) Read() (Frame, error) {
	data, err := r.reader.ReadSlice('\n')
	if err != nil {
		if err == io.EOF && len(data) == 0 {
			return Frame{}, io.EOF
		}
		return Frame{}, ErrProtocol
	}
	var f Frame
	if Decode(data[:len(data)-1], &f) != nil || !validFrame(f) {
		return Frame{}, ErrProtocol
	}
	return f, nil
}
func Encode(f Frame) ([]byte, error) {
	if !validFrame(f) {
		return nil, ErrProtocol
	}
	data, err := json.Marshal(f)
	var check Frame
	if err != nil || Decode(data, &check) != nil {
		return nil, ErrProtocol
	}
	return append(data, '\n'), nil
}
func Write(w io.Writer, f Frame) error {
	data, err := Encode(f)
	if err != nil {
		return err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
