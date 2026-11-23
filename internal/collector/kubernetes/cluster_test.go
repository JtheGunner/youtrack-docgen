package kubernetes

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/filter"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// shopManifests declares a namespace with one workload, the service that
// selects it and the ingress that routes to the service.
const shopManifests = "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n---\n" +
	"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: shop\n" +
	"spec:\n  replicas: 1\n  template:\n    metadata:\n      labels:\n        app: web\n---\n" +
	"apiVersion: v1\nkind: Service\nmetadata:\n  name: web\n  namespace: shop\nspec:\n  selector:\n    app: web\n---\n" +
	"apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: web\n  namespace: shop\n" +
	"spec:\n  rules:\n    - host: shop.example.com\n      http:\n        paths:\n          - backend:\n              service:\n                name: web\n"

// collectClusters collects one path per cluster, in the given order.
func collectClusters(t *testing.T, pathsByCluster ...[2]string) (*model.Graph, *Collector, string) {
	t.Helper()
	var sources []config.PathSource
	for _, pc := range pathsByCluster {
		sources = append(sources, config.PathSource{Path: pc[0], Cluster: pc[1]})
	}
	var warnings strings.Builder
	c := &Collector{paths: sources, warnings: &warnings}
	g := model.NewGraph()
	if err := c.Collect(context.Background(), g); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return g, c, warnings.String()
}

func TestClustersKeepIdenticallyNamedObjectsApart(t *testing.T) {
	dirA := writeTree(t, map[string]string{"shop.yaml": shopManifests})
	dirB := writeTree(t, map[string]string{"shop.yaml": shopManifests})
	g, c, warnings := collectClusters(t, [2]string{dirA, "alpha"}, [2]string{dirB, "beta"})
	if strings.Contains(warnings, "duplicate") || len(c.Incomplete()) != 0 {
		t.Errorf("objects of different clusters treated as duplicates: %s %v", warnings, c.Incomplete())
	}

	for _, cl := range []string{"alpha", "beta"} {
		clusterID := model.ID("kubernetes:cluster:" + cl)
		nsID := model.ID("kubernetes:namespace:" + cl + "/shop")
		workloadID := model.ID("kubernetes:workload:" + cl + "/shop/deployment/web")
		serviceID := model.ID("kubernetes:service:" + cl + "/shop/web")
		ingressID := model.ID("kubernetes:ingress:" + cl + "/shop/web")

		cluster, ok := g.Entity(clusterID)
		if !ok {
			t.Fatalf("cluster %s missing", clusterID)
		}
		if cluster.Name != cl || cluster.Source != Source || len(cluster.Labels) != 0 {
			t.Errorf("cluster = %+v, want name %q and no labels", cluster, cl)
		}
		names := map[model.ID]string{nsID: "shop", workloadID: "web", serviceID: "web", ingressID: "web"}
		for id, name := range names {
			e, ok := g.Entity(id)
			if !ok {
				t.Fatalf("entity %s missing", id)
			}
			if e.Name != name || e.Labels["namespace"] != "shop" || e.Attrs["cluster"] != cl {
				t.Errorf("%s: name %q, namespace label %q, cluster attr %q", id, e.Name, e.Labels["namespace"], e.Attrs["cluster"])
			}
		}
		want := []model.Relation{
			{From: nsID, To: clusterID, Kind: model.RelMemberOf},
			{From: workloadID, To: nsID, Kind: model.RelMemberOf},
			{From: serviceID, To: nsID, Kind: model.RelMemberOf},
			{From: ingressID, To: nsID, Kind: model.RelMemberOf},
			{From: serviceID, To: workloadID, Kind: model.RelRoutesTo},
			{From: ingressID, To: serviceID, Kind: model.RelRoutesTo},
		}
		for _, r := range want {
			if !slices.Contains(g.Relations(), r) {
				t.Errorf("relation missing: %+v", r)
			}
		}
	}
	// Routing never crosses a cluster boundary.
	for _, r := range g.Relations() {
		from, to := clusterOfKey(t, r.From), clusterOfKey(t, r.To)
		if from != "" && to != "" && from != to {
			t.Errorf("relation crosses clusters: %+v", r)
		}
	}
	if got := len(g.ByKind(model.KindCluster)); got != 2 {
		t.Errorf("clusters = %d, want 2", got)
	}
}

// clusterOfKey returns the cluster a cluster-scoped ID belongs to, or "".
func clusterOfKey(t *testing.T, id model.ID) string {
	t.Helper()
	_, kind, key, err := model.ParseID(id)
	if err != nil {
		t.Fatal(err)
	}
	switch kind {
	case model.KindCluster:
		return key
	case model.KindNamespace, model.KindWorkload, model.KindService, model.KindIngress:
		cluster, _, _ := strings.Cut(key, "/")
		return cluster
	}
	return ""
}

func TestSameObjectTwiceInOneClusterIsSkipped(t *testing.T) {
	dirA := writeTree(t, map[string]string{"shop.yaml": shopManifests})
	dirB := writeTree(t, map[string]string{"shop.yaml": shopManifests})
	g, c, warnings := collectClusters(t, [2]string{dirA, "alpha"}, [2]string{dirB, "alpha"})
	for _, want := range []string{
		"duplicate Namespace shop in cluster alpha", "duplicate Deployment shop/web in cluster alpha",
		"duplicate Service shop/web in cluster alpha", "duplicate Ingress shop/web in cluster alpha",
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings miss %q:\n%s", want, warnings)
		}
	}
	if len(c.Incomplete()) != 0 {
		t.Errorf("a skipped duplicate must not make the read incomplete: %v", c.Incomplete())
	}
	if got := len(g.ByKind(model.KindCluster)); got != 1 {
		t.Errorf("clusters = %d, want 1", got)
	}
	if got := len(g.ByKind(model.KindWorkload)); got != 1 {
		t.Errorf("workloads = %d, want 1", got)
	}
}

func TestImplicitNamespaceIsCreatedPerCluster(t *testing.T) {
	declared := writeTree(t, map[string]string{"shop.yaml": shopManifests})
	undeclared := writeTree(t, map[string]string{"deploy.yaml": webDeployment + "---\n" +
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: plain\n"})
	g, _, _ := collectClusters(t, [2]string{declared, "alpha"}, [2]string{undeclared, "beta"})

	for _, tt := range []struct{ ns, cluster model.ID }{
		{"kubernetes:namespace:beta/shop", "kubernetes:cluster:beta"},
		{"kubernetes:namespace:beta/default", "kubernetes:cluster:beta"},
		{"kubernetes:namespace:alpha/shop", "kubernetes:cluster:alpha"},
	} {
		ns, ok := g.Entity(tt.ns)
		if !ok {
			t.Fatalf("namespace %s missing", tt.ns)
		}
		if !slices.Contains(g.Relations(), model.Relation{From: ns.ID, To: tt.cluster, Kind: model.RelMemberOf}) {
			t.Errorf("%s is not a member of %s", tt.ns, tt.cluster)
		}
	}
	if _, ok := g.Entity("kubernetes:namespace:alpha/default"); ok {
		t.Error("namespace default created in a cluster without objects in it")
	}
	if _, ok := g.Entity("kubernetes:workload:beta/default/deployment/plain"); !ok {
		t.Error("workload without namespace not placed in the default namespace of its cluster")
	}
}

func TestClusterEntityRequiresAnObjectFromItsPaths(t *testing.T) {
	nodeOnly := writeTree(t, map[string]string{"node.yaml": "apiVersion: v1\nkind: Node\nmetadata:\n  name: node-x\n"})
	empty := writeTree(t, map[string]string{"values.yaml": "replicaCount: 2\n"})
	g, c, _ := collectClusters(t, [2]string{nodeOnly, "alpha"}, [2]string{empty, "beta"})
	if _, ok := g.Entity("kubernetes:cluster:alpha"); !ok {
		t.Error("cluster whose path holds only a Node must still exist")
	}
	if _, ok := g.Entity("kubernetes:cluster:beta"); ok {
		t.Error("cluster whose paths hold no object must not exist")
	}
	if len(c.Incomplete()) != 1 {
		t.Errorf("Incomplete() = %v, want exactly the empty path", c.Incomplete())
	}
}

// Seeded fake secrets in a Secret manifest, environment values, container
// arguments and annotations must never reach the graph or the filtered graph,
// whatever cluster the manifests are read for and whatever the rules remove.
func TestClusterCollectionNeverLeaksSeededSecrets(t *testing.T) {
	g, _, _ := collectClusters(t, [2]string{fixtures, "alpha"}, [2]string{fixtures, "beta"})
	assertNoSecret := func(t *testing.T, g *model.Graph) {
		t.Helper()
		for _, kind := range filter.AllKinds() {
			for _, e := range g.ByKind(kind) {
				if dump := fmt.Sprintf("%+v", e); strings.Contains(dump, "FAKE-SECRET") || strings.Contains(dump, "credentials") {
					t.Errorf("seeded secret reached entity %s: %s", e.ID, dump)
				}
			}
		}
		for _, r := range g.Relations() {
			if strings.Contains(fmt.Sprintf("%+v", r), "FAKE-SECRET") {
				t.Errorf("seeded secret reached relation %+v", r)
			}
		}
	}
	assertNoSecret(t, g)
	if len(g.ByKind(model.KindCluster)) != 2 {
		t.Fatalf("clusters = %d, want 2", len(g.ByKind(model.KindCluster)))
	}

	for _, rules := range []filter.Rules{
		{},
		{Deny: []filter.Match{{Namespaces: []string{"shop"}}}},
		{Allow: []filter.Match{{Kinds: []string{"cluster", "namespace"}}}},
		{Allow: []filter.Match{{Namespaces: []string{"shop"}}}},
		{Deny: []filter.Match{{Kinds: []string{"cluster"}}}},
	} {
		f, err := filter.New(filter.Config{Collectors: map[string]filter.Rules{Source: rules}})
		if err != nil {
			t.Fatal(err)
		}
		out, err := f.Apply(g)
		if err != nil {
			t.Fatal(err)
		}
		assertNoSecret(t, out)
	}
}
