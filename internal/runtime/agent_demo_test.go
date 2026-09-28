package runtime

import (
	"context"
	"encoding/json"
	"patchbay/internal/config"
	"patchbay/internal/provider"
	"patchbay/internal/supervisor"
	"testing"
)

func TestAgentDemoIsExplicitLocalFixtureWithoutProviderFallback(t *testing.T) {
	r, a := proposalRuntime(t, nil, &fakeRunner{}, supervisor.Options{})
	changeProposalConfig(t, r, func(c *config.Config) { c.Agents.Proposals.Demo = true; c.Agents.Codex.Model = "" })
	catalog := r.AgentCatalog(context.Background())
	if !catalog.Available || catalog.Model != "offline-demo-v1" || catalog.Destination != "local://patchbay/benchmark-fixture" {
		t.Fatal(catalog)
	}
	session := generateProposals(t, r)
	if session.State != "completed" || len(session.Proposals) != 0 || a.count() != 0 {
		t.Fatal(session, a.count())
	}
	if session.Destination != catalog.Destination || session.Model != catalog.Model {
		t.Fatal(session)
	}
	// A recipe, similarly named action, or arbitrary target cannot become this fixture's proposal.
	for _, target := range []supervisor.Target{{Kind: "experiment", ID: "benchmark"}, {Kind: "action", ID: "benchmark"}, {Kind: "experiment", ID: "recipe.example.benchmark"}} {
		input, _ := json.Marshal(map[string]any{"catalog": []supervisor.Target{target}})
		result, err := (demoAgent{}).Run(context.Background(), provider.AgentRequest{FrozenInput: string(input)}, provider.NewBudget(supervisor.MaxOutput), nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := supervisor.ParseOutput(result.Data["stdout"].(string))
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if target.Kind == "experiment" && target.ID == "benchmark" {
			want = 1
		}
		if len(out.Proposals) != want {
			t.Fatal(out)
		}
	}
	changeProposalConfig(t, r, func(c *config.Config) { c.Agents.Proposals.Demo = false; c.Agents.Codex.Model = "test-model" })
	if r.proposalProvider() != a {
		t.Fatal("demo replaced ordinary provider")
	}
}
