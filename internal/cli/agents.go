package cli

import (
	"context"
	"net/url"
	"patchbay/internal/client"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/supervisor"
)

func agentCommand(ctx context.Context, c *client.Client, o ctlOptions, command []string) (any, error) {
	switch command[1] {
	case "catalog":
		return call[supervisor.Catalog](ctx, c, "GET", nil, "agents", "catalog")
	case "context":
		var request supervisor.Selection
		if jsonstrict.Decode([]byte(command[2]), &request) != nil {
			return nil, usage("Context selection requires strict JSON.")
		}
		return call[supervisor.ContextPreview](ctx, c, "POST", request, "agents", "context", "prepare")
	case "start":
		return call[supervisor.Session](ctx, c, "POST", supervisor.Start{Preparation: command[2], Digest: command[3], RequestID: command[4], Confirmed: o.confirm}, "agents", "sessions")
	case "list":
		return call[supervisor.List](ctx, c, "GET", nil, "agents", "sessions")
	case "page":
		var out supervisor.List
		err := c.CallQuery(ctx, "GET", []string{"agents", "sessions"}, url.Values{"cursor": {command[2]}}, nil, &out)
		return out, err
	case "show":
		return call[supervisor.Session](ctx, c, "GET", nil, "agents", "sessions", command[2])
	case "cancel":
		return call[supervisor.Session](ctx, c, "DELETE", nil, "agents", "sessions", command[2])
	case "forget":
		return call[map[string]string](ctx, c, "POST", map[string]bool{"confirmed": o.confirm}, "agents", "sessions", command[2], "forget")
	}
	return nil, usage("Unsupported agent command.")
}
