package model

import (
	"net/netip"
	"slices"
	"strings"
)

// linkPlacement derives where workloads run and what hosts expose, from the
// graph as it stands after same_as linking. A relation is only added when the
// target host is unambiguous: zero candidates add nothing and several add
// nothing but a warning. It returns the number of relations added.
//
//   - A workload that runs_on a host of its own source (a Kubernetes node)
//     also runs_on the single host of another source that node is same_as.
//   - A host of another source owning one of a service's external or load
//     balancer IPs exposes that service.
//   - A host of another source whose name or alias equals an ingress hostname
//     (or is its short name) exposes that ingress.
//
// A workload that is not pinned to a node gets no host: without Node manifests
// the cluster does not say where it runs, and guessing would be wrong.
func linkPlacement(g *Graph) (int, []LinkWarning) {
	var warnings []LinkWarning
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
			twins := twinsOf(g, node)
			switch len(twins) {
			case 0:
			case 1:
				add(Relation{From: w.ID, To: twins[0], Kind: RelRunsOn})
			default:
				warnings = append(warnings, LinkWarning{Subject: w.ID, What: "workload node twin", Candidates: twins})
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
			found := foreignHosts(hosts, s.Source, func(h Entity) bool { return slices.Contains(h.Addresses, addr) })
			switch len(found) {
			case 0:
			case 1:
				add(Relation{From: found[0], To: s.ID, Kind: RelExposes})
			default:
				warnings = append(warnings, LinkWarning{Subject: s.ID, What: "service external IP", Candidates: found})
			}
		}
	}
	for _, in := range g.ByKind(KindIngress) {
		for _, name := range hostnames(in) {
			found := foreignHosts(hosts, in.Source, func(h Entity) bool {
				return slices.ContainsFunc(hostnames(h), func(n string) bool { return hostnamesMatch(n, name) })
			})
			switch len(found) {
			case 0:
			case 1:
				add(Relation{From: found[0], To: in.ID, Kind: RelExposes})
			default:
				warnings = append(warnings, LinkWarning{Subject: in.ID, What: "ingress hostname", Candidates: found})
			}
		}
	}
	return added, warnings
}

// twinsOf returns the hosts of other sources that are same_as node.
func twinsOf(g *Graph, node Entity) []ID {
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
	slices.Sort(twins)
	return twins
}

// foreignHosts returns the IDs of the hosts outside source that satisfy match.
func foreignHosts(hosts []Entity, source string, match func(Entity) bool) []ID {
	var found []ID
	for _, h := range hosts {
		if h.Source != source && match(h) {
			found = append(found, h.ID)
		}
	}
	return found
}
