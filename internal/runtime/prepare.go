package runtime

import (
	"context"
	"fmt"
	"maps"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/binding"
	"patchbay/internal/config"
	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/job"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/permission"
	"patchbay/internal/provider"
	"patchbay/internal/workflow"
	"patchbay/pkg/protocol"
)

type prepared struct {
	name        string
	risk        permission.Permission
	timeout     time.Duration
	command     *provider.Command
	steps       []*prepared
	stopOnError bool
}

func (r *Runtime) Invoke(ctx context.Context, name string, isWorkflow bool, invocation protocol.Invocation) (*job.Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invoke(ctx, name, isWorkflow, invocation)
}

func (r *Runtime) invoke(ctx context.Context, name string, isWorkflow bool, invocation protocol.Invocation) (*job.Handle, error) {
	if err := r.writable(ctx); err != nil {
		return nil, err
	}
	if invocation.Mode != "" && invocation.Mode != protocol.Sync && invocation.Mode != protocol.Async || invocation.TimeoutMS < 0 || invocation.TimeoutMS > math.MaxInt64/int64(time.Millisecond) {
		return nil, fault.New(protocol.InvalidRequest, "Invalid execution mode or timeout.")
	}
	remaining := 1024
	var plan *prepared
	var err error
	if isWorkflow {
		if len(invocation.Args) != 0 {
			return nil, fault.New(protocol.InvalidRequest, "Workflows do not accept top-level arguments.")
		}
		plan, err = r.prepareWorkflow(ctx, name, &remaining)
	} else {
		plan, err = r.prepareAction(ctx, name, invocation.Args, &remaining)
	}
	if err != nil {
		return nil, err
	}
	allow := r.cfg.Security.AllowDangerousActions
	if err := permission.Check(plan.risk, allow, invocation.Confirmed); err != nil {
		return nil, err
	}
	timeout := plan.timeout
	if requested := time.Duration(invocation.TimeoutMS) * time.Millisecond; requested > 0 && (timeout == 0 || requested < timeout) {
		timeout = requested
	}
	parent := context.Background()
	if invocation.Mode == protocol.Sync {
		parent = ctx
	}
	budget := provider.NewBudget(r.cfg.Jobs.OutputLimitBytes)
	return r.jobs.Submit(parent, name, r.generation, timeout, func(jobCtx context.Context, jobID string) (action.Result, error) {
		return r.execute(jobCtx, jobID, plan, budget, allow, invocation.Confirmed)
	})
}

func (r *Runtime) prepareWorkflow(ctx context.Context, name string, remaining *int) (*prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, fault.Safe(err)
	}
	*remaining--
	if *remaining < 0 {
		return nil, fault.New(protocol.InvalidConfig, "Workflow exceeds 1024 expanded actions.")
	}
	w, exists := r.cfg.Workflows[name]
	if !exists {
		return nil, fault.New(protocol.NotFound, "Workflow not found.")
	}
	plan := &prepared{name: name, risk: permission.Safe, stopOnError: *w.StopOnError, steps: make([]*prepared, 0, len(w.Steps))}
	for _, step := range w.Steps {
		child, err := r.prepareAction(ctx, step.Action, step.Args, remaining)
		if err != nil {
			return nil, err
		}
		plan.risk = permission.Strongest(plan.risk, child.risk)
		plan.steps = append(plan.steps, child)
	}
	return plan, nil
}

func (r *Runtime) prepareAction(ctx context.Context, name string, args map[string]any, remaining *int) (*prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, fault.Safe(err)
	}
	definition, exists := r.registries[r.context.Project].Get(name)
	if !exists {
		return nil, fault.New(protocol.ActionNotFound, "Action not found.")
	}
	values, err := definition.Arguments(args)
	if err != nil {
		return nil, fault.New(protocol.InvalidRequest, "Action arguments do not satisfy the declared schema.")
	}
	var plan *prepared
	if definition.Type == "workflow" {
		plan, err = r.prepareWorkflow(ctx, definition.Workflow, remaining)
		if err != nil {
			return nil, err
		}
		plan.name = name
	} else {
		*remaining--
		if *remaining < 0 {
			return nil, fault.New(protocol.InvalidConfig, "Workflow exceeds 1024 expanded actions.")
		}
		command, risk, err := r.prepareCommand(definition, values)
		if err != nil {
			return nil, err
		}
		plan = &prepared{name: name, command: command, risk: risk}
	}
	plan.risk = permission.Strongest(plan.risk, definition.Safety)
	if definition.Timeout != "" {
		plan.timeout, _ = time.ParseDuration(definition.Timeout)
	}
	return plan, nil
}

func (r *Runtime) prepareCommand(definition config.Action, args map[string]any) (*provider.Command, permission.Permission, error) {
	vars := map[string]string{".context.mode": r.context.Mode}
	for key, value := range r.context.Values {
		vars[".context.values."+key] = value
	}
	project, hasProject := r.cfg.Projects[r.context.Project]
	if hasProject {
		vars[".project.path"], vars[".project.id"], vars[".project.name"] = project.Path, project.ID, project.Name
	}
	for key, value := range args {
		vars[".args."+key] = fmt.Sprint(value)
	}
	render := func(text string) (string, error) {
		value, err := config.Render(text, vars)
		if err != nil || strings.ContainsRune(value, 0) {
			return "", fault.New(protocol.InvalidRequest, "An action template has a missing or invalid value.")
		}
		return value, nil
	}
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	for _, overlay := range []map[string]string{project.Environment, definition.Environment} {
		for key, template := range overlay {
			value, err := render(template)
			if err != nil {
				return nil, permission.Dangerous, err
			}
			env[key] = value
		}
	}
	command := &provider.Command{Dir: filepath.Dir(r.path)}
	if definition.Cwd != "" {
		cwd, err := render(definition.Cwd)
		if err != nil {
			return nil, permission.Dangerous, err
		}
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(filepath.Dir(r.path), cwd)
		}
		command.Dir = filepath.Clean(cwd)
	}
	info, err := os.Stat(command.Dir)
	if err != nil || !info.IsDir() {
		return nil, permission.Dangerous, fault.New(protocol.ProviderUnavailable, "Working directory is unavailable.")
	}
	risk := permission.Safe
	switch definition.Type {
	case "exec":
		command.Path = definition.Command
		for _, argument := range definition.Args {
			value, err := render(argument)
			if err != nil {
				return nil, permission.Dangerous, err
			}
			command.Args = append(command.Args, value)
		}
	case "open":
		target, err := render(definition.Target)
		if err != nil {
			return nil, permission.Dangerous, err
		}
		u, err := url.Parse(target)
		if err != nil || target == "" {
			return nil, permission.Dangerous, fault.New(protocol.InvalidRequest, "Invalid open target.")
		}
		if u.Scheme != "" {
			if u.Scheme != "file" && u.Scheme != "http" && u.Scheme != "https" || u.Scheme != "file" && u.Host == "" {
				return nil, permission.Dangerous, fault.New(protocol.InvalidRequest, "Unsupported open URL.")
			}
		} else if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(r.path), target)
		}
		command.Path, command.Args = r.opener, []string{"--", target}
	case "git":
		command.Path = "git"
		command.Args, risk, err = provider.GitCommand(definition.Operation, args)
		if err != nil {
			return nil, permission.Dangerous, err
		}
		env["GIT_TERMINAL_PROMPT"], env["GIT_EDITOR"], env["GIT_SEQUENCE_EDITOR"] = "0", "true", "true"
		env["GIT_PAGER"], env["GCM_INTERACTIVE"] = "cat", "never"
		env["GIT_ASKPASS"], env["SSH_ASKPASS"] = "/usr/bin/false", "/usr/bin/false"
		env["GIT_SSH_COMMAND"] = "ssh -oBatchMode=yes"
	default:
		return nil, permission.Dangerous, fault.New(protocol.ProviderUnavailable, "Provider unavailable.")
	}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		command.Env = append(command.Env, key+"="+env[key])
	}
	command.Path, err = provider.ResolveExecutable(command.Path, command.Dir, command.Env)
	if err != nil {
		return nil, permission.Dangerous, err
	}
	return command, risk, nil
}

func (r *Runtime) execute(ctx context.Context, jobID string, plan *prepared, budget *provider.Budget, allow, confirmed bool) (result action.Result, err error) {
	if err := ctx.Err(); err != nil {
		return action.Result{}, fault.Safe(err)
	}
	if err := permission.Check(plan.risk, allow, confirmed); err != nil {
		return action.Result{}, err
	}
	if plan.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, plan.timeout)
		defer cancel()
	}
	actionID := identity.New()
	r.bus.Emit(event.ActionStarted, map[string]string{"action_id": actionID, "job_id": jobID, "action": plan.name})
	defer func() {
		if recover() != nil {
			result = action.Result{Status: action.Failed, Message: "Action failed."}
			err = fault.New(protocol.Internal, "Action panicked.")
		}
		if ctx.Err() != nil {
			err = fault.Safe(ctx.Err())
		}
		if err != nil {
			result.Status = action.Failed
			if fault.Safe(err).Code == protocol.Cancelled {
				result.Status = action.Cancelled
			}
		}
		r.bus.Emit(event.ActionFinished, map[string]any{"action_id": actionID, "job_id": jobID, "action": plan.name, "status": result.Status})
	}()
	if plan.command != nil {
		result, err = r.runner.Run(ctx, *plan.command, budget)
	} else {
		steps := make([]workflow.Step, 0, len(plan.steps))
		for _, child := range plan.steps {
			steps = append(steps, workflow.Step{Name: child.name, Execute: func(stepCtx context.Context) (action.Result, error) {
				return r.execute(stepCtx, jobID, child, budget, allow, confirmed)
			}})
		}
		result, err = workflow.Run(ctx, steps, plan.stopOnError)
	}
	return result, err
}

func (r *Runtime) Control(ctx context.Context, request protocol.EventRequest) (protocol.EventResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writable(ctx); err != nil {
		return protocol.EventResponse{}, err
	}
	if request.Type != event.ControlPressed && request.Type != event.ControlReleased && request.Type != event.ControlRotated {
		return protocol.EventResponse{}, fault.New(protocol.InvalidRequest, "Only external control events are accepted.")
	}
	var payload struct {
		Device    string `json:"device"`
		Control   string `json:"control"`
		Delta     *int64 `json:"delta"`
		Confirmed *bool  `json:"confirmed"`
	}
	if err := jsonstrict.Decode(request.Payload, &payload); err != nil || !config.ValidName(payload.Control) || !config.ValidName(request.Source) || payload.Device != "" && !config.ValidName(payload.Device) || (request.Type == event.ControlRotated) != (payload.Delta != nil) || request.Type == event.ControlRotated && payload.Confirmed != nil {
		return protocol.EventResponse{}, fault.New(protocol.InvalidRequest, "Invalid control payload.")
	}
	target := binding.Resolve(r.cfg.Bindings, payload.Device, payload.Control, request.Type, r.context)
	response := protocol.EventResponse{EventID: identity.New(), Matched: target != nil}
	if target != nil {
		if target.Parameter != "" {
			if _, err := r.setParameter(target.Parameter, nil, payload.Delta); err != nil {
				return protocol.EventResponse{}, err
			}
		} else {
			handle, err := r.invoke(ctx, target.Action, false, protocol.Invocation{Mode: protocol.Async, Args: target.Args, Confirmed: payload.Confirmed != nil && *payload.Confirmed})
			if err != nil {
				return protocol.EventResponse{}, err
			}
			response.JobID = handle.ID
		}
	}
	r.bus.Publish(ctx, event.Event{ID: response.EventID, Type: request.Type, Source: request.Source, Timestamp: time.Now().UTC(), Payload: request.Payload})
	return response, nil
}
