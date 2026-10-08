package streamdeck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

type Launch struct {
	Port          int
	UUID          string
	RegisterEvent string
	Devices       []Device
	Socket        string
	ManagedSocket bool
}
type received struct {
	message Message
	at      time.Time
}

// Run owns reconnect backoff. Each connection gets an empty engine; inputs and
// confirmations never cross a WebSocket session. The host may also restart us.
func Run(ctx context.Context, launch Launch, diagnostics io.Writer) error {
	if launch.Port < 1 || launch.Port > 65535 || launch.UUID == "" || len(launch.UUID) > 256 || launch.RegisterEvent != "registerPlugin" {
		return errors.New("invalid Stream Deck launch arguments")
	}
	if launch.Socket == "" {
		launch.Socket = DefaultSocket
	}
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second, ReadBufferSize: 4096, WriteBufferSize: 4096}
	backoff := 250 * time.Millisecond
	for ctx.Err() == nil {
		conn, response, err := dialer.DialContext(ctx, "ws://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(launch.Port)), nil)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err == nil {
			err = Session(ctx, conn, launch)
			_ = conn.Close()
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			_, _ = fmt.Fprintln(diagnostics, "decksd: app connection unavailable; reconnecting")
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff = min(5*time.Second, backoff*2)
	}
	return nil
}

// Session owns the reader and all WebSocket writes. Closing its connection
// interrupts ReadMessage; defer joins the reader before the next session starts.
func Session(parent context.Context, conn *websocket.Conn, launch Launch) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	backend, err := NewHTTPBackend(launch.Socket)
	if err != nil {
		return err
	}
	defer func() { backend.Close() }()
	engine := NewEngine(backend, launch.Devices, time.Now)
	socket := launch.Socket
	configured := false // Wait for persisted global settings before accepting input.
	conn.SetReadLimit(64 << 10)
	if err = conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(30 * time.Second)) })
	write := func(command Command) error {
		if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
			return err
		}
		return conn.WriteJSON(command)
	}
	if err := write(Command{Event: launch.RegisterEvent, UUID: launch.UUID}); err != nil {
		return err
	}
	if err := write(Command{Event: "getGlobalSettings", Context: launch.UUID}); err != nil {
		return err
	}
	messages := make(chan received, InputCapacity)
	failures := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		failures <- readApp(ctx, conn, messages)
	}()
	defer func() { cancel(); _ = conn.Close(); <-done }()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	poll := time.NewTicker(PollInterval)
	defer poll.Stop()
	render := time.NewTimer(RenderInterval)
	defer render.Stop()
	ping := time.NewTicker(10 * time.Second)
	defer ping.Stop()
	flush := func(now time.Time) error {
		for _, frame := range engine.Render(now) {
			for _, command := range Commands(frame) {
				if err := write(command); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for {
		if err := flush(time.Now()); err != nil {
			return err
		}
		render.Reset(engine.renderDelay(time.Now()))
		select {
		case <-ctx.Done():
			return nil
		case err := <-failures:
			engine.offline()
			_ = flush(time.Now().Add(RenderInterval))
			return err
		case incoming := <-messages:
			m := incoming.message
			if m.Event == "didReceiveGlobalSettings" {
				var settings struct {
					Socket string `json:"socket"`
				}
				if json.Unmarshal(m.Payload.Settings, &settings) != nil {
					configured = false
					engine.offline()
					continue
				}
				if settings.Socket == "" {
					settings.Socket = launch.Socket
				}
				if launch.ManagedSocket {
					var all map[string]json.RawMessage
					if json.Unmarshal(m.Payload.Settings, &all) != nil || all == nil {
						all = map[string]json.RawMessage{}
					}
					var installed string
					var managed bool
					_ = json.Unmarshal(all["socket"], &installed)
					_ = json.Unmarshal(all["patchbayManaged"], &managed)
					if installed != launch.Socket || !managed {
						all["socket"], _ = json.Marshal(launch.Socket)
						all["patchbayManaged"] = json.RawMessage(`true`)
						if err := write(Command{Event: "setGlobalSettings", Context: launch.UUID, Payload: all}); err != nil {
							return err
						}
					}
					settings.Socket = launch.Socket
				}
				if socket != settings.Socket {
					next, err := NewHTTPBackend(settings.Socket)
					if err != nil {
						configured = false
						engine.offline()
						continue
					}
					backend.Close()
					backend = next
					socket = settings.Socket
					engine.Reset(backend)
				}
				configured = true
				_ = engine.Poll(ctx)
				continue
			}
			if err := engine.Handle(ctx, m, incoming.at); err != nil {
				engine.offline()
				_ = flush(time.Now().Add(RenderInterval))
				return err
			}
			if !configured {
				engine.offline()
			}
		case <-poll.C:
			if configured {
				_ = engine.Poll(ctx)
			}
		case <-render.C:
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(2*time.Second)); err != nil {
				return err
			}
		}
	}
}

type appReader interface{ ReadMessage() (int, []byte, error) }

func readApp(ctx context.Context, reader appReader, messages chan<- received) error {
	for ctx.Err() == nil {
		kind, data, err := reader.ReadMessage()
		if err != nil {
			return errors.New("app connection closed")
		}
		var message Message
		if kind != websocket.TextMessage || json.Unmarshal(data, &message) != nil {
			return errors.New("invalid app message")
		}
		select {
		case messages <- received{message, time.Now()}:
		case <-ctx.Done():
			return nil
		default:
			return errors.New("input queue saturated")
		}
	}
	return nil
}
