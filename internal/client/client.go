// Package client implements bounded HTTP requests over a local Unix socket.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"patchbay/pkg/protocol"
	"path/filepath"
	"strings"
	"time"
)

const (
	MinResponseBytes        int64 = 1 << 10
	DefaultMaxResponseBytes int64 = 128 << 20
	MaxResponseBytes        int64 = 1 << 30
)

type Options struct {
	Socket           string
	Timeout          time.Duration
	MaxResponseBytes int64
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type Client struct {
	http              *http.Client
	mutations         *http.Client
	transport         *http.Transport
	mutationTransport *http.Transport
	limit             int64
}

func New(options Options) (*Client, error) {
	if options.Timeout <= 0 {
		return nil, &Error{"usage", "Request timeout must be a positive duration."}
	}
	if options.MaxResponseBytes < MinResponseBytes || options.MaxResponseBytes > MaxResponseBytes {
		return nil, &Error{"usage", fmt.Sprintf("Response limit must be between %d and %d bytes.", MinResponseBytes, MaxResponseBytes)}
	}
	path := options.Socket
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, &Error{"transport", "Cannot determine the socket home directory."}
		}
		path = filepath.Join(home, path[2:])
	} else if strings.HasPrefix(path, "~") {
		return nil, &Error{"usage", "Socket paths support ~/ but not named-user expansion."}
	}
	if path == "" || strings.ContainsRune(path, 0) {
		return nil, &Error{"usage", "A valid socket path is required."}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, &Error{"transport", "Cannot resolve the socket path."}
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second, DisableCompression: true}
	// A pooled connection can trigger net/http's zero-byte request replay.
	// Mutations get a fresh connection, including bodyless cancellation requests.
	mutationTransport := transport.Clone()
	mutationTransport.DisableKeepAlives = true
	newHTTP := func(t *http.Transport) *http.Client {
		return &http.Client{Transport: t, Timeout: options.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Client{http: newHTTP(transport), mutations: newHTTP(mutationTransport), transport: transport, mutationTransport: mutationTransport, limit: options.MaxResponseBytes}, nil
}
func (c *Client) Close() {
	c.transport.CloseIdleConnections()
	c.mutationTransport.CloseIdleConnections()
}

// Call never retries mutations. Each resource segment is escaped independently.
// Additive response fields are accepted, but malformed/trailing JSON is rejected.
func (c *Client) Call(ctx context.Context, method string, segments []string, request, response any) error {
	return c.CallQuery(ctx, method, segments, nil, request, response)
}

// CallQuery escapes query values without treating them as resource paths.
func (c *Client) CallQuery(ctx context.Context, method string, segments []string, query url.Values, request, response any) error {
	var body io.Reader
	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			return &Error{"usage", "Request cannot be encoded as JSON."}
		}
		body = bytes.NewReader(data)
	}
	return c.callBody(ctx, method, segments, query, body, "application/json", response)
}

// Upload sends explicit bounded bytes once; it never retries a mutation.
func (c *Client) Upload(ctx context.Context, segments []string, query url.Values, data []byte, response any) error {
	if len(data) > 20<<20 {
		return &Error{"usage", "Upload exceeds 20 MiB."}
	}
	return c.callBody(ctx, "POST", segments, query, bytes.NewReader(data), "application/zip", response)
}
func (c *Client) callBody(ctx context.Context, method string, segments []string, query url.Values, body io.Reader, media string, response any) error {
	escaped := make([]string, len(segments))
	for i, part := range segments {
		escaped[i] = url.PathEscape(part)
	}
	endpoint := "http://deckd/v1/" + strings.Join(escaped, "/")
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return &Error{"usage", "Cannot construct the request."}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", media)
	}
	connection := c.http
	if method != "GET" && method != "HEAD" {
		connection = c.mutations
		req.GetBody = nil
	}
	result, err := connection.Do(req)
	if err != nil {
		return transportError(err)
	}
	defer func() { _ = result.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(result.Body, c.limit+1))
	if err != nil {
		return transportError(err)
	}
	if int64(len(data)) > c.limit {
		return &Error{"invalid_response", "Response exceeds --max-response-bytes; inspect one job or reduce retained output/history."}
	}
	if result.StatusCode >= 300 && result.StatusCode < 400 {
		return &Error{"invalid_response", "Daemon returned an unexpected redirect."}
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		var envelope protocol.ErrorResponse
		if err := decode(data, &envelope); err != nil || !envelope.Error.Code.Valid() || envelope.Error.Message == "" {
			return &Error{"invalid_response", "Daemon returned an invalid error response."}
		}
		return &envelope.Error
	}
	if err := decode(data, response); err != nil {
		return &Error{"invalid_response", "Daemon returned malformed or incompatible JSON."}
	}
	return nil
}

func decode(data []byte, response any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return errors.New("expected object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(response); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func transportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return context.DeadlineExceeded
	}
	return &Error{"transport", "Cannot communicate with deckd. Check that it is running and --socket selects its private socket."}
}
