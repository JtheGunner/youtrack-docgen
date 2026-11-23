package filter

import (
	"slices"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/model"
)

// The article of a cluster lists the names of its namespaces and counts their
// members. When the run did not regenerate it, a deny rule that may hide a
// namespace or member would leave a denied name in it, so the cluster ID is
// undecided; without such a rule the verdict is unchanged.
func TestDecideIDOfAClusterUnderNamespaceCapableDenies(t *testing.T) {
	tests := []struct {
		name string
		deny []Match
		want Verdict
	}{
		{"namespace name", []Match{{Namespaces: []string{"quarantine"}}}, Undecidable},
		{"namespace name on members only", []Match{{Kinds: []string{"service"}, Namespaces: []string{"quarantine"}}}, Undecidable},
		{"every namespace", []Match{{Kinds: []string{"namespace"}}}, Undecidable},
		{"namespace glob kind", []Match{{Kinds: []string{"name*"}}}, Undecidable},
		{"manifest label", []Match{{Labels: map[string]string{"label.team": "x"}}}, Undecidable},
		{"control: member kind only", []Match{{Kinds: []string{"service"}}}, Permitted},
		{"control: host kind only", []Match{{Kinds: []string{"host"}}}, Permitted},
		{"control: no deny rule", nil, Permitted},
		{"cluster deny still denies", []Match{{Kinds: []string{"cluster"}}, {Namespaces: []string{"quarantine"}}}, Denied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := New(Config{Collectors: map[string]Rules{kubernetesSource: {Deny: tt.deny}}})
			if err != nil {
				t.Fatal(err)
			}
			if got := f.DecideID("kubernetes:cluster:stage"); got != tt.want {
				t.Errorf("DecideID = %v, want %v", got, tt.want)
			}
		})
	}
}

// An allow list that may leave out a namespace could leave a name in a cluster
// article that the run did not regenerate, so the cluster ID is undecided;
// an allow list that admits every namespace keeps the verdict.
func TestDecideIDOfAClusterUnderNamespaceExcludingAllows(t *testing.T) {
	tests := []struct {
		name  string
		rules Rules
		want  Verdict
	}{
		{"allow names one namespace", Rules{Allow: []Match{{Kinds: []string{"cluster"}}, {Namespaces: []string{"public"}}}}, Undecidable},
		{"allow admits namespaces by label", Rules{Allow: []Match{{Kinds: []string{"cluster"}}, {Kinds: []string{"namespace"}, Labels: map[string]string{"label.team": "a"}}}}, Undecidable},
		{"allow admits no namespace", Rules{Allow: []Match{{Kinds: []string{"cluster"}}}}, Undecidable},
		{"control: no allow rules", Rules{}, Permitted},
		{"control: allow admits every namespace", Rules{Allow: []Match{{Kinds: []string{"namespace"}}, {Kinds: []string{"cluster"}}}}, Permitted},
		{"control: allow admits every kind", Rules{Allow: []Match{{Kinds: []string{"*"}}}}, Permitted},
		{"deny still wins", Rules{Allow: []Match{{Namespaces: []string{"public"}}}, Deny: []Match{{Kinds: []string{"cluster"}}}}, Denied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := New(Config{Collectors: map[string]Rules{kubernetesSource: tt.rules}})
			if err != nil {
				t.Fatal(err)
			}
			if got := f.DecideID("kubernetes:cluster:stage"); got != tt.want {
				t.Errorf("DecideID = %v, want %v", got, tt.want)
			}
		})
	}
}

// Only the built-in kubernetes source gets the cluster semantics: a cluster of
// another source is neither kept as the parent of a surviving namespace nor
// does its deny take its namespaces along.
func TestClusterCascadeIsLimitedToTheKubernetesSource(t *testing.T) {
	build := func(t *testing.T) *model.Graph {
		t.Helper()
		g := model.NewGraph()
		cluster := mustAdd(t, g, entity(t, "overlay", model.KindCluster, "a", nil, nil))
		ns := mustAdd(t, g, entity(t, "overlay", model.KindNamespace, "a/x", nil, nil))
		if err := g.AddRelation(model.Relation{From: ns.ID, To: cluster.ID, Kind: model.RelMemberOf}); err != nil {
			t.Fatal(err)
		}
		return g
	}
	tests := []struct {
		name  string
		rules Rules
		want  []model.ID
	}{
		{"deny of the cluster keeps the namespace", Rules{Deny: []Match{{Kinds: []string{"cluster"}}}}, []model.ID{"overlay:namespace:a/x"}},
		{"allow of the namespace does not keep the cluster", Rules{Allow: []Match{{Kinds: []string{"namespace"}}}}, []model.ID{"overlay:namespace:a/x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := mustApply(t, Config{Collectors: map[string]Rules{"overlay": tt.rules}}, build(t))
			var got []model.ID
			for _, id := range []model.ID{"overlay:cluster:a", "overlay:namespace:a/x"} {
				if _, ok := out.Entity(id); ok {
					got = append(got, id)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("kept = %v, want %v", got, tt.want)
			}
		})
	}
}
