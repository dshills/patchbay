package cli

import (
	"context"
	"patchbay/internal/decksetup"
)

func deckCommand(ctx context.Context, o ctlOptions, command []string) (any, error) {
	if command[1] == "list" {
		return decksetup.Presets(), nil
	}
	m, err := decksetup.Default()
	if err != nil {
		return nil, err
	}
	switch command[1] {
	case "devices":
		return m.Devices(ctx)
	case "use":
		return m.Use(ctx, command[2], o.device)
	case "restore":
		id := ""
		if len(command) == 3 {
			id = command[2]
		}
		return m.Restore(ctx, id)
	case "backups":
		backups, err := m.Backups()
		if err != nil {
			return nil, err
		}
		out := []deckBackup{}
		for _, b := range backups {
			out = append(out, deckBackup{b.ID, b.Created.Local().Format("2006-01-02 15:04 MST"), b.Reason, b.Preset})
		}
		return out, nil
	}
	return nil, usage("Unknown deck command.")
}

type deckBackup struct {
	ID      string `json:"id"`
	Created string `json:"created"`
	Reason  string `json:"reason"`
	Preset  string `json:"preset,omitempty"`
}
