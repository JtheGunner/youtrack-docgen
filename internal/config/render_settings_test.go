package config

import (
	"slices"
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

func TestParseKubernetesPathsWithCluster(t *testing.T) {
	const kube = "collectors:\n  kubernetes:\n    paths:\n      - {path: ./plain, cluster: prod-1}\n      - path: ./gitops\n        cluster: prod-1\n        repo_url: https://git.example.com/ops/gitops\n      - {path: ./other, cluster: staging.eu_1}\nsinks:\n  markdown:\n    dir: ./out\n"
	cfg, err := Parse([]byte(kube), envMap(nil))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	got, err := ParsePathSources(cfg.Collectors["kubernetes"].Settings["paths"])
	if err != nil {
		t.Fatal(err)
	}
	want := []PathSource{
		{Path: "./plain", Cluster: "prod-1"},
		{Path: "./gitops", Cluster: "prod-1", RepoURL: "https://git.example.com/ops/gitops"},
		{Path: "./other", Cluster: "staging.eu_1"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("paths = %+v, want %+v", got, want)
	}
}

func TestParseKubernetesPathsInvalid(t *testing.T) {
	tests := []struct {
		name, paths, want string
	}{
		{"missing path", "[{cluster: prod, repo_url: 'https://git.example.com/x'}]", "collectors.kubernetes.paths: entry 1: path must be a non-empty string"},
		{"missing cluster", "[{path: ./a}]", "collectors.kubernetes.paths: entry 1: cluster is required"},
		{"plain string entry", "[./a]", "collectors.kubernetes.paths: entry 1: must be a map"},
		{"unknown key", "[{path: ./a, cluster: prod, repo: 'https://git.example.com/x'}]", "unknown key \"repo\""},
		{"repo url without scheme", "[{path: ./a, cluster: prod, repo_url: git.example.com/x}]", "repo_url: must be an http(s) URL with a host"},
		{"ssh url", "[{path: ./a, cluster: prod, repo_url: 'git@git.example.com:ops/x.git'}]", "repo_url: must be an http(s) URL with a host"},
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

func TestParsePathSourcesRejectsInvalidClusterWithoutEchoingIt(t *testing.T) {
	for _, cluster := range []string{"", "Prod", "a:b", "a/b", "a b", "-a", "a-", "C0RP-SECRET"} {
		t.Run(cluster, func(t *testing.T) {
			_, err := ParsePathSources([]any{map[string]any{"path": "./a", "cluster": cluster}})
			if err == nil || !strings.Contains(err.Error(), "cluster") {
				t.Fatalf("ParsePathSources() error = %v, want a cluster error", err)
			}
			if cluster != "" && strings.Contains(err.Error(), cluster) {
				t.Errorf("error %q echoes the cluster value", err)
			}
		})
	}
	_, err := ParsePathSources([]any{map[string]any{"path": "./a", "cluster": 7}})
	if err == nil || !strings.Contains(err.Error(), "cluster") {
		t.Errorf("non-string cluster: error = %v", err)
	}
}

func TestParsePathSourcesDoesNotEchoPlainStringEntry(t *testing.T) {
	_, err := ParsePathSources([]any{"/secret/env/path"})
	if err == nil || strings.Contains(err.Error(), "/secret/env/path") {
		t.Errorf("error = %v, want a rejection that omits the value", err)
	}
}
