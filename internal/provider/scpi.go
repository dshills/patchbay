package provider

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"patchbay/internal/action"
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

type scpiDeviceState struct {
	settings SCPIDevice
	slot     chan struct{}
	mu       sync.Mutex
	health   Health
	used     bool
}
type SCPI struct {
	devices map[string]*scpiDeviceState
	closed  atomic.Bool
}

func NewSCPI(devices map[string]SCPIDevice) *SCPI {
	s := &SCPI{devices: make(map[string]*scpiDeviceState, len(devices))}
	for id, d := range devices {
		s.devices[id] = &scpiDeviceState{settings: d, slot: make(chan struct{}, 1), health: Health{Code: "not_checked"}}
	}
	return s
}
func (s *SCPI) Health(id string) Health {
	d := s.devices[id]
	if d == nil {
		return Health{Code: "not_configured"}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.health
}
func (s *SCPI) Run(ctx context.Context, request SCPIRequest, budget *Budget, observe func(SCPIObservation)) (action.Result, error) {
	return s.run(ctx, request, budget, observe, false)
}
func (s *SCPI) run(ctx context.Context, request SCPIRequest, budget *Budget, observe func(SCPIObservation), shutdown bool) (result action.Result, err error) {
	result = action.Result{Status: action.Failed, Message: "Instrument operation failed.", Data: map[string]any{"device": request.Device, "operation": request.Operation, "channel": request.Channel}}
	d := s.devices[request.Device]
	if d == nil || !d.settings.Supports(request.Operation, request.Channel) || !validSCPIValue(d.settings, request) {
		return result, fault.New(protocol.InvalidRequest, "Invalid instrument operation, channel or value.")
	}
	// Total cap includes waiting for another operation on this device.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case d.slot <- struct{}{}:
	case <-ctx.Done():
		return result, fault.Safe(ctx.Err())
	}
	defer func() { <-d.slot }()
	if s.closed.Load() && !shutdown {
		return result, fault.New(protocol.ShuttingDown, "Instrument provider is shutting down.")
	}
	values := map[string]any{}
	defer func() {
		err = scpiError(ctx, err)
		d.mu.Lock()
		d.health = Health{Available: err == nil}
		if err != nil {
			d.health.Code = string(fault.Safe(err).Code)
		}
		d.mu.Unlock()
		if err == nil {
			result.Status, result.Message = action.Success, "Instrument readback completed."
		}
		if len(values) > 0 {
			result.Data["observed"] = values
		}
		if observe != nil {
			observe(SCPIObservation{Device: request.Device, Channel: request.Channel, Values: values, Err: err})
		}
	}()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	dialTimeout, _ := time.ParseDuration(d.settings.DialTimeout)
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", d.settings.Address)
	if err != nil {
		return result, err
	}
	defer func() { _ = conn.Close() }()
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(stopped) })
	defer func() {
		if !stop() {
			<-stopped
		}
	}()
	ioTimeout, _ := time.ParseDuration(d.settings.IOTimeout)
	w := &scpiWire{conn: conn, reader: bufio.NewReader(conn), ctx: ctx, timeout: ioTimeout, limit: d.settings.MaxResponseBytes}
	id, err := w.query("*IDN?")
	if err != nil {
		return result, err
	}
	parts := strings.Split(id, ",")
	if len(parts) != 4 || !strings.EqualFold(strings.TrimSpace(parts[0]), "RIGOL TECHNOLOGIES") || strings.TrimSpace(parts[1]) != d.settings.Model || d.settings.Firmware != "" && strings.TrimSpace(parts[3]) != d.settings.Firmware {
		return result, fault.New(protocol.PermissionDenied, "Instrument identity does not match its configured profile, model or firmware.")
	}
	result.Data["model"], result.Data["firmware"] = d.settings.Model, strings.TrimSpace(parts[3])
	d.mu.Lock()
	d.used = true
	d.mu.Unlock()
	if request.Operation == "scope.capture" {
		var capture map[string]any
		capture, err = scopeCapture(w, request.Channel)
		if err == nil {
			if !reserveSCPIResult(budget, capture) {
				return result, fault.New(protocol.ExecutionFailed, "Waveform exceeds the remaining job output budget.")
			}
			result.Data["waveform"] = capture
		}
	} else {
		err = generatorOperation(w, d.settings, request, values)
	}
	return result, err
}
func validSCPIValue(d SCPIDevice, r SCPIRequest) bool {
	kind, _, lo, hi := d.ValueSpec(r.Operation)
	switch kind {
	case "float":
		v, ok := r.Value.(float64)
		return ok && finite(v) && v >= lo && v <= hi
	case "boolean":
		_, ok := r.Value.(bool)
		return ok
	default:
		return r.Value == nil
	}
}

func (s *SCPI) Close(ctx context.Context) error {
	if s.closed.Swap(true) {
		return nil
	}
	var wg sync.WaitGroup
	errorsOut := make(chan error, len(s.devices))
	for id, d := range s.devices {
		d.mu.Lock()
		used := d.used
		d.mu.Unlock()
		if !used || d.settings.Shutdown != "output_off" {
			continue
		}
		wg.Go(func() {
			var shutdownErr error
			for channel := 1; channel <= 2; channel++ {
				_, err := s.run(ctx, SCPIRequest{Device: id, Operation: "generator.disable", Channel: channel}, NewBudget(1024), nil, true)
				shutdownErr = errors.Join(shutdownErr, err)
			}
			if shutdownErr != nil {
				errorsOut <- shutdownErr
			}
		})
	}
	wg.Wait()
	close(errorsOut)
	var result error
	for err := range errorsOut {
		result = errors.Join(result, err)
	}
	return result
}
