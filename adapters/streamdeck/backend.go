package streamdeck

import (
	"context"
	"patchbay/internal/client"
	"patchbay/pkg/protocol"
	"time"
)

type HTTPBackend struct{ client *client.Client }

func NewHTTPBackend(socket string) (*HTTPBackend, error) {
	c, err := client.New(client.Options{Socket: socket, Timeout: time.Second, MaxResponseBytes: 8 << 20})
	if err != nil {
		return nil, err
	}
	return &HTTPBackend{c}, nil
}
func (b *HTTPBackend) Close() { b.client.Close() }
func (b *HTTPBackend) Snapshot(ctx context.Context, refs []protocol.ControlRef) (protocol.ControlSnapshot, error) {
	var value protocol.ControlSnapshot
	err := b.client.Call(ctx, "POST", []string{"controls", "snapshot"}, protocol.ControlSnapshotRequest{Controls: refs}, &value)
	return value, err
}
func (b *HTTPBackend) Control(ctx context.Context, request protocol.EventRequest) (protocol.EventResponse, error) {
	var value protocol.EventResponse
	err := b.client.Call(ctx, "POST", []string{"events"}, request, &value)
	return value, err
}
func (b *HTTPBackend) Job(ctx context.Context, id string) (protocol.Job, error) {
	var value protocol.Job
	err := b.client.Call(ctx, "GET", []string{"jobs", id}, nil, &value)
	return value, err
}
