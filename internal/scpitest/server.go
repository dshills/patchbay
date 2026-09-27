// Package scpitest provides loopback-only instrument simulators for integration tests.
package scpitest

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type Server struct {
	Address  string
	mu       sync.Mutex
	commands []string
}

// New deliberately fragments every response. A reply of !disconnect closes the
// connection without a response. Handler calls may run concurrently.
func New(t testing.TB, handle func(string) string) *Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Address: l.Addr().String()}
	var mu sync.Mutex
	closed := false
	connections := map[net.Conn]bool{}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			connections[conn] = true
			mu.Unlock()
			wg.Go(func() {
				defer func() { _ = conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				reader := bufio.NewScanner(conn)
				for reader.Scan() {
					command := reader.Text()
					s.mu.Lock()
					s.commands = append(s.commands, command)
					s.mu.Unlock()
					reply := handle(command)
					if reply == "!disconnect" {
						return
					}
					for len(reply) > 0 {
						n := min(3, len(reply))
						if _, err := conn.Write([]byte(reply[:n])); err != nil {
							return
						}
						reply = reply[n:]
					}
				}
			})
		}
	})
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		closed = true
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return s
}
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.commands...)
}

type Generator struct {
	mu                   sync.Mutex
	Frequency, Amplitude float64
	Output               [2]bool
	Replies              map[string]string
	Round                bool
	Reject               bool
}

func (g *Generator) Handle(command string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if reply, ok := g.Replies[command]; ok {
		return reply
	}
	switch command {
	case "*IDN?":
		return "RIGOL TECHNOLOGIES,DG812,TEST,00.02.06\n"
	case ":SYST:ERR?":
		if g.Reject {
			return "-222,\"Rejected\"\n"
		}
		return "0,\"No error\"\n"
	}
	for ch := 1; ch <= 2; ch++ {
		source, output := fmt.Sprintf(":SOUR%d", ch), fmt.Sprintf(":OUTP%d", ch)
		if strings.HasPrefix(command, fmt.Sprintf(":COUP%d:", ch)) && strings.HasSuffix(command, ":STAT?") {
			return "OFF\n"
		}
		switch command {
		case source + ":TRACK?":
			return "OFF\n"
		case output + "?":
			if g.Output[ch-1] {
				return "ON\n"
			}
			return "OFF\n"
		case output + " ON":
			g.Output[ch-1] = true
			return ""
		case output + " OFF":
			g.Output[ch-1] = false
			return ""
		case output + ":LOAD?":
			return "9.900000E+37\n"
		case source + ":FUNC?":
			return "SIN\n"
		case source + ":VOLT:UNIT?":
			return "VPP\n"
		case source + ":VOLT:UNIT VPP":
			return ""
		case source + ":VOLT:OFFS?":
			return "0\n"
		case source + ":MOD:STAT?", source + ":SWE:STAT?", source + ":BURS:STAT?", source + ":SUM:STAT?", source + ":HARM:STAT?":
			return "OFF\n"
		case source + ":FREQ?":
			return fmt.Sprintf("%.9g\n", g.Frequency)
		case source + ":VOLT?":
			return fmt.Sprintf("%.9g\n", g.Amplitude)
		}
		for suffix, destination := range map[string]*float64{":FREQ ": &g.Frequency, ":VOLT ": &g.Amplitude} {
			if value, ok := strings.CutPrefix(command, source+suffix); ok {
				n, err := strconv.ParseFloat(value, 64)
				if err != nil {
					return "!disconnect"
				}
				if g.Round {
					n -= 0.25
				}
				*destination = n
				return ""
			}
		}
	}
	return "-113,\"Unknown command\"\n"
}
func Scope(command string) string {
	switch command {
	case "*IDN?":
		return "RIGOL TECHNOLOGIES,MHO954,TEST,01.00\n"
	case ":SYST:ERR?":
		return "0,\"No error\"\n"
	case ":TRIG:STAT?":
		return "STOP\n"
	case ":WAV:PRE?":
		return "0,0,3,1,0.001,-0.1,0,0.01,0,127\n"
	case ":WAV:DATA?":
		return "#13\x7e\x7f\x80\n"
	}
	if strings.HasPrefix(command, ":WAV:") && !strings.Contains(command, "?") {
		return ""
	}
	return "-113,\"Unknown command\"\n"
}
