package buildinfo

import (
	"runtime"
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	withBuildInfo := func(mainVersion string, settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: mainVersion}, Settings: settings}, true
		}
	}
	noBuildInfo := func() (*debug.BuildInfo, bool) { return nil, false }

	tests := []struct {
		name         string
		ver, rev     string
		read         func() (*debug.BuildInfo, bool)
		wantVersion  string
		wantRevision string
	}{
		{
			name: "ldflags win", ver: "v0.1.0", rev: "abc123",
			read:        withBuildInfo("v9.9.9", debug.BuildSetting{Key: "vcs.revision", Value: "zzz"}),
			wantVersion: "0.1.0", wantRevision: "abc123",
		},
		{
			name: "go install module version",
			read: withBuildInfo("v0.2.0", debug.BuildSetting{Key: "vcs.revision", Value: "def456"},
				debug.BuildSetting{Key: "vcs.modified", Value: "false"}),
			wantVersion: "0.2.0", wantRevision: "def456",
		},
		{
			name: "local build with dirty tree",
			read: withBuildInfo("(devel)", debug.BuildSetting{Key: "vcs.revision", Value: "def456"},
				debug.BuildSetting{Key: "vcs.modified", Value: "true"}),
			wantVersion: DevVersion, wantRevision: "def456-dirty",
		},
		{
			name: "nothing known", read: noBuildInfo,
			wantVersion: DevVersion, wantRevision: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(tt.ver, tt.rev, tt.read)
			if got.Version != tt.wantVersion || got.Revision != tt.wantRevision {
				t.Fatalf("resolve() = %+v, want version %q revision %q", got, tt.wantVersion, tt.wantRevision)
			}
			if got.GoVersion != runtime.Version() || got.Platform != runtime.GOOS+"/"+runtime.GOARCH {
				t.Fatalf("resolve() Go fields = %+v", got)
			}
		})
	}
}

func TestGet(t *testing.T) {
	if got := Get(); got.Version == "" || got.Revision == "" {
		t.Fatalf("Get() = %+v, want non-empty version and revision", got)
	}
}
