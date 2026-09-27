// Package plugintest supplies deliberately broken subprocesses for host tests.
package plugintest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	wire "patchbay/pkg/plugin"
)

// Child is called by a TestPluginChild entry point in the test executable.
func Child() {
	mode := os.Getenv("PATCHBAY_PLUGIN_FIXTURE")
	if mode == "" {
		return
	}
	if path := os.Getenv("PATCHBAY_PLUGIN_PID"); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600)
	}
	switch mode {
	case "exit":
		os.Exit(3)
	case "hang", "hold":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "malformed":
		fmt.Println("{broken")
		time.Sleep(time.Minute)
	case "oversize":
		fmt.Print(strings.Repeat("x", wire.MaxFrameBytes+2))
		time.Sleep(time.Minute)
	case "partial":
		fmt.Print(`{"version":1`)
		time.Sleep(time.Minute)
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("secret", 20000))
		time.Sleep(time.Minute)
	}
	reader := wire.NewReader(os.Stdin)
	send := func(f wire.Frame) { _ = json.NewEncoder(os.Stdout).Encode(f) }
	reply := func(f wire.Frame, kind string, payload any) { out, _ := wire.NewFrame(f.ID, kind, payload); send(out) }
	var active string
	for {
		f, err := reader.Read()
		if err != nil {
			os.Exit(4)
		}
		if path := os.Getenv("PATCHBAY_PLUGIN_TRACE"); path != "" {
			file, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if e == nil {
				_, _ = fmt.Fprintln(file, f.Type)
				_ = file.Close()
			}
		}
		switch f.Type {
		case "hello":
			safety := "safe"
			if mode == "dangerous" {
				safety = "dangerous"
			}
			hello := wire.HelloResponse{SelectedVersion: 1, Operations: []wire.Operation{{Name: "echo", Safety: safety}, {Name: "wait", Safety: "confirm"}}}
			if mode == "extra" {
				hello.Operations = append(hello.Operations, wire.Operation{Name: "exec", Safety: "safe"})
			}
			if mode == "missing" {
				hello.Operations = hello.Operations[:1]
			}
			if mode == "version" {
				hello.SelectedVersion = 2
			}
			out, _ := wire.NewFrame(f.ID, "hello", hello)
			switch mode {
			case "wrong_id":
				out.ID = "9"
			case "future":
				out.Version = 2
			case "unknown":
				out.Type = "context.changed"
			case "duplicate_key":
				out.Payload = json.RawMessage(`{"selected_version":1,"selected_version":1,"operations":[]}`)
			}
			send(out)
			if mode == "duplicate" {
				send(out)
			}
		case "health":
			available := mode != "unhealthy"
			if mode == "health_missing" {
				reply(f, "health", struct{}{})
			} else {
				reply(f, "health", wire.Health{Available: &available})
			}
			if mode == "no_read" {
				time.Sleep(time.Minute)
			}
		case "execute":
			var request wire.Execute
			if wire.Decode(f.Payload, &request) != nil {
				os.Exit(5)
			}
			if request.Operation == "wait" {
				active = f.ID
				if mode == "descendant" {
					executable, _ := os.Executable()
					child := exec.Command(executable, "-test.run=^TestPluginChild$")
					child.Env = []string{"PATCHBAY_PLUGIN_FIXTURE=hold", "PATCHBAY_PLUGIN_PID=" + os.Getenv("PATCHBAY_PLUGIN_DESCENDANT")}
					child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
					if child.Start() != nil {
						os.Exit(6)
					}
				}
				continue
			}
			text, _ := request.Args["text"].(string)
			if mode == "context" {
				data, _ := json.Marshal(request.Context)
				text = string(data)
			}
			if mode == "environment" {
				text = os.Getenv("PATCHBAY_PLUGIN_SECRET") + "/" + os.Getenv("EXPLICIT")
			}
			status := "success"
			if mode == "failed" {
				status = "failed"
			}
			switch mode {
			case "forge":
				reply(f, "result", map[string]any{"status": "success", "text": "bad", "context": map[string]any{"mode": "forged"}})
			case "null":
				reply(f, "result", map[string]any{"status": "success", "text": nil})
			default:
				reply(f, "result", wire.Result{Status: status, Text: &text})
			}
			if mode == "early_exit" {
				os.Exit(0)
			}
		case "cancel":
			if f.ID != active {
				os.Exit(7)
			}
			if mode == "refuse_cancel" {
				continue
			}
			if mode == "cancel_result" {
				text := "late"
				reply(f, "result", wire.Result{Status: "success", Text: &text})
			} else {
				reply(f, "cancelled", struct{}{})
			}
			active = ""
		case "shutdown":
			if mode == "refuse_shutdown" {
				continue
			}
			reply(f, "shutdown", struct{}{})
			if mode == "hang_after_shutdown" {
				time.Sleep(time.Minute)
			}
			if mode == "shutdown_extra" {
				reply(f, "result", struct{}{})
			}
			if mode == "shutdown_exit" {
				os.Exit(8)
			}
			os.Exit(0)
		default:
			os.Exit(9)
		}
	}
}
