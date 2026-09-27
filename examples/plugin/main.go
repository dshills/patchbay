// A minimal text plugin: echo a string or wait for cooperative cancellation.
package main

import (
	"encoding/json"
	"os"
	"time"

	wire "patchbay/pkg/plugin"
)

func main() { os.Exit(serve()) }
func serve() int {
	frames := make(chan wire.Frame)
	go func() {
		defer close(frames)
		reader := wire.NewReader(os.Stdin)
		for {
			f, err := reader.Read()
			if err != nil {
				return
			}
			frames <- f
		}
	}()
	send := func(id, kind string, payload any) bool {
		f, err := wire.NewFrame(id, kind, payload)
		return err == nil && wire.Write(os.Stdout, f) == nil
	}
	state, active := "new", ""
	var timer *time.Timer
	var tick <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-tick:
			text := "Wait completed."
			if !send(active, "result", wire.Result{Status: "success", Text: &text}) {
				return 1
			}
			active, state, tick = "", "done", nil
		case f, ok := <-frames:
			if !ok {
				return 1
			}
			switch f.Type {
			case "hello":
				var request wire.HelloRequest
				if state != "new" || wire.Decode(f.Payload, &request) != nil || len(request.Versions) != 1 || request.Versions[0] != 1 {
					return 1
				}
				if !send(f.ID, "hello", wire.HelloResponse{SelectedVersion: 1, Operations: []wire.Operation{{Name: "echo", Safety: "confirm"}, {Name: "wait", Safety: "confirm"}}}) {
					return 1
				}
				state = "hello"
			case "health":
				if state != "hello" || wire.Decode(f.Payload, &struct{}{}) != nil {
					return 1
				}
				available := true
				if !send(f.ID, "health", wire.Health{Available: &available}) {
					return 1
				}
				state = "ready"
			case "execute":
				var request wire.Execute
				if state != "ready" || wire.Decode(f.Payload, &request) != nil {
					return 1
				}
				text, status := "Invalid arguments.", "failed"
				switch request.Operation {
				case "echo":
					if value, ok := request.Args["text"].(string); ok && len(request.Args) == 1 {
						text, status = value, "success"
					}
				case "wait":
					if value, ok := request.Args["milliseconds"].(json.Number); ok && len(request.Args) == 1 {
						if n, err := value.Int64(); err == nil && n >= 1 && n <= 60000 {
							timer = time.NewTimer(time.Duration(n) * time.Millisecond)
							tick = timer.C
							active, state = f.ID, "running"
							continue
						}
					}
				}
				if !send(f.ID, "result", wire.Result{Status: status, Text: &text}) {
					return 1
				}
				state = "done"
			case "cancel":
				// A result and cancel may cross; an already completed execution needs no reply.
				if state == "done" {
					continue
				}
				if state != "running" || f.ID != active || wire.Decode(f.Payload, &struct{}{}) != nil {
					return 1
				}
				timer.Stop()
				tick = nil
				if !send(f.ID, "cancelled", struct{}{}) {
					return 1
				}
				active, state = "", "done"
			case "shutdown":
				if state != "done" || wire.Decode(f.Payload, &struct{}{}) != nil {
					return 1
				}
				if !send(f.ID, "shutdown", struct{}{}) {
					return 1
				}
				return 0
			default:
				return 1
			}
		}
	}
}
