package streamdeck

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestManagedSocketRepairsGlobalSettingsAndReachesDaemon(t *testing.T) {
	d := startWireDaemon(t)
	app := newFakeApp(t)
	launch := app.launch(t, d.socket)
	launch.ManagedSocket = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, launch, io.Discard) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("adapter shutdown timed out")
		}
	})
	conn := app.accept(t)
	nextCommand(t, conn, func(c Command) bool { return c.Event == "getGlobalSettings" })
	sendMessage(t, conn, message("Encoder", "willAppear"))
	sendMessage(t, conn, map[string]any{"event": "didReceiveGlobalSettings", "payload": map[string]any{"settings": map[string]any{"socket": "/tmp/old-patchbay.sock", "retained": 42, "large": json.RawMessage(`9007199254740993`), "nested": json.RawMessage(`{"value":9007199254740993}`)}}})
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var p map[string]json.RawMessage
	for {
		var update struct {
			Event   string
			Payload json.RawMessage
		}
		if err := conn.ReadJSON(&update); err != nil {
			t.Fatal(err)
		}
		if update.Event != "setGlobalSettings" {
			continue
		}
		if err := json.Unmarshal(update.Payload, &p); err != nil {
			t.Fatal(err)
		}
		break
	}
	if string(p["socket"]) != strconv.Quote(d.socket) || string(p["patchbayManaged"]) != "true" || string(p["retained"]) != "42" || string(p["large"]) != "9007199254740993" || string(p["nested"]) != `{"value":9007199254740993}` {
		t.Fatal(p)
	}
	feedbackValue(t, conn, "50%")
	delta := int64(1)
	m := message("Encoder", "dialRotate")
	m.Payload.Ticks = &delta
	sendMessage(t, conn, m)
	feedbackValue(t, conn, "51%")
}

func TestSetupSocketIsPrivateAndOptional(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "bin", "decksd")
	if socket, err := SetupSocket(executable); err != nil || socket != "" {
		t.Fatal(socket, err)
	}
	path := filepath.Join(root, "patchbay-setup.json")
	if err = os.WriteFile(path, []byte(`{"socket":"/tmp/managed.sock"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if socket, err := SetupSocket(executable); err != nil || socket != "/tmp/managed.sock" {
		t.Fatal(socket, err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = SetupSocket(executable); err == nil {
		t.Fatal("accepted public setup")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(executable, path); err != nil {
		t.Fatal(err)
	}
	if _, err = SetupSocket(executable); err == nil {
		t.Fatal("accepted linked setup")
	}
}
