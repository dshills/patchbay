package streamdeck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"patchbay/internal/api"
	runtimecore "patchbay/internal/runtime"
	"patchbay/pkg/protocol"
)

const wireConfig = `version: 1
server: {socket: %q}
state: {path: %q}
context: {defaults: {mode: demo}}
actions:
  echo: {type: exec, safety: safe, command: /bin/echo, args: [success]}
  confirm: {type: exec, safety: confirm, command: /bin/echo, args: [confirmed]}
parameters:
  level: {type: integer, value: 50, min: 0, max: 100, persistent: true, unit: '%%'}
bindings:
  - control: dial-1
    rotate: {parameter: level}
    press: {action: confirm}
    touch: {action: echo}
    long_touch: {action: echo}
  - control: key-1
    press: {action: echo}
    long_press: {action: echo}
`

type wireDaemon struct {
	path, socket string
	runtime      *runtimecore.Runtime
	server       *http.Server
	done         chan error
}

func TestHTTPBackendExpandsHomeSocketPath(t *testing.T) {
	d := startWireDaemon(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(home, d.socket)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewHTTPBackend("~/" + relative)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	view, err := backend.Snapshot(context.Background(), []protocol.ControlRef{{Control: "dial-1"}})
	if err != nil || view.Guard.Instance == "" {
		t.Fatal("home-relative socket did not reach the daemon", view, err)
	}
}

func startWireDaemon(t testing.TB) *wireDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pb-sd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	d := &wireDaemon{path: filepath.Join(dir, "config.yaml"), socket: filepath.Join(dir, "sock")}
	if err := os.WriteFile(d.path, []byte(fmt.Sprintf(wireConfig, d.socket, filepath.Join(dir, "state.json"))), 0600); err != nil {
		t.Fatal(err)
	}
	d.start(t)
	t.Cleanup(func() { d.stop(t) })
	return d
}
func (d *wireDaemon) start(t testing.TB) {
	t.Helper()
	var err error
	d.runtime, err = runtimecore.New(d.path, runtimecore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := api.Listen(d.socket)
	if err != nil {
		t.Fatal(err)
	}
	d.server = &http.Server{Handler: api.NewHandler(d.runtime), ReadHeaderTimeout: time.Second}
	d.done = make(chan error, 1)
	go func() { d.done <- d.server.Serve(listener) }()
}
func (d *wireDaemon) stop(t testing.TB) {
	t.Helper()
	if d.runtime == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.runtime.Close(ctx); err != nil {
		t.Error(err)
	}
	_ = d.server.Close()
	<-d.done
	d.runtime = nil
}

type fakeApp struct {
	server      *httptest.Server
	connections chan *websocket.Conn
}

func newFakeApp(t testing.TB) *fakeApp {
	t.Helper()
	a := &fakeApp{connections: make(chan *websocket.Conn, 4)}
	a.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		a.connections <- conn
	}))
	t.Cleanup(func() {
		a.server.Close()
		for len(a.connections) > 0 {
			_ = (<-a.connections).Close()
		}
	})
	return a
}
func (a *fakeApp) launch(t testing.TB, socket string) Launch {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(a.server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	return Launch{Port: n, UUID: "test-plugin", RegisterEvent: "registerPlugin", Socket: socket, Devices: []Device{{ID: "device", Type: 7}}}
}
func (a *fakeApp) accept(t testing.TB) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-a.connections:
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	case <-time.After(5 * time.Second):
		t.Fatal("adapter did not connect")
		return nil
	}
}
func sendMessage(t testing.TB, conn *websocket.Conn, value any) {
	t.Helper()
	if err := conn.WriteJSON(value); err != nil {
		t.Fatal(err)
	}
}
func nextCommand(t testing.TB, conn *websocket.Conn, predicate func(Command) bool) Command {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var c Command
		if err := conn.ReadJSON(&c); err != nil {
			t.Fatal(err)
		}
		if predicate(c) {
			return c
		}
	}
}
func feedbackValue(t testing.TB, conn *websocket.Conn, want string) {
	t.Helper()
	nextCommand(t, conn, func(c Command) bool {
		p, ok := c.Payload.(map[string]any)
		return c.Event == "setFeedback" && ok && p["value"] == want
	})
}
func feedbackStatus(t testing.TB, conn *websocket.Conn, contains string) {
	t.Helper()
	nextCommand(t, conn, func(c Command) bool {
		p, ok := c.Payload.(map[string]any)
		if c.Event != "setFeedback" || !ok {
			return false
		}
		s, ok := p["status"].(map[string]any)
		if !ok {
			return false
		}
		value, _ := s["value"].(string)
		return strings.Contains(value, contains)
	})
}

func TestFakeAppWithRealDaemonAndRestart(t *testing.T) {
	d := startWireDaemon(t)
	app := newFakeApp(t)
	launch := app.launch(t, d.socket)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	if binary := os.Getenv("PATCHBAY_DECKSD_BINARY"); binary != "" {
		info, _ := json.Marshal(map[string]any{"devices": launch.Devices})
		cmd := exec.CommandContext(ctx, binary, "-port", strconv.Itoa(launch.Port), "-pluginUUID", launch.UUID, "-registerEvent", launch.RegisterEvent, "-info", string(info), "-socket", d.socket)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { done <- cmd.Wait() }()
	} else {
		go func() { done <- Run(ctx, launch, io.Discard) }()
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("adapter shutdown did not finish")
		}
	})
	conn := app.accept(t)
	registration := nextCommand(t, conn, func(c Command) bool { return c.Event == "registerPlugin" })
	if registration.UUID != launch.UUID {
		t.Fatal(registration)
	}
	nextCommand(t, conn, func(c Command) bool { return c.Event == "getGlobalSettings" })
	// Persisted socket settings must arrive before any control can execute.
	sendMessage(t, conn, message("Encoder", "willAppear"))
	feedbackStatus(t, conn, "disconnected")
	sendMessage(t, conn, message("Encoder", "dialDown"))
	sendMessage(t, conn, message("Encoder", "dialUp"))
	sendMessage(t, conn, map[string]any{"event": "didReceiveGlobalSettings", "payload": map[string]any{"settings": map[string]string{"socket": d.socket}}})
	feedbackValue(t, conn, "50%")
	for _, ticks := range []int64{3, -2, 7} {
		m := message("Encoder", "dialRotate")
		m.Payload.Ticks = &ticks
		sendMessage(t, conn, m)
	}
	feedbackValue(t, conn, "58%")
	latencies := make([]time.Duration, 0, 20)
	for i := range 20 {
		delta := int64(1)
		value := "59%"
		if i%2 == 1 {
			delta = -1
			value = "58%"
		}
		m := message("Encoder", "dialRotate")
		m.Payload.Ticks = &delta
		started := time.Now()
		sendMessage(t, conn, m)
		feedbackValue(t, conn, value)
		latencies = append(latencies, time.Since(started))
	}
	slices.Sort(latencies)
	t.Logf("20 app-to-render samples: median=%s p95=%s max=%s", latencies[10], latencies[18], latencies[19])
	sendMessage(t, conn, map[string]any{"event": "didReceiveGlobalSettings", "payload": map[string]any{"settings": map[string]string{"socket": "~named-user/invalid"}}})
	feedbackStatus(t, conn, "disconnected")
	ignored := int64(20)
	ignoredInput := message("Encoder", "dialRotate")
	ignoredInput.Payload.Ticks = &ignored
	sendMessage(t, conn, ignoredInput)
	sendMessage(t, conn, map[string]any{"event": "didReceiveGlobalSettings", "payload": map[string]any{"settings": map[string]string{"socket": d.socket}}})
	feedbackValue(t, conn, "58%")
	// A short press must prompt before admitting the protected action.
	sendMessage(t, conn, message("Encoder", "dialDown"))
	sendMessage(t, conn, message("Encoder", "dialUp"))
	feedbackStatus(t, conn, "Hold to confirm")
	if len(d.runtime.Jobs().List()) != 0 {
		t.Fatal("confirmation executed without a hold")
	}
	sendMessage(t, conn, message("Encoder", "dialDown"))
	// This delay simulates the physical duration measured by the socket reader.
	time.Sleep(HoldDuration + 50*time.Millisecond)
	sendMessage(t, conn, message("Encoder", "dialUp"))
	feedbackStatus(t, conn, "success")
	if len(d.runtime.Jobs().List()) != 1 {
		t.Fatal("confirmation did not execute exactly once")
	}
	mode := "review"
	if _, err := d.runtime.PatchContext(context.Background(), protocol.ContextPatch{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	nextCommand(t, conn, func(c Command) bool {
		p, ok := c.Payload.(map[string]any)
		return c.Event == "setFeedback" && ok && strings.Contains(fmt.Sprint(p["context"]), "review")
	})
	d.stop(t)
	feedbackStatus(t, conn, "disconnected")
	ticks := int64(10)
	m := message("Encoder", "dialRotate")
	m.Payload.Ticks = &ticks
	sendMessage(t, conn, m)
	d.start(t)
	feedbackValue(t, conn, "58%")
	ticks = 1
	sendMessage(t, conn, m)
	feedbackValue(t, conn, "59%")
	// Re-registering the WebSocket starts with no cached controls/challenges.
	_ = conn.Close()
	conn = app.accept(t)
	nextCommand(t, conn, func(c Command) bool { return c.Event == "getGlobalSettings" })
	sendMessage(t, conn, map[string]any{"event": "didReceiveGlobalSettings", "payload": map[string]any{"settings": map[string]string{"socket": d.socket}}})
	sendMessage(t, conn, message("Encoder", "willAppear"))
	feedbackValue(t, conn, "59%")
}

func TestSessionRejectsMalformedAppMessageAndRunArguments(t *testing.T) {
	app := newFakeApp(t)
	launch := app.launch(t, filepath.Join(t.TempDir(), "absent"))
	client, _, err := websocket.DefaultDialer.Dial(strings.Replace(app.server.URL, "http://", "ws://", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	server := app.accept(t)
	done := make(chan error, 1)
	go func() { done <- Session(context.Background(), client, launch) }()
	nextCommand(t, server, func(c Command) bool { return c.Event == "getGlobalSettings" })
	if err := server.WriteMessage(websocket.TextMessage, []byte(`{"payload":{"ticks":1.5}}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("malformed input accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reader leaked")
	}
	for _, value := range []Launch{{}, {Port: 42, UUID: "x", RegisterEvent: "registerPropertyInspector"}} {
		if Run(context.Background(), value, io.Discard) == nil {
			t.Fatal("bad launch accepted")
		}
	}
}

type replayReader struct{ count int }

func (r *replayReader) ReadMessage() (int, []byte, error) {
	r.count++
	return websocket.TextMessage, []byte(`{"event":"dialRotate","payload":{"ticks":1}}`), nil
}
func TestInputQueueSaturationFailsClosed(t *testing.T) {
	reader := &replayReader{}
	queue := make(chan received, InputCapacity)
	err := readApp(context.Background(), reader, queue)
	if err == nil || err.Error() != "input queue saturated" || len(queue) != InputCapacity || reader.count != InputCapacity+1 {
		t.Fatal(err, len(queue), reader.count)
	}
	for len(queue) > 0 {
		m := <-queue
		if m.message.Payload.Ticks == nil || *m.message.Payload.Ticks != 1 || m.at.IsZero() {
			t.Fatal(m)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if readApp(ctx, reader, queue) != nil || reader.count != InputCapacity+1 {
		t.Fatal("cancelled reader consumed input")
	}
}
