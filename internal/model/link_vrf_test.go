package model

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// addVRFAddress gives host an address that lives in vrf, the way the NetBox
// collector models it: an IP address entity on an interface of the host.
func addVRFAddress(t *testing.T, g *Graph, h Entity, addr, vrf string) {
	t.Helper()
	nic := Entity{ID: ID(h.Source + ":interface:" + h.Name + "-" + vrf), Kind: KindInterface, Source: h.Source, Name: vrf}
	ip := Entity{
		ID: ID(h.Source + ":ipaddress:" + h.Name + "-" + vrf), Kind: KindIPAddress, Source: h.Source, Name: addr,
		Attrs: map[string]string{AttrVRF: vrf}, Addresses: []netip.Addr{netip.MustParseAddr(addr)},
	}
	mustAdd(t, g, nic, ip)
	for _, rel := range []Relation{{From: ip.ID, To: nic.ID, Kind: RelMemberOf}, {From: nic.ID, To: h.ID, Kind: RelMemberOf}} {
		if err := g.AddRelation(rel); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLinkConsidersVRF(t *testing.T) {
	const addr = "192.0.2.90"
	tests := []struct {
		name       string
		vrfs       map[string]string // NetBox host key -> VRF of its address
		wantSameAs int
		wantWarns  int
	}{
		// The Kubernetes node knows no VRF, so it cannot tell two VRFs apart.
		{name: "same address in two VRFs is ambiguous for a node without VRF", vrfs: map[string]string{"1": "red", "2": "blue"}, wantWarns: 1},
		{name: "a single host with the address links", vrfs: map[string]string{"1": "red"}, wantSameAs: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraph()
			mustAdd(t, g, host("k8s", "node", "node", addr))
			for key, vrf := range tt.vrfs {
				h := host("netbox", key, "h"+key+".example.com", addr)
				mustAdd(t, g, h)
				addVRFAddress(t, g, h, addr, vrf)
			}
			_, warnings := LinkWithWarnings(g)
			sameAs := 0
			for _, r := range g.Relations() {
				if r.Kind == RelSameAs {
					sameAs++
				}
			}
			if sameAs != tt.wantSameAs || len(warnings) != tt.wantWarns {
				t.Errorf("same_as = %d, warnings = %v; want %d and %d warnings", sameAs, warnings, tt.wantSameAs, tt.wantWarns)
			}
		})
	}
}

func TestLinkSeparatesVRFsOfBothSides(t *testing.T) {
	g := NewGraph()
	red := host("netbox", "1", "a.example.com", "192.0.2.91")
	blue := host("other", "1", "b.example.com", "192.0.2.91")
	mustAdd(t, g, red, blue)
	addVRFAddress(t, g, red, "192.0.2.91", "red")
	addVRFAddress(t, g, blue, "192.0.2.91", "blue")
	if got := Link(g); got != 0 {
		t.Errorf("Link() added %d relations for the same address in different VRFs, want 0", got)
	}
}

func TestLinkWarnsAboutAmbiguousMatches(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g,
		host("netbox", "1", "web01.example.com"),
		host("netbox", "2", "web01.example.org"),
		host("k8s", "n1", "web01"),
	)
	_, warnings := LinkWithWarnings(g)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	w := warnings[0]
	all := func(ID) bool { return true }
	msg, ok := w.Describe(all)
	if !ok || !strings.Contains(msg, "ambiguous") || !strings.Contains(msg, "k8s:host:n1") {
		t.Errorf("warning message = %q, %v", msg, ok)
	}
	if want := []ID{"netbox:host:1", "netbox:host:2"}; !slices.Equal(w.Candidates, want) {
		t.Errorf("candidates = %v, want %v", w.Candidates, want)
	}
	// Describe never names an entity that is not kept, and stays silent when
	// no ambiguity is left.
	if msg, ok := w.Describe(func(id ID) bool { return id != "netbox:host:2" }); ok {
		t.Errorf("warning with a removed candidate = %q, want none", msg)
	}
	if _, ok := w.Describe(func(id ID) bool { return id != "k8s:host:n1" }); ok {
		t.Error("warning with a removed subject, want none")
	}
}

func TestLinkRecordsAmbiguousTwinsWithoutPublishingThem(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g,
		host("netbox", "1", "web01.example.com"),
		host("netbox", "2", "web01.example.org"),
		host("k8s", "n1", "web01"),
	)
	Link(g)
	if rels := g.Relations(); len(rels) != 0 {
		t.Errorf("Relations() = %v, want none", rels)
	}
	want := []AmbiguousTwin{{A: "k8s:host:n1", B: "netbox:host:1"}, {A: "k8s:host:n1", B: "netbox:host:2"}}
	if got := g.AmbiguousTwins(); !slices.Equal(got, want) {
		t.Errorf("AmbiguousTwins() = %v, want %v", got, want)
	}
}

func TestLinkDoesNotRecordVRFIncompatiblePairsAsAmbiguous(t *testing.T) {
	g := NewGraph()
	red := host("netbox", "1", "a.example.com", "192.0.2.92")
	blue := host("other", "1", "b.example.com", "192.0.2.92")
	mustAdd(t, g, red, blue)
	addVRFAddress(t, g, red, "192.0.2.92", "red")
	addVRFAddress(t, g, blue, "192.0.2.92", "blue")
	Link(g)
	if got := g.AmbiguousTwins(); len(got) != 0 {
		t.Errorf("AmbiguousTwins() = %v, want none", got)
	}
}

func TestLinkPlacementWarnsAndLeavesUnpinnedWorkloadsAlone(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g,
		clusterEntity("k8s:workload:free", KindWorkload, "free", nil),
		clusterEntity("k8s:service:lb", KindService, "lb", map[string]string{AttrExternalIPs: "192.0.2.95"}),
		host("netbox", "1", "a.example.com", "192.0.2.95"),
		host("netbox", "2", "b.example.com", "192.0.2.95"),
		host("netbox", "3", "c.example.com", "192.0.2.96"),
	)
	_, warnings := LinkWithWarnings(g)
	for _, r := range g.Relations() {
		t.Errorf("unexpected relation %+v", r)
	}
	if len(warnings) != 1 || warnings[0].Subject != "k8s:service:lb" {
		t.Errorf("warnings = %v, want one about the service", warnings)
	}
}
