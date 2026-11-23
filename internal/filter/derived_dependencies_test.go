package filter

import (
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// TestDerivedDependenciesNeverMentionRejectedEntities runs the derivation the
// way the pipeline does, on the filtered graph, and checks that no depends_on
// relation touches an entity the rules rejected.
func TestDerivedDependenciesNeverMentionRejectedEntities(t *testing.T) {
	const secretName = "internal-" + fakeCustom
	tests := []struct {
		name    string
		deny    Match
		denied  string
		wantRel []string
	}{
		{"nothing denied", Match{Kinds: []string{"none"}}, "", []string{
			"k8s:ingress:web>k8s:service:web", "k8s:service:web>k8s:workload:web", "k8s:workload:web>netbox:host:node1",
		}},
		{"denied service", Match{Kinds: []string{"service"}}, "k8s:service:web", []string{
			"k8s:workload:web>netbox:host:node1",
		}},
		{"denied workload", Match{Kinds: []string{"workload"}}, "k8s:workload:web", []string{
			"k8s:ingress:web>k8s:service:web",
		}},
		{"denied host", Match{Kinds: []string{"host"}}, "netbox:host:node1", []string{
			"k8s:ingress:web>k8s:service:web", "k8s:service:web>k8s:workload:web",
		}},
		{"denied ingress", Match{Kinds: []string{"ingress"}}, "k8s:ingress:web", []string{
			"k8s:service:web>k8s:workload:web", "k8s:workload:web>netbox:host:node1",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := model.NewGraph()
			ids := map[string]model.Kind{
				"k8s:ingress:web": model.KindIngress, "k8s:service:web": model.KindService,
				"k8s:workload:web": model.KindWorkload, "netbox:host:node1": model.KindHost,
			}
			for id, kind := range ids {
				source, _, key, err := model.ParseID(model.ID(id))
				if err != nil {
					t.Fatal(err)
				}
				e := model.Entity{ID: model.ID(id), Kind: kind, Source: source, Name: key}
				if id == tt.denied {
					e.Name = secretName
				}
				if err := g.AddEntity(e); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range []model.Relation{
				{From: "k8s:ingress:web", To: "k8s:service:web", Kind: model.RelRoutesTo},
				{From: "k8s:service:web", To: "k8s:workload:web", Kind: model.RelRoutesTo},
				{From: "k8s:workload:web", To: "netbox:host:node1", Kind: model.RelRunsOn},
			} {
				if err := g.AddRelation(r); err != nil {
					t.Fatal(err)
				}
			}
			f, err := New(Config{Collectors: map[string]Rules{"k8s": {Deny: []Match{tt.deny}}, "netbox": {Deny: []Match{tt.deny}}}})
			if err != nil {
				t.Fatal(err)
			}
			out, err := f.Apply(g)
			if err != nil {
				t.Fatal(err)
			}
			model.DeriveDependencies(out)

			var got []string
			for _, r := range out.Relations() {
				if r.Kind != model.RelDependsOn {
					continue
				}
				got = append(got, string(r.From)+">"+string(r.To))
				if tt.denied != "" && (string(r.From) == tt.denied || string(r.To) == tt.denied) {
					t.Errorf("depends_on %s -> %s mentions rejected %s", r.From, r.To, tt.denied)
				}
			}
			if strings.Join(got, ",") != strings.Join(tt.wantRel, ",") {
				t.Errorf("depends_on = %v, want %v", got, tt.wantRel)
			}
			if strings.Contains(strings.Join(got, ","), secretName) {
				t.Errorf("a rejected name leaked into %v", got)
			}
		})
	}
}
