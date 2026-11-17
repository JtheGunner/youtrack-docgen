package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

const fixtures = "testdata/manifests"

var collectedKinds = []model.Kind{model.KindNamespace, model.KindWorkload, model.KindService, model.KindIngress}

func collect(t *testing.T, paths ...string) *model.Graph {
	t.Helper()
	g := model.NewGraph()
	if err := NewFromPaths(paths...).Collect(context.Background(), g); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return g
}

func allEntities(g *model.Graph) []model.Entity {
	var out []model.Entity
	for _, k := range collectedKinds {
		out = append(out, g.ByKind(k)...)
	}
	return out
}

func TestCollectMapsEntities(t *testing.T) {
	g := collect(t, fixtures)
	tests := []struct {
		id        model.ID
		name      string
		attrs     map[string]string
		aliases   []string
		addresses []string
	}{
		{id: "kubernetes:namespace:shop", name: "shop"},
		{id: "kubernetes:namespace:default", name: "default"},
		{
			id: "kubernetes:workload:shop/deployment/web", name: "web",
			attrs: map[string]string{
				"kind": "Deployment", "namespace": "shop", "replicas": "3",
				"images": "registry.example.com/shop/migrate:1.4.0, registry.example.com/shop/web:1.4.0",
			},
		},
		{
			id: "kubernetes:workload:default/statefulset/db", name: "db",
			attrs: map[string]string{"kind": "StatefulSet", "replicas": "1", "images": "registry.example.com/shop/db:16"},
		},
		{
			id: "kubernetes:workload:default/cronjob/backup", name: "backup",
			attrs: map[string]string{"kind": "CronJob", "schedule": "0 3 * * *", "images": "registry.example.com/shop/backup:2"},
		},
		{
			id: "kubernetes:service:shop/web", name: "web",
			attrs:     map[string]string{"type": "ClusterIP", "ports": "80/TCP->8080"},
			addresses: []string{"10.96.0.20"},
		},
		{
			id: "kubernetes:service:default/db", name: "db",
			attrs: map[string]string{"ports": "5432/TCP"},
		},
		{
			id: "kubernetes:ingress:shop/web", name: "web",
			attrs:   map[string]string{"class": "nginx", "hosts": "shop.example.com, www.shop.example.com"},
			aliases: []string{"shop.example.com", "www.shop.example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(string(tt.id), func(t *testing.T) {
			e, ok := g.Entity(tt.id)
			if !ok {
				t.Fatalf("entity %s missing", tt.id)
			}
			if e.Name != tt.name || e.Source != Source {
				t.Errorf("name/source = %q/%q", e.Name, e.Source)
			}
			for k, want := range tt.attrs {
				if got := e.Attrs[k]; got != want {
					t.Errorf("attr %s = %q, want %q", k, got, want)
				}
			}
			if !reflect.DeepEqual(e.Aliases, tt.aliases) {
				t.Errorf("aliases = %v, want %v", e.Aliases, tt.aliases)
			}
			var want []netip.Addr
			for _, a := range tt.addresses {
				want = append(want, netip.MustParseAddr(a))
			}
			if !reflect.DeepEqual(e.Addresses, want) {
				t.Errorf("addresses = %v, want %v", e.Addresses, want)
			}
		})
	}
	for kind, want := range map[model.Kind]int{
		model.KindNamespace: 2, model.KindWorkload: 3, model.KindService: 2, model.KindIngress: 1,
	} {
		if got := len(g.ByKind(kind)); got != want {
			t.Errorf("%s entities = %d, want %d", kind, got, want)
		}
	}
}

func TestCollectMapsRelations(t *testing.T) {
	g := collect(t, fixtures)
	want := []model.Relation{
		{From: "kubernetes:ingress:shop/web", To: "kubernetes:namespace:shop", Kind: model.RelMemberOf},
		{From: "kubernetes:ingress:shop/web", To: "kubernetes:service:shop/web", Kind: model.RelRoutesTo},
		{From: "kubernetes:service:shop/web", To: "kubernetes:namespace:shop", Kind: model.RelMemberOf},
		{From: "kubernetes:service:shop/web", To: "kubernetes:workload:shop/deployment/web", Kind: model.RelRoutesTo},
		{From: "kubernetes:service:default/db", To: "kubernetes:workload:default/statefulset/db", Kind: model.RelRoutesTo},
		{From: "kubernetes:workload:shop/deployment/web", To: "kubernetes:namespace:shop", Kind: model.RelMemberOf},
	}
	have := map[model.Relation]bool{}
	for _, r := range g.Relations() {
		have[r] = true
		if r.Kind == model.RelSameAs || !strings.HasPrefix(string(r.To), "kubernetes:") {
			t.Errorf("collector created a cross-source relation: %+v", r)
		}
	}
	for _, r := range want {
		if !have[r] {
			t.Errorf("relation missing: %+v", r)
		}
	}
	// The ingress backend "missing" has no manifest, so only one routes_to exists.
	if got := len(g.RelationsFrom("kubernetes:ingress:shop/web")); got != 2 {
		t.Errorf("ingress relations = %d, want 2", got)
	}
}

func TestCollectSkipsSecretsAndNeverLeaksValues(t *testing.T) {
	g := collect(t, fixtures)
	for _, e := range allEntities(g) {
		if strings.Contains(string(e.ID), "credentials") || strings.Contains(e.Name, "credentials") {
			t.Errorf("Secret mapped into the model: %s", e.ID)
		}
		if dump := fmt.Sprintf("%+v", e); strings.Contains(dump, "FAKE-SECRET") {
			t.Errorf("seeded secret reached entity %s: %s", e.ID, dump)
		}
	}
	for _, r := range g.Relations() {
		if strings.Contains(fmt.Sprintf("%+v", r), "FAKE-SECRET") {
			t.Errorf("seeded secret reached relation %+v", r)
		}
	}
}

func TestCollectSingleFileAndDeterminism(t *testing.T) {
	a := collect(t, filepath.Join(fixtures, "apps", "web.yaml"))
	if got := len(a.ByKind(model.KindWorkload)); got != 1 {
		t.Errorf("workloads = %d, want 1", got)
	}
	x, y := collect(t, fixtures), collect(t, fixtures)
	if !reflect.DeepEqual(allEntities(x), allEntities(y)) || !reflect.DeepEqual(x.Relations(), y.Relations()) {
		t.Error("two runs on the same input differ")
	}
}

func TestCollectErrors(t *testing.T) {
	write := func(t *testing.T, files map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	svc := "apiVersion: v1\nkind: Service\nmetadata:\n  name: a\n"
	tests := []struct {
		name  string
		setup func(t *testing.T) string
		want  string
		dup   bool
	}{
		{"missing path", func(t *testing.T) string { return filepath.Join(t.TempDir(), "nope") }, "no such file", false},
		{"invalid yaml", func(t *testing.T) string { return write(t, map[string]string{"bad.yaml": "kind: [\n"}) }, "bad.yaml", false},
		{"duplicate object", func(t *testing.T) string { return write(t, map[string]string{"a.yaml": svc, "b.yml": svc}) }, "", true},
		{"nameless service", func(t *testing.T) string {
			return write(t, map[string]string{"a.yaml": "apiVersion: v1\nkind: Service\n"})
		}, "no name", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := model.NewGraph()
			err := NewFromPaths(tt.setup(t)).Collect(context.Background(), g)
			if err == nil {
				t.Fatal("Collect() error = nil")
			}
			if tt.dup && !errors.Is(err, model.ErrDuplicateEntity) {
				t.Errorf("error = %v, want ErrDuplicateEntity", err)
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
			if len(allEntities(g)) != 0 {
				t.Error("graph was modified despite the error")
			}
		})
	}
}

func TestCollectHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewFromPaths(fixtures).Collect(ctx, model.NewGraph()); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestSkipsHiddenDirectoriesAndNonManifests(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		".git/x.yaml": "kind: [\n",
		"values.yaml": "replicaCount: 2\n",
		"notes.txt":   "not yaml: [\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(allEntities(collect(t, dir))); got != 0 {
		t.Errorf("entities = %d, want 0", got)
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		settings map[string]any
		wantErr  bool
	}{
		{"valid", map[string]any{"paths": []any{"./k8s"}}, false},
		{"missing", nil, true},
		{"empty list", map[string]any{"paths": []any{}}, true},
		{"not a list", map[string]any{"paths": "./k8s"}, true},
		{"non-string entry", map[string]any{"paths": []any{1}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(config.Module{Settings: tt.settings})
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && c.Name() != Source {
				t.Errorf("Name() = %q", c.Name())
			}
		})
	}
}
