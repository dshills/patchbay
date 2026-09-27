package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOfflineCommandsAndSafeLaunchErrors(t *testing.T) {
	for _, args := range [][]string{{"--capabilities"}, {"--version"}} {
		var out, err bytes.Buffer
		if code := run(context.Background(), args, &out, &err); code != 0 || !json.Valid(out.Bytes()) || err.Len() != 0 {
			t.Fatal(code, out.String(), err.String())
		}
	}
	for _, args := range [][]string{{}, {"--port", "secret"}, {"--info", "secret"}, {"--info", "{}"}, {"--unknown", "secret"}, {"positional"}} {
		var out, err bytes.Buffer
		if code := run(context.Background(), args, &out, &err); code != 2 || strings.Contains(out.String()+err.String(), "secret") {
			t.Fatal(code, out.String(), err.String())
		}
	}
}
