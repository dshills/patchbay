// Package streamdeck translates Elgato input and presentation. It owns no actions.
package streamdeck

import (
	"context"
	"encoding/json"
	"time"

	"patchbay/pkg/protocol"
)

const (
	PluginID       = "local.patchbay.deckd"
	ActionID       = PluginID + ".control"
	DefaultSocket  = "~/.deckd/deckd.sock"
	MaxControls    = 64
	InputCapacity  = 256
	PollInterval   = 250 * time.Millisecond
	RenderInterval = 50 * time.Millisecond
	HoldDuration   = 700 * time.Millisecond
	Press          = "control.pressed"
	Release        = "control.released"
	Rotate         = "control.rotated"
	LongPress      = "control.long_pressed"
	Touch          = "control.touched"
	LongTouch      = "control.long_touched"
)

type Backend interface {
	Snapshot(context.Context, []protocol.ControlRef) (protocol.ControlSnapshot, error)
	Control(context.Context, protocol.EventRequest) (protocol.EventResponse, error)
	Job(context.Context, string) (protocol.Job, error)
}

type Coordinates struct {
	Column int `json:"column"`
	Row    int `json:"row"`
}
type Device struct {
	ID   string `json:"id"`
	Type int    `json:"type"`
}
type Message struct {
	Event      string `json:"event"`
	Action     string `json:"action"`
	Context    string `json:"context"`
	Device     string `json:"device"`
	DeviceInfo Device `json:"deviceInfo"`
	Payload    struct {
		Controller  string          `json:"controller"`
		Coordinates *Coordinates    `json:"coordinates"`
		Settings    json.RawMessage `json:"settings"`
		Ticks       *int64          `json:"ticks"`
		Pressed     bool            `json:"pressed"`
		Hold        bool            `json:"hold"`
	} `json:"payload"`
}

type Settings struct {
	Control string `json:"control"`
	Label   string `json:"label"`
}
type Frame struct{ Title, Value, Context, State, Detail string }
type Render struct {
	Context, Controller string
	Frame               Frame
}

type Capabilities struct {
	Device               string   `json:"device"`
	Keys                 int      `json:"keys"`
	Dials                int      `json:"dials"`
	Gestures             []string `json:"gestures"`
	Swipe                bool     `json:"swipe"`
	PhysicalVerification string   `json:"physical_verification"`
}

func SupportedCapabilities() Capabilities {
	return Capabilities{Device: "Stream Deck+", Keys: 8, Dials: 4, Gestures: []string{Press, Release, Rotate, LongPress, Touch, LongTouch}, PhysicalVerification: "See specs/reviews/PHASE3.md"}
}
