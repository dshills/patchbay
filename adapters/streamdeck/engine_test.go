package streamdeck

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"patchbay/pkg/protocol"
)

func engineFixture(t testing.TB) (*Engine, *fakeBackend, *fakeDevice) {
	t.Helper()
	d := &fakeDevice{now: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	b := &fakeBackend{snapshot: protocol.ControlSnapshot{Guard: protocol.ControlGuard{Instance: "daemon", Revision: 1}, Generation: 1, Context: protocol.Context{Project: "demo", Mode: "dev"}}, jobs: map[string]protocol.Job{}}
	for _, name := range []string{"key-1", "dial-1"} {
		view := protocol.ControlView{ControlRef: protocol.ControlRef{Device: DeviceID("device"), Control: name}, Targets: map[string]protocol.ControlTarget{}}
		for _, gesture := range []string{Press, Release, LongPress, Touch, LongTouch} {
			view.Targets[gesture] = protocol.ControlTarget{Action: gesture, Enabled: true, Safety: "safe"}
		}
		if name == "dial-1" {
			view.Targets[Rotate] = protocol.ControlTarget{Enabled: true, Parameter: &protocol.Parameter{Name: "level", Value: json.Number("50"), Unit: "%"}}
		}
		b.snapshot.Controls = append(b.snapshot.Controls, view)
	}
	e := NewEngine(b, []Device{{ID: "device", Type: 7}}, func() time.Time { return d.now })
	return e, b, d
}
func message(controller, event string) Message {
	m := Message{Event: event, Action: ActionID, Context: controller, Device: "device"}
	m.Payload.Controller = controller
	m.Payload.Coordinates = &Coordinates{}
	m.Payload.Settings = json.RawMessage(`{}`)
	return m
}
func input(t testing.TB, e *Engine, d *fakeDevice, m Message) {
	t.Helper()
	if err := e.Handle(context.Background(), m, d.now); err != nil {
		t.Fatal(err)
	}
}
func click(t testing.TB, e *Engine, d *fakeDevice, controller string, hold time.Duration) {
	t.Helper()
	down, up := "keyDown", "keyUp"
	if controller == "Encoder" {
		down, up = "dialDown", "dialUp"
	}
	input(t, e, d, message(controller, down))
	d.advance(hold)
	input(t, e, d, message(controller, up))
}

func TestNormalizeKeysDialsTouchAndHeldRotation(t *testing.T) {
	e, b, d := engineFixture(t)
	for _, controller := range []string{"Keypad", "Encoder"} {
		input(t, e, d, message(controller, "willAppear"))
		click(t, e, d, controller, 10*time.Millisecond)
		click(t, e, d, controller, HoldDuration)
	}
	for _, hold := range []bool{false, true} {
		m := message("Encoder", "touchTap")
		m.Payload.Hold = hold
		input(t, e, d, m)
	}
	want := []string{Press, Release, LongPress, Press, Release, LongPress, Touch, LongTouch}
	if len(b.requests) != len(want) {
		t.Fatal(b.requests)
	}
	for i, r := range b.requests {
		if r.Type != want[i] {
			t.Fatal(i, r.Type)
		}
	}
	input(t, e, d, message("Encoder", "dialDown"))
	ticks := int64(-7)
	m := message("Encoder", "dialRotate")
	m.Payload.Ticks = &ticks
	m.Payload.Pressed = true
	input(t, e, d, m)
	input(t, e, d, message("Encoder", "dialUp"))
	if len(b.requests) != 9 || b.requests[8].Type != Rotate || !strings.Contains(string(b.requests[8].Payload), `"delta":-7`) {
		t.Fatal(b.requests)
	}
	// Duplicate releases, wrong devices/controllers and unrelated action UUIDs do nothing.
	input(t, e, d, message("Encoder", "dialUp"))
	m = message("Keypad", "keyDown")
	m.Device = "other"
	input(t, e, d, m)
	m = message("Encoder", "keyDown")
	input(t, e, d, m)
	m.Action = "other"
	input(t, e, d, m)
	if len(b.requests) != 9 {
		t.Fatal("unexpected event")
	}
}

func TestRapidRotationPreservesEveryDeltaAndLatestFrame(t *testing.T) {
	e, b, d := engineFixture(t)
	input(t, e, d, message("Encoder", "willAppear"))
	d.capture(e)
	value := int64(50)
	total := int64(0)
	b.onControl = func(request protocol.EventRequest) (protocol.EventResponse, error) {
		var payload struct {
			Delta int64 `json:"delta"`
		}
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		total += payload.Delta
		value = max(0, min(100, value+payload.Delta))
		b.snapshot.Controls[1].Targets[Rotate].Parameter.Value = value
		return protocol.EventResponse{Matched: true}, nil
	}
	deltas := []int64{100, -100, 17, 0, -3, 8}
	for _, delta := range deltas {
		m := message("Encoder", "dialRotate")
		m.Payload.Ticks = &delta
		input(t, e, d, m)
		d.advance(time.Millisecond)
		d.capture(e)
	}
	if len(b.requests) != len(deltas) || total != 22 || value != 22 {
		t.Fatal(len(b.requests), total, value)
	}
	if len(d.frames) != 1 {
		t.Fatal("rendering not throttled", len(d.frames))
	}
	if delay := e.renderDelay(d.now); delay != RenderInterval-time.Duration(len(deltas))*time.Millisecond {
		t.Fatal("input burst postponed frame deadline", delay)
	}
	d.advance(RenderInterval)
	d.capture(e)
	if len(d.frames) != 2 || d.frames[1].Frame.Value != "22%" {
		t.Fatal(d.frames)
	}
	d.advance(RenderInterval)
	d.capture(e)
	if len(d.frames) != 2 {
		t.Fatal("unchanged frame re-rendered")
	}
}

func TestFailedPressSuppressesRelease(t *testing.T) {
	for _, controller := range []string{"Keypad", "Encoder"} {
		for _, failure := range []struct {
			name string
			err  error
		}{
			{"unavailable", &protocol.Error{Code: protocol.ProviderUnavailable}},
			{"confirmation", &protocol.Error{Code: protocol.ConfirmationRequired}},
			{"denied", &protocol.Error{Code: protocol.PermissionDenied}},
			{"busy", &protocol.Error{Code: protocol.Busy}},
			{"transport", errors.New("lost response")},
		} {
			t.Run(controller+"/"+failure.name, func(t *testing.T) {
				e, b, d := engineFixture(t)
				input(t, e, d, message(controller, "willAppear"))
				b.onControl = func(protocol.EventRequest) (protocol.EventResponse, error) {
					return protocol.EventResponse{}, failure.err
				}
				click(t, e, d, controller, time.Millisecond)
				if len(b.requests) != 1 || b.requests[0].Type != Press {
					t.Fatal("failed press emitted a companion release or retry", b.requests)
				}
			})
		}
	}
}

func TestContextChangeAfterPressSuppressesOldRelease(t *testing.T) {
	e, b, d := engineFixture(t)
	input(t, e, d, message("Keypad", "willAppear"))
	b.onControl = func(protocol.EventRequest) (protocol.EventResponse, error) {
		b.snapshot.Guard.Revision++
		b.snapshot.Context.Mode = "new-context"
		return protocol.EventResponse{Matched: true}, nil
	}
	click(t, e, d, "Keypad", time.Millisecond)
	if len(b.requests) != 1 || b.requests[0].Type != Press || e.snapshot.Context.Mode != "new-context" {
		t.Fatal("companion release crossed a context boundary", b.requests, e.snapshot)
	}
}

func TestConfirmationNeedsLaterHoldAndClearsOnContextOrOtherInput(t *testing.T) {
	e, b, d := engineFixture(t)
	input(t, e, d, message("Keypad", "willAppear"))
	input(t, e, d, message("Encoder", "willAppear"))
	confirmed := 0
	b.onControl = func(request protocol.EventRequest) (protocol.EventResponse, error) {
		var payload struct {
			Confirmation string `json:"confirmation"`
		}
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Confirmation != "" {
			confirmed++
			return protocol.EventResponse{Matched: true}, nil
		}
		return protocol.EventResponse{}, &protocol.Error{Code: protocol.ConfirmationRequired, Message: "confirm", Confirmation: &protocol.ControlConfirmation{Token: "token", Action: "dangerous-action", ExpiresAt: d.now.Add(5 * time.Second)}}
	}
	click(t, e, d, "Keypad", time.Millisecond)
	if len(b.requests) != 1 || confirmed != 0 || e.controls["Keypad"].pending == nil {
		t.Fatal("confirmation admission", b.requests)
	}
	d.capture(e)
	if e.controls["Keypad"].last.State != "confirm" {
		t.Fatal(d.frames)
	}
	click(t, e, d, "Keypad", time.Millisecond)
	if len(b.requests) != 1 {
		t.Fatal("short click confirmed")
	}
	click(t, e, d, "Keypad", HoldDuration)
	if len(b.requests) != 2 || confirmed != 1 || b.requests[1].Type != Press {
		t.Fatal(b.requests, confirmed)
	}
	click(t, e, d, "Keypad", time.Millisecond)
	input(t, e, d, message("Keypad", "keyDown"))
	b.snapshot.Guard.Revision++
	if err := e.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.advance(HoldDuration)
	input(t, e, d, message("Keypad", "keyUp"))
	if confirmed != 1 || e.controls["Keypad"].pending != nil {
		t.Fatal("stale context confirmed")
	}
	click(t, e, d, "Keypad", time.Millisecond)
	input(t, e, d, message("Encoder", "dialDown"))
	if e.controls["Keypad"].pending != nil {
		t.Fatal("other control retained confirmation")
	}
	click(t, e, d, "Keypad", time.Millisecond)
	d.advance(6 * time.Second)
	if err := e.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.controls["Keypad"].pending != nil {
		t.Fatal("expired challenge")
	}
}

func TestOfflineRestartSettingsAndStaleFeedback(t *testing.T) {
	e, b, d := engineFixture(t)
	input(t, e, d, message("Keypad", "willAppear"))
	b.jobs["job"] = protocol.Job{ID: "job", Action: "test", State: "running"}
	b.onControl = func(protocol.EventRequest) (protocol.EventResponse, error) {
		return protocol.EventResponse{JobID: "job", Matched: true}, nil
	}
	click(t, e, d, "Keypad", time.Millisecond)
	d.capture(e)
	if e.controls["Keypad"].last.State != "running" {
		t.Fatal(d.frames)
	}
	b.jobs["job"] = protocol.Job{ID: "job", Action: "test", State: "failed", Error: &protocol.Error{Code: protocol.ExecutionFailed}}
	if err := e.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.advance(RenderInterval)
	d.capture(e)
	if e.controls["Keypad"].last.State != "error" {
		t.Fatal(d.frames)
	}
	b.err = errors.New("offline")
	if e.Poll(context.Background()) == nil {
		t.Fatal("expected offline")
	}
	before := len(b.requests)
	click(t, e, d, "Keypad", time.Millisecond)
	if len(b.requests) != before {
		t.Fatal("offline input executed")
	}
	d.capture(e)
	queuedAt := d.now
	d.advance(time.Second)
	b.err = nil
	b.snapshot.Guard.Instance = "new-daemon"
	b.snapshot.Context.Mode = "review"
	if err := e.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Handle(context.Background(), message("Keypad", "keyDown"), queuedAt); err != nil {
		t.Fatal(err)
	}
	input(t, e, d, message("Keypad", "keyUp"))
	if len(b.requests) != before {
		t.Fatal("queued input replayed after restart")
	}
	d.capture(e)
	f := e.controls["Keypad"].last
	if f.State != "idle" || !strings.Contains(f.Context, "review") {
		t.Fatal(f)
	}
	m := message("Keypad", "didReceiveSettings")
	m.Payload.Settings = json.RawMessage(`{"control":"missing","label":"Custom"}`)
	input(t, e, d, m)
	d.advance(RenderInterval)
	d.capture(e)
	f = e.controls["Keypad"].last
	if f.State != "disabled" || f.Title != "Custom" {
		t.Fatal(f)
	}
	input(t, e, d, Message{Event: "deviceDidDisconnect", Device: "device"})
	click(t, e, d, "Keypad", time.Millisecond)
	if len(b.requests) != before {
		t.Fatal("disconnected input executed")
	}
	input(t, e, d, Message{Event: "deviceDidConnect", Device: "device", DeviceInfo: Device{Type: 7}})
	if len(e.controls) != 0 {
		t.Fatal("reconnect kept stale instances")
	}
	input(t, e, d, message("Keypad", "willAppear"))
	input(t, e, d, message("Keypad", "willDisappear"))
	if len(e.controls) != 0 {
		t.Fatal("hidden control retained")
	}
}

func TestInvalidControlsSnapshotsAndEscapedRendering(t *testing.T) {
	e, b, d := engineFixture(t)
	for _, settings := range []string{`{"control":"../bad"}`, `{"control":42}`, `{"label":"` + strings.Repeat("x", 65) + `"}`} {
		m := message("Keypad", "willAppear")
		m.Payload.Settings = json.RawMessage(settings)
		input(t, e, d, m)
		if e.controls["Keypad"].valid {
			t.Fatal(settings)
		}
	}
	m := message("Keypad", "willAppear")
	m.Payload.Coordinates.Row = 3
	input(t, e, d, m)
	if e.controls["Keypad"].valid {
		t.Fatal("unsupported geometry")
	}
	input(t, e, d, message("Keypad", "willAppear"))
	b.snapshot.Guard = protocol.ControlGuard{}
	if e.Poll(context.Background()) == nil || e.online {
		t.Fatal("invalid snapshot accepted")
	}
	commands := Commands(Render{Context: "x", Controller: "Keypad", Frame: Frame{Title: `<&"`, State: "error"}})
	image := commands[0].Payload.(map[string]any)["image"].(string)
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image, "data:image/svg+xml;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "&lt;&amp;") || strings.Contains(string(data), `<&"`) {
		t.Fatal(string(data))
	}
	if len(Commands(Render{Controller: "Encoder"})) != 1 {
		t.Fatal("dial frame")
	}
}

func BenchmarkRotationAndFrame(b *testing.B) {
	e, backend, d := engineFixture(b)
	input(b, e, d, message("Encoder", "willAppear"))
	delta := int64(1)
	m := message("Encoder", "dialRotate")
	m.Payload.Ticks = &delta
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		input(b, e, d, m)
		backend.requests = backend.requests[:0]
		d.advance(RenderInterval)
		_ = e.Render(d.now)
	}
}

func TestLostAdmissionIsNotRetriedAndRemainsVisibleAfterResync(t *testing.T) {
	e, b, d := engineFixture(t)
	input(t, e, d, message("Keypad", "willAppear"))
	b.onControl = func(protocol.EventRequest) (protocol.EventResponse, error) {
		return protocol.EventResponse{}, errors.New("lost response")
	}
	click(t, e, d, "Keypad", time.Millisecond)
	if len(b.requests) != 1 || e.online {
		t.Fatal("lost admission was retried")
	}
	if err := e.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.capture(e)
	if e.controls["Keypad"].last.State != "warning" || !strings.Contains(e.controls["Keypad"].last.Detail, "unknown") {
		t.Fatal(d.frames)
	}
}

func TestInstrumentFrameKeepsDesiredAndObservedAfterJobSuccess(t *testing.T) {
	e, b, d := engineFixture(t)
	p := b.snapshot.Controls[1].Targets[Rotate].Parameter
	p.Unit, p.Value = "Hz", float64(2000)
	p.Synchronization = &protocol.ParameterSynchronization{Desired: float64(2000), Observed: 1999.75, Status: "different"}
	input(t, e, d, message("Encoder", "willAppear"))
	for _, c := range e.controls {
		c.job = protocol.Job{ID: "old", State: "success", Action: "apply"}
		f := e.frame(c, d.now)
		if f.State != "warning" || !strings.Contains(f.Value, "Want 2000Hz") || !strings.Contains(f.Detail, "Read 1999.75 different") {
			t.Fatal(f)
		}
	}
}

func TestEvidenceControlUsesDisplayedRun(t *testing.T) {
	e, b, d := engineFixture(t)
	value := 1.25
	target := protocol.ControlTarget{Action: "baseline.bench", Enabled: true, Result: &protocol.ControlResult{Experiment: "bench", RunID: "saved-run", State: "success", Measurement: &protocol.Measurement{Value: &value, Unit: "ms", Status: "valid"}}}
	b.snapshot.Controls[0].Targets = map[string]protocol.ControlTarget{Press: target}
	input(t, e, d, message("Keypad", "willAppear"))
	frames := e.Render(d.now)
	if len(frames) != 1 || frames[0].Frame.Value != "1.25 ms" || frames[0].Frame.Detail != "success" {
		t.Fatal(frames)
	}
	click(t, e, d, "Keypad", 10*time.Millisecond)
	if len(b.requests) != 1 {
		t.Fatal(b.requests)
	}
	var payload protocol.ControlPayload
	if json.Unmarshal(b.requests[0].Payload, &payload) != nil || payload.RunID != "saved-run" || payload.Guard == nil {
		t.Fatal("missing exact displayed selection")
	}
}
