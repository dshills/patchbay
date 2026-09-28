package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"patchbay/pkg/protocol"
)

const captureFixture = `
experiments:
  compare:
    schema_version: 1
    title: Compare
    workflow: outer
    inputs: [{step: 0, input: text, parameter: label}]
    collectors: [{name: output, step: 0, action: echo, kind: text, source: native, path: [stdout]}]
  scoped:
    schema_version: 1
    title: Scoped
    action: scoped
    collectors: [{name: output, step: 0, action: scoped, kind: text, source: native, path: [stdout]}]
`

func TestCapturePreparationPinningAndPrivacy(t *testing.T) {
	t.Setenv("PATCHBAY_TEST_SECRET", "never-persist-this-value")
	r, _ := setup(t, fixture+captureFixture, &fakeRunner{})
	ctx := context.Background()
	p, err := r.PrepareCapture(ctx, protocol.CapturePrepare{Experiment: "compare"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Experiment != "compare" || len(p.Steps) != 2 || p.Steps[0].Arguments[0] != "initial" || p.Steps[1].Arguments[0] != "old" || p.Parameters["label"] != "initial" {
		t.Fatalf("bad mapping %#v", p)
	}
	data, _ := json.Marshal(p)
	if strings.Contains(string(data), "never-persist-this-value") || !strings.Contains(string(data), "PATCHBAY_TEST_SECRET") {
		t.Fatalf("environment redaction: %s", data)
	}
	if len(p.Digest) != 64 || len(r.captures) != 1 {
		t.Fatal("missing private plan binding")
	}
	p.Steps[0].Arguments[0] = "forged"
	if r.captures[p.ID].preview.Steps[0].Arguments[0] != "initial" {
		t.Fatal("public preview aliases private plan")
	}
	_, err = r.SetParameter(ctx, "label", "changed")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.captures) != 0 {
		t.Fatal("parameter write retained stale previews")
	}
	p, err = r.PrepareCapture(ctx, protocol.CapturePrepare{Experiment: "scoped"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Safety != "confirm" || p.Steps[0].Arguments[0] != "project" {
		t.Fatal("override or permission floor lost")
	}
	mode := "other"
	_, err = r.PatchContext(ctx, protocol.ContextPatch{Mode: &mode})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.captures) != 0 {
		t.Fatal("context change retained preparation")
	}
	if !r.Storage().Available || r.Storage().ReadOnly {
		t.Fatalf("store: %#v", r.Storage())
	}
}
