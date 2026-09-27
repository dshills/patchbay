package plugin

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestGoldenFrames(t *testing.T) {
	for _, fixture := range []struct {
		path, kind string
		payload    any
	}{{"hello-request", "hello", HelloRequest{Versions: []int{1}}}, {"hello-response", "hello", HelloResponse{SelectedVersion: 1, Operations: []Operation{{Name: "echo", Safety: "confirm"}}}}} {
		data, err := os.ReadFile("testdata/" + fixture.path + ".json")
		if err != nil {
			t.Fatal(err)
		}
		f, err := NewFrame("1", fixture.kind, fixture.payload)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err = Write(&b, f); err != nil || !bytes.Equal(data, b.Bytes()) {
			t.Fatal(b.String(), err)
		}
		r := NewReader(&b)
		if _, err = r.Read(); err != nil {
			t.Fatal(err)
		}
		if _, err = r.Read(); err != io.EOF {
			t.Fatal(err)
		}
	}
}
func TestRejectInvalidFrames(t *testing.T) {
	for _, text := range []string{"\n", "null\n", "[]\n", "{}\n", `{"version":1,"id":"1","type":"hello","payload":{}}`, strings.Repeat("x", MaxFrameBytes+1) + "\n",
		`{"version":1,"id":"1","type":"hello","payload":null}` + "\n", `{"version":1,"id":"1","id":"1","type":"hello","payload":{}}` + "\n",
		`{"version":1,"id":"1","type":"hello","payload":{},"context":{}}` + "\n",
		`{"version":2,"id":"1","type":"hello","payload":{}}` + "\n", `{"version":1,"id":"01","type":"hello","payload":{}}` + "\n",
		`{"version":1,"id":"1","type":"event","payload":{}}` + "\n", string([]byte{'{', 0xff, '}', '\n'})} {
		if _, err := NewReader(strings.NewReader(text)).Read(); err == nil {
			t.Fatal("accepted", text[:min(len(text), 100)])
		}
	}
}

func TestDecodeRequiresEmptyObjectAndValidUTF8(t *testing.T) {
	for _, data := range []string{"null", `{"unknown":1}`, `[]`, `{"id":null}`} {
		if Decode([]byte(data), &struct{}{}) == nil {
			t.Fatal("accepted nonempty or null payload", data)
		}
	}
	if err := Decode([]byte(" {} "), &struct{}{}); err != nil {
		t.Fatal(err)
	}
	// encoding/json replaces invalid UTF-8 inside strings; the wire must reject it.
	raw := []byte{'{', '"', 't', 'e', 'x', 't', '"', ':', '"', 0xff, '"', '}'}
	var value struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal("fixture should be accepted by ordinary JSON decoding", err)
	}
	if err := Decode(raw, &value); err == nil {
		t.Fatal("plugin protocol accepted invalid UTF-8")
	}
}
