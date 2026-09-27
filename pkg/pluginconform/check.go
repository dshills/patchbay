// Package pluginconform runs the production host against a plugin's chosen
// success and long-running cancellation operations. It executes trusted code;
// use only with an operator-approved executable and test inputs.
package pluginconform

import (
	"context"
	"errors"

	"patchbay/internal/fault"
	"patchbay/internal/permission"
	"patchbay/internal/provider"
	wire "patchbay/pkg/plugin"
	"patchbay/pkg/protocol"
)

type Specification struct {
	Command         string
	Args            []string
	Directory       string
	Environment     map[string]string
	Operations      map[string]string
	AllowDangerous  bool
	Execute, Cancel wire.Execute
}
type Report struct {
	Operations   []wire.Operation `json:"operations"`
	Execution    bool             `json:"execution"`
	Cancellation bool             `json:"cancellation"`
	Shutdown     bool             `json:"shutdown"`
}

func Check(ctx context.Context, s Specification) (Report, error) {
	c := provider.PluginConfig{Command: s.Command, Args: s.Args, Cwd: s.Directory, Environment: s.Environment, Operations: map[string]permission.Permission{}}
	for name, risk := range s.Operations {
		c.Operations[name] = permission.Permission(risk)
	}
	if err := c.Normalize(); err != nil {
		return Report{}, err
	}
	host := provider.NewPlugins(map[string]provider.PluginConfig{"candidate": c})
	defer func() { _ = host.Close(context.Background()) }()
	request := func(e wire.Execute) provider.PluginRequest {
		return provider.PluginRequest{Plugin: "candidate", Operation: e.Operation, Args: e.Args, Context: e.Context, Confirmed: true, AllowDangerous: s.AllowDangerous}
	}
	result, err := host.Run(ctx, request(s.Execute), provider.NewBudget(wire.MaxFrameBytes))
	if err != nil {
		return Report{}, err
	}
	report := Report{Operations: host.Snapshot("candidate").Operations, Execution: result.Status == "success", Shutdown: result.Data["shutdown_clean"] == true}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	started, done := make(chan struct{}), make(chan struct{})
	r := request(s.Cancel)
	r.Started = func() { close(started) }
	var cooperative, shutdown bool
	var cancelErr error
	go func() {
		out, e := host.Run(child, r, provider.NewBudget(wire.MaxFrameBytes))
		cooperative = out.Data["cooperative_cancel"] == true
		shutdown = out.Data["shutdown_clean"] == true
		cancelErr = e
		close(done)
	}()
	select {
	case <-started:
		cancel()
	case <-done:
		return report, errors.New("cancellation operation did not remain active")
	case <-ctx.Done():
		cancel()
	}
	<-done // Host cancellation grace and process cleanup remain bounded.
	report.Cancellation = cooperative && fault.Safe(cancelErr).Code == protocol.Cancelled
	report.Shutdown = report.Shutdown && shutdown
	if !report.Execution || !report.Cancellation || !report.Shutdown {
		return report, errors.New("plugin did not complete execution, cooperative cancellation and clean shutdown")
	}
	return report, nil
}
