package jsonstrict

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStrictJSON(t *testing.T) {
	for _, s := range []string{``, `null`, `[]`, `{} {}`, `{"v":1,"v":2}`, `{"v":1,"V":2}`, `{"V":1}`, `{"v":null}`, `{"unknown":1}`, `{"v":{"k":1,"k":2}}`, `{"v":[null]}`, `{"v":` + strings.Repeat(`[`, 65) + `0` + strings.Repeat(`]`, 65) + `}`} {
		var out struct {
			V any `json:"v"`
		}
		if Decode([]byte(s), &out) == nil {
			t.Errorf("accepted %s", s)
		}
	}
	var out struct {
		V any `json:"v"`
	}
	if err := Decode([]byte(`{"v":9223372036854775807}`), &out); err != nil || out.V != json.Number("9223372036854775807") {
		t.Fatalf("precision %v %v", out, err)
	}
}
