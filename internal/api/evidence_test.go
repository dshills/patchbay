package api

import (
	"context"
	"os"
	"testing"
)

func TestEvidenceRoutesAndStrictRequests(t *testing.T) {
	f := newAPI(t)
	text := f.text + `experiments:
  echo:
    schema_version: 1
    title: Echo evidence
    action: echo
    collectors: [{name: output, step: 0, action: echo, kind: text, source: native, path: [stdout]}]
`
	if err := os.WriteFile(f.path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	caps := request(t, f, "GET", "/v1/capabilities", "", 200)
	if caps["features"].(map[string]any)["run_store"] != float64(1) {
		t.Fatal(caps)
	}
	storage := request(t, f, "GET", "/v1/storage", "", 200)
	if storage["available"] != true {
		t.Fatal(storage)
	}
	request(t, f, "POST", "/v1/captures/prepare", `{"experiment":"echo","arbitrary":"no"}`, 400)
	request(t, f, "POST", "/v1/captures/prepare", `{"experiment":null}`, 400)
	p := request(t, f, "POST", "/v1/captures/prepare", `{"experiment":"echo"}`, 200)
	if p["experiment"] != "echo" || len(p["digest"].(string)) != 64 {
		t.Fatal(p)
	}
	request(t, f, "GET", "/v1/runs?limit=0", "", 400)
	request(t, f, "GET", "/v1/runs?limit=101", "", 400)
	request(t, f, "GET", "/v1/runs?cursor=invalid", "", 400)
	list := request(t, f, "GET", "/v1/runs", "", 200)
	if len(list["runs"].([]any)) != 0 {
		t.Fatal(list)
	}
	request(t, f, "GET", "/v1/runs/missing", "", 404)
	request(t, f, "PUT", "/v1/baselines/echo", `{"revision":0,"run_id":"missing"}`, 404)
	request(t, f, "PUT", "/v1/runs/missing/annotation", `{"revision":0,"title":"x"}`, 404)
}
