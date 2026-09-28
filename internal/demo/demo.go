// Package demo supplies a bounded, offline measurement workload for first use.
package demo

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"sort"
	"time"
)

type Measurement struct {
	Value    float64 `json:"value"`
	Unit     string  `json:"unit"`
	Quantity string  `json:"quantity"`
	Repeats  int     `json:"repeats"`
	Spread   float64 `json:"spread"`
}
type Envelope struct {
	SchemaVersion int            `json:"schema_version"`
	Data          map[string]any `json:"data"`
}

func Run(iterations, repeats int) (Envelope, error) {
	if iterations < 1 || iterations > 100000 || repeats < 2 || repeats > 20 {
		return Envelope{}, fmt.Errorf("iterations must be 1–100000 and repeats 2–20")
	}
	samples := make([]float64, repeats)
	var data [32]byte
	deadline := time.Now().Add(10 * time.Second)
	for i := range repeats {
		start := time.Now()
		for j := range iterations {
			binary.LittleEndian.PutUint64(data[:8], uint64(j))
			data = sha256.Sum256(data[:])
			if j%1024 == 0 && time.Now().After(deadline) {
				return Envelope{}, fmt.Errorf("demo exceeded its 10-second limit")
			}
		}
		samples[i] = float64(time.Since(start).Nanoseconds()) / 1e6
	}
	sort.Float64s(samples)
	median := samples[len(samples)/2]
	if len(samples)%2 == 0 {
		median = (samples[len(samples)/2-1] + median) / 2
	}
	low, high := samples[0], samples[len(samples)-1]
	return Envelope{SchemaVersion: 1, Data: map[string]any{
		"duration": Measurement{Value: median, Unit: "ms", Quantity: "duration", Repeats: repeats, Spread: math.Max(0, high-low)},
		"host":     fmt.Sprintf("%s/%s; %d logical CPUs; %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version()),
		"work":     fmt.Sprintf("%d SHA-256 iterations per repeat; checksum prefix %x", iterations, data[:4]),
	}}, nil
}
