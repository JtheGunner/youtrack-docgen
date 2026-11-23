package filter

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

func mustApply(t *testing.T, cfg Config, g *model.Graph) *model.Graph {
	t.Helper()
	f, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustAdd(t *testing.T, g *model.Graph, e model.Entity) model.Entity {
	t.Helper()
	if err := g.AddEntity(e); err != nil {
		t.Fatal(err)
	}
	return e
}

var denyLabTenant = Config{Collectors: map[string]Rules{"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}}}}

// The short name of a denied host is removed from free text at word
// boundaries. Short names below four characters are a known limit and stay.
func TestApplyRemovesShortNamesOfDeniedHosts(t *testing.T) {
	tests := []struct {
		name       string
		deniedHost string
		text       string
		want       string
	}{
		{"short name removed", "lab01.example.com", "runs on lab01 today", "runs on " + Placeholder + " today"},
		{"case-insensitive", "lab01.example.com", "see LAB01.", "see " + Placeholder + "."},
		{"similar name stays", "lab01.example.com", "see lab011 and xlab01 and lab01-eth0", "see lab011 and xlab01 and lab01-eth0"},
		{"alias short name removed", "other.example.com", "moved to labx now", "moved to " + Placeholder + " now"},
		{"three characters stay", "web.example.com", "the web tier and db", "the web tier and db"},
		{"four characters removed", "mail.example.com", "the mail tier", "the " + Placeholder + " tier"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := model.NewGraph()
			denied := entity(t, "netbox", model.KindHost, tc.deniedHost, map[string]string{LabelTenant: "lab"}, nil)
			denied.Aliases = []string{"labx.example.com"}
			mustAdd(t, g, denied)
			mustAdd(t, g, entity(t, "netbox", model.KindHost, "keep.example.com", nil, map[string]string{"description": tc.text}))

			out := mustApply(t, denyLabTenant, g)
			kept, ok := out.Entity("netbox:host:keep.example.com")
			if !ok {
				t.Fatal("the unrelated host must stay")
			}
			if got := kept.Attrs["description"]; got != tc.want {
				t.Errorf("description = %q, want %q", got, tc.want)
			}
		})
	}
}

// The reverse-DNS names of a denied address are removed, for IPv4 and IPv6.
func TestApplyRemovesReverseDNSNamesOfDeniedAddresses(t *testing.T) {
	g := model.NewGraph()
	denied := entity(t, "netbox", model.KindHost, "lab01.example.com", map[string]string{LabelTenant: "lab"}, nil)
	denied.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.50"), netip.MustParseAddr("2001:db8::1")}
	mustAdd(t, g, denied)
	mustAdd(t, g, entity(t, "netbox", model.KindHost, "keep.example.com", nil, map[string]string{
		"ptr4":  "50.2.0.192.in-addr.arpa.",
		"ptr6":  "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa",
		"other": "51.2.0.192.in-addr.arpa",
	}))

	out := mustApply(t, denyLabTenant, g)
	kept, _ := out.Entity("netbox:host:keep.example.com")
	for _, key := range []string{"ptr4", "ptr6"} {
		if v := kept.Attrs[key]; strings.Contains(v, "arpa") {
			t.Errorf("%s = %q still holds a reverse-DNS name", key, v)
		}
	}
	if got := kept.Attrs["other"]; got != "51.2.0.192.in-addr.arpa" {
		t.Errorf("an unrelated reverse name changed: %q", got)
	}
}

// A denied IP entity removes its address from hosts and interfaces, in text and
// in the address lists, including its reverse-DNS name.
func TestApplyRemovesAddressOfDeniedIPEntity(t *testing.T) {
	g := model.NewGraph()
	host := entity(t, "netbox", model.KindHost, "web01.example.com", nil, map[string]string{"description": "primary 192.0.2.50 ok, 192.0.2.51 too"})
	host.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.50"), netip.MustParseAddr("192.0.2.51")}
	mustAdd(t, g, host)
	nic := entity(t, "netbox", model.KindInterface, "eth0", nil, map[string]string{"ip": "192.0.2.50/24", "ptr": "50.2.0.192.in-addr.arpa"})
	nic.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.50")}
	mustAdd(t, g, nic)
	ip := mustAdd(t, g, entity(t, "netbox", model.KindIPAddress, "192.0.2.50", map[string]string{LabelTenant: "lab"}, nil))
	if err := g.AddRelation(model.Relation{From: ip.ID, To: nic.ID, Kind: model.RelMemberOf}); err != nil {
		t.Fatal(err)
	}

	out := mustApply(t, denyLabTenant, g)
	if _, ok := out.Entity(ip.ID); ok {
		t.Fatal("the denied IP entity must be removed")
	}
	for _, e := range append(out.ByKind(model.KindHost), out.ByKind(model.KindInterface)...) {
		for _, a := range e.Addresses {
			if a == netip.MustParseAddr("192.0.2.50") {
				t.Errorf("%s still lists the denied address", e.ID)
			}
		}
		for k, v := range e.Attrs {
			if strings.Contains(v, "192.0.2.50") || strings.Contains(v, "50.2.0.192") {
				t.Errorf("%s attr %s = %q still holds the denied address", e.ID, k, v)
			}
		}
	}
	out2, _ := out.Entity("netbox:host:web01.example.com")
	if len(out2.Addresses) != 1 || !strings.Contains(out2.Attrs["description"], "192.0.2.51") {
		t.Errorf("the other address must stay: %+v", out2)
	}
}

// A denied namespace removes its workloads, services and ingresses with their
// relations; other namespaces stay.
func TestApplyRemovesMembersOfDeniedNamespace(t *testing.T) {
	g := model.NewGraph()
	nsLab := mustAdd(t, g, entity(t, "k8s", model.KindNamespace, "lab", nil, nil))
	nsApp := mustAdd(t, g, entity(t, "k8s", model.KindNamespace, "app", nil, nil))
	web := mustAdd(t, g, entity(t, "k8s", model.KindWorkload, "app/web", nil, nil))
	denied := []model.Entity{
		mustAdd(t, g, entity(t, "k8s", model.KindWorkload, "lab/api", nil, nil)),
		mustAdd(t, g, entity(t, "k8s", model.KindService, "lab/api", nil, nil)),
		mustAdd(t, g, entity(t, "k8s", model.KindIngress, "lab/api", nil, nil)),
	}
	for _, e := range denied {
		if err := g.AddRelation(model.Relation{From: e.ID, To: nsLab.ID, Kind: model.RelMemberOf}); err != nil {
			t.Fatal(err)
		}
		if err := g.AddRelation(model.Relation{From: web.ID, To: e.ID, Kind: model.RelDependsOn}); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddRelation(model.Relation{From: web.ID, To: nsApp.ID, Kind: model.RelMemberOf}); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Collectors: map[string]Rules{"k8s": {Deny: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"lab"}}}}}}
	out := mustApply(t, cfg, g)
	for _, e := range append(denied, nsLab) {
		if _, ok := out.Entity(e.ID); ok {
			t.Errorf("%s must be removed", e.ID)
		}
	}
	if _, ok := out.Entity(web.ID); !ok {
		t.Fatal("the workload of the other namespace must stay")
	}
	rels := out.Relations()
	if len(rels) != 1 || rels[0].To != nsApp.ID {
		t.Errorf("relations = %v, want only the member_of of the kept workload", rels)
	}
}
