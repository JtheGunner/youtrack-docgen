package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/filter"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

const fixtures = "testdata/manifests"

var collectedKinds = []model.Kind{model.KindNamespace, model.KindWorkload, model.KindService, model.KindIngress}

func collect(t *testing.T, paths ...string) *model.Graph {
	t.Helper()
	g := model.NewGraph()
	if err := NewFromPaths(paths...).WithWarnings(io.Discard).Collect(context.Background(), g); err != nil {
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
	tests := []struct {
		name  string
		setup func(t *testing.T) string
		want  string
	}{
		{"missing path", func(t *testing.T) string { return filepath.Join(t.TempDir(), "nope") }, "no such file"},
		{"nameless service", func(t *testing.T) string {
			return write(t, map[string]string{"a.yaml": "apiVersion: v1\nkind: Service\n"})
		}, "no name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := model.NewGraph()
			err := NewFromPaths(tt.setup(t)).WithWarnings(io.Discard).Collect(context.Background(), g)
			if err == nil {
				t.Fatal("Collect() error = nil")
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
		{"valid", map[string]any{"paths": []any{map[string]any{"path": "./k8s", "cluster": "test"}}}, false},
		{"plain string entry", map[string]any{"paths": []any{"./k8s"}}, true},
		{"missing cluster", map[string]any{"paths": []any{map[string]any{"path": "./k8s"}}}, true},
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

func TestCollectMapsNodesAndPinnedWorkloads(t *testing.T) {
	g := collect(t, "testdata/nodes")

	node, ok := g.Entity("kubernetes:host:node-a")
	if !ok {
		t.Fatal("node-a host missing")
	}
	if node.Name != "node-a" || !reflect.DeepEqual(node.Aliases, []string{"node-a.example.com"}) ||
		!reflect.DeepEqual(node.Addresses, []netip.Addr{netip.MustParseAddr("192.0.2.21")}) {
		t.Errorf("node-a = %+v", node)
	}
	if _, ok := g.Entity("kubernetes:host:node-lost"); !ok {
		t.Error("manifest node without workloads must still be a host")
	}
	if _, ok := g.Entity("kubernetes:host:node-ghost"); !ok {
		t.Error("node referenced only by a nodeSelector must exist as a name-only host")
	}

	wantRuns := map[model.ID]model.ID{
		"kubernetes:workload:shop/deployment/api":   "kubernetes:host:node-a",
		"kubernetes:workload:shop/deployment/batch": "kubernetes:host:node-ghost",
	}
	for w, h := range wantRuns {
		rel := model.Relation{From: w, To: h, Kind: model.RelRunsOn}
		if !slices.Contains(g.Relations(), rel) {
			t.Errorf("relation missing: %+v", rel)
		}
	}

	svc, _ := g.Entity("kubernetes:service:shop/edge")
	if got := svc.Attrs[model.AttrExternalIPs]; got != "192.0.2.21" {
		t.Errorf("external_ips = %q, want only the valid external IP", got)
	}
	for _, e := range g.ByKind(model.KindHost) {
		for k, v := range e.Attrs {
			if strings.Contains(k+v, "FAKE-LABEL-SECRET") {
				t.Errorf("node label leaked into %s", e.ID)
			}
		}
	}
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func collectWithWarnings(t *testing.T, paths ...string) (*model.Graph, string) {
	t.Helper()
	var warnings strings.Builder
	g := model.NewGraph()
	if err := NewFromPaths(paths...).WithWarnings(&warnings).Collect(context.Background(), g); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return g, warnings.String()
}

const (
	webDeployment = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: shop\nspec:\n  replicas: 2\n"
	webPatch      = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: shop\nspec:\n  replicas: 5\n"
)

func TestCollectToleratesGitOpsInputs(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a-base/deploy.yaml":             webDeployment,
		"b-overlay/patch.yaml":           webPatch,
		"c-helm/templates/svc.yaml":      "apiVersion: v1\nkind: Service\nmetadata:\n  name: {{ .Release.Name }}\n",
		"d-patch/json6902.yaml":          "- op: replace\n  path: /spec/replicas\n  value: 3\n",
		"e-crd/widget.yaml":              "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\nspec:\n  replicas: [1, 2]\n  template: 7\n",
		"f-quoted/deploy.yaml":           "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: quoted\nspec:\n  replicas: \"3\"\n",
		"g-ns/ns1.yaml":                  "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n",
		"g-ns/ns2.yaml":                  "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n",
		"h-kustomize/kustomization.yaml": "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: [deploy.yaml]\n",
	})
	g, warnings := collectWithWarnings(t, dir)

	web, ok := g.Entity("kubernetes:workload:shop/deployment/web")
	if !ok || web.Attrs["replicas"] != "2" {
		t.Errorf("first duplicate must win: %+v", web)
	}
	quoted, ok := g.Entity("kubernetes:workload:default/deployment/quoted")
	if !ok || quoted.Attrs["replicas"] != "3" {
		t.Errorf("string replicas = %+v", quoted)
	}
	if got := len(g.ByKind(model.KindNamespace)); got != 2 { // shop, default
		t.Errorf("namespaces = %d, want 2", got)
	}
	for _, want := range []string{
		filepath.Join("b-overlay", "patch.yaml"), "duplicate Deployment shop/web",
		filepath.Join("c-helm", "templates", "svc.yaml"),
		filepath.Join("d-patch", "json6902.yaml"),
		filepath.Join("g-ns", "ns2.yaml"), "duplicate Namespace shop",
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings missing %q:\n%s", want, warnings)
		}
	}
	if strings.Contains(warnings, "widget.yaml") || strings.Contains(warnings, "no Kubernetes objects") {
		t.Errorf("unexpected warnings:\n%s", warnings)
	}
}

func TestCollectFollowsSymlinkedRoot(t *testing.T) {
	target := writeTree(t, map[string]string{"deploy.yaml": webDeployment})
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	g, warnings := collectWithWarnings(t, link)
	if _, ok := g.Entity("kubernetes:workload:shop/deployment/web"); !ok {
		t.Errorf("symlinked path yielded no objects; warnings:\n%s", warnings)
	}
}

func TestCollectWarnsAboutPathWithoutObjects(t *testing.T) {
	dir := writeTree(t, map[string]string{"values.yaml": "replicaCount: 2\n"})
	g, warnings := collectWithWarnings(t, dir)
	if len(allEntities(g)) != 0 {
		t.Error("expected no entities")
	}
	if !strings.Contains(warnings, dir) || !strings.Contains(warnings, "no Kubernetes objects") {
		t.Errorf("warnings = %q", warnings)
	}
}

func labelManifests() string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: shop\n" +
		"  labels:\n    team: payments\n    namespace: spoofed\n    api-token: FAKE-SECRET-LABEL-7\n" +
		"  annotations:\n    note: FAKE-SECRET-ANNOTATION-8\n---\n" +
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: batch\n  namespace: shop\n  labels:\n    team: data\n"
}

func TestCollectTakesOverLabelsAndLabelRulesFilter(t *testing.T) {
	g, _ := collectWithWarnings(t, writeTree(t, map[string]string{"d.yaml": labelManifests()}))
	web, _ := g.Entity("kubernetes:workload:shop/deployment/web")
	if web.Labels["label.team"] != "payments" || web.Labels["namespace"] != "shop" {
		t.Errorf("labels = %v", web.Labels)
	}

	f, err := filter.New(filter.Config{Collectors: map[string]filter.Rules{
		Source: {Deny: []filter.Match{{Kinds: []string{"workload"}, Labels: map[string]string{"label.team": "data"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Entity("kubernetes:workload:shop/deployment/batch"); ok {
		t.Error("label deny rule did not drop the batch workload")
	}
	if _, ok := out.Entity("kubernetes:workload:shop/deployment/web"); !ok {
		t.Error("label deny rule dropped the wrong workload")
	}
}

func TestLabelsAndAnnotationsNeverLeakSecrets(t *testing.T) {
	g, _ := collectWithWarnings(t, writeTree(t, map[string]string{"d.yaml": labelManifests()}))
	f, err := filter.New(filter.Config{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range out.ByKind(model.KindWorkload) {
		if dump := fmt.Sprintf("%+v", e); strings.Contains(dump, "FAKE-SECRET") {
			t.Errorf("seeded secret reached filtered entity %s: %s", e.ID, dump)
		}
	}
}

func TestManifestLabelsCannotSpoofFilterDimensions(t *testing.T) {
	labels := "  labels:\n    tenant: acme\n    role: web\n    tag.public: \"true\"\n    namespace: spoofed\n"
	dir := writeTree(t, map[string]string{
		"d.yaml": "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n" + labels + "---\n" +
			"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: shop\n" + labels + "---\n" +
			"apiVersion: v1\nkind: Service\nmetadata:\n  name: web\n  namespace: shop\n" + labels + "---\n" +
			"apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: web\n  namespace: shop\n" + labels,
	})
	g, _ := collectWithWarnings(t, dir)
	for _, e := range allEntities(g) {
		for _, reserved := range []string{"tenant", "role", "tag.public"} {
			if _, ok := e.Labels[reserved]; ok {
				t.Errorf("%s: manifest label set reserved key %q: %v", e.ID, reserved, e.Labels)
			}
		}
		if ns, ok := e.Labels["namespace"]; ok && ns == "spoofed" {
			t.Errorf("%s: manifest label overrode namespace", e.ID)
		}
		if e.Labels["label.tenant"] != "acme" {
			t.Errorf("%s: manifest label not kept under prefix: %v", e.ID, e.Labels)
		}
	}

	// An allow rule on each reserved dimension must not admit any object.
	for _, m := range []filter.Match{
		{Tenants: []string{"acme"}}, {Roles: []string{"web"}},
		{Tags: []string{"public"}}, {Namespaces: []string{"spoofed"}},
	} {
		f, err := filter.New(filter.Config{Collectors: map[string]filter.Rules{Source: {Allow: []filter.Match{m}}}})
		if err != nil {
			t.Fatal(err)
		}
		out, err := f.Apply(g)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(allEntities(out)); n != 0 {
			t.Errorf("rule %+v admitted %d spoofed entities", m, n)
		}
	}
}

func TestCollectSkipsNamespaceWithoutName(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
	}{
		{"empty name", "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: \"\"\n"},
		{"missing name", "apiVersion: v1\nkind: Namespace\nmetadata:\n  labels:\n    team: a\n"},
		{"missing metadata", "apiVersion: v1\nkind: Namespace\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeTree(t, map[string]string{
				"a-bad.yaml":    tt.manifest,
				"b-deploy.yaml": webDeployment,
			})
			g, warnings := collectWithWarnings(t, dir)
			if !strings.Contains(warnings, "a-bad.yaml") || !strings.Contains(warnings, "Namespace without metadata.name") {
				t.Errorf("warnings = %q, want one naming a-bad.yaml", warnings)
			}
			want, _ := collectWithWarnings(t, writeTree(t, map[string]string{"deploy.yaml": webDeployment}))
			if got, wantN := len(allEntities(g)), len(allEntities(want)); got != wantN || got == 0 {
				t.Errorf("entities = %d, want %d (only the sibling deployment)", got, wantN)
			}
		})
	}
}

func TestCollectReadsLoadBalancerIPs(t *testing.T) {
	g := collect(t, filepath.Join("testdata", "linking"))
	svc, ok := g.Entity("kubernetes:service:shop/lb")
	if !ok {
		t.Fatal("service shop/lb missing")
	}
	if got := svc.Attrs[model.AttrExternalIPs]; got != "192.0.2.41,192.0.2.40" {
		t.Errorf("external_ips = %q, want the external IP and the load balancer IP once each", got)
	}
	for _, a := range []string{"10.96.0.30", "192.0.2.40", "192.0.2.41"} {
		if !slices.Contains(svc.Addresses, netip.MustParseAddr(a)) {
			t.Errorf("service addresses %v miss %s", svc.Addresses, a)
		}
	}
}
