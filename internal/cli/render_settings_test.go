package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

const youtrackConnection = "youtrack:\n  url: https://youtrack.example.com\n  token: ${YOUTRACK_TOKEN}\n  project: INFRA\n"

func TestGenerateUsesTheConfiguredRootArticleTitle(t *testing.T) {
	tests := []struct {
		name, sink, want string
	}{
		{"default", "", "# Infrastructure documentation"},
		{"configured", "  youtrack:\n    root_article: Platform docs\n", "# Platform docs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
			t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
			srv := fakeNetBox(t)
			out := filepath.Join(t.TempDir(), "out")
			cfg := youtrackConnection + generateConfig(srv.URL, out, "") + tt.sink

			if _, stderr, err := runGenerate(t, cfg); err != nil {
				t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
			}
			if got := string(readDir(t, out)["index.md"]); !strings.HasPrefix(got, tt.want+"\n") {
				t.Errorf("index.md does not start with %q:\n%s", tt.want, got)
			}
		})
	}
}

func TestGenerateWritesDiagramsAsTextUnlessMermaidIsConfigured(t *testing.T) {
	fence := strings.Repeat("`", 3) + "mermaid"
	tests := []struct {
		name, setting string
		wantBlock     bool
	}{
		{"default", "", false},
		{"text", "diagrams: text\n", false},
		{"mermaid", "diagrams: mermaid\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
			srv := fakeNetBox(t)
			out := filepath.Join(t.TempDir(), "out")

			if _, stderr, err := runGenerate(t, tt.setting+generateConfig(srv.URL, out, "")); err != nil {
				t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
			}
			found := false
			for _, body := range readDir(t, out) {
				found = found || strings.Contains(string(body), fence)
			}
			if found != tt.wantBlock {
				t.Errorf("mermaid block present = %v, want %v", found, tt.wantBlock)
			}
		})
	}
}

func TestDiffShowsTheConfiguredRootTitle(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	out := filepath.Join(t.TempDir(), "out")
	cfg := strings.Replace(diffConfig(nb.URL, yt.srv.URL, out), "  youtrack: {}\n", "  youtrack:\n    root_article: Platform docs\n", 1)

	stdout, stderr, err := runCLI(t, cfg, "diff")
	if err != nil {
		t.Fatalf("error = %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Title: Platform docs\n") {
		t.Errorf("diff lacks the configured root title:\n%s", stdout)
	}
}
