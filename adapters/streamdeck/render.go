package streamdeck

import (
	"encoding/base64"
	"fmt"
	"html"
)

type Command struct {
	Event   string `json:"event"`
	Context string `json:"context,omitempty"`
	UUID    string `json:"uuid,omitempty"`
	Payload any    `json:"payload,omitempty"`
}

func Commands(r Render) []Command {
	color := "#687484"
	switch r.Frame.State {
	case "running":
		color = "#4FA3FF"
	case "success":
		color = "#52C898"
	case "confirm", "warning":
		color = "#FFC462"
	case "error":
		color = "#F47B82"
	}
	if r.Controller == "Encoder" {
		return []Command{{Event: "setFeedback", Context: r.Context, Payload: map[string]any{
			"title": r.Frame.Title, "value": r.Frame.Value, "context": r.Frame.Context,
			"status": map[string]any{"value": r.Frame.State + " · " + r.Frame.Detail, "color": color},
		}}}
	}
	// Draw fixed-layout text only. No daemon strings become SVG markup or URLs.
	f := r.Frame
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="144" height="144" viewBox="0 0 144 144"><rect width="144" height="144" rx="12" fill="#111923"/><rect x="8" y="8" width="128" height="5" rx="2" fill="%s"/><g fill="#F3F5F7" font-family="sans-serif" text-anchor="middle"><text x="72" y="38" font-size="13">%s</text><text x="72" y="68" font-size="23">%s</text><text x="72" y="91" font-size="11" fill="%s">%s</text><text x="72" y="112" font-size="10">%s</text><text x="72" y="132" font-size="10">%s</text></g></svg>`, color, html.EscapeString(clean(f.Title, 18)), html.EscapeString(clean(f.Value, 12)), color, html.EscapeString(clean(f.State, 18)), html.EscapeString(clean(f.Detail, 22)), html.EscapeString(clean(f.Context, 22)))
	return []Command{{Event: "setImage", Context: r.Context, Payload: map[string]any{"image": "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)), "target": 0}}}
}
