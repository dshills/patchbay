package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/internal/job"
	"patchbay/internal/provider"
	"patchbay/internal/scpitest"
	"patchbay/pkg/protocol"
)

func benchRuntime(t *testing.T, rounded bool) (*Runtime, *scpitest.Server, string) {
	t.Helper()
	g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.1, Round: rounded}
	return benchRuntimeHandler(t, g.Handle)
}
func benchRuntimeHandler(t *testing.T, handle func(string) string) (*Runtime, *scpitest.Server, string) {
	t.Helper()
	s := scpitest.New(t, handle)
	data, err := os.ReadFile("../../configs/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "~/.deckd/", dir+"/")
	text = strings.Replace(text, "127.0.0.1:5501", s.Address, 1)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := New(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = r.Close(ctx)
	})
	return r, s, path
}
func benchInvoke(t *testing.T, r *Runtime, name string, workflow bool) job.Job {
	t.Helper()
	h, err := r.Invoke(context.Background(), name, workflow, protocol.Invocation{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	j, err := r.Jobs().Wait(ctx, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func TestInstrumentDesiredObservedPermissionsAndPersistence(t *testing.T) {
	r, server, path := benchRuntime(t, false)
	if len(server.Commands()) != 0 {
		t.Fatal("startup contacted equipment")
	}
	p, err := r.SetParameter(context.Background(), "bench.frequency", float64(2000))
	if err != nil {
		t.Fatal(err)
	}
	if p.Synchronization.Status != "pending" || p.Synchronization.Observed != nil || len(server.Commands()) != 0 {
		t.Fatal(p)
	}
	if _, err := r.Invoke(context.Background(), "generator.frequency", false, protocol.Invocation{}); fault.Safe(err).Code != protocol.ConfirmationRequired {
		t.Fatal(err)
	}
	if _, err := r.Invoke(context.Background(), "generator.output", false, protocol.Invocation{Confirmed: true, Args: map[string]any{"value": true}}); fault.Safe(err).Code != protocol.PermissionDenied {
		t.Fatal(err)
	}
	if len(server.Commands()) != 0 {
		t.Fatal("denied requests contacted equipment")
	}
	if j := benchInvoke(t, r, "bench.prepare", true); j.State != job.Success {
		t.Fatal(j.Error)
	}
	p, _ = r.Parameter("bench.frequency")
	if p.Value != float64(2000) || p.Synchronization.Observed != float64(2000) || p.Synchronization.Status != "matched" {
		t.Fatal(p)
	}
	p.Instrument.Device = "mutated"
	*p.Synchronization.ObservedAt = time.Time{}
	p, _ = r.Parameter("bench.frequency")
	if p.Instrument.Device != "generator" || p.Synchronization.ObservedAt.IsZero() {
		t.Fatal("mutable metadata escaped")
	}
	before := len(server.Commands())
	if _, err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, _ = r.Parameter("bench.frequency")
	if p.Synchronization.Status != "unobserved" || p.Value != float64(2000) || len(server.Commands()) != before {
		t.Fatal(p)
	}
	text, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(text), "amplitude_max_vpp: 1", "amplitude_max_vpp: 2", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reload(context.Background()); fault.Safe(err).Code != protocol.InvalidConfig {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, text, 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close(context.Background()) }()
	p, _ = restarted.Parameter("bench.frequency")
	if p.Value != float64(2000) || p.Synchronization.Observed != nil || len(server.Commands()) != before {
		t.Fatal(p)
	}
}
func TestInstrumentRoundingDoesNotOverwriteDesired(t *testing.T) {
	r, _, _ := benchRuntime(t, true)
	_, err := r.SetParameter(context.Background(), "bench.frequency", float64(2000))
	if err != nil {
		t.Fatal(err)
	}
	j := benchInvoke(t, r, "generator.frequency", false)
	if j.State != job.Failed {
		t.Fatal(j)
	}
	p, _ := r.Parameter("bench.frequency")
	if p.Value != float64(2000) || p.Synchronization.Observed != 1999.75 || p.Synchronization.Status != "error" {
		t.Fatal(p)
	}
}

func TestInstrumentPinsDesiredValueAndDiscardsOldGenerationObservation(t *testing.T) {
	for _, reload := range []bool{false, true} {
		t.Run(map[bool]string{false: "desired changes", true: "reload"}[reload], func(t *testing.T) {
			g := &scpitest.Generator{Frequency: 1000, Amplitude: 0.1}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			r, server, _ := benchRuntimeHandler(t, func(c string) string {
				if c == "*IDN?" {
					once.Do(func() { close(entered); <-release })
				}
				return g.Handle(c)
			})
			var unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(release) }) })
			if _, err := r.SetParameter(context.Background(), "bench.frequency", float64(2000)); err != nil {
				t.Fatal(err)
			}
			h, err := r.Invoke(context.Background(), "generator.frequency", false, protocol.Invocation{Confirmed: true})
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			if reload {
				_, err = r.Reload(context.Background())
			} else {
				_, err = r.SetParameter(context.Background(), "bench.frequency", float64(3000))
			}
			if err != nil {
				t.Fatal(err)
			}
			unblock.Do(func() { close(release) })
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			j, err := r.Jobs().Wait(ctx, h.ID)
			if err != nil || j.State != job.Success {
				t.Fatal(j, err)
			}
			if !strings.Contains(strings.Join(server.Commands(), "\n"), ":SOUR1:FREQ 2000") {
				t.Fatal("desired value was not pinned")
			}
			p, _ := r.Parameter("bench.frequency")
			if reload {
				if p.Synchronization.Status != "unobserved" || p.Synchronization.Observed != nil {
					t.Fatal(p)
				}
			} else if p.Value != float64(3000) || p.Synchronization.Status != "different" || p.Synchronization.Observed != float64(2000) {
				t.Fatal(p)
			}
		})
	}
}

func TestInstrumentDesiredEditInvalidatesControlConfirmation(t *testing.T) {
	r, server, _ := benchRuntime(t, false)
	guard := snapshotControls(t, r).Guard
	payload := protocol.ControlPayload{Control: "bench.frequency", Guard: &guard}
	_, err := r.Control(context.Background(), controlRequest(t, event.ControlPressed, payload))
	known := fault.Safe(err)
	if known.Code != protocol.ConfirmationRequired || known.Confirmation == nil {
		t.Fatal(err)
	}
	payload.Confirmation = known.Confirmation.Token
	if _, err := r.SetParameter(context.Background(), "bench.frequency", float64(2000)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Control(context.Background(), controlRequest(t, event.ControlPressed, payload)); err == nil {
		t.Fatal("stale confirmation accepted")
	}
	if len(server.Commands()) != 0 || len(r.Jobs().List()) != 0 {
		t.Fatal("stale confirmation reached equipment")
	}
}

func TestLateOldInstrumentObservationInvalidatesNewReadback(t *testing.T) {
	r, _, _ := benchRuntime(t, false)
	old := r.generation
	if _, err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := benchInvoke(t, r, "generator.inspect", false); j.State != job.Success {
		t.Fatal(j)
	}
	p, _ := r.Parameter("bench.frequency")
	if p.Synchronization.Status != "matched" {
		t.Fatal(p)
	}
	r.observeInstrument(old, provider.SCPIObservation{Device: "generator", Channel: 1, Values: map[string]any{"generator.set_frequency": float64(900)}})
	p, _ = r.Parameter("bench.frequency")
	if p.Synchronization.Status != "unobserved" || p.Synchronization.Observed != nil || p.Value != float64(1000) {
		t.Fatal(p)
	}
}
