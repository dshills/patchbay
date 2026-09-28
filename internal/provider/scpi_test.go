package provider

import (
	"context"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"patchbay/internal/fault"
	"patchbay/internal/scpitest"
	"patchbay/pkg/protocol"
)

func scpiFixture(t *testing.T, address string) SCPIDevice {
	t.Helper()
	d := SCPIDevice{Provider: "scpi", Transport: "tcp", Profile: "rigol-dg800", Model: "DG812", Address: address, IOTimeout: "100ms", Limits: SCPILimits{FrequencyMinHz: 1, FrequencyMaxHz: 100000, AmplitudeMinVPP: 0.01, AmplitudeMaxVPP: 2}}
	if err := d.Normalize(); err != nil {
		t.Fatal(err)
	}
	return d
}
func TestSCPIGeneratorReadbackAndOutputPolicy(t *testing.T) {
	g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.5}
	server := scpitest.New(t, g.Handle)
	s := NewSCPI(map[string]SCPIDevice{"gen": scpiFixture(t, server.Address)})
	if s.Health("gen").Available {
		t.Fatal("uncontacted instrument healthy")
	}
	for _, r := range []SCPIRequest{{"gen", "generator.set_frequency", 1, float64(2000)}, {"gen", "generator.set_amplitude", 1, 0.25}, {"gen", "generator.output", 1, true}, {"gen", "generator.disable", 1, nil}} {
		result, err := s.Run(context.Background(), r, NewBudget(4096), nil)
		if err != nil || result.Status != "success" {
			t.Fatal(result, err)
		}
		if !s.Health("gen").Available {
			t.Fatal(s.Health("gen"))
		}
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := len(server.Commands())
	if _, err := s.Run(context.Background(), SCPIRequest{"gen", "generator.inspect", 1, nil}, NewBudget(100), nil); fault.Safe(err).Code != protocol.ShuttingDown {
		t.Fatal(err)
	}
	if len(server.Commands()) != before {
		t.Fatal("shutdown allowed I/O")
	}
}

func TestSCPIRejectsUnsafeOrUnsupportedStates(t *testing.T) {
	for _, tc := range []struct {
		name, command, reply, operation string
		value                           any
		code                            protocol.Code
	}{
		{"wrong model", "*IDN?", "RIGOL TECHNOLOGIES,DG832,TEST,01\n", "generator.inspect", nil, protocol.PermissionDenied},
		{"malformed", "*IDN?", "bad\n", "generator.inspect", nil, protocol.PermissionDenied},
		{"oversize", "*IDN?", strings.Repeat("x", 4098) + "\n", "generator.inspect", nil, protocol.ExecutionFailed},
		{"disconnect", "*IDN?", "!disconnect", "generator.inspect", nil, protocol.ProviderUnavailable},
		{"timeout", "*IDN?", "", "generator.inspect", nil, protocol.Timeout},
		{"error queue", ":SYST:ERR?", "-222,\"bad\"\n", "generator.set_frequency", float64(2000), protocol.ExecutionFailed},
		{"other channel on", ":OUTP2?", "ON\n", "generator.set_frequency", float64(2000), protocol.PermissionDenied},
		{"unit", ":SOUR1:VOLT:UNIT?", "VRMS\n", "generator.output", true, protocol.PermissionDenied},
		{"offset", ":SOUR1:VOLT:OFFS?", "1\n", "generator.output", true, protocol.PermissionDenied},
		{"load", ":OUTP1:LOAD?", "50\n", "generator.output", true, protocol.PermissionDenied},
		{"mode", ":SOUR1:SUM:STAT?", "ON\n", "generator.output", true, protocol.PermissionDenied},
		{"tracking", ":SOUR2:TRACK?", "INV\n", "generator.set_frequency", float64(2000), protocol.PermissionDenied},
		{"coupling", ":COUP2:AMPL:STAT?", "ON\n", "generator.output", true, protocol.PermissionDenied},
		{"high amplitude", ":SOUR1:VOLT?", "20\n", "generator.output", true, protocol.PermissionDenied},
		{"nonfinite", ":SOUR1:FREQ?", "NaN\n", "generator.inspect", nil, protocol.ExecutionFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.5, Replies: map[string]string{tc.command: tc.reply}}
			server := scpitest.New(t, g.Handle)
			s := NewSCPI(map[string]SCPIDevice{"gen": scpiFixture(t, server.Address)})
			_, err := s.Run(context.Background(), SCPIRequest{"gen", tc.operation, 1, tc.value}, NewBudget(4096), nil)
			if err == nil || fault.Safe(err).Code != tc.code {
				t.Fatal(err)
			}
			for _, c := range server.Commands() {
				if strings.HasSuffix(c, " ON") || strings.HasPrefix(c, ":SOUR1:FREQ ") {
					t.Fatal("unsafe write", c)
				}
			}
		})
	}
}
func TestSCPIRoundedSettingsAndNoAutomaticReplay(t *testing.T) {
	g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.5, Round: true}
	server := scpitest.New(t, g.Handle)
	s := NewSCPI(map[string]SCPIDevice{"gen": scpiFixture(t, server.Address)})
	var observation SCPIObservation
	_, err := s.Run(context.Background(), SCPIRequest{"gen", "generator.set_frequency", 1, float64(2000)}, NewBudget(4096), func(o SCPIObservation) { observation = o })
	if err == nil || observation.Values["generator.set_frequency"] != 1999.75 {
		t.Fatal(observation, err)
	}
	writes := 0
	for _, c := range server.Commands() {
		if c == ":SOUR1:FREQ 2000" {
			writes++
		}
	}
	if writes != 1 {
		t.Fatal(writes)
	}
	if _, err := s.Run(context.Background(), SCPIRequest{"gen", "generator.inspect", 1, nil}, NewBudget(4096), nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range server.Commands() {
		if strings.HasSuffix(c, " ON") {
			t.Fatal("reconnect enabled output")
		}
	}
}
func TestSCPIValueValidationBeforeNetwork(t *testing.T) {
	s := NewSCPI(map[string]SCPIDevice{"gen": scpiFixture(t, "127.0.0.1:1")})
	for _, r := range []SCPIRequest{{"missing", "generator.inspect", 1, nil}, {"gen", "raw", 1, nil}, {"gen", "generator.output", 1, "ON"}, {"gen", "generator.set_frequency", 1, math.NaN()}, {"gen", "generator.set_amplitude", 1, float64(100)}, {"gen", "generator.inspect", 3, nil}} {
		if _, err := s.Run(context.Background(), r, NewBudget(100), nil); fault.Safe(err).Code != protocol.InvalidRequest {
			t.Fatal(err)
		}
	}
}
func TestSCPIScopeBinaryFramingAndBounds(t *testing.T) {
	for _, bad := range []string{"", "#0abc\n", "#9009999999", "#13\x7f\n", "#13\x7e\x7f\x80junk\n"} {
		t.Run(bad, func(t *testing.T) {
			server := scpitest.New(t, func(c string) string {
				if c == ":WAV:DATA?" && bad != "" {
					return bad
				}
				return scpitest.Scope(c)
			})
			d := scpiFixture(t, server.Address)
			d.Profile = "rigol-mho900"
			d.Model = "MHO954"
			d.Limits = SCPILimits{}
			s := NewSCPI(map[string]SCPIDevice{"scope": d})
			result, err := s.Run(context.Background(), SCPIRequest{"scope", "scope.capture", 1, nil}, NewBudget(4096), nil)
			if bad != "" {
				if err == nil {
					t.Fatal("accepted malformed block")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wave := result.Data["waveform"].(map[string]any)
			samples := wave["samples_v"].([]float64)
			if len(samples) != 3 || samples[0] != -0.01 || samples[2] != 0.01 {
				t.Fatal(wave)
			}
			if _, err := s.Run(context.Background(), SCPIRequest{"scope", "scope.capture", 1, nil}, NewBudget(1), nil); err == nil {
				t.Fatal("ignored output budget")
			}
		})
	}
}
func TestSCPICancellationAndSerialization(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.5}
	server := scpitest.New(t, func(c string) string {
		if c == "*IDN?" {
			once.Do(func() { close(entered); <-release })
		}
		return g.Handle(c)
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	d := scpiFixture(t, server.Address)
	d.IOTimeout = "5s"
	s := NewSCPI(map[string]SCPIDevice{"gen": d})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(ctx, SCPIRequest{"gen", "generator.inspect", 1, nil}, NewBudget(4096), nil)
		done <- err
	}()
	<-entered
	queued, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err := s.Run(queued, SCPIRequest{"gen", "generator.inspect", 1, nil}, NewBudget(4096), nil); fault.Safe(err).Code != protocol.Timeout {
		t.Fatal(err)
	}
	if len(server.Commands()) != 1 {
		t.Fatal("concurrent request bypassed device slot")
	}
	cancel()
	select {
	case err := <-done:
		if fault.Safe(err).Code != protocol.Cancelled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation hung")
	}
	close(release)
	if _, err := s.Run(context.Background(), SCPIRequest{"gen", "generator.inspect", 1, nil}, NewBudget(4096), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSCPIShutdownPoliciesAndPartialFailure(t *testing.T) {
	for _, policy := range []string{"preserve", "output_off", "output_off_error", "unused"} {
		t.Run(policy, func(t *testing.T) {
			g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.5}
			server := scpitest.New(t, g.Handle)
			d := scpiFixture(t, server.Address)
			if policy != "preserve" {
				d.Shutdown = "output_off"
			}
			s := NewSCPI(map[string]SCPIDevice{"gen": d})
			if policy != "unused" {
				if _, err := s.Run(context.Background(), SCPIRequest{"gen", "generator.inspect", 1, nil}, NewBudget(4096), nil); err != nil {
					t.Fatal(err)
				}
			}
			// No requests are active while changing the simulator's front-panel state.
			g.Output = [2]bool{true, true}
			g.Reject = policy == "output_off_error"
			before := len(server.Commands())
			err := s.Close(context.Background())
			if (err != nil) != g.Reject {
				t.Fatal(err)
			}
			commands := strings.Join(server.Commands()[before:], "\n")
			if policy == "preserve" || policy == "unused" {
				if commands != "" {
					t.Fatal(commands)
				}
			} else if !strings.Contains(commands, ":OUTP1 OFF") || !strings.Contains(commands, ":OUTP2 OFF") {
				t.Fatal("did not attempt both channels", commands)
			}
		})
	}
}

func TestSCPIUncertainWriteIsNotReplayed(t *testing.T) {
	g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.5}
	var disconnect sync.Once
	server := scpitest.New(t, func(c string) string {
		reply := g.Handle(c)
		if c == ":SOUR1:FREQ 2000" {
			disconnect.Do(func() { reply = "!disconnect" })
		}
		return reply
	})
	s := NewSCPI(map[string]SCPIDevice{"gen": scpiFixture(t, server.Address)})
	_, err := s.Run(context.Background(), SCPIRequest{"gen", "generator.set_frequency", 1, float64(2000)}, NewBudget(4096), nil)
	if fault.Safe(err).Code != protocol.ProviderUnavailable {
		t.Fatal(err)
	}
	result, err := s.Run(context.Background(), SCPIRequest{"gen", "generator.inspect", 1, nil}, NewBudget(4096), nil)
	if err != nil || result.Data["observed"].(map[string]any)["generator.set_frequency"] != float64(2000) {
		t.Fatal(result, err)
	}
	if strings.Count(strings.Join(server.Commands(), "\n"), ":SOUR1:FREQ 2000") != 1 {
		t.Fatal(server.Commands())
	}
}

func TestSCPIIndependentDevicesAndBoundedShutdown(t *testing.T) {
	g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.5}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	slow := scpitest.New(t, func(c string) string {
		if c == ":OUTP1?" {
			once.Do(func() { close(entered); <-release })
		}
		return g.Handle(c)
	})
	t.Cleanup(func() { close(release) })
	fast := scpitest.New(t, (&scpitest.Generator{Frequency: 1000, Amplitude: 0.5}).Handle)
	d := scpiFixture(t, slow.Address)
	d.Shutdown, d.IOTimeout = "output_off", "5s"
	s := NewSCPI(map[string]SCPIDevice{"slow": d, "fast": scpiFixture(t, fast.Address)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(ctx, SCPIRequest{"slow", "generator.inspect", 1, nil}, NewBudget(4096), nil)
		done <- err
	}()
	<-entered
	if _, err := s.Run(context.Background(), SCPIRequest{"fast", "generator.inspect", 1, nil}, NewBudget(4096), nil); err != nil {
		t.Fatal(err)
	}
	shutdown, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := s.Close(shutdown); fault.Safe(err).Code != protocol.Timeout {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; fault.Safe(err).Code != protocol.Cancelled {
		t.Fatal(err)
	}
}

func TestSCPIFirmwarePinAndScopePreconditions(t *testing.T) {
	server := scpitest.New(t, (&scpitest.Generator{}).Handle)
	d := scpiFixture(t, server.Address)
	d.Firmware = "other"
	s := NewSCPI(map[string]SCPIDevice{"gen": d})
	if _, err := s.Run(context.Background(), SCPIRequest{"gen", "generator.disable", 1, nil}, NewBudget(4096), nil); fault.Safe(err).Code != protocol.PermissionDenied {
		t.Fatal(err)
	}
	if len(server.Commands()) != 1 {
		t.Fatal(server.Commands())
	}
	for _, tc := range []struct{ command, reply string }{
		{":TRIG:STAT?", "RUN\n"}, {":WAV:PRE?", "0,0,3,1,NaN,0,0,1,0,0\n"},
		{":WAV:PRE?", "0,0,1001,1,1,0,0,1,0,0\n"}, {":WAV:PRE?", "0,0,3,1.5,1,0,0,1,0,0\n"},
		{":WAV:PRE?", "0,0,4,1,1,0,0,1,0,0\n"},
	} {
		server := scpitest.New(t, func(c string) string {
			if c == tc.command {
				return tc.reply
			}
			return scpitest.Scope(c)
		})
		d := scpiFixture(t, server.Address)
		d.Profile, d.Model, d.Limits = "rigol-mho900", "MHO954", SCPILimits{}
		s := NewSCPI(map[string]SCPIDevice{"scope": d})
		if _, err := s.Run(context.Background(), SCPIRequest{"scope", "scope.capture", 1, nil}, NewBudget(4096), nil); err == nil {
			t.Fatal(tc)
		}
	}
}

func TestSCPITextWhitespaceAndCRLF(t *testing.T) {
	pad := func(reply string) string {
		if reply == "" {
			return ""
		}
		return " \t" + strings.TrimSuffix(reply, "\n") + " \t\r\n"
	}
	g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.5}
	server := scpitest.New(t, func(c string) string { return pad(g.Handle(c)) })
	s := NewSCPI(map[string]SCPIDevice{"gen": scpiFixture(t, server.Address)})
	for _, r := range []SCPIRequest{{"gen", "generator.set_amplitude", 1, 0.25}, {"gen", "generator.output", 1, true}, {"gen", "generator.disable", 1, nil}} {
		if _, err := s.Run(context.Background(), r, NewBudget(4096), nil); err != nil {
			t.Fatal(err)
		}
	}
	scope := scpitest.New(t, func(c string) string {
		reply := scpitest.Scope(c)
		if c == ":WAV:DATA?" {
			return strings.TrimSuffix(reply, "\n") + "\r\n"
		}
		return pad(reply)
	})
	d := scpiFixture(t, scope.Address)
	d.Profile, d.Model, d.Limits = "rigol-mho900", "MHO954", SCPILimits{}
	s = NewSCPI(map[string]SCPIDevice{"scope": d})
	if _, err := s.Run(context.Background(), SCPIRequest{"scope", "scope.capture", 1, nil}, NewBudget(4096), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSCPIScientificNotationReadbackMatchesExactly(t *testing.T) {
	g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.1, Replies: map[string]string{":SOUR1:FREQ?": "2.000000000E+03\n", ":SOUR1:VOLT?": "1.000000000E-01\n"}}
	server := scpitest.New(t, g.Handle)
	s := NewSCPI(map[string]SCPIDevice{"gen": scpiFixture(t, server.Address)})
	for _, r := range []SCPIRequest{{"gen", "generator.set_frequency", 1, float64(2000)}, {"gen", "generator.set_amplitude", 1, 0.1}} {
		result, err := s.Run(context.Background(), r, NewBudget(4096), nil)
		if err != nil || result.Data["observed"].(map[string]any)[r.Operation] != r.Value {
			t.Fatal(result, err)
		}
	}
}

func TestScopePostTransferStatePreservesSuspectEvidence(t *testing.T) {
	for _, final := range []string{"STOP\n", "RUN\n", "UNKNOWN\n", "!disconnect"} {
		t.Run(final, func(t *testing.T) {
			var queries atomic.Int32
			server := scpitest.New(t, func(command string) string {
				if command == ":TRIG:STAT?" && queries.Add(1) == 3 {
					return final
				}
				return scpitest.Scope(command)
			})
			d := scpiFixture(t, server.Address)
			d.Profile = "rigol-mho900"
			d.Model = "MHO954"
			d.Limits = SCPILimits{}
			p := NewSCPI(map[string]SCPIDevice{"scope": d})
			result, err := p.Run(context.Background(), SCPIRequest{"scope", "scope.capture", 1, nil}, NewBudget(8192), nil)
			if (err == nil) != (final == "STOP\n") {
				t.Fatal("unexpected final-state decision", err)
			}
			wave, ok := result.Data["waveform"].(map[string]any)
			if !ok {
				t.Fatal("lost bounded diagnostic waveform")
			}
			series := wave["series"].(protocol.Series)
			if series.X[0] != -0.1 || series.Y[0] != -0.01 || len(series.X) != 3 {
				t.Fatal("bad scaling", series)
			}
			if (series.Quality == "valid") != (final == "STOP\n") {
				t.Fatal("suspect trace accepted")
			}
			observation := result.Data["instrument"].(*protocol.InstrumentObservation)
			if observation.Model != "MHO954" || observation.Firmware != "01.00" || observation.Waveform.AcquisitionTime != "unknown" || observation.Waveform.Before.State != "STOP" || observation.Waveform.After.ObservedAt.Before(observation.Waveform.Before.ObservedAt) {
				t.Fatal(observation)
			}
			commands := server.Commands()
			for i, command := range commands {
				if command == ":WAV:DATA?" && (i == 0 || i+1 >= len(commands) || commands[i-1] != ":TRIG:STAT?" || commands[i+1] != ":TRIG:STAT?") {
					t.Fatal("transfer not bracketed by observations", commands)
				}
				if command == ":STOP" || command == ":RUN" || command == ":SING" {
					t.Fatal("automatic acquisition", commands)
				}
			}
		})
	}
}
