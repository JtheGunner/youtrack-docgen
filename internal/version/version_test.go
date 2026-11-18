package version

import (
	"runtime/debug"
	"testing"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		info    *debug.BuildInfo
		want    string
	}{
		{
			name:    "linker values win over build info",
			version: "v1.2.3",
			commit:  "abcdef0",
			info:    &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}},
			want:    "v1.2.3 (commit abcdef0)",
		},
		{
			name: "module version and vcs revision from build info",
			info: &debug.BuildInfo{
				Main:     debug.Module{Version: "v0.4.0"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "1234567890abcdef"}},
			},
			want: "v0.4.0 (commit 1234567890ab)",
		},
		{
			name: "devel module version is reported as dev",
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			want: "dev (commit unknown)",
		},
		{
			name: "no build info",
			want: "dev (commit unknown)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := format(tt.version, tt.commit, tt.info); got != tt.want {
				t.Errorf("format() = %q, want %q", got, tt.want)
			}
		})
	}
}
