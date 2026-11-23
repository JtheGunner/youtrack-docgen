package model

import "testing"

func deriveGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	for _, e := range []Entity{
		{ID: "k8s:ingress:shop/web", Kind: KindIngress, Source: "k8s", Name: "web"},
		{ID: "k8s:service:shop/web", Kind: KindService, Source: "k8s", Name: "web"},
		{ID: "k8s:workload:shop/web", Kind: KindWorkload, Source: "k8s", Name: "web"},
		{ID: "k8s:host:node1", Kind: KindHost, Source: "k8s", Name: "node1"},
		{ID: "k8s:namespace:shop", Kind: KindNamespace, Source: "k8s", Name: "shop"},
		{ID: "netbox:cluster:prod", Kind: KindCluster, Source: "netbox", Name: "prod"},
		{ID: "netbox:host:vm1", Kind: KindHost, Source: "netbox", Name: "vm1", Attrs: map[string]string{"type": "vm", "cluster": "prod"}},
		{ID: "netbox:host:vm2", Kind: KindHost, Source: "netbox", Name: "vm2", Attrs: map[string]string{"type": "vm", "cluster": "missing"}},
		{ID: "netbox:host:box1", Kind: KindHost, Source: "netbox", Name: "box1", Attrs: map[string]string{"type": "device", "cluster": "prod"}},
	} {
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []Relation{
		{From: "k8s:ingress:shop/web", To: "k8s:service:shop/web", Kind: RelRoutesTo},
		{From: "k8s:service:shop/web", To: "k8s:workload:shop/web", Kind: RelRoutesTo},
		{From: "k8s:workload:shop/web", To: "k8s:host:node1", Kind: RelRunsOn},
		{From: "k8s:workload:shop/web", To: "k8s:namespace:shop", Kind: RelMemberOf},
	} {
		if err := g.AddRelation(r); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func dependsOn(g *Graph) []Relation {
	var out []Relation
	for _, r := range g.Relations() {
		if r.Kind == RelDependsOn {
			out = append(out, r)
		}
	}
	return out
}

func TestDeriveDependencies(t *testing.T) {
	g := deriveGraph(t)
	if got := DeriveDependencies(g); got != 4 {
		t.Fatalf("added = %d, want 4", got)
	}
	want := []Relation{
		{From: "k8s:ingress:shop/web", To: "k8s:service:shop/web", Kind: RelDependsOn},
		{From: "k8s:service:shop/web", To: "k8s:workload:shop/web", Kind: RelDependsOn},
		{From: "k8s:workload:shop/web", To: "k8s:host:node1", Kind: RelDependsOn},
		{From: "netbox:host:vm1", To: "netbox:cluster:prod", Kind: RelDependsOn},
	}
	got := dependsOn(g)
	if len(got) != len(want) {
		t.Fatalf("depends_on = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("depends_on[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestDeriveDependenciesIsIdempotent(t *testing.T) {
	g := deriveGraph(t)
	DeriveDependencies(g)
	if got := DeriveDependencies(g); got != 0 {
		t.Errorf("second run added %d relations, want 0", got)
	}
}

func TestDeriveDependenciesIgnoresOtherKinds(t *testing.T) {
	g := NewGraph()
	for _, e := range []Entity{
		{ID: "k8s:service:a", Kind: KindService, Source: "k8s", Name: "a"},
		{ID: "k8s:host:n", Kind: KindHost, Source: "k8s", Name: "n"},
		{ID: "k8s:ingress:i", Kind: KindIngress, Source: "k8s", Name: "i"},
		{ID: "k8s:workload:w", Kind: KindWorkload, Source: "k8s", Name: "w"},
	} {
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []Relation{
		{From: "k8s:service:a", To: "k8s:host:n", Kind: RelRunsOn},
		{From: "k8s:ingress:i", To: "k8s:workload:w", Kind: RelRoutesTo},
		{From: "k8s:workload:w", To: "k8s:host:n", Kind: RelExposes},
	} {
		if err := g.AddRelation(r); err != nil {
			t.Fatal(err)
		}
	}
	if got := DeriveDependencies(g); got != 0 {
		t.Errorf("added %d relations, want 0: %v", got, dependsOn(g))
	}
}
