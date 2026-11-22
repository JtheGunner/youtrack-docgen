package render

import (
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// Document IDs of the fixed documents.
const (
	RootID         = "overview:root"
	VLANsID        = "overview:vlans"
	IPAllocationID = "overview:ip-allocation"
	ServicesID     = "overview:services"
	NetworkMapID   = "overview:network-diagram"
)

// Options configure Render.
type Options struct {
	// GeneratedAt is stamped into every document. It is injected so output is
	// deterministic in tests.
	GeneratedAt time.Time
}

// Render builds the document tree for g: a root document whose children are
// the VLAN, IP allocation and service overviews followed by one document per
// host, sorted by host ID.
//
// Only an explicit set of entity fields is rendered (name, aliases, addresses,
// the attributes named by the model.Attr constants and the
// "role" label). Every other attribute or label never reaches a document, and
// URLs are stripped of credentials, query and fragment.
func Render(g *model.Graph, opts Options) Document {
	r := &renderer{g: g, at: opts.GeneratedAt.UTC(), nets: indexNetworks(g)}

	root := Document{ID: RootID, Title: "Infrastructure documentation"}
	root.Children = append(root.Children, r.vlans(), r.ipAllocation(), r.services(), r.networkMap())
	hosts := g.ByKind(model.KindHost)
	for _, h := range hosts {
		root.Children = append(root.Children, r.host(h))
	}

	index := List{}
	for _, c := range root.Children {
		index = append(index, c.Title)
	}
	root.Sections = []Section{
		{ID: "contents", Heading: "Contents", Blocks: []Block{index}},
		r.generated(sources(hosts, g.ByKind(model.KindNetwork), g.ByKind(model.KindService))),
	}
	return root
}

type renderer struct {
	g    *model.Graph
	at   time.Time
	nets []network
}

type network struct {
	entity model.Entity
	// name is what the network is called in articles. A prefix that has no
	// name of its own takes the name of the VLAN it belongs to.
	name   string
	prefix netip.Prefix // invalid when the entity has no valid prefix attribute
	// shownByMember is set for a network without prefix (a VLAN) that has
	// member networks with a prefix: those rows already show it.
	shownByMember bool
}

// label is the name with the prefix, for diagrams.
func (n network) label() string {
	if n.name == n.prefix.String() {
		return n.name
	}
	return n.name + " " + n.prefix.String()
}

func indexNetworks(g *model.Graph) []network {
	var out []network
	index := map[model.ID]int{}
	for _, e := range g.ByKind(model.KindNetwork) {
		n := network{entity: e, name: e.Name}
		if p, err := netip.ParsePrefix(e.Attrs[model.AttrPrefix]); err == nil {
			n.prefix = p.Masked()
		}
		index[e.ID] = len(out)
		out = append(out, n)
	}
	for i := range out {
		n := &out[i]
		if !n.prefix.IsValid() {
			continue
		}
		for _, rel := range g.RelationsFrom(n.entity.ID) {
			p, isNetwork := index[rel.To]
			if rel.Kind != model.RelMemberOf || !isNetwork || out[p].prefix.IsValid() {
				continue
			}
			out[p].shownByMember = true
			if n.entity.Name == n.entity.Attrs[model.AttrPrefix] {
				n.name = out[p].entity.Name
			}
		}
	}
	return out
}

func (r *renderer) generated(srcs []string) Section {
	from := "no sources"
	if len(srcs) > 0 {
		from = strings.Join(srcs, ", ")
	}
	text := fmt.Sprintf("Generated at %s from %s.", r.at.Format(time.RFC3339), from)
	return Section{ID: "generated", Heading: "Generated", Blocks: []Block{Paragraph(text)}, Volatile: true}
}

// sources returns the sorted distinct sources of the given entities.
func sources(groups ...[]model.Entity) []string {
	var out []string
	for _, group := range groups {
		for _, e := range group {
			out = append(out, e.Source)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// safeURL returns raw without credentials, query and fragment, or "" when it
// is not an absolute http(s) URL.
func safeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
	return urlEscaper.Replace(u.String())
}

// urlEscaper percent-encodes characters that are legal in a URL but carry
// meaning in Markdown (notably parentheses, which end a link destination), so
// the result is safe inside Markdown text or a link.
var urlEscaper = strings.NewReplacer(
	"(", "%28", ")", "%29", "[", "%5B", "]", "%5D", "<", "%3C", ">", "%3E",
	"`", "%60", "\\", "%5C", "|", "%7C", "*", "%2A", "_", "%5F", "!", "%21",
	"\"", "%22", "'", "%27", " ", "%20",
)

func addrStrings(addrs []netip.Addr) []string {
	sorted := slices.SortedFunc(slices.Values(addrs), netip.Addr.Compare)
	out := make([]string, len(sorted))
	for i, a := range sorted {
		out[i] = a.String()
	}
	return out
}

// networkNames returns the names of the networks whose prefix contains addr.
func (r *renderer) networkNames(addr netip.Addr) []string {
	var out []string
	for _, n := range r.nets {
		if n.prefix.IsValid() && n.prefix.Contains(addr) {
			out = append(out, n.name)
		}
	}
	return out
}

func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}
