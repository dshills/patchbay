package recipe

import (
	"context"
	"os"
	"strings"
	"testing"

	"patchbay/internal/config"
	"patchbay/internal/parameter"
	"patchbay/internal/permission"
)

func composeBase(t *testing.T) *config.Config {
	t.Helper()
	base, err := config.Parse([]byte(`version: 1
projects: {p: {name: Example, path: /tmp}}
`), "/", "/")
	if err != nil {
		t.Fatal(err)
	}
	return base
}
func TestCompositionNamespacesProjectAndCollisions(t *testing.T) {
	p := benchmark(t)
	base := composeBase(t)
	entry := Installation{ID: "rtest", Active: true, BaseDigest: BaseDigest(base), Content: p.Digest, Mappings: map[string]Mapping{"benchmark": {Project: "p"}, "demo": {Tool: "/bin/echo"}}}
	out, err := Compose(base, []Installation{entry}, map[string]*Package{p.Digest: p})
	if err != nil {
		t.Fatal(err)
	}
	action := Namespace(entry.ID) + "benchmark.measure"
	if out.Config.Actions[action].Safety != permission.Confirm || !out.Allows(action, "p") || out.Allows(action, "other") {
		t.Fatal("scope/floor lost")
	}
	if len(base.Actions) != 0 || len(base.Experiments) != 0 {
		t.Fatal("base mutated")
	}
	base.Actions = map[string]config.Action{action: {Type: "exec", Command: "/bin/echo"}}
	entry.BaseDigest = BaseDigest(base)
	if _, err := Compose(base, []Installation{entry}, map[string]*Package{p.Digest: p}); err == nil {
		t.Fatal("collision accepted")
	}
	base = composeBase(t)
	entry.BaseDigest = BaseDigest(base)
	entry.Assignments = map[string]Assignment{"capture": {Control: "dial", Gesture: "rotate"}}
	if _, err := Compose(base, []Installation{entry}, map[string]*Package{p.Digest: p}); err == nil {
		t.Fatal("illegal control mapping accepted")
	}
}
func TestSCPIIntersectionPreservesHostLimits(t *testing.T) {
	raw, err := os.ReadFile("../../configs/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.Parse(raw, "/tmp", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	param := base.Parameters["bench.frequency"]
	param.Min = float64(500)
	param.Max = float64(5000)
	base.Parameters["bench.frequency"] = param
	lo, hi := 100.0, 10000.0
	requirement := Requirement{Kind: "action", Provider: "scpi", Operation: "generator.set_frequency", Model: "DG812", Channel: 1, Unit: "Hz", Min: &lo, Max: &hi}
	p := &Package{Digest: strings.Repeat("a", 64), Manifest: Manifest{ID: "test", Version: "1.0.0", Requirements: map[string]Requirement{"frequency": requirement}, Actions: map[string]Action{"apply": {Type: "reference", Reference: "frequency"}}}}
	entry := Installation{ID: "rtest", Content: p.Digest, Active: true, BaseDigest: BaseDigest(base), Mappings: map[string]Mapping{"frequency": {Action: "generator.frequency"}}}
	build := func() (Composition, error) {
		return Compose(base, []Installation{entry}, map[string]*Package{p.Digest: p})
	}
	out, err := build()
	if err != nil {
		t.Fatal(err)
	}
	a := out.Config.Actions[Namespace(entry.ID)+"apply"]
	if a.Inputs["value"].Min != float64(500) || a.Inputs["value"].Max != float64(5000) || a.Parameter != "" {
		t.Fatal("host bounds widened", a.Inputs)
	}
	if _, err := a.Arguments(map[string]any{"value": 6000}); err == nil {
		t.Fatal("host limit bypassed")
	}
	if base.Actions["generator.frequency"].Parameter == "" {
		t.Fatal("host definition mutated")
	}
	for _, change := range []string{"model", "channel", "unit", "provider", "limits"} {
		t.Run(change, func(t *testing.T) {
			r := requirement
			switch change {
			case "model":
				r.Model = "DG822"
			case "channel":
				r.Channel = 2
			case "unit":
				r.Unit = "V"
			case "provider":
				r.Provider = "exec"
			case "limits":
				n := 6000.0
				r.Min = &n
			}
			p.Manifest.Requirements["frequency"] = r
			if _, err := build(); err == nil {
				t.Fatal("incompatible mapping accepted")
			}
			p.Manifest.Requirements["frequency"] = requirement
		})
	}
}
func TestReferencedInputBoundsCannotWiden(t *testing.T) {
	host := config.Input{Type: parameter.Integer, Required: true, Min: int64(20), Max: int64(40)}
	merged, err := intersectInput(host, config.Input{Type: parameter.Integer, Min: 1, Max: 100})
	if err != nil || merged.Min != int64(20) || merged.Max != int64(40) || !merged.Required {
		t.Fatal("host constraints weakened", err)
	}
	if _, err := intersectInput(host, config.Input{Type: parameter.Integer, Min: 50, Max: 100}); err == nil {
		t.Fatal("empty interval accepted")
	}
	host = config.Input{Type: parameter.Enum, Enum: []string{"list", "show"}}
	merged, err = intersectInput(host, config.Input{Type: parameter.Enum, Enum: []string{"list", "delete"}})
	if err != nil || len(merged.Enum) != 1 || merged.Enum[0] != "list" {
		t.Fatal("enum expanded", err)
	}
}

func TestCuratedRecipesConformAndRigolMappingsStayInert(t *testing.T) {
	for _, name := range []string{"benchmark", "project-checkup", "rigol-capture"} {
		t.Run(name, func(t *testing.T) {
			p, err := Inspect(context.Background(), "../../recipes/"+name)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Samples) != 2 {
				t.Fatal("missing illustrative comparison")
			}
			data, err := ZIP(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := InspectZIP(context.Background(), data)
			if err != nil || restored.Digest != p.Digest {
				t.Fatal("content round trip", err)
			}
		})
	}
	p, err := Inspect(context.Background(), "../../recipes/rigol-capture")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../configs/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.Parse(append(raw, []byte("\nprojects: {bench: {name: Bench, path: /tmp}}\n")...), "/tmp", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	entry := Installation{ID: "rbench", Active: true, Content: p.Digest, BaseDigest: BaseDigest(base), Mappings: map[string]Mapping{"bench": {Project: "bench"}, "generator": {Action: "generator.inspect"}, "scope": {Action: "scope.capture"}}}
	out, err := Compose(base, []Installation{entry}, map[string]*Package{p.Digest: p})
	if err != nil {
		t.Fatal(err)
	}
	for name, a := range out.Config.Actions {
		if strings.HasPrefix(name, Namespace(entry.ID)) && a.Type == "scpi" && a.Operation != "generator.inspect" && a.Operation != "scope.capture" {
			t.Fatal("recipe added hardware writes")
		}
	}
	for name, p := range out.Config.Parameters {
		if strings.HasPrefix(name, Namespace(entry.ID)) && p.Instrument != nil {
			t.Fatal("intended value became a hardware binding")
		}
	}
	if !strings.Contains(p.Manifest.Description, "Physical verification pending") {
		t.Fatal("unearned hardware claim")
	}
}
