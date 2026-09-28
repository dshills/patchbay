package provider

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

func queryNumber(w *scpiWire, command string) (float64, error) {
	text, err := w.query(command)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || !finite(n) {
		return 0, scpiInvalid()
	}
	return n, nil
}
func queryBool(w *scpiWire, command string) (bool, error) {
	text, err := w.query(command)
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(text) {
	case "ON", "1":
		return true, nil
	case "OFF", "0":
		return false, nil
	}
	return false, scpiInvalid()
}
func instrumentErrors(w *scpiWire) error {
	text, err := w.query(":SYST:ERR?")
	if err != nil {
		return err
	}
	code, _, ok := strings.Cut(text, ",")
	n, err := strconv.Atoi(strings.TrimSpace(code))
	if !ok || err != nil {
		return scpiInvalid()
	}
	if n != 0 {
		return fault.New(protocol.ExecutionFailed, "Instrument error queue reports a rejected operation; inspect the front panel.")
	}
	return nil
}

func generatorOperation(w *scpiWire, d SCPIDevice, r SCPIRequest, observed map[string]any) error {
	source := fmt.Sprintf(":SOUR%d", r.Channel)
	output := fmt.Sprintf(":OUTP%d", r.Channel)
	if r.Operation == "generator.disable" {
		if err := w.write(output + " OFF"); err != nil {
			return err
		}
		on, err := queryBool(w, output+"?")
		if err != nil {
			return err
		}
		observed["generator.output"] = on
		if on {
			return fault.New(protocol.ExecutionFailed, "Instrument output did not turn off.")
		}
		return instrumentErrors(w)
	}
	if err := instrumentErrors(w); err != nil {
		return err
	}
	if r.Operation == "generator.set_frequency" || r.Operation == "generator.set_amplitude" {
		if err := generatorIndependentChannels(w); err != nil {
			return err
		}
		for ch := 1; ch <= 2; ch++ {
			on, err := queryBool(w, fmt.Sprintf(":OUTP%d?", ch))
			if err != nil {
				return err
			}
			if on {
				return fault.New(protocol.PermissionDenied, "Disable both generator outputs before changing a setting.")
			}
		}
		command := source + ":FREQ"
		if r.Operation == "generator.set_amplitude" {
			if err := w.write(source + ":VOLT:UNIT VPP"); err != nil {
				return err
			}
			command = source + ":VOLT"
		}
		if err := w.write(command + " " + strconv.FormatFloat(r.Value.(float64), 'g', -1, 64)); err != nil {
			return err
		}
		if err := instrumentErrors(w); err != nil {
			return err
		}
	}
	if r.Operation == "generator.output" {
		if r.Value.(bool) {
			if err := generatorEnableChecks(w, d, source, output); err != nil {
				return err
			}
		}
		state := "OFF"
		if r.Value.(bool) {
			state = "ON"
		}
		if err := w.write(output + " " + state); err != nil {
			return err
		}
		if err := instrumentErrors(w); err != nil {
			return err
		}
	}
	unit, err := w.query(source + ":VOLT:UNIT?")
	if err != nil {
		return err
	}
	if unit != "VPP" {
		return fault.New(protocol.ExecutionFailed, "Generator amplitude readback requires Vpp units.")
	}
	freq, err := queryNumber(w, source+":FREQ?")
	if err != nil {
		return err
	}
	amp, err := queryNumber(w, source+":VOLT?")
	if err != nil {
		return err
	}
	on, err := queryBool(w, output+"?")
	if err != nil {
		return err
	}
	observed["generator.set_frequency"], observed["generator.set_amplitude"], observed["generator.output"] = freq, amp, on
	if err := instrumentErrors(w); err != nil {
		return err
	}
	if r.Operation == "generator.output" && on != r.Value {
		return fault.New(protocol.ExecutionFailed, "Instrument output readback differs from the request.")
	}
	if r.Operation == "generator.set_frequency" || r.Operation == "generator.set_amplitude" {
		if observed[r.Operation] != r.Value {
			return fault.New(protocol.ExecutionFailed, "Instrument setting was rounded or rejected; inspect desired and observed values.")
		}
	}
	return nil
}

func generatorIndependentChannels(w *scpiWire) error {
	for ch := 1; ch <= 2; ch++ {
		track, err := w.query(fmt.Sprintf(":SOUR%d:TRACK?", ch))
		if err != nil {
			return err
		}
		if track != "OFF" {
			return fault.New(protocol.PermissionDenied, "Disable generator channel tracking before applying settings or enabling output.")
		}
		for _, property := range []string{"FREQ", "AMPL", "PHAS", "TRIG"} {
			on, err := queryBool(w, fmt.Sprintf(":COUP%d:%s:STAT?", ch, property))
			if err != nil {
				return err
			}
			if on {
				return fault.New(protocol.PermissionDenied, "Disable generator channel coupling before applying settings or enabling output.")
			}
		}
	}
	return instrumentErrors(w)
}

func generatorEnableChecks(w *scpiWire, d SCPIDevice, source, output string) error {
	if err := generatorIndependentChannels(w); err != nil {
		return err
	}
	unsafe := func() error {
		return fault.New(protocol.PermissionDenied, "Output enable requires a bounded sine waveform, Vpp, high-impedance load, zero offset and disabled modulation/sweep/burst/summing/harmonics.")
	}
	for _, check := range []struct{ command, want string }{{source + ":FUNC?", "SIN"}, {source + ":VOLT:UNIT?", "VPP"}} {
		got, err := w.query(check.command)
		if err != nil {
			return err
		}
		if got != check.want {
			return unsafe()
		}
	}
	for _, mode := range []string{":MOD:STAT?", ":SWE:STAT?", ":BURS:STAT?", ":SUM:STAT?", ":HARM:STAT?"} {
		on, err := queryBool(w, source+mode)
		if err != nil {
			return err
		}
		if on {
			return unsafe()
		}
	}
	offset, err := queryNumber(w, source+":VOLT:OFFS?")
	if err != nil {
		return err
	}
	if offset != 0 {
		return unsafe()
	}
	load, err := w.query(output + ":LOAD?")
	if err != nil {
		return err
	}
	if load != "INF" && load != "INFINITY" {
		n, e := strconv.ParseFloat(load, 64)
		if e != nil || !finite(n) || n < 1e30 {
			return unsafe()
		}
	}
	freq, err := queryNumber(w, source+":FREQ?")
	if err != nil {
		return err
	}
	amp, err := queryNumber(w, source+":VOLT?")
	if err != nil {
		return err
	}
	l := d.Limits
	if freq < l.FrequencyMinHz || freq > l.FrequencyMaxHz || amp < l.AmplitudeMinVPP || amp > l.AmplitudeMaxVPP {
		return unsafe()
	}
	return instrumentErrors(w)
}

func scopeCapture(w *scpiWire, channel int, observation *protocol.WaveformObservation) (map[string]any, error) {
	state, err := w.query(":TRIG:STAT?")
	if err != nil {
		return nil, err
	}
	observation.Before = acquisitionState(state)
	if state != "STOP" {
		return nil, fault.New(protocol.PermissionDenied, "Stop scope acquisition before capture.")
	}
	if err := instrumentErrors(w); err != nil {
		return nil, err
	}
	for _, command := range []string{fmt.Sprintf(":WAV:SOUR CHAN%d", channel), ":WAV:MODE NORM", ":WAV:FORM BYTE", ":WAV:POIN 1000", ":WAV:STAR 1", ":WAV:STOP 1000"} {
		if err := w.write(command); err != nil {
			return nil, err
		}
	}
	if err := instrumentErrors(w); err != nil {
		return nil, err
	}
	preamble, err := w.query(":WAV:PRE?")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(preamble, ",")
	if len(fields) != 10 {
		return nil, scpiInvalid()
	}
	var p [10]float64
	for i, text := range fields {
		n, e := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if e != nil || !finite(n) {
			return nil, scpiInvalid()
		}
		p[i] = n
	}
	if p[0] != 0 || p[1] != 0 || p[2] < 1 || p[2] > 1000 || p[2] != math.Trunc(p[2]) || p[3] < 1 || p[3] != math.Trunc(p[3]) || p[4] <= 0 || p[7] <= 0 {
		return nil, scpiInvalid()
	}
	observation.Preamble = append([]float64{}, p[:]...)
	// Settings/preamble queries may take time. Check immediately before the transfer.
	state, err = w.query(":TRIG:STAT?")
	observation.Before = acquisitionState(state)
	if err != nil {
		return nil, err
	}
	if state != "STOP" {
		return nil, fault.New(protocol.PermissionDenied, "Acquisition changed before transfer; stop it and review again.")
	}
	data, err := w.block(":WAV:DATA?", 1000)
	if err != nil {
		return nil, err
	}
	state, finalErr := w.query(":TRIG:STAT?")
	observation.After = acquisitionState(state)
	if finalErr == nil && state != "STOP" {
		finalErr = fault.New(protocol.ExecutionFailed, "Acquisition changed during transfer; the retained trace is suspect.")
	}
	if len(data) != int(p[2]) {
		return nil, scpiInvalid()
	}
	if finalErr == nil {
		finalErr = instrumentErrors(w)
	}
	samples := make([]float64, len(data))
	times := make([]float64, len(data))
	for i, value := range data {
		samples[i] = (float64(value) - p[8] - p[9]) * p[7]
		times[i] = (float64(i)-p[6])*p[4] + p[5]
		if !finite(samples[i]) || !finite(times[i]) {
			return nil, scpiInvalid()
		}
	}
	series := protocol.Series{SchemaVersion: 1, Name: "waveform", X: times, Y: samples, XUnit: "s", YUnit: "V", Quantity: "voltage", Quality: "valid"}
	if finalErr != nil {
		series.Quality, series.Reason = "suspect", "Post-transfer acquisition or instrument status was changed or unavailable."
	}
	return map[string]any{"series": series, "channel": channel, "samples_v": samples, "points": len(samples), "x_increment_s": p[4], "x_origin_s": p[5], "x_reference": p[6], "y_increment_v": p[7], "y_origin": p[8], "y_reference": p[9]}, finalErr
}
func acquisitionState(value string) protocol.AcquisitionState {
	if value != "STOP" && value != "RUN" && value != "WAIT" && value != "TD" && value != "AUTO" {
		value = "unknown"
	}
	return protocol.AcquisitionState{State: value, ObservedAt: time.Now().UTC()}
}
func reserveSCPIResult(b *Budget, value map[string]any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if int64(len(data)) > b.remaining {
		return false
	}
	b.remaining -= int64(len(data))
	return true
}
