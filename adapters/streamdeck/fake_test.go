package streamdeck

import (
	"context"
	"time"

	"patchbay/pkg/protocol"
)

// fakeDevice replays timestamped app messages and captures the same frames that
// the real session sends to hardware. It never needs a USB device or app install.
type fakeDevice struct {
	now    time.Time
	frames []Render
}

func (d *fakeDevice) advance(duration time.Duration) { d.now = d.now.Add(duration) }
func (d *fakeDevice) capture(e *Engine)              { d.frames = append(d.frames, e.Render(d.now)...) }

type fakeBackend struct {
	snapshot  protocol.ControlSnapshot
	requests  []protocol.EventRequest
	jobs      map[string]protocol.Job
	err       error
	onControl func(protocol.EventRequest) (protocol.EventResponse, error)
}

func (b *fakeBackend) Snapshot(_ context.Context, refs []protocol.ControlRef) (protocol.ControlSnapshot, error) {
	result := b.snapshot
	result.Controls = make([]protocol.ControlView, 0, len(refs))
	for _, ref := range refs {
		view := protocol.ControlView{ControlRef: ref, Targets: map[string]protocol.ControlTarget{}}
		for _, candidate := range b.snapshot.Controls {
			if candidate.ControlRef == ref {
				view = candidate
			}
		}
		result.Controls = append(result.Controls, view)
	}
	return result, b.err
}
func (b *fakeBackend) Control(_ context.Context, r protocol.EventRequest) (protocol.EventResponse, error) {
	b.requests = append(b.requests, r)
	if b.onControl != nil {
		return b.onControl(r)
	}
	return protocol.EventResponse{EventID: "event", Matched: true}, b.err
}
func (b *fakeBackend) Job(_ context.Context, id string) (protocol.Job, error) {
	return b.jobs[id], b.err
}
