package api

import (
	"patchbay/internal/action"
	runtimecontext "patchbay/internal/context"
	"patchbay/internal/job"
	"patchbay/internal/parameter"
	"patchbay/pkg/protocol"
)

// Keep the public field set explicit when internal snapshots evolve.
func wireContext(value runtimecontext.RuntimeContext) protocol.Context {
	return protocol.Context{Project: value.Project, Mode: value.Mode, Values: value.Values}
}
func wireParameter(value parameter.Parameter) protocol.Parameter {
	return protocol.Parameter{Name: value.Name, Type: string(value.Type), Value: value.Value, Min: value.Min, Max: value.Max, Step: value.Step, Enum: value.Enum, Unit: value.Unit, Persistent: value.Persistent}
}
func wireParameters(values []parameter.Parameter) protocol.ParameterList {
	result := protocol.ParameterList{Parameters: make([]protocol.Parameter, 0, len(values))}
	for _, value := range values {
		result.Parameters = append(result.Parameters, wireParameter(value))
	}
	return result
}
func wireResult(value *action.Result) *protocol.Result {
	if value == nil {
		return nil
	}
	result := &protocol.Result{Status: string(value.Status), Message: value.Message, Data: value.Data}
	if d := value.Display; d != nil {
		result.Display = &protocol.DisplayResult{Title: d.Title, Value: d.Value, State: string(d.State), Progress: d.Progress}
	}
	return result
}
func wireJob(value job.Job) protocol.Job {
	return protocol.Job{ID: value.ID, Action: value.Action, State: string(value.State), Generation: value.Generation, CreatedAt: value.CreatedAt, StartedAt: value.StartedAt, FinishedAt: value.FinishedAt, Result: wireResult(value.Result), Error: value.Error}
}
func wireJobs(values []job.Job) protocol.JobList {
	result := protocol.JobList{Jobs: make([]protocol.Job, 0, len(values))}
	for _, value := range values {
		result.Jobs = append(result.Jobs, wireJob(value))
	}
	return result
}
