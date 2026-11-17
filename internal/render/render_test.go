package render

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
	"github.com/JtheGunner/youtrack-docgen/internal/testutil/golden"
)

var generatedAt = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

const (
	fakeToken    = "tok-FAKE-0123456789"
	fakePassword = "hunter2-FAKE"
	fakeQuery    = "private_token=FAKE-QUERY"
)

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddr(a)
	}
	return out
}

func fixture(t *testing.T) *model.Graph {
	t.Helper()
	g := model.NewGraph()
	entities := []model.Entity{
		{
			ID: "netbox:network:10", Kind: model.KindNetwork, Source: "netbox", Name: "servers",
			Attrs: map[string]string{model.AttrVLAN: "10", model.AttrPrefix: "10.0.10.0/24"},
		},
		{
			ID: "netbox:network:20", Kind: model.KindNetwork, Source: "netbox", Name: "databases",
			Attrs: map[string]string{model.AttrVLAN: "20", model.AttrPrefix: "10.0.20.0/24"},
		},
		{
			ID: "netbox:network:mgmt", Kind: model.KindNetwork, Source: "netbox", Name: "management",
			Attrs: map[string]string{model.AttrPrefix: "192.0.2.0/24"},
		},
		{
			ID: "netbox:host:web01", Kind: model.KindHost, Source: "netbox", Name: "web01",
			Aliases:   []string{"www", "shop-front"},
			Addresses: addrs("10.0.10.6", "10.0.10.5"),
			Labels:    map[string]string{"role": "web server", "api_token": fakeToken},
			Attrs: map[string]string{
				"fqdn":        "web01.example.com",
				"description": "Front-end | web tier\nsecond line",
				"url":         "https://admin:" + fakePassword + "@netbox.example.com/dcim/devices/1/?" + fakeQuery + "#frag",
				"password":    fakePassword,
			},
		},
		{
			ID: "netbox:host:db01", Kind: model.KindHost, Source: "netbox", Name: "db01",
			Addresses: addrs("10.0.20.5"),
			Attrs:     map[string]string{"fqdn": "db01.example.com", "secret": fakeToken},
		},
		{
			ID: "k8s:host:web01", Kind: model.KindHost, Source: "k8s", Name: "web01",
			Addresses: addrs("10.0.10.5"),
			Attrs:     map[string]string{"url": "ftp://example.com/not-http"},
		},
		{
			ID: "k8s:service:shop", Kind: model.KindService, Source: "k8s", Name: "shop",
			Addresses: addrs("10.96.0.10"),
			Attrs:     map[string]string{"token": fakeToken},
		},
		{ID: "k8s:service:cache", Kind: model.KindService, Source: "k8s", Name: "cache"},
		{
			ID: "netbox:ipaddress:vip", Kind: model.KindIPAddress, Source: "netbox", Name: "shop-vip",
			Addresses: addrs("192.0.2.10"),
		},
	}
	for _, e := range entities {
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	relations := []model.Relation{
		{From: "k8s:service:shop", To: "netbox:host:web01", Kind: model.RelRunsOn},
		{From: "k8s:service:shop", To: "netbox:host:db01", Kind: model.RelDependsOn},
		{From: "netbox:host:web01", To: "netbox:host:db01", Kind: model.RelDependsOn},
		{From: "netbox:host:db01", To: "k8s:service:cache", Kind: model.RelExposes},
	}
	for _, rel := range relations {
		if err := g.AddRelation(rel); err != nil {
			t.Fatal(err)
		}
	}
	model.Link(g)
	return g
}

func dump(root Document) []byte {
	var b strings.Builder
	root.Walk(func(d Document) {
		b.WriteString("<!-- document " + d.ID + " -->\n")
		b.WriteString(d.Markdown())
		b.WriteString("\n")
	})
	return []byte(b.String())
}

func TestRenderGolden(t *testing.T) {
	root := Render(fixture(t), Options{GeneratedAt: generatedAt})
	golden.Assert(t, "tree", dump(root))
}

func TestRenderEmptyGraphGolden(t *testing.T) {
	root := Render(model.NewGraph(), Options{GeneratedAt: generatedAt})
	golden.Assert(t, "empty", dump(root))
}

func TestRenderIsDeterministic(t *testing.T) {
	g := fixture(t)
	first := string(dump(Render(g, Options{GeneratedAt: generatedAt})))
	for range 5 {
		if got := string(dump(Render(g, Options{GeneratedAt: generatedAt}))); got != first {
			t.Fatal("output differs between runs")
		}
	}
}

func TestHostDocumentSearchability(t *testing.T) {
	root := Render(fixture(t), Options{GeneratedAt: generatedAt})
	var web Document
	root.Walk(func(d Document) {
		if d.ID == "netbox:host:web01" {
			web = d
		}
	})
	if web.Title != "web01" {
		t.Fatalf("title = %q, want hostname", web.Title)
	}
	md := web.Markdown()
	for _, want := range []string{"web01.example.com", "www", "shop-front", "10.0.10.5", "10.0.10.6", "shop", "db01"} {
		if !strings.Contains(md, want) {
			t.Errorf("host document lacks plain text %q", want)
		}
	}
	var headings []string
	for _, s := range web.Sections {
		headings = append(headings, s.Heading)
	}
	want := "Summary,Network,Diagram,Runs here,Depends on,Needed by,Links,Generated"
	if got := strings.Join(headings, ","); got != want {
		t.Errorf("sections = %s, want %s", got, want)
	}
}

func TestGeneratedNote(t *testing.T) {
	root := Render(fixture(t), Options{GeneratedAt: generatedAt.In(time.FixedZone("x", 3600))})
	if !strings.Contains(root.Markdown(), "Generated at 2025-01-02T03:04:05Z from k8s, netbox.") {
		t.Errorf("unexpected generated note:\n%s", root.Markdown())
	}
}

func TestSeededSecretsNeverReachOutput(t *testing.T) {
	out := string(dump(Render(fixture(t), Options{GeneratedAt: generatedAt})))
	for _, secret := range []string{fakeToken, fakePassword, fakeQuery, "admin:", "#frag", "ftp://"} {
		if strings.Contains(out, secret) {
			t.Errorf("output contains %q", secret)
		}
	}
	if !strings.Contains(out, "https://netbox.example.com/dcim/devices/1/") {
		t.Error("sanitized source URL is missing")
	}
}

func TestSafeURL(t *testing.T) {
	tests := map[string]string{
		"https://u:p@example.com/a?x=1#f": "https://example.com/a",
		"http://example.com":              "http://example.com",
		"ftp://example.com":               "",
		"javascript:alert(1)":             "",
		"/relative":                       "",
		"":                                "",
	}
	for in, want := range tests {
		if got := safeURL(in); got != want {
			t.Errorf("safeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompareVLAN(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2", "10", -1}, {"10", "2", 1}, {"5", "5", 0}, {"", "5", 1}, {"5", "", -1}, {"a", "b", -1},
	}
	for _, tt := range tests {
		if got := compareVLAN(tt.a, tt.b); got != tt.want {
			t.Errorf("compareVLAN(%q,%q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// The timestamp changes on every run. Sinks rely on the flag to keep a run on
// unchanged data from rewriting every article.
func TestOnlyTheGeneratedSectionIsVolatile(t *testing.T) {
	root := Render(model.NewGraph(), Options{GeneratedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)})
	seen := false
	root.Walk(func(d Document) {
		for _, s := range d.Sections {
			if s.Volatile != (s.ID == "generated") {
				t.Errorf("%s/%s: Volatile = %v", d.ID, s.ID, s.Volatile)
			}
			seen = seen || s.Volatile
		}
	})
	if !seen {
		t.Error("no generated section found")
	}
}

// vlanTable renders the VLAN overview of a graph built from the given
// networks and member_of relations.
func vlanTable(t *testing.T, nets []model.Entity, memberOf [][2]model.ID) string {
	t.Helper()
	g := model.NewGraph()
	for _, e := range nets {
		e.Kind, e.Source = model.KindNetwork, "netbox"
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range memberOf {
		if err := g.AddRelation(model.Relation{From: r[0], To: r[1], Kind: model.RelMemberOf}); err != nil {
			t.Fatal(err)
		}
	}
	root := Render(g, Options{GeneratedAt: generatedAt})
	return findDoc(t, root, VLANsID).Sections[0].Markdown()
}

func TestVLANOverviewJoinsAVLANWithItsPrefixes(t *testing.T) {
	vlan := model.Entity{ID: "netbox:network:vlan-100", Name: "servers", Attrs: map[string]string{model.AttrVLAN: "100"}}
	tests := []struct {
		name     string
		nets     []model.Entity
		memberOf [][2]model.ID
		want     []string
		notWant  []string
	}{
		{
			name: "a prefix without a name takes the name of its VLAN",
			nets: []model.Entity{vlan, {ID: "netbox:network:a", Name: "192.0.2.0/24", Attrs: map[string]string{
				model.AttrPrefix: "192.0.2.0/24", model.AttrVLAN: "100", model.AttrGateway: "192.0.2.1"}}},
			memberOf: [][2]model.ID{{"netbox:network:a", "netbox:network:vlan-100"}},
			want:     []string{"| 100 | servers | 192.0.2.0/24 | 192.0.2.1 | 0 | netbox |"},
			notWant:  []string{"| 100 | servers | - |"},
		},
		{
			name: "a prefix with a name of its own keeps it",
			nets: []model.Entity{vlan, {ID: "netbox:network:a", Name: "frontend", Attrs: map[string]string{
				model.AttrPrefix: "192.0.2.0/24", model.AttrVLAN: "100"}}},
			memberOf: [][2]model.ID{{"netbox:network:a", "netbox:network:vlan-100"}},
			want:     []string{"| 100 | frontend | 192.0.2.0/24 | - | 0 | netbox |"},
			notWant:  []string{"servers"},
		},
		{
			name: "a VLAN without any prefix keeps its own row",
			nets: []model.Entity{vlan},
			want: []string{"| 100 | servers | - | - | 0 | netbox |"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vlanTable(t, tt.nets, tt.memberOf)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("unexpected %q in:\n%s", notWant, got)
				}
			}
		})
	}
}
