package workflow

import (
	"context"
	"patchbay/internal/action"
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
	"time"
)

type Step struct {
	Name    string
	Execute func(context.Context) (action.Result, error)
}

// Run executes prepared steps synchronously on the parent's worker. It does not
// submit child jobs or create goroutines. All preflight/policy checks precede Run.
func Run(ctx context.Context, steps []Step, stopOnError bool) (action.Result, error) {
	results := make([]StepResult, len(steps))
	var aggregate error
	stop := false
	for i, step := range steps {
		results[i] = StepResult{Index: i, Action: step.Name}
		if err := ctx.Err(); err != nil {
			aggregate, stop = fault.Safe(err), true
		}
		if stop {
			results[i].Skipped = true
			continue
		}
		started := time.Now().UTC()
		result, err := step.Execute(ctx)
		finished := time.Now().UTC()
		results[i].StartedAt, results[i].FinishedAt = &started, &finished
		if err != nil || result.Status == action.Failed || result.Status == action.Cancelled {
			if err == nil {
				err = fault.New(protocol.ExecutionFailed, "Workflow step failed.")
				if result.Status == action.Cancelled {
					err = fault.Safe(context.Canceled)
				}
			}
			aggregate = err
			stop = stopOnError || result.Status == action.Cancelled || fault.Safe(err).Code == protocol.Cancelled
		}
		results[i].Result, results[i].Error = &result, fault.Safe(err)
	}
	if err := ctx.Err(); err != nil {
		aggregate = fault.Safe(err)
	}
	result := action.Result{Status: action.Success, Message: "Workflow completed.", Data: map[string]any{"steps": results}}
	if aggregate != nil {
		result.Status, result.Message = action.Failed, "Workflow failed."
		if fault.Safe(aggregate).Code == protocol.Cancelled {
			result.Status = action.Cancelled
		}
	}
	return result, aggregate
}
