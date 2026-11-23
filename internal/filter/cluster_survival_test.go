package filter

import (
	"slices"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// clusterGraph holds clusters a and b, each with namespaces x and y and one
// service per namespace. With relations, namespaces are members of their
// cluster and services of their namespace, as the collector writes them.
// marked entities get the label marker=yes.
func clusterGraph(t *testing.T, withRelations bool, marked ...model.ID) *model.Graph {
	t.Helper()
	g := model.NewGraph()
	add := func(e model.Entity) model.Entity {
		if slices.Contains(marked, e.ID) {
			if e.Labels == nil {
				e.Labels = map[string]string{}
			}
			e.Labels["marker"] = "yes"
		}
		return mustAdd(t, g, e)
	}
	relate := func(from, to model.ID) {
		if !withRelations {
			return
		}
		if err := g.AddRelation(model.Relation{From: from, To: to, Kind: model.RelMemberOf}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []string{"a", "b"} {
		cluster := add(kubeEntity(t, model.KindCluster, c, nil))
		for _, ns := range []string{"x", "y"} {
			nsEntity := add(kubeEntity(t, model.KindNamespace, c+"/"+ns, nil))
			relate(nsEntity.ID, cluster.ID)
			workload := add(kubeEntity(t, model.KindWorkload, c+"/"+ns+"/deployment/app-"+c+ns, nil))
			relate(workload.ID, nsEntity.ID)
		}
	}
	return g
}

var clusterGraphIDs = []model.ID{
	"k8s:cluster:a", "k8s:namespace:a/x", "k8s:workload:a/x/deployment/app-ax", "k8s:namespace:a/y", "k8s:workload:a/y/deployment/app-ay",
	"k8s:cluster:b", "k8s:namespace:b/x", "k8s:workload:b/x/deployment/app-bx", "k8s:namespace:b/y", "k8s:workload:b/y/deployment/app-by",
}

func keptIDs(out *model.Graph) []model.ID {
	var have []model.ID
	for _, id := range clusterGraphIDs {
		if _, ok := out.Entity(id); ok {
			have = append(have, id)
		}
	}
	return have
}

// A cluster survives whenever one of its namespaces does, so a namespace never
// loses its parent. An explicit kind deny on the cluster takes its namespaces
// and their members along, with or without member_of relations.
func TestApplyKeepsTheClusterOfASurvivingNamespace(t *testing.T) {
	marker := Match{Labels: map[string]string{"marker": "yes"}}
	tests := []struct {
		name   string
		rules  Rules
		marked []model.ID
		want   []model.ID
	}{
		{name: "no rules keep everything", want: clusterGraphIDs},
		{
			name:  "namespace-only allow list keeps the clusters",
			rules: Rules{Allow: []Match{{Namespaces: []string{"x"}}}},
			want:  []model.ID{"k8s:cluster:a", "k8s:namespace:a/x", "k8s:workload:a/x/deployment/app-ax", "k8s:cluster:b", "k8s:namespace:b/x", "k8s:workload:b/x/deployment/app-bx"},
		},
		{
			name:   "allowed namespace in one cluster keeps only that cluster",
			rules:  Rules{Allow: []Match{marker}},
			marked: []model.ID{"k8s:namespace:a/x", "k8s:workload:a/x/deployment/app-ax"},
			want:   []model.ID{"k8s:cluster:a", "k8s:namespace:a/x", "k8s:workload:a/x/deployment/app-ax"},
		},
		{
			name:  "allow list drops a cluster without surviving namespace",
			rules: Rules{Allow: []Match{{Kinds: []string{"host"}}}},
		},
		{
			name:  "denying every namespace keeps the clusters without an allow list",
			rules: Rules{Deny: []Match{{Kinds: []string{"namespace"}}}},
			want:  []model.ID{"k8s:cluster:a", "k8s:cluster:b"},
		},
		{
			name:  "cluster kind deny removes its namespaces and members",
			rules: Rules{Deny: []Match{{Kinds: []string{"cluster"}}}},
		},
		{
			name:  "cluster deny wins over an allowed namespace",
			rules: Rules{Allow: []Match{{Namespaces: []string{"x"}}}, Deny: []Match{{Kinds: []string{"cluster"}}}},
		},
		{
			name:   "deny of one cluster keeps the other",
			rules:  Rules{Deny: []Match{{Kinds: []string{"cluster"}, Labels: map[string]string{"marker": "yes"}}}},
			marked: []model.ID{"k8s:cluster:a"},
			want:   []model.ID{"k8s:cluster:b", "k8s:namespace:b/x", "k8s:workload:b/x/deployment/app-bx", "k8s:namespace:b/y", "k8s:workload:b/y/deployment/app-by"},
		},
	}
	for _, tt := range tests {
		for _, withRelations := range []bool{true, false} {
			if !withRelations && tt.rules.Allow != nil {
				continue // admission of a cluster as parent follows its member_of relations
			}
			t.Run(tt.name, func(t *testing.T) {
				out := mustApply(t, Config{Collectors: map[string]Rules{"k8s": tt.rules}}, clusterGraph(t, withRelations, tt.marked...))
				if got := keptIDs(out); !slices.Equal(got, tt.want) {
					t.Errorf("relations %v: kept = %v, want %v", withRelations, got, tt.want)
				}
			})
		}
	}
}

// DecideID of a namespace or member follows the verdict of its cluster.
func TestDecideIDFollowsTheCluster(t *testing.T) {
	denyCluster := Rules{Deny: []Match{{Kinds: []string{"cluster"}}}}
	labelDenyCluster := Rules{Deny: []Match{{Kinds: []string{"cluster"}, Labels: map[string]string{"label.env": "lab"}}}}
	allowX := Rules{Allow: []Match{{Namespaces: []string{"x"}}}}
	tests := []struct {
		name  string
		rules Rules
		id    string
		want  Verdict
	}{
		{"namespace of a denied cluster", denyCluster, "kubernetes:namespace:a/x", Denied},
		{"workload of a denied cluster", denyCluster, "kubernetes:workload:a/x/deployment/web", Denied},
		{"service of a denied cluster", denyCluster, "kubernetes:service:a/x/web", Denied},
		{"ingress of a denied cluster", denyCluster, "kubernetes:ingress:a/x/web", Denied},
		{"possibly denied cluster leaves the namespace undecided", labelDenyCluster, "kubernetes:namespace:a/x", Undecidable},
		{"possibly denied cluster leaves the member undecided", labelDenyCluster, "kubernetes:service:a/x/web", Undecidable},
		{"failed allow of the cluster does not hold back the member", allowX, "kubernetes:service:a/x/web", Permitted},
		{"failed allow of the cluster does not hold back the namespace", allowX, "kubernetes:namespace:b/x", Permitted},
		{"allow list still excludes other namespaces", allowX, "kubernetes:namespace:b/y", Denied},
		{"cluster of an allowed namespace is undecided", allowX, "kubernetes:cluster:b", Undecidable},
		{"denied cluster", denyCluster, "kubernetes:cluster:b", Denied},
		{"redacted cluster key denies its members", Rules{}, "kubernetes:service:token=FAKE-KEY-SECRET-10/x/web", Denied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := New(Config{Collectors: map[string]Rules{"kubernetes": tt.rules}})
			if err != nil {
				t.Fatal(err)
			}
			if got := f.DecideID(model.ID(tt.id)); got != tt.want {
				t.Errorf("DecideID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
