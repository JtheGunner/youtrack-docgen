package config

import (
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
)

func TestRootTitleAndDiagramFormat(t *testing.T) {
	withoutRootArticle := strings.Replace(validYAML, "    root_article: Infrastructure\n", "    enabled: true\n", 1)
	tests := []struct {
		name        string
		yaml        string
		wantTitle   string
		wantDiagram render.DiagramFormat
	}{
		{"defaults", validYAML, "Infrastructure", ""},
		{"root article unset", withoutRootArticle, "", ""},
		{"mermaid", "diagrams: mermaid\n" + validYAML, "Infrastructure", render.DiagramMermaid},
		{"text", "diagrams: text\n" + validYAML, "Infrastructure", render.DiagramText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml), envMap(fullEnv))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got := cfg.RootTitle(); got != tt.wantTitle {
				t.Errorf("RootTitle() = %q, want %q", got, tt.wantTitle)
			}
			if got := cfg.DiagramFormat(); got != tt.wantDiagram {
				t.Errorf("DiagramFormat() = %q, want %q", got, tt.wantDiagram)
			}
		})
	}
}

func TestParseDiagramsInvalid(t *testing.T) {
	_, err := Parse([]byte("diagrams: svg\n"+validYAML), envMap(fullEnv))
	if err == nil || !strings.Contains(err.Error(), "diagrams: must be \"text\" or \"mermaid\"") {
		t.Errorf("Parse() error = %v", err)
	}
}

func TestParseKubernetesPathsWithRepoURL(t *testing.T) {
	const kube = "collectors:\n  kubernetes:\n    paths:\n      - ./plain\n      - path: ./gitops\n        repo_url: https://git.example.com/ops/gitops\nsinks:\n  markdown:\n    dir: ./out\n"
	cfg, err := Parse([]byte(kube), envMap(nil))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	got, err := ParsePathSources(cfg.Collectors["kubernetes"].Settings["paths"])
	if err != nil {
		t.Fatal(err)
	}
	want := []PathSource{{Path: "./plain"}, {Path: "./gitops", RepoURL: "https://git.example.com/ops/gitops"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("paths = %+v, want %+v", got, want)
	}
}

func TestParseKubernetesPathsInvalid(t *testing.T) {
	tests := []struct {
		name, paths, want string
	}{
		{"missing path", "[{repo_url: 'https://git.example.com/x'}]", "collectors.kubernetes.paths: entry 1: path must be a non-empty string"},
		{"unknown key", "[{path: ./a, repo: 'https://git.example.com/x'}]", "unknown key \"repo\""},
		{"repo url without scheme", "[{path: ./a, repo_url: git.example.com/x}]", "repo_url: must be an http(s) URL with a host"},
		{"ssh url", "[{path: ./a, repo_url: 'git@git.example.com:ops/x.git'}]", "repo_url: must be an http(s) URL with a host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := "collectors:\n  kubernetes:\n    paths: " + tt.paths + "\nsinks:\n  markdown:\n    dir: ./out\n"
			_, err := Parse([]byte(text), envMap(nil))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse() error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}
