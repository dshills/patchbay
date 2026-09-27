// Package version exposes build metadata shared by both executables.
package version

import "fmt"

// Set by -ldflags during release builds.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

func Current() Info { return Info{Version: Version, Commit: Commit, BuildTime: BuildTime} }

func (i Info) String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", i.Version, i.Commit, i.BuildTime)
}
