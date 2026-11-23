package render

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/filter"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
	"github.com/JtheGunner/youtrack-docgen/internal/testutil/golden"
)

func findDoc(t *testing.T, root Document, id string) Document {
	t.Helper()
	var found *Document
	root.Walk(func(d Document) {
		if d.ID == id {
			found = &d
		}
	})
	if found == nil {
		t.Fatalf("document %q not found", id)
	}
	return *found
}

func mustEntity(t *testing.T, g *model.Graph, id model.ID) model.Entity {
	t.Helper()
	e, ok := g.Entity(id)
	if !ok {
		t.Fatalf("entity %s missing", id)
	}
	return e
}

// diagramFormats lists the golden name suffix and the options of each diagram
// format. The mermaid goldens keep their original names.
var diagramFormats = []struct {
	suffix string
	opts   Options
}{
	{"", Options{GeneratedAt: generatedAt, Diagrams: DiagramMermaid}},
	{"_text", Options{GeneratedAt: generatedAt, Diagrams: DiagramText}},
}

func TestHostDiagramGolden(t *testing.T) {
	for _, f := range diagramFormats {
		root := Render(fixture(t), f.opts)
		golden.Assert(t, "diagram_host"+f.suffix, []byte(findDoc(t, root, "netbox:host:web01").Markdown()))
	}
}

func TestServiceDiagramsGolden(t *testing.T) {
	for _, f := range diagramFormats {
		root := Render(fixture(t), f.opts)
		golden.Assert(t, "diagram_services"+f.suffix, []byte(findDoc(t, root, ServicesID).Markdown()))
	}
}

func TestNetworkDiagramGolden(t *testing.T) {
	for _, f := range diagramFormats {
		root := Render(fixture(t), f.opts)
		golden.Assert(t, "diagram_network"+f.suffix, []byte(findDoc(t, root, NetworkMapID).Markdown()))
	}
}

func TestNetworkDiagramWithoutPrefixesFallsBackToAddresses(t *testing.T) {
	g := model.NewGraph()
	if err := g.AddEntity(model.Entity{
		ID: "netbox:host:a", Kind: model.KindHost, Source: "netbox", Name: "a", Addresses: addrs("192.0.2.1"),
	}); err != nil {
		t.Fatal(err)
	}
	for _, f := range diagramFormats {
		root := Render(g, f.opts)
		golden.Assert(t, "diagram_network_no_prefix"+f.suffix, []byte(findDoc(t, root, NetworkMapID).Markdown()))
	}
}

func TestDiagramFormatDefaultsToText(t *testing.T) {
	g := fixture(t)
	unset := dump(Render(g, Options{GeneratedAt: generatedAt}))
	text := dump(Render(g, Options{GeneratedAt: generatedAt, Diagrams: DiagramText}))
	if string(unset) != string(text) {
		t.Error("the zero diagram format is not the text format")
	}
	if strings.Contains(string(unset), "```mermaid") {
		t.Error("the default output contains a mermaid block")
	}
	mermaid := string(dump(Render(g, Options{GeneratedAt: generatedAt, Diagrams: DiagramMermaid})))
	if !strings.Contains(mermaid, "```mermaid") {
		t.Error("the mermaid format emits no mermaid block")
	}
}

func TestDiagramsAreByteStable(t *testing.T) {
	g := fixture(t)
	first := dump(Render(g, Options{GeneratedAt: generatedAt}))
	if second := dump(Render(g, Options{GeneratedAt: generatedAt})); string(first) != string(second) {
		t.Fatal("diagram output differs between runs")
	}
}

func TestDeniedHostNeverAppearsInDiagrams(t *testing.T) {
	g := fixture(t)
	denied := model.Entity{
		ID: "netbox:host:hidden", Kind: model.KindHost, Source: "netbox", Name: "hidden-denied-host",
		Addresses: addrs("10.0.10.99"), Labels: map[string]string{"role": "denied"},
	}
	if err := g.AddEntity(denied); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []model.Relation{
		{From: "k8s:service:shop", To: denied.ID, Kind: model.RelRunsOn},
		{From: denied.ID, To: "netbox:host:db01", Kind: model.RelDependsOn},
	} {
		if err := g.AddRelation(rel); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(string(dump(Render(g, Options{GeneratedAt: generatedAt}))), "hidden-denied-host") {
		t.Fatal("precondition: the unfiltered graph should show the host")
	}

	f, err := filter.New(filter.Config{Collectors: map[string]filter.Rules{
		"netbox": {Deny: []filter.Match{{Kinds: []string{"host"}, Labels: map[string]string{"role": "denied"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	out := string(dump(Render(filtered, Options{GeneratedAt: generatedAt})))
	if strings.Contains(out, "hidden-denied-host") || strings.Contains(out, "10.0.10.99") {
		t.Errorf("denied host leaked into output:\n%s", out)
	}
}

func TestDiagramsEscapeHostileNamesAndKeepSecretsOut(t *testing.T) {
	g := model.NewGraph()
	hostile := "x\"]\n```\n## injected\n n9 --> n0; click n0 call evil()<script>"
	for _, e := range []model.Entity{
		{ID: "netbox:host:a", Kind: model.KindHost, Source: "netbox", Name: hostile,
			Attrs: map[string]string{"password": fakePassword}, Labels: map[string]string{"api_token": fakeToken}},
		{ID: "netbox:host:b", Kind: model.KindHost, Source: "netbox", Name: "b"},
	} {
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddRelation(model.Relation{From: "netbox:host:a", To: "netbox:host:b", Kind: model.RelDependsOn}); err != nil {
		t.Fatal(err)
	}
	d, ok := (&renderer{g: g}).entityDiagram(mustEntity(t, g, "netbox:host:a"))
	if !ok {
		t.Fatal("no diagram")
	}
	if strings.Count(d.markdown(), "```") != 2 {
		t.Errorf("label broke out of the code fence:\n%s", d.markdown())
	}
	for _, line := range strings.Split(strings.TrimSpace(d.Source), "\n")[1:] {
		if !strings.HasPrefix(line, "    n") {
			t.Errorf("unexpected diagram line %q", line)
		}
	}
	out := string(dump(Render(g, Options{GeneratedAt: generatedAt})))
	for _, forbidden := range []string{fakePassword, fakeToken, "<script>"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output contains %q", forbidden)
		}
	}
}

func TestDiagramCapsNeighbors(t *testing.T) {
	g := model.NewGraph()
	hub := model.Entity{ID: "netbox:host:hub", Kind: model.KindHost, Source: "netbox", Name: "hub"}
	if err := g.AddEntity(hub); err != nil {
		t.Fatal(err)
	}
	for i := range maxDiagramNeighbors + 3 {
		id := model.ID("netbox:host:h" + string(rune('a'+i)))
		if err := g.AddEntity(model.Entity{ID: id, Kind: model.KindHost, Source: "netbox", Name: string(id)}); err != nil {
			t.Fatal(err)
		}
		if err := g.AddRelation(model.Relation{From: id, To: hub.ID, Kind: model.RelDependsOn}); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := (&renderer{g: g}).entityDiagram(hub)
	if !strings.Contains(d.Source, "+3 more") {
		t.Errorf("missing overflow node:\n%s", d.Source)
	}
	if got := strings.Count(d.Source, "[\""); got != maxDiagramNeighbors+2 {
		t.Errorf("box node count = %d, want %d", got, maxDiagramNeighbors+2)
	}
}

func TestDiagramCapsAreVisibleInTheRenderedDocuments(t *testing.T) {
	g := model.NewGraph()
	add := func(e model.Entity) {
		t.Helper()
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	// One crowded network (maxNetworkDiagramHosts+4 hosts) plus enough empty
	// networks to exceed maxNetworkDiagramNetworks.
	const crowdedHosts = maxNetworkDiagramHosts + 4
	const networks = maxNetworkDiagramNetworks + 5
	for i := range networks {
		prefix := fmt.Sprintf("198.51.%d.0/24", i)
		add(model.Entity{
			ID: model.ID(fmt.Sprintf("netbox:network:n%02d", i)), Kind: model.KindNetwork, Source: "netbox",
			Name: prefix, Attrs: map[string]string{model.AttrPrefix: prefix},
		})
	}
	for i := range crowdedHosts {
		add(model.Entity{
			ID: model.ID(fmt.Sprintf("netbox:host:h%02d", i)), Kind: model.KindHost, Source: "netbox",
			Name:      fmt.Sprintf("h%02d.example.com", i),
			Addresses: []netip.Addr{netip.MustParseAddr(fmt.Sprintf("198.51.0.%d", i+1))},
		})
	}
	// A hub with more neighbours than maxDiagramNeighbors.
	add(model.Entity{ID: "netbox:host:hub", Kind: model.KindHost, Source: "netbox", Name: "hub.example.com"})
	for i := range maxDiagramNeighbors + 2 {
		id := model.ID(fmt.Sprintf("netbox:host:dep%02d", i))
		add(model.Entity{ID: id, Kind: model.KindHost, Source: "netbox", Name: string(id)})
		if err := g.AddRelation(model.Relation{From: id, To: "netbox:host:hub", Kind: model.RelDependsOn}); err != nil {
			t.Fatal(err)
		}
	}

	root := Render(g, Options{GeneratedAt: generatedAt, Diagrams: DiagramMermaid})

	overview := findDoc(t, root, NetworkMapID).Markdown()
	for _, want := range []string{
		fmt.Sprintf("+%d more hosts", crowdedHosts-maxNetworkDiagramHosts),
		fmt.Sprintf("+%d more networks", networks-maxNetworkDiagramNetworks),
	} {
		if !strings.Contains(overview, want) {
			t.Errorf("network overview lacks %q", want)
		}
	}
	hub := findDoc(t, root, "netbox:host:hub").Markdown()
	fence := "```"
	_, afterOpen, _ := strings.Cut(hub, fence+"mermaid")
	diagram, _, _ := strings.Cut(afterOpen, fence)
	if want := "+2 more"; !strings.Contains(diagram, want) {
		t.Errorf("host diagram lacks %q:\n%s", want, diagram)
	}
	if !strings.Contains(diagram, "dep00") || strings.Contains(diagram, fmt.Sprintf("dep%02d", maxDiagramNeighbors+1)) {
		t.Errorf("host diagram does not stop at the neighbour cap:\n%s", diagram)
	}
}
