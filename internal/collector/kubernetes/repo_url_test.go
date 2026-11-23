package kubernetes

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

const repoURL = "https://git.example.com/ops/cluster"

func collectFromModule(t *testing.T, paths []any) *model.Graph {
	t.Helper()
	c, err := New(config.Module{Settings: map[string]any{"paths": paths}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	g := model.NewGraph()
	if err := c.WithWarnings(io.Discard).Collect(context.Background(), g); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return g
}

func TestRepoURLIsAttachedToObjectsOfItsPathOnly(t *testing.T) {
	g := collectFromModule(t, []any{
		map[string]any{"path": "testdata/nodes", "cluster": "test", "repo_url": repoURL},
		map[string]any{"path": "testdata/manifests", "cluster": "test"},
	})

	for _, id := range []model.ID{"kubernetes:host:node-a", "kubernetes:host:node-lost"} {
		e, ok := g.Entity(id)
		if !ok {
			t.Fatalf("entity %s missing", id)
		}
		if got := e.Attrs[model.AttrRepoURL]; got != repoURL {
			t.Errorf("%s repo_url = %q, want %q", id, got, repoURL)
		}
	}
	web, ok := g.Entity("kubernetes:workload:test/shop/deployment/web")
	if !ok {
		t.Fatal("workload from the plain path missing")
	}
	if got, has := web.Attrs[model.AttrRepoURL]; has {
		t.Errorf("object from a path without repo_url has repo_url %q", got)
	}
}

func TestRepoURLOnWorkloadsServicesAndIngresses(t *testing.T) {
	g := collectFromModule(t, []any{map[string]any{"path": fixtures, "cluster": "test", "repo_url": repoURL}})
	seen := 0
	for _, k := range []model.Kind{model.KindWorkload, model.KindService, model.KindIngress} {
		for _, e := range g.ByKind(k) {
			seen++
			if got := e.Attrs[model.AttrRepoURL]; got != repoURL {
				t.Errorf("%s repo_url = %q, want %q", e.ID, got, repoURL)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no workload, service or ingress collected")
	}
}

func TestNewRejectsMalformedPaths(t *testing.T) {
	tests := []struct {
		name  string
		paths any
	}{
		{"not a list", "testdata"},
		{"empty list", []any{}},
		{"entry of wrong type", []any{1}},
		{"map without path", []any{map[string]any{"repo_url": repoURL}}},
		{"unknown key", []any{map[string]any{"path": "x", "repo": repoURL}}},
		{"repo_url not http", []any{map[string]any{"path": "x", "repo_url": "git@example.com:ops/cluster.git"}}},
		{"repo_url not a string", []any{map[string]any{"path": "x", "repo_url": 7}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(config.Module{Settings: map[string]any{"paths": tt.paths}})
			if err == nil {
				t.Fatal("New() error = nil, want an error")
			}
			if strings.Contains(err.Error(), "git@example.com") {
				t.Errorf("error leaks the value: %v", err)
			}
		})
	}
}
