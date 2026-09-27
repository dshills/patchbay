package permission

import (
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, c := range []struct {
		risk             Permission
		allow, confirmed bool
		code             protocol.Code
	}{
		{Safe, false, false, ""}, {Confirm, false, false, protocol.ConfirmationRequired}, {Confirm, false, true, ""},
		{Dangerous, false, true, protocol.PermissionDenied}, {Dangerous, true, false, protocol.ConfirmationRequired}, {Dangerous, true, true, ""},
		{Permission("unknown"), true, true, protocol.PermissionDenied},
	} {
		err := Check(c.risk, c.allow, c.confirmed)
		if c.code == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || fault.Safe(err).Code != c.code {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}
