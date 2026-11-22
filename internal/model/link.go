package model

import "slices"

// Link connects hosts that different sources report for the same machine, for
// example a Kubernetes node and its NetBox VM. Two hosts of different sources
// are linked with a RelSameAs relation when they share an IP address or a
// hostname (name or alias, compared ignoring case and a trailing dot). The relation
// points from the lower to the higher ID, so repeated runs add nothing new.
// It then adds the runs_on and exposes relations between cluster objects and
// those hosts (see linkPlacement). Link returns the number of relations it added.
func Link(g *Graph) int {
	hosts := g.ByKind(KindHost)
	names := make([][]string, len(hosts))
	for i, h := range hosts {
		names[i] = hostnames(h)
	}

	added := 0
	for i := range hosts {
		for j := i + 1; j < len(hosts); j++ {
			if hosts[i].Source == hosts[j].Source || !sameMachine(hosts[i], hosts[j], names[i], names[j]) {
				continue
			}
			rel := Relation{From: hosts[i].ID, To: hosts[j].ID, Kind: RelSameAs}
			if _, exists := g.relations[rel]; exists {
				continue
			}
			g.relations[rel] = struct{}{}
			added++
		}
	}
	return added + linkPlacement(g)
}

func sameMachine(a, b Entity, namesA, namesB []string) bool {
	for _, addr := range a.Addresses {
		if slices.Contains(b.Addresses, addr) {
			return true
		}
	}
	return slices.ContainsFunc(namesA, func(n string) bool { return slices.Contains(namesB, n) })
}

// hostnames returns the normalized, non-empty names and aliases of e.
func hostnames(e Entity) []string {
	out := make([]string, 0, 1+len(e.Aliases))
	for _, n := range append([]string{e.Name}, e.Aliases...) {
		if n = normalizeHostname(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}
