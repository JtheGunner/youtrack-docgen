package filter

import (
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// A Kubernetes node that matches two NetBox hosts is not linked to either, but
// one of them may be the node's machine. When one candidate is denied the node
// is withheld (fail closed). The other candidate is not denied as a twin, but
// it may still disappear when its name contains the withheld short name.
func TestApplyWithholdsHostsWithADeniedAmbiguousCandidate(t *testing.T) {
	g := model.NewGraph()
	add := func(e model.Entity) model.Entity {
		t.Helper()
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	lab := add(entity(t, "netbox", model.KindHost, "web01.example.org", map[string]string{LabelTenant: "lab"}, nil))
	add(entity(t, "netbox", model.KindHost, "web01.example.com", map[string]string{LabelTenant: "acme"}, nil))
	node := add(entity(t, "k8s", model.KindHost, "web01", nil, nil))

	model.Link(g)
	if rels := g.Relations(); len(rels) != 0 {
		t.Fatalf("an ambiguous match must not be published, got %v", rels)
	}

	f, err := New(Config{Collectors: map[string]Rules{"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []model.ID{lab.ID, node.ID} {
		if _, ok := out.Entity(id); ok {
			t.Errorf("%s must be withheld", id)
		}
	}
	if rels := out.Relations(); len(rels) != 0 {
		t.Errorf("Relations() = %v, want none", rels)
	}
}
