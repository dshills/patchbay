package binding

import (
	runtimecontext "patchbay/internal/context"
	"patchbay/internal/event"
	"testing"
)

func TestResolvePrecedenceAndPresence(t *testing.T) {
	bindings := []Binding{
		{Control: "key", Press: &Target{Action: "global"}},
		{Device: "deck", Control: "key", Press: &Target{Action: "device"}},
		{Control: "key", When: map[string]string{"mode": "dev"}, Press: &Target{Action: "context"}},
		{Device: "deck", Control: "key", When: map[string]string{"mode": "dev", "project": "p"}, Press: &Target{Action: "both"}},
		{Control: "empty", When: map[string]string{"values.foo": ""}, Press: &Target{Action: "present"}},
	}
	for _, c := range []struct{ device, mode, project, want string }{{"", "", "", "global"}, {"deck", "", "", "device"}, {"", "dev", "", "context"}, {"deck", "dev", "p", "both"}, {"deck", "dev", "q", "context"}} {
		got := Resolve(bindings, c.device, "key", event.ControlPressed, runtimecontext.RuntimeContext{Mode: c.mode, Project: c.project})
		if got == nil || got.Action != c.want {
			t.Fatalf("%+v: %v", c, got)
		}
	}
	if Resolve(bindings, "", "empty", event.ControlPressed, runtimecontext.RuntimeContext{}) != nil {
		t.Fatal("missing is not an empty value")
	}
	if Resolve(bindings, "", "key", event.ControlReleased, runtimecontext.RuntimeContext{}) != nil {
		t.Fatal("wrong gesture")
	}
	if Resolve(bindings, "", "key", "unknown", runtimecontext.RuntimeContext{}) != nil {
		t.Fatal("unknown gesture")
	}
}
