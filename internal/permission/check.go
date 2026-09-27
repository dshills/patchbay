package permission

import (
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

func Check(risk Permission, allowDangerous, confirmed bool) error {
	if !risk.Valid() || risk == Dangerous && !allowDangerous {
		return fault.New(protocol.PermissionDenied, "This action is disabled by policy.")
	}
	if risk != Safe && !confirmed {
		return fault.New(protocol.ConfirmationRequired, "Explicit confirmation is required.")
	}
	return nil
}
