// Package evidence stores bounded local run records independently of job history.
package evidence

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

const (
	Version          = 1
	MaxManifestBytes = 1 << 20
	MaxMetadataBytes = 8 << 20
	MaxArtifacts     = 32
	MaxPoints        = 10000
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func NewRequestID(now time.Time) string { return fmt.Sprintf("%d-%s", now.UnixMilli(), rand.Text()) }

func RequestTime(id string) (time.Time, error) {
	prefix, suffix, ok := strings.Cut(id, "-")
	n, err := strconv.ParseInt(prefix, 10, 64)
	if !ok || err != nil || len(prefix) != 13 || !identifier.MatchString(suffix) {
		return time.Time{}, fault.New(protocol.InvalidRequest, "Expected a timestamped request ID.")
	}
	return time.UnixMilli(n).UTC(), nil
}

func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func Clone[T any](value T) T {
	data, err := json.Marshal(value)
	if err != nil {
		panic("invalid evidence clone")
	}
	var copy T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&copy); err != nil {
		panic("invalid evidence clone")
	}
	return copy
}

func Terminal(state string) bool {
	switch state {
	case "success", "failed", "cancelled", "interrupted", "recording_failed":
		return true
	default:
		return false
	}
}

func ValidSeries(s protocol.Series) error {
	if s.SchemaVersion != Version || s.Name == "" || len(s.Name) > 128 || len(s.X) == 0 || len(s.X) != len(s.Y) || len(s.X) > MaxPoints || len(s.XUnit) > 32 || len(s.YUnit) > 32 || (s.Quality != "valid" && s.Quality != "suspect") {
		return fault.New(protocol.InvalidRequest, "Invalid series schema, units, quality or point count.")
	}
	for i := range s.X {
		if math.IsNaN(s.X[i]) || math.IsInf(s.X[i], 0) || math.IsNaN(s.Y[i]) || math.IsInf(s.Y[i], 0) || i > 0 && s.X[i] <= s.X[i-1] {
			return fault.New(protocol.InvalidRequest, "Series requires finite values and increasing coordinates.")
		}
	}
	return nil
}

func validateRun(r protocol.Run) error {
	if r.SchemaVersion != Version || r.Origin != "measured" || !identifier.MatchString(r.ID) || r.Experiment.SchemaVersion != Version || r.Experiment.ID == "" || r.CreatedAt.IsZero() || r.Parameters == nil || r.Measurements == nil || r.Artifacts == nil || len(r.Artifacts) > MaxArtifacts || !digestPattern.MatchString(r.RequestDigest) {
		return fault.New(protocol.InvalidRequest, "Invalid run manifest.")
	}
	if _, err := RequestTime(r.RequestID); err != nil {
		return err
	}
	if !Terminal(r.State) && r.State != "queued" && r.State != "running" {
		return fault.New(protocol.InvalidRequest, "Invalid run state.")
	}
	if Terminal(r.State) != (r.FinishedAt != nil) {
		return fault.New(protocol.InvalidRequest, "Run state and terminal timestamp disagree.")
	}
	seen := map[string]bool{}
	for _, a := range r.Artifacts {
		if a.SchemaVersion != Version || !identifier.MatchString(a.ID) || seen[a.ID] || !digestPattern.MatchString(a.SHA256) || a.Size < 0 || a.Size > 4<<20 {
			return fault.New(protocol.InvalidRequest, "Invalid artifact inventory.")
		}
		seen[a.ID] = true
	}
	for _, m := range r.Measurements {
		if m.Value != nil && (math.IsNaN(*m.Value) || math.IsInf(*m.Value, 0)) {
			return fault.New(protocol.InvalidRequest, "Measurement values must be finite.")
		}
	}
	return nil
}
