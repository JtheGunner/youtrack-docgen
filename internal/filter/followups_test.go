package filter

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/model"
)

var denyLabNamespace = Config{Collectors: map[string]Rules{kubernetesSource: {Deny: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"lab"}}}}}}

// A namespace that a kind rule denies also denies the members that only exist
// as an ID (an article of an object that was not collected).
func TestDecideIDFollowsTheNamespaceCascade(t *testing.T) {
	tests := []struct {
		name  string
		rules Rules
		id    string
		want  Verdict
	}{
		{"workload of a denied namespace", Rules{Deny: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"lab"}}}}, "kubernetes:workload:prod/lab/deployment/api", Denied},
		{"service of a denied namespace", Rules{Deny: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"lab"}}}}, "kubernetes:service:prod/lab/api", Denied},
		{"ingress of a denied namespace", Rules{Deny: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"lab"}}}}, "kubernetes:ingress:prod/lab/api", Denied},
		{"other namespace stays permitted", Rules{Deny: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"lab"}}}}, "kubernetes:service:prod/app/api", Permitted},
		{"every namespace denied", Rules{Deny: []Match{{Kinds: []string{"namespace"}}}}, "kubernetes:service:prod/app/api", Denied},
		{"allow rules without the namespace deny its members", Rules{Allow: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"app"}}, {Kinds: []string{"service"}}}}, "kubernetes:service:prod/lab/api", Denied},
		{"label rule on the namespace leaves the member undecided", Rules{Deny: []Match{{Kinds: []string{"namespace"}, Tags: []string{"lab"}}}}, "kubernetes:service:prod/app/api", Undecidable},
		{"namespace itself is unchanged", Rules{Deny: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"lab"}}}}, "kubernetes:namespace:prod/lab", Denied},
		{"host is no namespace member", Rules{Deny: []Match{{Kinds: []string{"namespace"}}}}, "kubernetes:host:node01", Permitted},
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

// The names, qualified keys, ingress hosts and addresses of the members of a
// denied namespace are removed from the free text of the entities that stay.
func TestApplyScrubsMembersOfDeniedNamespaceFromFreeText(t *testing.T) {
	g := model.NewGraph()
	mustAdd(t, g, kubeEntity(t, model.KindNamespace, "prod/lab", nil))
	svc := kubeEntity(t, model.KindService, "prod/lab/billing", nil)
	svc.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.80")}
	mustAdd(t, g, svc)
	ing := kubeEntity(t, model.KindIngress, "prod/lab/billing", nil)
	ing.Aliases = []string{"billing.lab.example.com"}
	mustAdd(t, g, ing)

	mustAdd(t, g, kubeEntity(t, model.KindNamespace, "prod/app", nil))
	text := "calls billing.lab, billing.lab.svc, billing.lab.svc.cluster.local, " +
		"https://billing.lab.example.com/x and 192.0.2.80; also billing-ui stays"
	mustAdd(t, g, kubeEntity(t, model.KindWorkload, "prod/app/deployment/web", map[string]string{"notes": text}))

	out := mustApply(t, denyLabNamespace, g)
	web, ok := out.Entity("kubernetes:workload:prod/app/deployment/web")
	if !ok {
		t.Fatal("the workload of the other namespace must stay")
	}
	got := web.Attrs["notes"]
	for _, leaked := range []string{"billing.lab", "192.0.2.80"} {
		if strings.Contains(got, leaked) {
			t.Errorf("notes = %q still holds %q", got, leaked)
		}
	}
	if !strings.Contains(got, "billing-ui stays") {
		t.Errorf("notes = %q lost unrelated text", got)
	}
}

// A value that a kept entity owns is never removed, even when a removed
// namespace member carries the same one.
func TestApplyKeepsValuesOwnedByKeptEntities(t *testing.T) {
	g := model.NewGraph()
	mustAdd(t, g, kubeEntity(t, model.KindNamespace, "prod/lab", nil))
	svc := kubeEntity(t, model.KindService, "prod/lab/billing", nil)
	svc.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.80")}
	mustAdd(t, g, svc)
	ing := kubeEntity(t, model.KindIngress, "prod/lab/billing", nil)
	ing.Aliases = []string{"shared.example.com"}
	mustAdd(t, g, ing)

	mustAdd(t, g, kubeEntity(t, model.KindNamespace, "prod/app", nil))
	owner := kubeEntity(t, model.KindIngress, "prod/app/shared", nil)
	owner.Aliases = []string{"shared.example.com"}
	owner.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.80")}
	mustAdd(t, g, owner)
	mustAdd(t, g, kubeEntity(t, model.KindWorkload, "prod/app/deployment/web", map[string]string{
		"notes": "served on shared.example.com at 192.0.2.80 by billing.lab.svc",
	}))

	out := mustApply(t, denyLabNamespace, g)
	web, _ := out.Entity("kubernetes:workload:prod/app/deployment/web")
	if got, want := web.Attrs["notes"], "served on shared.example.com at 192.0.2.80 by "+Placeholder; got != want {
		t.Errorf("notes = %q, want %q", got, want)
	}
	kept, _ := out.Entity("kubernetes:ingress:prod/app/shared")
	if len(kept.Addresses) != 1 || len(kept.Aliases) != 1 {
		t.Errorf("the owner must keep its own address and alias: %+v", kept)
	}
}

// The DNS name of a denied IP entity is removed from every surviving entity,
// also when the entity is written the way the NetBox collector writes it: with
// a prefix length and the address in Addresses.
func TestApplyRemovesDNSNameOfDeniedIPEntity(t *testing.T) {
	g := model.NewGraph()
	host := entity(t, "netbox", model.KindHost, "web01.example.com", nil, map[string]string{"description": "reachable as vip.example.com"})
	host.Aliases = []string{"vip.example.com", "web01-alias.example.com"}
	mustAdd(t, g, host)
	ip := entity(t, "netbox", model.KindIPAddress, "192.0.2.50/24", map[string]string{LabelTenant: "lab"}, map[string]string{model.AttrDNSName: "vip.example.com"})
	ip.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.50")}
	ip.Aliases = []string{"vip.example.com"}
	mustAdd(t, g, ip)

	out := mustApply(t, denyLabTenant, g)
	if _, ok := out.Entity(ip.ID); ok {
		t.Fatal("the denied IP entity must be removed")
	}
	kept, ok := out.Entity("netbox:host:web01.example.com")
	if !ok {
		t.Fatal("the host must stay")
	}
	if strings.Contains(kept.Attrs["description"], "vip.example.com") {
		t.Errorf("description = %q still holds the DNS name", kept.Attrs["description"])
	}
	if len(kept.Aliases) != 1 || kept.Aliases[0] != "web01-alias.example.com" {
		t.Errorf("aliases = %v, want only the unrelated alias", kept.Aliases)
	}
}
