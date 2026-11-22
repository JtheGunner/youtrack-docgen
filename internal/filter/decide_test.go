package filter

import (
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

func TestDecideID(t *testing.T) {
	tests := []struct {
		name  string
		rules Rules
		id    string
		want  Verdict
	}{
		{"no rules permit", Rules{}, "kubernetes:workload:shop/deployment/web", Permitted},
		{"deny by namespace", Rules{Deny: []Match{{Namespaces: []string{"shop"}}}}, "kubernetes:workload:shop/deployment/web", Denied},
		{"other namespace permitted", Rules{Deny: []Match{{Namespaces: []string{"shop"}}}}, "kubernetes:workload:ops/deployment/web", Permitted},
		{"namespace entity carries its name", Rules{Deny: []Match{{Namespaces: []string{"shop"}}}}, "kubernetes:namespace:shop", Denied},
		{"deny by kind", Rules{Deny: []Match{{Kinds: []string{"service"}}}}, "kubernetes:service:shop/web", Denied},
		{"label rule is undecidable", Rules{Deny: []Match{{Labels: map[string]string{"label.team": "x"}}}}, "kubernetes:workload:shop/deployment/web", Undecidable},
		{"known mismatch rules a label rule out", Rules{Deny: []Match{{Kinds: []string{"ingress"}, Tags: []string{"lab"}}}}, "kubernetes:workload:shop/deployment/web", Permitted},
		{"role rule on a host is undecidable", Rules{Deny: []Match{{Roles: []string{"lab"}}}}, "kubernetes:host:web01", Undecidable},
		{"namespace of a host is unknown", Rules{Deny: []Match{{Namespaces: []string{"shop"}}}}, "kubernetes:host:web01", Undecidable},
		{"allow excludes by namespace", Rules{Allow: []Match{{Namespaces: []string{"shop"}}}}, "kubernetes:workload:ops/deployment/web", Denied},
		{"allow includes by namespace", Rules{Allow: []Match{{Namespaces: []string{"shop"}}}}, "kubernetes:workload:shop/deployment/web", Permitted},
		{"allow by tag is undecidable", Rules{Allow: []Match{{Tags: []string{"prod"}}}}, "kubernetes:workload:shop/deployment/web", Undecidable},
		{"deny wins over undecidable allow", Rules{
			Allow: []Match{{Tags: []string{"prod"}}}, Deny: []Match{{Namespaces: []string{"shop"}}},
		}, "kubernetes:workload:shop/deployment/web", Denied},
		{"malformed ID is undecidable", Rules{}, "not-an-id", Undecidable},
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
