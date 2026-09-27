package provider

import (
	"errors"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"patchbay/internal/permission"
)

type SCPIDevice struct {
	Provider         string     `yaml:"provider"`
	Transport        string     `yaml:"transport"`
	Address          string     `yaml:"address"`
	Profile          string     `yaml:"profile"`
	Model            string     `yaml:"model"`
	Firmware         string     `yaml:"firmware,omitempty"`
	DialTimeout      string     `yaml:"dial_timeout,omitempty"`
	IOTimeout        string     `yaml:"io_timeout,omitempty"`
	MaxResponseBytes int        `yaml:"max_response_bytes,omitempty"`
	Shutdown         string     `yaml:"shutdown,omitempty"`
	Limits           SCPILimits `yaml:"limits,omitempty"`
}
type SCPILimits struct {
	FrequencyMinHz  float64 `yaml:"frequency_min_hz"`
	FrequencyMaxHz  float64 `yaml:"frequency_max_hz"`
	AmplitudeMinVPP float64 `yaml:"amplitude_min_vpp"`
	AmplitudeMaxVPP float64 `yaml:"amplitude_max_vpp"`
}

func (d *SCPIDevice) Normalize() error {
	bad := func() error { return errors.New("invalid SCPI device, profile, model, endpoint, limits or timeout") }
	if d.Provider != "scpi" || d.Transport != "tcp" {
		return bad()
	}
	host, port, err := net.SplitHostPort(d.Address)
	ip, ipErr := netip.ParseAddr(host)
	p, portErr := strconv.Atoi(port)
	if err != nil || ipErr != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || portErr != nil || p < 1 || p > 65535 {
		return bad()
	}
	d.Address = net.JoinHostPort(ip.Unmap().String(), strconv.Itoa(p))
	if len(d.Firmware) > 128 || strings.ContainsAny(d.Firmware, "\r\n\x00,") {
		return bad()
	}
	for _, timeout := range []*string{&d.DialTimeout, &d.IOTimeout} {
		if *timeout == "" {
			*timeout = "2s"
		}
		n, err := time.ParseDuration(*timeout)
		if err != nil || n < time.Millisecond || n > 30*time.Second {
			return bad()
		}
	}
	if d.MaxResponseBytes == 0 {
		d.MaxResponseBytes = 65536
	}
	if d.MaxResponseBytes < 1024 || d.MaxResponseBytes > 1<<20 {
		return bad()
	}
	if d.Shutdown == "" {
		d.Shutdown = "preserve"
	}
	if d.Shutdown != "preserve" && d.Shutdown != "output_off" {
		return bad()
	}
	switch d.Profile {
	case "rigol-dg800":
		if d.Model != "DG812" {
			return bad()
		}
		l := d.Limits
		if !finite(l.FrequencyMinHz, l.FrequencyMaxHz, l.AmplitudeMinVPP, l.AmplitudeMaxVPP) || l.FrequencyMinHz < 1e-6 || l.FrequencyMaxHz > 10e6 || l.FrequencyMaxHz < l.FrequencyMinHz || l.AmplitudeMinVPP < 0.002 || l.AmplitudeMaxVPP > 10 || l.AmplitudeMaxVPP < l.AmplitudeMinVPP {
			return bad()
		}
	case "rigol-mho900":
		if d.Model != "MHO954" || d.Shutdown != "preserve" || d.Limits != (SCPILimits{}) {
			return bad()
		}
	default:
		return bad()
	}
	return nil
}

func finite(values ...float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func (d SCPIDevice) Supports(operation string, channel int) bool {
	if channel < 1 {
		return false
	}
	if d.Profile == "rigol-dg800" && channel <= 2 {
		switch operation {
		case "generator.inspect", "generator.set_frequency", "generator.set_amplitude", "generator.output", "generator.disable":
			return true
		}
	}
	return d.Profile == "rigol-mho900" && channel <= 4 && operation == "scope.capture"
}
func SCPIRisk(operation string) permission.Permission {
	if operation == "generator.output" {
		return permission.Dangerous
	}
	return permission.Confirm
}
func (d SCPIDevice) ValueSpec(operation string) (kind, unit string, lo, hi float64) {
	switch operation {
	case "generator.set_frequency":
		return "float", "Hz", d.Limits.FrequencyMinHz, d.Limits.FrequencyMaxHz
	case "generator.set_amplitude":
		return "float", "Vpp", d.Limits.AmplitudeMinVPP, d.Limits.AmplitudeMaxVPP
	case "generator.output":
		return "boolean", "", 0, 0
	}
	return "", "", 0, 0
}

type SCPIRequest struct {
	Device, Operation string
	Channel           int
	Value             any
}
type SCPIObservation struct {
	Device  string
	Channel int
	Values  map[string]any
	Err     error
}
