package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRecipeInspectionIsOfflineAndHasHumanOutput(t *testing.T) {
	for _, format := range []string{"human", "json"} {
		var out, errOut bytes.Buffer
		args := []string{"recipe", "inspect", "../../recipes/benchmark"}
		if format == "json" {
			args = append(args, "--json")
		}
		if code := runDeckctl(context.Background(), args, &out, &errOut); code != 0 {
			t.Fatal(code, errOut.String())
		}
		if format == "json" {
			var result map[string]any
			if json.Unmarshal(out.Bytes(), &result) != nil || result["package_digest"] == nil {
				t.Fatal("missing verified identity")
			}
		} else if !strings.Contains(out.String(), "Offline inspection only") || !strings.Contains(out.String(), "Role demo: tool") {
			t.Fatal(out.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := runDeckctl(context.Background(), []string{"recipe", "inspect", "../../recipes/benchmark", "--socket", "/nonexistent"}, &out, &errOut); code != 2 {
		t.Fatal("offline inspection accepted transport options", code)
	}
}
