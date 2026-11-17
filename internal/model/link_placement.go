package model

import (
	"net/netip"
	"slices"
	"strings"
)

// linkPlacement derives where workloads run and what hosts expose, from the
// graph as it stands after same_as linking. A relation is only added when the
// target host is unambiguous: zero or several candidate hosts add nothing.
//
//   - A workload that runs_on a host of its own source (a Kubernetes node)
//     also runs_on the single host of another source that node is same_as.
//   - A host of another source owning a service's external IP exposes that
//     service.
//   - A host of another source whose name or alias equals an ingress hostname
//     exposes that ingress.
func linkPlacement(g *Graph) int {
	added := 0
	add := func(rel Relation) {
		if _, exists := g.relations[rel]; !exists {
			g.relations[rel] = struct{}{}
			added++
		}
	}

	for _, w := range g.ByKind(KindWorkload) {
		for _, rel := range g.RelationsFrom(w.ID) {
			node, ok := g.Entity(rel.To)
			if rel.Kind != RelRunsOn || !ok || node.Kind != KindHost || node.Source != w.Source {
				continue
			}
			if twin, ok := soleTwin(g, node); ok {
				add(Relation{From: w.ID, To: twin, Kind: RelRunsOn})
			}
		}
	}

	hosts := g.ByKind(KindHost)
	for _, s := range g.ByKind(KindService) {
		for _, raw := range strings.Split(s.Attrs[AttrExternalIPs], ",") {
			addr, err := netip.ParseAddr(strings.TrimSpace(raw))
			if err != nil {
				continue
			}
			if id, ok := soleForeignHost(hosts, s.Source, func(h Entity) bool { return slices.Contains(h.Addresses, addr) }); ok {
				add(Relation{From: id, To: s.ID, Kind: RelExposes})
			}
		}
	}
	for _, in := range g.ByKind(KindIngress) {
		for _, name := range hostnames(in) {
			if id, ok := soleForeignHost(hosts, in.Source, func(h Entity) bool { return slices.Contains(hostnames(h), name) }); ok {
				add(Relation{From: id, To: in.ID, Kind: RelExposes})
			}
		}
	}
	return added
}

// soleTwin returns the only host of another source that is same_as node.
func soleTwin(g *Graph, node Entity) (ID, bool) {
	var twins []ID
	for rel := range g.relations {
		if rel.Kind != RelSameAs {
			continue
		}
		var other ID
		switch node.ID {
		case rel.From:
			other = rel.To
		case rel.To:
			other = rel.From
		default:
			continue
		}
		if e, ok := g.Entity(other); ok && e.Kind == KindHost && e.Source != node.Source {
			twins = append(twins, other)
		}
	}
	if len(twins) != 1 {
		return "", false
	}
	return twins[0], true
}

// soleForeignHost returns the only host outside source that satisfies match.
func soleForeignHost(hosts []Entity, source string, match func(Entity) bool) (ID, bool) {
	var found []ID
	for _, h := range hosts {
		if h.Source != source && match(h) {
			found = append(found, h.ID)
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}
