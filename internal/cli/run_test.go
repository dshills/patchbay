package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFoundationCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		code int
		json bool
	}{
		{"deckd", []string{"--version"}, 0, false},
		{"deckctl", []string{"--version", "--json"}, 0, true},
		{"deckd", []string{"--validate", "--config", path}, 0, false},
		{"deckd", []string{"--json", "--validate", "--config", path}, 0, true},
		{"deckctl", []string{"config", "validate", "--config", path}, 0, false},
		{"deckctl", []string{"--json", "config", "validate", "--config", path}, 0, true},
		{"deckctl", []string{"config", "validate", "--config", path, "--json"}, 0, true},
		{"deckctl", []string{"config", "validate", "--config", path + "-missing", "--json"}, 1, true},
		{"deckd", []string{"--validate", "--config", path + "-missing"}, 1, false},
		{"deckd", []string{"--help"}, 0, false},
		{"deckctl", []string{"config", "validate", "--help"}, 0, false},
		{"deckd", []string{"--config", path + "-missing"}, 1, false},
		{"deckctl", []string{"--socket", path + "-missing.sock", "status"}, 3, false},
		{"deckctl", []string{"config", "validate", "extra"}, 2, false},
		{"deckctl", []string{"--unknown"}, 2, false},
		{"deckctl", []string{"config", "validate", "--unknown"}, 2, false},
		{"deckd", []string{"--version", "--validate"}, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			if code := Run(tc.name, tc.args, &out, &diagnostic); code != tc.code {
				t.Fatalf("exit %d, want %d: %s", code, tc.code, diagnostic.String())
			}
			if tc.json && !json.Valid(out.Bytes()) {
				t.Fatalf("invalid JSON: %s", out.String())
			}
		})
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestOutputFailureAndSecretDiagnostics(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"--version", "--json"}} {
		var stderr bytes.Buffer
		if Run("deckctl", args, failedWriter{}, &stderr) != 1 {
			t.Fatal("output failure ignored")
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nactions: {a: {type: SECRET_SENTINEL}}"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if Run("deckd", []string{"--validate", "--config", path, "--json"}, &out, &diagnostic) != 1 {
		t.Fatal("invalid config succeeded")
	}
	if strings.Contains(out.String()+diagnostic.String(), "SECRET_SENTINEL") {
		t.Fatal("secret leaked")
	}
}
