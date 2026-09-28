package cli

import (
	"context"
	"net/url"
	"patchbay/internal/client"
	"patchbay/internal/evidence"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/recipe"
)

func recipeCommand(ctx context.Context, c *client.Client, o ctlOptions, command []string) (any, error) {
	switch command[1] {
	case "import", "stage":
		path := command[2]
		query := url.Values{}
		if command[1] == "stage" {
			path = command[3]
			query.Set("installation", command[2])
		}
		p, err := recipe.Inspect(ctx, path)
		if err != nil {
			return nil, &commandError{Code: "invalid_request", Message: err.Error(), Exit: 2}
		}
		data, err := recipe.ZIP(ctx, p)
		if err != nil {
			return nil, err
		}
		var response recipe.ImportResult
		err = c.Upload(ctx, []string{"recipes", "imports"}, query, data, &response)
		return response, err
	case "list":
		return call[recipe.List](ctx, c, "GET", nil, "recipes")
	case "show":
		return call[recipe.View](ctx, c, "GET", nil, "recipes", command[2])
	case "prepare":
		var request recipe.Prepare
		if jsonstrict.Decode([]byte(command[3]), &request) != nil {
			return nil, usage("Recipe preparation requires strict JSON.")
		}
		return call[recipe.Preview](ctx, c, "POST", request, "recipes", command[2], "prepare")
	case "commit":
		if _, err := evidence.RequestTime(command[5]); err != nil {
			return nil, usage("Use deckctl request-id for a new management operation.")
		}
		return call[recipe.ManagementResult](ctx, c, "POST", recipe.Commit{Preparation: command[3], Digest: command[4], RequestID: command[5], Confirmed: o.confirm}, "recipes", command[2], "commit")
	}
	return nil, usage("Unsupported recipe command.")
}
