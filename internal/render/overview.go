package render

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/model"
)

// vlans lists every network with its VLAN, prefix and gateway, ordered by
// VLAN number.
func (r *renderer) vlans() Document {
	nets := slices.DeleteFunc(slices.Clone(r.nets), func(n network) bool { return n.shownByMember })
	slices.SortFunc(nets, func(a, b network) int {
		return cmp.Or(
			compareVLAN(a.entity.Attrs[model.AttrVLAN], b.entity.Attrs[model.AttrVLAN]),
			strings.Compare(a.name, b.name),
			strings.Compare(string(a.entity.ID), string(b.entity.ID)),
		)
	})

	var block Block = Paragraph("No networks are known.")
	if len(nets) > 0 {
		t := Table{Header: []string{"VLAN", "Network", "Prefix", "Gateway", "Hosts", "Source"}}
		for _, n := range nets {
			t.Rows = append(t.Rows, []string{
				dash(n.entity.Attrs[model.AttrVLAN]), n.name, dash(n.entity.Attrs[model.AttrPrefix]),
				dash(n.entity.Attrs[model.AttrGateway]), strconv.Itoa(r.countHostsIn(n)), n.entity.Source,
			})
		}
		block = t
	}
	return r.overview(VLANsID, "VLANs", block, r.g.ByKind(model.KindNetwork))
}

// compareVLAN orders numeric VLAN IDs numerically; missing or non-numeric
// values sort last.
func compareVLAN(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA != nil && errB != nil:
		return strings.Compare(a, b)
	case errA != nil:
		return 1
	case errB != nil:
		return -1
	}
	return cmp.Compare(na, nb)
}

func (r *renderer) countHostsIn(n network) int {
	if !n.prefix.IsValid() {
		return 0
	}
	count := 0
	for _, h := range r.g.ByKind(model.KindHost) {
		if slices.ContainsFunc(h.Addresses, n.prefix.Contains) {
			count++
		}
	}
	return count
}

// ipAllocation lists every address once, with the host and interface that
// carry it. An address NetBox knows as an IP address object, as an interface
// address and as a host address is one row, not three.
func (r *renderer) ipAllocation() Document {
	type row struct {
		addr      netip.Addr
		owner     model.Entity
		iface     string
		source    string
		sortOwner model.ID
	}
	var rows []row
	seen := map[string]bool{}
	// Most specific first, so the richest description of an address wins.
	for _, kind := range []model.Kind{model.KindIPAddress, model.KindInterface, model.KindHost, model.KindService, model.KindWorkload, model.KindIngress} {
		for _, e := range r.g.ByKind(kind) {
			owner, iface := r.addressOwner(e)
			for _, a := range e.Addresses {
				key := a.String() + "|" + string(owner.ID)
				if seen[key] {
					continue
				}
				seen[key] = true
				rows = append(rows, row{addr: a, owner: owner, iface: iface, source: owner.Source, sortOwner: owner.ID})
			}
		}
	}
	slices.SortFunc(rows, func(a, b row) int {
		return cmp.Or(a.addr.Compare(b.addr), strings.Compare(string(a.sortOwner), string(b.sortOwner)), strings.Compare(a.iface, b.iface))
	})

	var block Block = Paragraph("No IP addresses are known.")
	if len(rows) > 0 {
		t := Table{Header: []string{"IP address", "Host", "Interface", "Kind", "Network", "Source"}}
		for _, rw := range rows {
			t.Rows = append(t.Rows, []string{
				rw.addr.String(), rw.owner.Name, dash(rw.iface), string(rw.owner.Kind),
				joinOrDash(r.networkNames(rw.addr)), rw.source,
			})
		}
		block = t
	}
	return r.overview(IPAllocationID, "IP allocation", block, r.g.ByKind(model.KindHost), r.g.ByKind(model.KindIPAddress))
}

// addressOwner resolves the entity that holds the addresses of e and the name
// of the interface they sit on. An IP address assigned to an interface and an
// interface both resolve to their host; everything else owns its addresses.
func (r *renderer) addressOwner(e model.Entity) (owner model.Entity, iface string) {
	switch e.Kind {
	case model.KindIPAddress:
		for _, rel := range r.g.RelationsFrom(e.ID) {
			if nic, ok := r.g.Entity(rel.To); ok && rel.Kind == model.RelMemberOf && nic.Kind == model.KindInterface {
				host, _ := r.addressOwner(nic)
				return host, nic.Name
			}
		}
	case model.KindInterface:
		for _, rel := range r.g.RelationsFrom(e.ID) {
			if host, ok := r.g.Entity(rel.To); ok && rel.Kind == model.RelMemberOf && host.Kind == model.KindHost {
				return host, e.Name
			}
		}
	}
	return e, ""
}

// services lists every service with where it runs and how to reach it.
func (r *renderer) services() Document {
	svcs := r.g.ByKind(model.KindService)
	var block Block = Paragraph("No services are known.")
	if len(svcs) > 0 {
		t := Table{Header: []string{"Service", "Runs on", "Addresses", "Source"}}
		for _, s := range svcs {
			t.Rows = append(t.Rows, []string{s.Name, joinOrDash(r.serviceHosts(s)), joinOrDash(addrStrings(s.Addresses)), s.Source})
		}
		block = t
	}
	doc := r.overview(ServicesID, "Services", block, svcs)
	if diagrams := r.serviceDiagrams(svcs); len(diagrams) > 0 {
		generated := doc.Sections[len(doc.Sections)-1]
		doc.Sections = append(doc.Sections[:len(doc.Sections)-1],
			Section{ID: "diagrams", Heading: "Service diagrams", Blocks: diagrams}, generated)
	}
	return doc
}

// serviceDiagrams returns a caption and a relation diagram for every service
// that has relations or networks to draw.
func (r *renderer) serviceDiagrams(svcs []model.Entity) []Block {
	var blocks []Block
	for _, s := range svcs {
		if d, ok := r.entityDiagram(s); ok {
			blocks = append(blocks, Paragraph(s.Name+" ("+s.Source+")"), r.diagramBlock(d))
		}
	}
	return blocks
}

// networkMap draws the networks with their hosts, or the IP addresses per host
// when no network prefix is known.
func (r *renderer) networkMap() Document {
	blocks := []Block{Paragraph("No network data is known.")}
	if d, note, ok := r.networkDiagram(); ok {
		blocks = []Block{r.diagramBlock(d)}
		if note != "" {
			blocks = []Block{Paragraph(note), r.diagramBlock(d)}
		}
	}
	doc := r.overview(NetworkMapID, "Network diagram", nil, r.g.ByKind(model.KindHost), r.g.ByKind(model.KindNetwork))
	doc.Sections[0].Blocks = blocks
	return doc
}

// serviceHosts returns the sorted names of the hosts a service runs on or is
// exposed by.
func (r *renderer) serviceHosts(s model.Entity) []string {
	var names []string
	add := func(id model.ID) {
		if e, ok := r.g.Entity(id); ok && e.Kind == model.KindHost {
			names = append(names, e.Name)
		}
	}
	for _, rel := range r.g.RelationsFrom(s.ID) {
		if rel.Kind == model.RelRunsOn {
			add(rel.To)
		}
	}
	for _, rel := range r.g.RelationsTo(s.ID) {
		if rel.Kind == model.RelExposes {
			add(rel.From)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func (r *renderer) overview(id, title string, block Block, from ...[]model.Entity) Document {
	return Document{
		ID:    id,
		Title: title,
		Sections: []Section{
			{ID: "table", Heading: title, Blocks: []Block{block}},
			r.generated(sources(from...)),
		},
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
