package render

import (
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/testutil/golden"
	"github.com/JtheGunner/youtrack-docgen/model"
)

// clustersFixture is a graph with two Kubernetes clusters that both hold a
// namespace called "shop", a NetBox cluster that must not become a document and
// an empty namespace.
func clustersFixture(t *testing.T) *model.Graph {
	t.Helper()
	g := model.NewGraph()
	k := func(id, name string, kind model.Kind, attrs map[string]string, addrList ...string) model.Entity {
		return model.Entity{ID: model.ID(id), Kind: kind, Source: "kubernetes", Name: name, Attrs: attrs, Addresses: addrs(addrList...)}
	}
	entities := []model.Entity{
		k("kubernetes:cluster:alpha", "alpha", model.KindCluster, nil),
		k("kubernetes:cluster:beta", "beta", model.KindCluster, nil),
		{ID: "netbox:cluster:prod", Kind: model.KindCluster, Source: "netbox", Name: "prod"},
		k("kubernetes:namespace:alpha/shop", "shop", model.KindNamespace, map[string]string{"cluster": "alpha"}),
		k("kubernetes:namespace:alpha/empty", "empty", model.KindNamespace, map[string]string{"cluster": "alpha"}),
		k("kubernetes:namespace:beta/shop", "shop", model.KindNamespace, map[string]string{"cluster": "beta"}),
		k("kubernetes:workload:alpha/shop/deployment/web", "web", model.KindWorkload, map[string]string{
			"kind": "Deployment", "replicas": "3", "images": "registry.example.com/shop/web:1.4.0",
			model.AttrRepoURL: "https://u:FAKE-PASS@git.example.com/shop/manifests?private_token=FAKE-QUERY#frag",
		}),
		k("kubernetes:service:alpha/shop/web", "web-svc", model.KindService, map[string]string{"type": "ClusterIP", "ports": "80/TCP->8080"}, "10.96.0.20"),
		k("kubernetes:ingress:alpha/shop/web-ing", "web-ing", model.KindIngress, map[string]string{"hosts": "shop.example.com, www.example.com"}),
		k("kubernetes:workload:beta/shop/cronjob/nightly", "nightly", model.KindWorkload, map[string]string{"kind": "CronJob"}),
		k("kubernetes:host:node1", "node1", model.KindHost, map[string]string{"type": "kubernetes-node"}, "192.0.2.11"),
	}
	for _, e := range entities {
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []model.Relation{
		{From: "kubernetes:namespace:alpha/shop", To: "kubernetes:cluster:alpha", Kind: model.RelMemberOf},
		{From: "kubernetes:namespace:alpha/empty", To: "kubernetes:cluster:alpha", Kind: model.RelMemberOf},
		{From: "kubernetes:namespace:beta/shop", To: "kubernetes:cluster:beta", Kind: model.RelMemberOf},
		{From: "kubernetes:workload:alpha/shop/deployment/web", To: "kubernetes:namespace:alpha/shop", Kind: model.RelMemberOf},
		{From: "kubernetes:service:alpha/shop/web", To: "kubernetes:namespace:alpha/shop", Kind: model.RelMemberOf},
		{From: "kubernetes:ingress:alpha/shop/web-ing", To: "kubernetes:namespace:alpha/shop", Kind: model.RelMemberOf},
		{From: "kubernetes:workload:beta/shop/cronjob/nightly", To: "kubernetes:namespace:beta/shop", Kind: model.RelMemberOf},
		{From: "kubernetes:ingress:alpha/shop/web-ing", To: "kubernetes:service:alpha/shop/web", Kind: model.RelRoutesTo},
		{From: "kubernetes:service:alpha/shop/web", To: "kubernetes:workload:alpha/shop/deployment/web", Kind: model.RelRoutesTo},
		{From: "kubernetes:workload:alpha/shop/deployment/web", To: "kubernetes:host:node1", Kind: model.RelRunsOn},
	} {
		if err := g.AddRelation(rel); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func docByID(t *testing.T, root Document, id string) Document {
	t.Helper()
	var found *Document
	root.Walk(func(d Document) {
		if d.ID == id {
			found = &d
		}
	})
	if found == nil {
		t.Fatalf("document %s not found", id)
	}
	return *found
}

func TestRenderKubernetesClustersGolden(t *testing.T) {
	root := Render(clustersFixture(t), Options{GeneratedAt: generatedAt})
	golden.Assert(t, "tree_kubernetes", dump(root))
}

func TestKubernetesDocumentTree(t *testing.T) {
	root := Render(clustersFixture(t), Options{GeneratedAt: generatedAt})

	var rootIDs []string
	for _, c := range root.Children {
		rootIDs = append(rootIDs, c.ID)
	}
	want := []string{
		"overview:vlans", "overview:ip-allocation", "overview:services", "overview:network-diagram",
		"kubernetes:host:node1", "kubernetes:cluster:alpha", "kubernetes:cluster:beta",
	}
	if strings.Join(rootIDs, ",") != strings.Join(want, ",") {
		t.Errorf("root children = %v, want %v", rootIDs, want)
	}
	contents := root.Markdown()
	for _, title := range []string{"alpha", "beta"} {
		if !strings.Contains(contents, "- "+title+"\n") {
			t.Errorf("contents does not list cluster %s:\n%s", title, contents)
		}
	}
	if strings.Contains(contents, "- prod\n") {
		t.Error("NetBox cluster listed as a Kubernetes document")
	}

	alpha := docByID(t, root, "kubernetes:cluster:alpha")
	var children []string
	for _, c := range alpha.Children {
		children = append(children, c.ID)
	}
	if strings.Join(children, ",") != "kubernetes:namespace:alpha/empty,kubernetes:namespace:alpha/shop" {
		t.Errorf("cluster children = %v", children)
	}
	if alpha.Notes != nil {
		t.Error("cluster document carries manual notes")
	}
	for _, c := range alpha.Children {
		if c.Notes == nil {
			t.Errorf("namespace %s has no manual notes", c.ID)
		}
		if c.Title != strings.TrimPrefix(c.ID, "kubernetes:namespace:alpha/") {
			t.Errorf("namespace title = %q for %s", c.Title, c.ID)
		}
	}
}

func TestKubernetesNamespaceIsSearchable(t *testing.T) {
	root := Render(clustersFixture(t), Options{GeneratedAt: generatedAt})
	md := docByID(t, root, "kubernetes:namespace:alpha/shop").Markdown()
	for _, want := range []string{
		"- Cluster: alpha\n", "- Namespace: shop\n", "web-svc", "web-ing",
		"shop.example.com, www.example.com", "10.96.0.20", "node1",
		"https://git.example.com/shop/manifests",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("namespace document lacks %q:\n%s", want, md)
		}
	}
	for _, leaked := range []string{"FAKE-PASS", "FAKE-QUERY", "frag", "u:"} {
		if strings.Contains(md, leaked) {
			t.Errorf("namespace document contains %q", leaked)
		}
	}
	other := docByID(t, root, "kubernetes:namespace:beta/shop").Markdown()
	if !strings.Contains(other, "- Cluster: beta\n") || strings.Contains(other, "web-svc") {
		t.Errorf("same-named namespace of another cluster mixes data:\n%s", other)
	}
}

func TestKubernetesEmptyNamespaceRenders(t *testing.T) {
	root := Render(clustersFixture(t), Options{GeneratedAt: generatedAt})
	md := docByID(t, root, "kubernetes:namespace:alpha/empty").Markdown()
	for _, want := range []string{"No workloads are known.", "No services are known.", "No ingresses are known.", "No links are known."} {
		if !strings.Contains(md, want) {
			t.Errorf("empty namespace lacks %q", want)
		}
	}
}

func TestGraphWithoutKubernetesClusterRendersAsBefore(t *testing.T) {
	g := fixture(t)
	if err := g.AddEntity(model.Entity{ID: "netbox:cluster:prod", Kind: model.KindCluster, Source: "netbox", Name: "prod"}); err != nil {
		t.Fatal(err)
	}
	got := dump(Render(g, Options{GeneratedAt: generatedAt}))
	want := dump(Render(fixture(t), Options{GeneratedAt: generatedAt}))
	if string(got) != string(want) {
		t.Errorf("a NetBox cluster changed the output:\n%s", got)
	}
}

func TestOnlyNamespaceDocumentsCarryNotesAmongKubernetes(t *testing.T) {
	root := Render(clustersFixture(t), Options{GeneratedAt: generatedAt})
	root.Walk(func(d Document) {
		if d.Notes != nil && !strings.HasPrefix(d.ID, "kubernetes:namespace:") && !strings.HasPrefix(d.ID, "kubernetes:host:") {
			t.Errorf("document %s carries manual notes", d.ID)
		}
	})
}
