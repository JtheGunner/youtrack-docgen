package render

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// vlans lists every network with its VLAN and prefix, ordered by VLAN number.
func (r *renderer) vlans() Document {
	nets := slices.Clone(r.nets)
	slices.SortFunc(nets, func(a, b network) int {
		return cmp.Or(
			compareVLAN(a.entity.Attrs["vlan"], b.entity.Attrs["vlan"]),
			strings.Compare(a.entity.Name, b.entity.Name),
			strings.Compare(string(a.entity.ID), string(b.entity.ID)),
		)
	})

	var block Block = Paragraph("No networks are known.")
	if len(nets) > 0 {
		t := Table{Header: []string{"VLAN", "Network", "Prefix", "Hosts", "Source"}}
		for _, n := range nets {
			t.Rows = append(t.Rows, []string{
				dash(n.entity.Attrs["vlan"]), n.entity.Name, dash(n.entity.Attrs["prefix"]),
				strconv.Itoa(r.countHostsIn(n)), n.entity.Source,
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

// ipAllocation lists every address owned by an entity, in address order.
func (r *renderer) ipAllocation() Document {
	type row struct {
		addr   netip.Addr
		owner  model.Entity
		source string
	}
	var rows []row
	for _, kind := range []model.Kind{model.KindHost, model.KindIPAddress, model.KindService, model.KindWorkload, model.KindIngress, model.KindInterface} {
		for _, e := range r.g.ByKind(kind) {
			for _, a := range e.Addresses {
				rows = append(rows, row{addr: a, owner: e, source: e.Source})
			}
		}
	}
	slices.SortFunc(rows, func(a, b row) int {
		return cmp.Or(a.addr.Compare(b.addr), strings.Compare(string(a.owner.ID), string(b.owner.ID)))
	})

	var block Block = Paragraph("No IP addresses are known.")
	if len(rows) > 0 {
		t := Table{Header: []string{"IP address", "Owner", "Kind", "Network", "Source"}}
		for _, rw := range rows {
			t.Rows = append(t.Rows, []string{
				rw.addr.String(), rw.owner.Name, string(rw.owner.Kind),
				joinOrDash(r.networkNames(rw.addr)), rw.source,
			})
		}
		block = t
	}
	return r.overview(IPAllocationID, "IP allocation", block, r.g.ByKind(model.KindHost), r.g.ByKind(model.KindIPAddress))
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
	return r.overview(ServicesID, "Services", block, svcs)
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
