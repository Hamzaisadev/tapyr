// Package buildinfo reports the version, VCS revision and Go version of the
// running binary.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Set at build time with:
//
//	-ldflags "-X github.com/kentrow/tapyr/internal/buildinfo.version=0.1.0
//	          -X github.com/kentrow/tapyr/internal/buildinfo.revision=<sha>"
var (
	version  string
	revision string
)

// DevVersion is reported when no version was set at build time.
const DevVersion = "0.0.0-dev"

// Info describes the running binary.
type Info struct {
	// Version is a SemVer version without the "v" prefix.
	Version string
	// Revision is the VCS commit, with a "-dirty" suffix for modified trees.
	Revision string
	// GoVersion is the Go toolchain that built the binary.
	GoVersion string
	// Platform is GOOS/GOARCH.
	Platform string
}

// Get returns the build information, preferring values set with -ldflags and
// falling back to the information embedded by the Go toolchain.
func Get() Info {
	return resolve(version, revision, debug.ReadBuildInfo)
}

func resolve(ver, rev string, read func() (*debug.BuildInfo, bool)) Info {
	info := Info{
		Version:   strings.TrimPrefix(ver, "v"),
		Revision:  rev,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi, ok := read(); ok {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = strings.TrimPrefix(bi.Main.Version, "v")
		}
		if info.Revision == "" {
			info.Revision = vcsRevision(bi)
		}
	}
	if info.Version == "" {
		info.Version = DevVersion
	}
	if info.Revision == "" {
		info.Revision = "unknown"
	}
	return info
}

func vcsRevision(bi *debug.BuildInfo) string {
	var rev string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev != "" && modified {
		rev += "-dirty"
	}
	return rev
}
