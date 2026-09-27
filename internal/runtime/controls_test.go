package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"patchbay/internal/event"
	"patchbay/pkg/protocol"
)

func snapshotControls(t *testing.T, r *Runtime, refs ...protocol.ControlRef) protocol.ControlSnapshot {
	t.Helper()
	value, err := r.ControlSnapshot(context.Background(), protocol.ControlSnapshotRequest{Controls: refs})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func controlRequest(t *testing.T, kind string, payload any) protocol.EventRequest {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.EventRequest{Type: kind, Source: "adapter", Payload: data}
}
func challengeToken(t *testing.T, r *Runtime, guard protocol.ControlGuard) string {
	t.Helper()
	_, err := r.Control(context.Background(), controlRequest(t, event.ControlPressed, protocol.ControlPayload{Device: "d", Control: "key", Guard: &guard}))
	var known *protocol.Error
	if !errors.As(err, &known) || known.Code != protocol.ConfirmationRequired || known.Confirmation == nil || known.Confirmation.Action != "confirm" {
		t.Fatal(err)
	}
	return known.Confirmation.Token
}

func TestControlSnapshotAndExtendedGestures(t *testing.T) {
	text := fixture + `  - control: touch
    long_press: {action: echo}
    touch: {action: echo}
    long_touch: {action: echo}
  - control: blocked
    press: {action: danger}
`
	r, _ := setup(t, text, &fakeRunner{})
	s := snapshotControls(t, r, protocol.ControlRef{Device: "d", Control: "dial"}, protocol.ControlRef{Device: "d", Control: "key"}, protocol.ControlRef{Control: "blocked"})
	if s.Guard.Instance == "" || s.Guard.Revision != 1 || s.Context.Project != "p" || s.Controls[0].Targets[event.ControlRotated].Parameter.Value != int64(1) || s.Controls[1].Targets[event.ControlPressed].Safety != "confirm" || s.Controls[2].Targets[event.ControlPressed].Enabled {
		t.Fatal(s)
	}
	s.Context.Values["tag"] = "mutated"
	if r.Context().Values["tag"] != "initial" {
		t.Fatal("snapshot shared context")
	}
	for _, kind := range []string{event.ControlLongPressed, event.ControlTouched, event.ControlLongTouched} {
		result, err := r.Control(context.Background(), controlRequest(t, kind, protocol.ControlPayload{Control: "touch", Guard: &s.Guard}))
		if err != nil || result.JobID == "" {
			t.Fatal(result, err)
		}
		if _, err := r.Jobs().Wait(context.Background(), result.JobID); err != nil {
			t.Fatal(err)
		}
	}
	for _, refs := range [][]protocol.ControlRef{{{Control: "../bad"}}, {{Control: "key"}, {Control: "key"}}, make([]protocol.ControlRef, 65)} {
		_, err := r.ControlSnapshot(context.Background(), protocol.ControlSnapshotRequest{Controls: refs})
		wantCode(t, err, protocol.InvalidRequest)
	}
}

func TestControlConfirmationSingleUseAndBoundToInvocation(t *testing.T) {
	r, _ := setup(t, fixture, &fakeRunner{})
	guard := snapshotControls(t, r).Guard
	token := challengeToken(t, r, guard)
	if len(r.Jobs().List()) != 0 {
		t.Fatal("challenge executed an action")
	}
	request := controlRequest(t, event.ControlPressed, protocol.ControlPayload{Device: "d", Control: "key", Guard: &guard, Confirmation: token})
	result, err := r.Control(context.Background(), request)
	if err != nil || result.JobID == "" {
		t.Fatal(result, err)
	}
	if _, err := r.Jobs().Wait(context.Background(), result.JobID); err != nil {
		t.Fatal(err)
	}
	_, err = r.Control(context.Background(), request)
	wantCode(t, err, protocol.InvalidRequest)
	for _, change := range []string{"source", "device", "control", "gesture", "expired", "replacement", "guard", "legacy-evidence"} {
		t.Run(change, func(t *testing.T) {
			token := challengeToken(t, r, guard)
			payload := protocol.ControlPayload{Device: "d", Control: "key", Guard: &guard, Confirmation: token}
			kind := event.ControlPressed
			switch change {
			case "source":
			case "device":
				payload.Device = "other"
			case "control":
				payload.Control = "other"
			case "gesture":
				kind = event.ControlReleased
			case "expired":
				r.mu.Lock()
				ticket := r.confirmations[token]
				ticket.expires = time.Now().Add(-time.Second)
				r.confirmations[token] = ticket
				r.mu.Unlock()
			case "replacement":
				_ = challengeToken(t, r, guard)
			case "guard":
				payload.Guard = nil
			case "legacy-evidence":
				payload.Confirmed = true
			}
			request := controlRequest(t, kind, payload)
			if change == "source" {
				request.Source = "other"
			}
			_, err := r.Control(context.Background(), request)
			wantCode(t, err, protocol.InvalidRequest)
		})
	}
	if len(r.Jobs().List()) != 1 {
		t.Fatal("invalid confirmation executed work")
	}
}

func TestControlGuardRejectsContextABAAndReload(t *testing.T) {
	r, path := setup(t, fixture, &fakeRunner{})
	guard := snapshotControls(t, r).Guard
	token := challengeToken(t, r, guard)
	for _, mode := range []string{"other", "dev"} {
		if _, err := r.PatchContext(context.Background(), protocol.ContextPatch{Mode: &mode}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.Control(context.Background(), controlRequest(t, event.ControlPressed, protocol.ControlPayload{Device: "d", Control: "key", Guard: &guard, Confirmation: token}))
	wantCode(t, err, protocol.InvalidRequest)
	guard = snapshotControls(t, r).Guard
	token = challengeToken(t, r, guard)
	writeConfig(t, path, strings.Replace(fixture, "args: [confirmed]", "args: [changed]", 1))
	if _, err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = r.Control(context.Background(), controlRequest(t, event.ControlPressed, protocol.ControlPayload{Device: "d", Control: "key", Guard: &guard, Confirmation: token}))
	wantCode(t, err, protocol.InvalidRequest)
	guard = snapshotControls(t, r).Guard
	guard.Instance = "previous-daemon"
	_, err = r.Control(context.Background(), controlRequest(t, event.ControlRotated, map[string]any{"control": "dial", "delta": 1, "guard": guard}))
	wantCode(t, err, protocol.InvalidRequest)
	p, _ := r.Parameter("count")
	if p.Value != int64(1) || len(r.Jobs().List()) != 0 {
		t.Fatal("stale control changed state")
	}
}

func TestControlConfirmationCapacityAndNoTokenInEvents(t *testing.T) {
	r, _ := setup(t, fixture, &fakeRunner{})
	guard := snapshotControls(t, r).Guard
	r.mu.Lock()
	r.confirmations = map[string]controlConfirmation{}
	for i := range maxConfirmations {
		r.confirmations[string(rune(i))] = controlConfirmation{expires: time.Now().Add(time.Hour)}
	}
	r.mu.Unlock()
	_, err := r.Control(context.Background(), controlRequest(t, event.ControlPressed, protocol.ControlPayload{Device: "d", Control: "key", Guard: &guard}))
	wantCode(t, err, protocol.Busy)
	r.mu.Lock()
	r.confirmations = nil
	r.mu.Unlock()
	sub := r.Events().Subscribe(context.Background())
	defer sub.Close()
	token := challengeToken(t, r, guard)
	response, err := r.Control(context.Background(), controlRequest(t, event.ControlPressed, protocol.ControlPayload{Device: "d", Control: "key", Guard: &guard, Confirmation: token}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Jobs().Wait(context.Background(), response.JobID); err != nil {
		t.Fatal(err)
	}
	for len(sub.C) > 0 {
		entry := <-sub.C
		if strings.Contains(string(entry.Payload), token) {
			t.Fatal("token leaked to event bus")
		}
	}
}
