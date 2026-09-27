package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"patchbay/pkg/protocol"
)

func TestStructuredSecretSafeErrors(t *testing.T) {
	for _, failure := range []error{
		nil,
		errors.New("SECRET_SENTINEL"),
		fmt.Errorf("SECRET_SENTINEL: %w", &protocol.Error{Code: protocol.InvalidConfig, Message: "SECRET_SENTINEL"}),
		&protocol.Error{Code: "SECRET_SENTINEL", Message: "SECRET_SENTINEL"},
	} {
		var out bytes.Buffer
		New(&out).Operation(context.Background(), Record{Component: "config", EventID: "e1", ActionID: "a1", JobID: "j1", Duration: 3 * time.Millisecond, Outcome: "checked", Err: failure})
		if strings.Contains(out.String(), "SECRET_SENTINEL") {
			t.Fatal("secret leaked to logs")
		}
		var record map[string]any
		if err := json.Unmarshal(out.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if record["component"] != "config" || record["duration_ms"] != float64(3) || record["job_id"] != "j1" {
			t.Fatalf("missing structured fields: %v", record)
		}
		if failure != nil && record["error"] == nil {
			t.Fatal("missing sanitized error code")
		}
	}
}
