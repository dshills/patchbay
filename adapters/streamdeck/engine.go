package streamdeck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"patchbay/pkg/protocol"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type pending struct {
	challenge protocol.ControlConfirmation
	gesture   string
}
type control struct {
	ref                       protocol.ControlRef
	device, controller, label string
	valid                     bool
	down                      time.Time
	rotated                   bool
	pending                   *pending
	jobID                     string
	job                       protocol.Job
	view                      protocol.ControlView
	notice                    string
	noticeUntil               time.Time
	uncertainUntil            time.Time
	last                      Frame
	lastSent                  time.Time
}

// Engine is owned by the app session's main loop. Methods are intentionally
// serialized; the WebSocket reader never accesses engine state.
type Engine struct {
	backend     Backend
	now         func() time.Time
	devices     map[string]int
	controls    map[string]*control
	snapshot    protocol.ControlSnapshot
	online      bool
	acceptAfter time.Time
}

func NewEngine(backend Backend, devices []Device, now func() time.Time) *Engine {
	if now == nil {
		now = time.Now
	}
	e := &Engine{backend: backend, now: now, devices: map[string]int{}, controls: map[string]*control{}}
	for _, d := range devices {
		e.devices[d.ID] = d.Type
	}
	return e
}

func DeviceID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "streamdeck." + hex.EncodeToString(sum[:])
}

func (e *Engine) Reset(backend Backend) {
	e.backend = backend
	e.offline()
	e.snapshot = protocol.ControlSnapshot{}
}
func (e *Engine) offline() {
	e.online = false
	e.acceptAfter = e.now()
	for _, c := range e.controls {
		c.pending = nil
		c.down = time.Time{}
		c.jobID = ""
		c.job = protocol.Job{}
		c.notice = ""
	}
}

func (e *Engine) add(m Message) error {
	if _, exists := e.controls[m.Context]; !exists && len(e.controls) >= MaxControls {
		return errors.New("too many visible controls")
	}
	c := &control{device: m.Device, controller: m.Payload.Controller}
	e.controls[m.Context] = c
	var settings Settings
	if len(m.Payload.Settings) > 0 && string(m.Payload.Settings) != "null" {
		if err := json.Unmarshal(m.Payload.Settings, &settings); err != nil {
			return nil
		}
	}
	position := m.Payload.Coordinates
	if e.devices[m.Device] != 7 || position == nil || position.Column < 0 || position.Column > 3 || position.Row < 0 {
		return nil
	}
	name := ""
	switch c.controller {
	case "Keypad":
		if position.Row > 1 {
			return nil
		}
		name = fmt.Sprintf("key-%d", position.Row*4+position.Column+1)
	case "Encoder":
		if position.Row != 0 {
			return nil
		}
		name = fmt.Sprintf("dial-%d", position.Column+1)
	default:
		return nil
	}
	if settings.Control != "" {
		name = settings.Control
	}
	if !identifier.MatchString(name) || len([]rune(settings.Label)) > 64 {
		return nil
	}
	c.ref = protocol.ControlRef{Device: DeviceID(m.Device), Control: name}
	c.label = clean(settings.Label, 64)
	c.valid = true
	return nil
}

func (e *Engine) Handle(ctx context.Context, m Message, at time.Time) error {
	switch m.Event {
	case "deviceDidDisconnect":
		delete(e.devices, m.Device)
		for _, c := range e.controls {
			if c.device == m.Device {
				c.valid = false
				c.pending = nil
				c.down = time.Time{}
				c.jobID = ""
			}
		}
		return nil
	case "deviceDidConnect":
		if _, exists := e.devices[m.Device]; !exists && len(e.devices) >= MaxControls {
			return errors.New("too many devices")
		}
		e.devices[m.Device] = m.DeviceInfo.Type
		// New willAppear messages must establish fresh action instances.
		for id, c := range e.controls {
			if c.device == m.Device {
				delete(e.controls, id)
			}
		}
		return nil
	case "systemDidWakeUp":
		e.offline()
		return nil
	}
	if m.Action != ActionID || m.Context == "" || len(m.Context) > 256 || len(m.Device) > 256 {
		return nil
	}
	if m.Event == "willDisappear" {
		delete(e.controls, m.Context)
		return nil
	}
	if m.Event == "willAppear" {
		if err := e.add(m); err != nil {
			return err
		}
		_ = e.Poll(ctx)
		return nil
	}
	c := e.controls[m.Context]
	if c == nil || c.device != m.Device {
		return nil
	}
	if m.Event == "didReceiveSettings" {
		// Settings events carry the current controller and coordinates.
		if err := e.add(m); err != nil {
			return err
		}
		_ = e.Poll(ctx)
		return nil
	}
	if !c.valid || !e.online || at.Before(e.acceptAfter) {
		return nil
	}
	if m.Payload.Controller != c.controller {
		return nil
	}
	validInput := m.Event == "keyDown" && c.controller == "Keypad" || m.Event == "keyUp" && c.controller == "Keypad" || c.controller == "Encoder" && (m.Event == "dialDown" || m.Event == "dialUp" || m.Event == "dialRotate" || m.Event == "touchTap")
	if !validInput {
		return nil
	}
	for _, other := range e.controls {
		if other != c {
			other.pending = nil
		}
	}
	if c.pending != nil && !e.now().Before(c.pending.challenge.ExpiresAt) {
		c.pending = nil
		c.down = time.Time{}
		c.notice = "Confirmation expired"
		c.noticeUntil = e.now().Add(3 * time.Second)
		return nil
	}
	switch m.Event {
	case "keyDown", "dialDown":
		if c.down.IsZero() {
			c.down = at
			c.rotated = false
		}
	case "keyUp", "dialUp":
		if c.down.IsZero() || at.Before(c.down) {
			return nil
		}
		held := at.Sub(c.down) >= HoldDuration
		c.down = time.Time{}
		if c.rotated {
			c.rotated = false
			return nil
		}
		if c.pending != nil {
			if held {
				e.confirm(ctx, c)
			}
			return nil
		}
		if held {
			e.emit(ctx, c, LongPress, nil, "")
		} else if e.emit(ctx, c, Press, nil, "") {
			e.emit(ctx, c, Release, nil, "")
		}
	case "dialRotate":
		c.pending = nil
		if !c.down.IsZero() || m.Payload.Pressed {
			c.rotated = true
		}
		if m.Payload.Ticks == nil {
			return nil
		}
		e.emit(ctx, c, Rotate, m.Payload.Ticks, "")
	case "touchTap":
		if c.pending != nil {
			if m.Payload.Hold {
				e.confirm(ctx, c)
			}
			return nil
		}
		gesture := Touch
		if m.Payload.Hold {
			gesture = LongTouch
		}
		e.emit(ctx, c, gesture, nil, "")
	}
	return nil
}

func (e *Engine) confirm(ctx context.Context, c *control) {
	p := c.pending
	c.pending = nil
	if p != nil && e.now().Before(p.challenge.ExpiresAt) {
		e.emit(ctx, c, p.gesture, nil, p.challenge.Token)
	}
}

// emit returns false when the release companion must be suppressed. No error
// response or lost admission is retried; confirmation needs another gesture.
func (e *Engine) emit(ctx context.Context, c *control, gesture string, delta *int64, token string) bool {
	if !e.online {
		return false
	}
	target, exists := c.view.Targets[gesture]
	if !exists {
		return true
	}
	if !target.Enabled {
		c.notice = "Disabled by policy"
		c.noticeUntil = e.now().Add(3 * time.Second)
		return false
	}
	payload := struct {
		protocol.ControlRef
		Delta        *int64                `json:"delta,omitempty"`
		Guard        protocol.ControlGuard `json:"guard"`
		Confirmation string                `json:"confirmation,omitempty"`
		RunID        string                `json:"run_id,omitempty"`
	}{c.ref, delta, e.snapshot.Guard, token, ""}
	if target.Result != nil {
		payload.RunID = target.Result.RunID
	}
	data, _ := json.Marshal(payload)
	response, err := e.backend.Control(ctx, protocol.EventRequest{Type: gesture, Source: "streamdeck", Payload: data})
	if err != nil {
		var known *protocol.Error
		if errors.As(err, &known) {
			if known.Code == protocol.ConfirmationRequired && known.Confirmation != nil && identifier.MatchString(known.Confirmation.Token) && e.now().Before(known.Confirmation.ExpiresAt) {
				c.pending = &pending{challenge: *known.Confirmation, gesture: gesture}
			} else {
				c.notice = string(known.Code)
				c.noticeUntil = e.now().Add(3 * time.Second)
			}
		} else {
			e.offline()
			c.uncertainUntil = e.now().Add(5 * time.Second)
		}
		return false
	}
	c.notice = ""
	c.pending = nil
	c.uncertainUntil = time.Time{}
	c.job = protocol.Job{}
	c.jobID = ""
	if response.JobID != "" {
		if !identifier.MatchString(response.JobID) {
			e.offline()
			return false
		}
		c.jobID = response.JobID
		c.job = protocol.Job{ID: response.JobID, State: "queued", Action: target.Action}
	}
	previous := e.snapshot.Guard
	_ = e.Poll(ctx)
	return e.online && e.snapshot.Guard == previous
}

func (e *Engine) Poll(ctx context.Context) error {
	if len(e.controls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	refs := make([]protocol.ControlRef, 0, len(e.controls))
	seen := map[protocol.ControlRef]bool{}
	for _, id := range slices.Sorted(maps.Keys(e.controls)) {
		c := e.controls[id]
		if c.valid && !seen[c.ref] {
			refs = append(refs, c.ref)
			seen[c.ref] = true
		}
	}
	snapshot, err := e.backend.Snapshot(ctx, refs)
	if err != nil {
		e.offline()
		return err
	}
	if snapshot.Guard.Instance == "" || snapshot.Guard.Revision == 0 || len(snapshot.Controls) != len(refs) {
		e.offline()
		return errors.New("invalid control snapshot")
	}
	views := map[protocol.ControlRef]protocol.ControlView{}
	for _, view := range snapshot.Controls {
		if !seen[view.ControlRef] {
			e.offline()
			return errors.New("unexpected control snapshot")
		}
		if _, exists := views[view.ControlRef]; exists {
			e.offline()
			return errors.New("duplicate control snapshot")
		}
		views[view.ControlRef] = view
	}
	guardChanged := snapshot.Guard != e.snapshot.Guard
	changed := !e.online || guardChanged
	if changed {
		e.acceptAfter = e.now()
	}
	e.online = true
	e.snapshot = snapshot
	for _, id := range slices.Sorted(maps.Keys(e.controls)) {
		c := e.controls[id]
		if !c.valid {
			continue
		}
		c.view = views[c.ref]
		if changed {
			c.pending = nil
			c.down = time.Time{}
			c.jobID = ""
			c.job = protocol.Job{}
			c.notice = ""
			if guardChanged {
				c.uncertainUntil = time.Time{}
			}
		}
		if c.pending != nil && !e.now().Before(c.pending.challenge.ExpiresAt) {
			c.pending = nil
			c.down = time.Time{}
			c.notice = "Confirmation expired"
			c.noticeUntil = e.now().Add(3 * time.Second)
		}
		if c.jobID != "" {
			j, err := e.backend.Job(ctx, c.jobID)
			if err != nil || j.ID != c.jobID {
				c.jobID = ""
				c.job = protocol.Job{}
				c.notice = "Job result unavailable"
				c.noticeUntil = e.now().Add(3 * time.Second)
				continue
			}
			switch j.State {
			case "queued", "running", "success", "failed", "cancelled":
				c.job = j
			default:
				c.jobID = ""
				c.notice = "Invalid job state"
				c.noticeUntil = e.now().Add(3 * time.Second)
			}
			if j.State == "success" || j.State == "failed" || j.State == "cancelled" {
				c.jobID = ""
			}
		}
	}
	return nil
}

func (e *Engine) frame(c *control, now time.Time) Frame {
	f := Frame{Title: c.label, State: "idle", Context: clean(strings.TrimSpace(e.snapshot.Context.Project+" / "+e.snapshot.Context.Mode), 28)}
	instrument := false
	if f.Title == "" {
		f.Title = c.ref.Control
	}
	if !c.valid {
		f.State = "disabled"
		f.Detail = "Unsupported or invalid control"
		return f
	}
	if !e.online {
		f.State = "disconnected"
		f.Detail = "Deckd unavailable"
		if now.Before(c.uncertainUntil) {
			f.Detail = "Outcome unknown; inspect jobs"
		}
		return f
	}
	if len(c.view.Targets) == 0 {
		f.State = "disabled"
		f.Detail = "No binding"
		return f
	}
	for _, gesture := range []string{Rotate, Press, LongPress, Touch, LongTouch, Release} {
		t, ok := c.view.Targets[gesture]
		if !ok {
			continue
		}
		if c.label == "" {
			f.Title = t.Action
		}
		if t.Parameter != nil {
			if c.label == "" {
				f.Title = t.Parameter.Name
			}
			f.Value = clean(fmt.Sprint(t.Parameter.Value)+t.Parameter.Unit, 28)
			if s := t.Parameter.Synchronization; s != nil {
				instrument = true
				f.Value = "Want " + f.Value
				f.Detail = clean("Read "+fmt.Sprint(s.Observed)+" "+s.Status, 40)
				if s.Status != "matched" {
					f.State = "warning"
				}
			}
		}
		if t.Result != nil {
			f.Detail = t.Result.State
			if t.Result.Baseline {
				f.Detail += " · baseline"
			}
			f.Value = clean(t.Result.RunID, 8)
			if m := t.Result.Measurement; m != nil && m.Value != nil && m.Status == "valid" {
				f.Value = fmt.Sprintf("%.4g %s", *m.Value, m.Unit)
			}
		}
		if !t.Enabled {
			f.State = "disabled"
			f.Detail = "Disabled by policy"
		}
		break
	}
	if c.job.ID != "" && (!instrument || c.job.State != "success") {
		f.State = c.job.State
		if f.State == "queued" {
			f.State = "running"
		}
		if f.State == "failed" {
			f.State = "error"
		}
		if f.State == "cancelled" {
			f.State = "warning"
		}
		f.Detail = c.job.Action + ": " + c.job.State
		if c.job.Error != nil {
			f.Detail = string(c.job.Error.Code)
		}
		if c.job.Result != nil && c.job.Result.Display != nil {
			d := c.job.Result.Display
			if d.Progress != nil {
				f.Detail = fmt.Sprintf("%d%%", *d.Progress)
			}
		}
	}
	if c.notice != "" && now.Before(c.noticeUntil) {
		f.State = "error"
		f.Detail = c.notice
	}
	if now.Before(c.uncertainUntil) {
		f.State = "warning"
		f.Detail = "Outcome unknown; inspect jobs"
	}
	if c.pending != nil && now.Before(c.pending.challenge.ExpiresAt) {
		f.State = "confirm"
		f.Title = c.pending.challenge.Action
		f.Detail = "Hold to confirm"
	}
	f.Title = clean(f.Title, 28)
	f.Detail = clean(f.Detail, 40)
	return f
}

func (e *Engine) Render(now time.Time) []Render {
	frames := []Render{}
	for _, id := range slices.Sorted(maps.Keys(e.controls)) {
		c := e.controls[id]
		if !c.lastSent.IsZero() && now.Sub(c.lastSent) < RenderInterval {
			continue
		}
		f := e.frame(c, now)
		if !c.lastSent.IsZero() && f == c.last {
			continue
		}
		frames = append(frames, Render{Context: id, Controller: c.controller, Frame: f})
		c.last = f
		c.lastSent = now
	}
	return frames
}

// Schedule the earliest changed frame's deadline, rather than rounding it up
// to another periodic tick. Unchanged controls cause no display writes.
func (e *Engine) renderDelay(now time.Time) time.Duration {
	delay := time.Hour
	for _, c := range e.controls {
		if c.lastSent.IsZero() {
			return 0
		}
		// Defer frame formatting until this control is eligible to render.
		// The deadline stays fixed during an input burst.
		if remaining := c.lastSent.Add(RenderInterval).Sub(now); remaining > 0 {
			delay = min(delay, remaining)
			continue
		}
		if e.frame(c, now) != c.last {
			return 0
		}
	}
	return delay
}

func clean(text string, limit int) string {
	runes := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text))
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return string(runes)
}
