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
		{"key the redactor would change is denied", Rules{}, "kubernetes:workload:shop/deployment/api_key=FAKE-KEY-SECRET-1", Denied},
		{"redacted key is denied despite allow rule", Rules{Allow: []Match{{Namespaces: []string{"shop"}}}}, "kubernetes:service:shop/password=FAKE-KEY-SECRET-2", Denied},
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

// Apply admits the interfaces, IPs, networks and sites of an allowed host as
// references. An ID alone cannot tell whether such an entity is one, so a failed
// allow match must not read as Denied: that would wipe the article of an
// allowed host's interface. A deny rule that definitely matches still denies.
func TestDecideIDKeepsAllowedHostReferencesUndecided(t *testing.T) {
	allowHosts := []Match{{Kinds: []string{"host"}, Tags: []string{"documented"}}}
	tests := []struct {
		name  string
		rules Rules
		id    string
		want  Verdict
	}{
		{"interface of a possibly allowed host", Rules{Allow: allowHosts}, "netbox:interface:web01/eth0", Undecidable},
		{"ip address", Rules{Allow: allowHosts}, "netbox:ipaddress:192.0.2.10/24", Undecidable},
		{"network", Rules{Allow: allowHosts}, "netbox:network:192.0.2.0/24", Undecidable},
		{"site", Rules{Allow: allowHosts}, "netbox:site:dc1", Undecidable},
		{"host is still denied by a missed allow", Rules{Allow: []Match{{Kinds: []string{"site"}}}}, "netbox:host:web01", Denied},
		{"deny rule still wins for a reference kind", Rules{Allow: allowHosts, Deny: []Match{{Kinds: []string{"network"}}}}, "netbox:network:192.0.2.0/24", Denied},
		{"workload is no reference kind", Rules{Allow: allowHosts}, "netbox:workload:ns/web", Denied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := New(Config{Collectors: map[string]Rules{"netbox": tt.rules}})
			if err != nil {
				t.Fatal(err)
			}
			if got := f.DecideID(model.ID(tt.id)); got != tt.want {
				t.Errorf("DecideID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
