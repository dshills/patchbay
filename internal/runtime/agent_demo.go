package runtime

import (
	"context"
	"encoding/json"
	"patchbay/internal/action"
	"patchbay/internal/fault"
	"patchbay/internal/provider"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
)

func (r *Runtime) proposalProvider() provider.Agent {
	if r.cfg.Agents.Proposals.Demo {
		return demoAgent{}
	}
	return r.agent
}
func (r *Runtime) proposalDestination() (string, string) {
	if r.cfg.Agents.Proposals.Demo {
		return "offline-demo-v1", "local://patchbay/benchmark-fixture"
	}
	return r.cfg.Agents.Codex.Model, "https://api.openai.com/v1/responses"
}

// demoAgent is an explicit offline teaching fixture, never a live model fallback.
// It only suggests the locally granted Benchmark Playground experiment.
type demoAgent struct{}

func (demoAgent) Health(context.Context) provider.Health { return provider.Health{Available: true} }
func (demoAgent) Run(ctx context.Context, request provider.AgentRequest, _ *provider.Budget, _ func(action.Result)) (action.Result, error) {
	if err := ctx.Err(); err != nil {
		return action.Result{}, fault.Safe(err)
	}
	var input struct {
		Items   []supervisor.Item   `json:"selected_context"`
		Catalog []supervisor.Target `json:"catalog"`
	}
	if len(request.FrozenInput) > supervisor.MaxContext || json.Unmarshal([]byte(request.FrozenInput), &input) != nil {
		return action.Result{}, fault.New(protocol.InvalidRequest, "Invalid offline fixture context.")
	}
	output := supervisor.Output{SchemaVersion: 1, Summary: "Offline demonstration: this fixed explanation does not analyze your data. Grant the Benchmark Playground experiment locally, then request a new session to practice reviewing one measured run.", ContextRefs: []string{}, Proposals: []supervisor.Suggestion{}}
	for _, item := range input.Items {
		output.ContextRefs = append(output.ContextRefs, item.ID)
	}
	for _, target := range input.Catalog {
		if target.Kind == "experiment" && target.ID == "benchmark" {
			output.Summary = "Offline demonstration: repeat the configured Benchmark Playground workload and compare its measured duration with a baseline. This fixed suggestion makes no claim about performance improvement."
			output.Proposals = append(output.Proposals, supervisor.Suggestion{Kind: "experiment", Target: target.ID, Rationale: "Practice the review and approval flow with your configured local workload.", Expected: "A saved run with measured repeated SHA-256 duration; compare it with a baseline after completion."})
			break
		}
	}
	data, err := json.Marshal(output)
	if err != nil {
		return action.Result{}, err
	}
	return action.Result{Status: action.Success, Data: map[string]any{"stdout": string(data)}}, nil
}
