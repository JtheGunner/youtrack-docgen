// Package version reports the version and commit of the running binary.
package version

import (
	"fmt"
	"runtime/debug"
)

// Version and Commit are set at release time through the linker, for example
// -ldflags "-X github.com/JtheGunner/youtrack-docgen/internal/version.Version=v1.2.3".
// When unset, String falls back to the build information embedded by the Go
// toolchain, which covers `go install module@version` and local builds.
var (
	Version string
	Commit  string
)

const (
	unknownVersion = "dev"
	unknownCommit  = "unknown"
	shortCommitLen = 12
)

// String returns "<version> (commit <sha>)".
func String() string {
	info, _ := debug.ReadBuildInfo()
	return format(Version, Commit, info)
}

func format(version, commit string, info *debug.BuildInfo) string {
	if info != nil {
		if version == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		if commit == "" {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					commit = setting.Value
				}
			}
		}
	}
	if version == "" {
		version = unknownVersion
	}
	if commit == "" {
		commit = unknownCommit
	}
	if len(commit) > shortCommitLen {
		commit = commit[:shortCommitLen]
	}
	return fmt.Sprintf("%s (commit %s)", version, commit)
}
