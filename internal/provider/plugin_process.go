package provider

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"

	"patchbay/internal/fault"
	wire "patchbay/pkg/plugin"
	"patchbay/pkg/protocol"
)

const maxPluginStderr = 64 << 10

type pluginProcess struct {
	cmd                              *exec.Cmd
	stdin, stdout, stderr            *os.File
	mu                               sync.Mutex
	pendingID, pendingType           string
	cancelling                       bool
	nextID                           uint64
	responses                        chan wire.Frame
	broken                           chan struct{}
	once                             sync.Once
	failure                          error
	closing                          atomic.Bool
	shutdownAck                      atomic.Bool
	stderrBytes                      atomic.Int64
	ioWG                             sync.WaitGroup
	readerDone, stderrDone, waitDone chan struct{}
	waitErr                          error
}

func pluginFailure() error {
	return fault.New(protocol.ExecutionFailed, "Plugin violated its protocol or output limits.")
}
func startPlugin(c PluginConfig) (*pluginProcess, error) {
	// Parent owns every pipe, including cleanup after descendants inherit them.
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, err
	}
	cmd := exec.CommandContext(context.Background(), c.Command, c.Args...)
	cmd.Dir, cmd.Env = c.Cwd, []string{}
	for k, v := range c.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	configureProcessGroup(cmd)
	err = cmd.Start()
	_ = inR.Close()
	_ = outW.Close()
	_ = errW.Close()
	if err != nil {
		_ = inW.Close()
		_ = outR.Close()
		_ = errR.Close()
		return nil, err
	}
	p := &pluginProcess{cmd: cmd, stdin: inW, stdout: outR, stderr: errR, responses: make(chan wire.Frame, 1), broken: make(chan struct{}), readerDone: make(chan struct{}), stderrDone: make(chan struct{}), waitDone: make(chan struct{})}
	go func() { p.waitErr = cmd.Wait(); close(p.waitDone) }()
	p.ioWG.Go(p.read)
	p.ioWG.Go(p.drainStderr)
	return p, nil
}
func (p *pluginProcess) fail(err error) {
	p.once.Do(func() {
		p.failure = err
		close(p.broken)
		killProcessGroup(p.cmd)
		p.closePipes()
	})
}
func (p *pluginProcess) closePipes() { _ = p.stdin.Close(); _ = p.stdout.Close(); _ = p.stderr.Close() }
func (p *pluginProcess) cleanup() {
	p.closing.Store(true)
	killProcessGroup(p.cmd)
	p.closePipes()
	<-p.waitDone
	p.ioWG.Wait()
}
func (p *pluginProcess) read() {
	defer close(p.readerDone)
	r := wire.NewReader(p.stdout)
	for {
		f, err := r.Read()
		if err != nil {
			if p.closing.Load() || errors.Is(err, io.EOF) && p.shutdownAck.Load() {
				return
			}
			p.fail(pluginFailure())
			return
		}
		p.mu.Lock()
		kind := p.pendingType
		valid := p.pendingID != "" && f.ID == p.pendingID && (f.Type == kind || kind == "execute" && (f.Type == "result" || p.cancelling && f.Type == "cancelled"))
		// Execute responses use result/cancelled, never a mirrored execute request.
		if kind == "execute" && f.Type == "execute" {
			valid = false
		}
		if valid {
			p.pendingID, p.pendingType = "", ""
		}
		p.mu.Unlock()
		if !valid || !validPluginResponse(f) {
			p.fail(pluginFailure())
			return
		}
		if f.Type == "shutdown" {
			if wire.Decode(f.Payload, &struct{}{}) != nil {
				p.fail(pluginFailure())
				return
			}
			p.shutdownAck.Store(true)
		}
		select {
		case p.responses <- f:
		default:
			p.fail(pluginFailure())
			return
		}
	}
}
func (p *pluginProcess) drainStderr() {
	defer close(p.stderrDone)
	buf := make([]byte, 4096)
	for {
		n, err := p.stderr.Read(buf)
		if p.stderrBytes.Add(int64(n)) > maxPluginStderr {
			p.fail(pluginFailure())
			return
		}
		if err != nil {
			return
		}
	}
}
func (p *pluginProcess) send(ctx context.Context, f wire.Frame) error {
	data, err := wire.Encode(f)
	if err != nil {
		return fault.New(protocol.InvalidRequest, "Plugin request exceeds the protocol frame limit.")
	}
	done := make(chan error, 1)
	p.ioWG.Go(func() {
		n, e := p.stdin.Write(data)
		if e == nil && n != len(data) {
			e = io.ErrShortWrite
		}
		done <- e
	})
	select {
	case err = <-done:
		if err != nil {
			p.fail(pluginFailure())
			return pluginFailure()
		}
		return nil
	case <-p.broken:
		return p.failure
	case <-ctx.Done():
		p.fail(fault.Safe(ctx.Err()))
		return fault.Safe(ctx.Err())
	}
}
func (p *pluginProcess) begin(ctx context.Context, kind string, payload any) error {
	if ctx.Err() != nil {
		return fault.Safe(ctx.Err())
	}
	p.mu.Lock()
	p.nextID++
	id := strconv.FormatUint(p.nextID, 10)
	p.pendingID, p.pendingType, p.cancelling = id, kind, false
	p.mu.Unlock()
	f, err := wire.NewFrame(id, kind, payload)
	if err != nil {
		return pluginFailure()
	}
	return p.send(ctx, f)
}
func (p *pluginProcess) receive(ctx context.Context) (wire.Frame, error) {
	select {
	case <-p.broken:
		return wire.Frame{}, p.failure
	default:
	}
	select {
	case f := <-p.responses:
		return f, nil
	case <-p.broken:
		return wire.Frame{}, p.failure
	case <-ctx.Done():
		return wire.Frame{}, fault.Safe(ctx.Err())
	}
}
func (p *pluginProcess) call(ctx context.Context, kind string, payload, destination any) error {
	if err := p.begin(ctx, kind, payload); err != nil {
		return err
	}
	f, err := p.receive(ctx)
	if err != nil {
		return err
	}
	if wire.Decode(f.Payload, destination) != nil {
		p.fail(pluginFailure())
		return pluginFailure()
	}
	return nil
}
func (p *pluginProcess) cancel(ctx context.Context) bool {
	p.mu.Lock()
	id := p.pendingID
	if p.pendingType != "execute" {
		p.mu.Unlock()
		return false
	}
	p.cancelling = true
	p.mu.Unlock()
	f, _ := wire.NewFrame(id, "cancel", struct{}{})
	if p.send(ctx, f) != nil {
		return false
	}
	reply, err := p.receive(ctx)
	if err != nil {
		return false
	}
	if reply.Type == "cancelled" && wire.Decode(reply.Payload, &struct{}{}) == nil {
		return true
	}
	// A final result may cross the cancel frame. It is not cooperative cancellation.
	return false
}
func (p *pluginProcess) shutdown(ctx context.Context) error {
	if err := p.call(ctx, "shutdown", struct{}{}, &struct{}{}); err != nil {
		return err
	}
	for _, done := range []chan struct{}{p.waitDone, p.readerDone, p.stderrDone} {
		select {
		case <-done:
		case <-p.broken:
			return p.failure
		case <-ctx.Done():
			return fault.Safe(ctx.Err())
		}
	}
	select {
	case <-p.broken:
		return p.failure
	default:
	}
	if p.waitErr != nil {
		return fault.New(protocol.ExecutionFailed, "Plugin exited unsuccessfully.")
	}
	return nil
}

func validPluginResponse(f wire.Frame) bool {
	switch f.Type {
	case "hello":
		var value wire.HelloResponse
		return wire.Decode(f.Payload, &value) == nil && value.SelectedVersion == wire.Version && len(value.Operations) > 0 && len(value.Operations) <= wire.MaxOperations
	case "health":
		var value wire.Health
		return wire.Decode(f.Payload, &value) == nil && value.Available != nil
	case "result":
		var value wire.Result
		return wire.Decode(f.Payload, &value) == nil && value.Text != nil && (value.Status == "success" || value.Status == "failed")
	case "cancelled", "shutdown":
		return wire.Decode(f.Payload, &struct{}{}) == nil
	}
	return false
}
