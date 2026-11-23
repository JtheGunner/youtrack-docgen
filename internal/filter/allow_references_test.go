package filter

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// allowGraph builds two hosts (one tagged "documented") with interfaces, IPs,
// networks and sites, plus a second network and site only the untagged host uses.
func allowGraph(t *testing.T) *model.Graph {
	t.Helper()
	g := model.NewGraph()
	add := func(e model.Entity) model.Entity {
		t.Helper()
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	tagged := map[string]string{LabelTagPrefix + "documented": "true"}
	site := add(entity(t, "netbox", model.KindSite, "dc1", nil, nil))
	otherSite := add(entity(t, "netbox", model.KindSite, "dc2", nil, nil))
	net := add(entity(t, "netbox", model.KindNetwork, "192.0.2.0/24", nil, nil))
	otherNet := add(entity(t, "netbox", model.KindNetwork, "198.51.100.0/24", nil, nil))
	host := add(entity(t, "netbox", model.KindHost, "web01", tagged, nil))
	nic := add(entity(t, "netbox", model.KindInterface, "web01/eth0", nil, nil))
	ip := entity(t, "netbox", model.KindIPAddress, "192.0.2.10/24", nil, nil)
	ip.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.10")}
	ip = add(ip)
	other := add(entity(t, "netbox", model.KindHost, "app01", nil, nil))
	otherNic := add(entity(t, "netbox", model.KindInterface, "app01/eth0", nil, nil))
	otherIP := add(entity(t, "netbox", model.KindIPAddress, "198.51.100.10/24", nil, nil))
	// A site and a network that only reference each other must not be pulled
	// further than the host's own references.
	netSite := add(entity(t, "netbox", model.KindSite, "dc3", nil, nil))

	for _, r := range []model.Relation{
		{From: host.ID, To: site.ID, Kind: model.RelMemberOf},
		{From: nic.ID, To: host.ID, Kind: model.RelMemberOf},
		{From: ip.ID, To: nic.ID, Kind: model.RelMemberOf},
		{From: ip.ID, To: net.ID, Kind: model.RelMemberOf},
		{From: net.ID, To: netSite.ID, Kind: model.RelMemberOf},
		{From: other.ID, To: otherSite.ID, Kind: model.RelMemberOf},
		{From: otherNic.ID, To: other.ID, Kind: model.RelMemberOf},
		{From: otherIP.ID, To: otherNic.ID, Kind: model.RelMemberOf},
		{From: otherIP.ID, To: otherNet.ID, Kind: model.RelMemberOf},
	} {
		if err := g.AddRelation(r); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func keptNames(g *model.Graph) []string {
	var names []string
	for _, id := range g.IDs() {
		names = append(names, string(id))
	}
	slices.Sort(names)
	return names
}

func TestAllowOnHostKeepsDependentsAndReferences(t *testing.T) {
	documented := Rules{Allow: []Match{{Tags: []string{"documented"}}}}
	tests := []struct {
		name string
		deny []Match
		want []string
	}{
		{
			name: "README example rule",
			want: []string{
				"netbox:host:web01", "netbox:interface:web01/eth0", "netbox:ipaddress:192.0.2.10/24",
				"netbox:network:192.0.2.0/24", "netbox:site:dc1",
			},
		},
		{
			name: "deny wins over a referenced network",
			deny: []Match{{Kinds: []string{"network"}}},
			want: []string{
				"netbox:host:web01", "netbox:interface:web01/eth0", "netbox:ipaddress:192.0.2.10/24", "netbox:site:dc1",
			},
		},
		{
			name: "deny wins over a referenced site",
			deny: []Match{{Kinds: []string{"site"}}},
			want: []string{
				"netbox:host:web01", "netbox:interface:web01/eth0", "netbox:ipaddress:192.0.2.10/24",
				"netbox:network:192.0.2.0/24",
			},
		},
		{
			name: "a denied IP takes its network reference with it",
			deny: []Match{{Kinds: []string{"ipaddress"}}},
			want: []string{"netbox:host:web01", "netbox:interface:web01/eth0", "netbox:site:dc1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules := documented
			rules.Deny = tc.deny
			f, err := New(Config{Collectors: map[string]Rules{"netbox": rules}})
			if err != nil {
				t.Fatal(err)
			}
			out, err := f.Apply(allowGraph(t))
			if err != nil {
				t.Fatal(err)
			}
			if got := keptNames(out); !slices.Equal(got, tc.want) {
				t.Errorf("kept = %v\nwant   %v", got, tc.want)
			}
		})
	}
}

func TestAllowReferencesAreNotExpandedFurther(t *testing.T) {
	f, err := New(Config{Collectors: map[string]Rules{"netbox": {Allow: []Match{{Tags: []string{"documented"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(allowGraph(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Entity("netbox:site:dc3"); ok {
		t.Error("the site of a referenced network must not be admitted")
	}
	if _, ok := out.Entity("netbox:host:app01"); ok {
		t.Error("an untagged host must not be admitted")
	}
}

func TestAllowReferencesNeverReadmitDeniedHostData(t *testing.T) {
	// The interface of the allowed host is denied by name through a denied twin
	// host; the cascade still removes it although it is a dependent.
	g := allowGraph(t)
	rules := Rules{
		Allow: []Match{{Tags: []string{"documented"}}},
		Deny:  []Match{{Tags: []string{"documented"}, Kinds: []string{"host"}}},
	}
	f, err := New(Config{Collectors: map[string]Rules{"netbox": rules}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	if got := keptNames(out); len(got) != 0 {
		t.Errorf("a denied host must leave no dependents, kept %v", got)
	}
}

func TestApplyWithReportCountsOnly(t *testing.T) {
	rules := Rules{
		Allow: []Match{{Tags: []string{"documented"}}},
		Deny:  []Match{{Kinds: []string{"site"}}},
	}
	f, err := New(Config{Collectors: map[string]Rules{"netbox": rules}})
	if err != nil {
		t.Fatal(err)
	}
	_, report, err := f.ApplyWithReport(allowGraph(t))
	if err != nil {
		t.Fatal(err)
	}
	got := report["netbox"]
	// 11 entities. Denied: the 3 sites. Not allowed: the untagged host, its
	// interface, IP and network. Kept: host, interface, IP, network.
	want := SourceReport{Total: 11, RemovedByDeny: 3, RemovedByAllow: 4, HasAllow: true}
	if got != want {
		t.Errorf("report = %+v, want %+v", got, want)
	}
	if got.Kept() != 4 {
		t.Errorf("Kept() = %d, want 4", got.Kept())
	}
	if w := report.Warnings(); len(w) != 0 {
		t.Errorf("warnings = %v, want none", w)
	}
}

func TestReportWarnsWhenAllowRemovesEverythingWithoutNames(t *testing.T) {
	rules := Rules{Allow: []Match{{Tags: []string{"typo"}}}, Deny: []Match{{Roles: []string{"lab"}}}}
	f, err := New(Config{Collectors: map[string]Rules{"netbox": rules}})
	if err != nil {
		t.Fatal(err)
	}
	_, report, err := f.ApplyWithReport(allowGraph(t))
	if err != nil {
		t.Fatal(err)
	}
	warnings := report.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "netbox") {
		t.Fatalf("warnings = %v, want one for netbox", warnings)
	}
	for _, name := range []string{"web01", "app01", "192.0.2", "dc1", "eth0"} {
		if strings.Contains(warnings[0], name) {
			t.Errorf("warning names %q: %s", name, warnings[0])
		}
	}
}

func TestReportDoesNotWarnWithoutAllowRulesOrEntities(t *testing.T) {
	tests := []struct {
		name   string
		report Report
	}{
		{"deny only", Report{"netbox": {Total: 2, RemovedByDeny: 2}}},
		{"empty source", Report{"netbox": {HasAllow: true}}},
		{"allow keeps some", Report{"netbox": {Total: 2, RemovedByAllow: 1, HasAllow: true}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if w := tc.report.Warnings(); len(w) != 0 {
				t.Errorf("warnings = %v, want none", w)
			}
		})
	}
}

// A host that passes the rules but is removed by the cascade must not keep its
// sites and networks alive as orphans.
func TestAllowReferencesOnlyComeFromSurvivingHosts(t *testing.T) {
	tagged := map[string]string{LabelTagPrefix + "documented": "true"}
	tests := []struct {
		name     string
		hostKey  string
		twinDeny bool
	}{
		{name: "same_as twin of a denied host", hostKey: "web01", twinDeny: true},
		{name: "identity carries a secret", hostKey: "api_key=FAKE-KEY-SECRET-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := model.NewGraph()
			add := func(e model.Entity) model.Entity {
				t.Helper()
				if err := g.AddEntity(e); err != nil {
					t.Fatal(err)
				}
				return e
			}
			site := add(entity(t, "netbox", model.KindSite, "dc1", nil, nil))
			net := add(entity(t, "netbox", model.KindNetwork, "192.0.2.0/24", nil, nil))
			host := add(entity(t, "netbox", model.KindHost, tc.hostKey, tagged, nil))
			nic := add(entity(t, "netbox", model.KindInterface, tc.hostKey+"/eth0", nil, nil))
			ip := add(entity(t, "netbox", model.KindIPAddress, "192.0.2.10/24", nil, nil))
			twin := add(entity(t, "kubernetes", model.KindHost, "node1", nil, nil))
			rels := []model.Relation{
				{From: host.ID, To: site.ID, Kind: model.RelMemberOf},
				{From: nic.ID, To: host.ID, Kind: model.RelMemberOf},
				{From: ip.ID, To: nic.ID, Kind: model.RelMemberOf},
				{From: ip.ID, To: net.ID, Kind: model.RelMemberOf},
			}
			if tc.twinDeny {
				rels = append(rels, model.Relation{From: host.ID, To: twin.ID, Kind: model.RelSameAs})
			}
			for _, r := range rels {
				if err := g.AddRelation(r); err != nil {
					t.Fatal(err)
				}
			}
			rules := map[string]Rules{"netbox": {Allow: []Match{{Tags: []string{"documented"}}}}}
			if tc.twinDeny {
				rules["kubernetes"] = Rules{Deny: []Match{{Kinds: []string{"host"}}}}
			}
			f, err := New(Config{Collectors: rules})
			if err != nil {
				t.Fatal(err)
			}
			out, err := f.Apply(g)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []model.ID{host.ID, site.ID, net.ID} {
				if _, ok := out.Entity(id); ok {
					t.Errorf("%s must not be emitted", id)
				}
			}
		})
	}
}
