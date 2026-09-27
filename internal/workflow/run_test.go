package workflow

import (
	"context"
	"patchbay/internal/action"
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
	"testing"
)

func TestWorkflowPoliciesAndCancellation(t *testing.T) {
	for _, stop := range []bool{true, false} {
		for failure := range 3 {
			t.Run(string(rune('a'+failure))+map[bool]string{true: "stop", false: "continue"}[stop], func(t *testing.T) {
				count := 0
				steps := make([]Step, 3)
				for i := range steps {
					steps[i] = Step{Name: "step", Execute: func(context.Context) (action.Result, error) {
						count++
						if i == failure {
							return action.Result{Status: action.Failed}, nil
						}
						return action.Result{Status: action.Success}, nil
					}}
				}
				result, err := Run(context.Background(), steps, stop)
				if result.Status != action.Failed || err == nil {
					t.Fatal(result, err)
				}
				records := result.Data["steps"].([]StepResult)
				want := 3
				if stop {
					want = failure + 1
				}
				if count != want {
					t.Fatal(count, want)
				}
				if records[failure].Error == nil || records[failure].StartedAt == nil || records[failure].FinishedAt == nil {
					t.Fatal(records)
				}
				for i := want; i < 3; i++ {
					if !records[i].Skipped {
						t.Fatal(records)
					}
				}
			})
		}
	}
	for _, explicit := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		steps := []Step{{Name: "cancel", Execute: func(context.Context) (action.Result, error) {
			if explicit {
				return action.Result{Status: action.Cancelled}, nil
			}
			cancel()
			return action.Result{Status: action.Success}, nil
		}}, {Name: "skipped", Execute: func(context.Context) (action.Result, error) {
			t.Fatal("ran after cancellation")
			return action.Result{}, nil
		}}}
		result, err := Run(ctx, steps, false)
		cancel()
		if result.Status != action.Cancelled || fault.Safe(err).Code != protocol.Cancelled || !result.Data["steps"].([]StepResult)[1].Skipped {
			t.Fatal(result, err)
		}
	}
}
