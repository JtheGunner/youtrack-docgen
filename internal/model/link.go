package model

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// LinkWarning reports a match that Link refused to turn into a relation
// because it was ambiguous: Subject matched every one of Candidates. It holds
// no free text built from entity data; Describe renders it from the entities
// the caller still publishes.
type LinkWarning struct {
	Subject    ID
	Candidates []ID
	// What names the kind of match, for example "host" or "service external IP".
	What string
}

// Describe renders the warning using only entities for which keep is true. It
// reports false when the subject is not kept or fewer than two candidates
// are, because then nothing ambiguous is left to show and the message would
// only hint at an entity that is not published.
func (w LinkWarning) Describe(keep func(ID) bool) (string, bool) {
	if !keep(w.Subject) {
		return "", false
	}
	var kept []string
	for _, id := range w.Candidates {
		if keep(id) {
			kept = append(kept, string(id))
		}
	}
	if len(kept) < 2 {
		return "", false
	}
	return fmt.Sprintf("ambiguous match, no relation set: %s %s matches %d candidates: %s",
		w.What, w.Subject, len(kept), strings.Join(kept, ", ")), true
}

// Link connects hosts that different sources report for the same machine, for
// example a Kubernetes node and its NetBox VM. Two hosts of different sources
// are linked with a RelSameAs relation when they share an IP address (in a
// compatible VRF) or a hostname (name or alias, compared ignoring case and a
// trailing dot; a bare short name also matches the first label of a fully
// qualified name). The relation is only set when the match is unique in both
// directions: a host with several candidates in one other source gets no
// same_as relation to any of them. The relation points from the lower to the
// higher ID, so repeated runs add nothing new.
// It then adds the runs_on and exposes relations between cluster objects and
// those hosts (see linkPlacement). Link returns the number of relations it added.
func Link(g *Graph) int {
	added, _ := LinkWithWarnings(g)
	return added
}

// LinkWithWarnings is Link that also reports every ambiguous match it skipped.
func LinkWithWarnings(g *Graph) (int, []LinkWarning) {
	hosts := g.ByKind(KindHost)
	vrfs := hostVRFs(g, hosts)
	names := make([][]string, len(hosts))
	for i, h := range hosts {
		names[i] = hostnames(h)
	}

	// candidates[i] holds, per foreign source, the hosts that match hosts[i].
	candidates := make([]map[string][]int, len(hosts))
	for i := range hosts {
		candidates[i] = map[string][]int{}
		for j := range hosts {
			if hosts[i].Source != hosts[j].Source && sameMachine(hosts[i], hosts[j], names[i], names[j], vrfs[i], vrfs[j]) {
				candidates[i][hosts[j].Source] = append(candidates[i][hosts[j].Source], j)
			}
		}
	}

	var warnings []LinkWarning
	for i, bySource := range candidates {
		for _, source := range sortedKeys(bySource) {
			if matches := bySource[source]; len(matches) > 1 {
				warnings = append(warnings, LinkWarning{Subject: hosts[i].ID, What: "host", Candidates: idsOf(hosts, matches)})
			}
		}
	}

	added := 0
	for i := range hosts {
		for _, matches := range candidates[i] {
			unique := len(matches) == 1 && len(candidates[matches[0]][hosts[i].Source]) == 1
			if !unique {
				// Not published, but remembered so the filter can withhold a
				// host whose possible twin is denied.
				for _, j := range matches {
					g.addAmbiguousTwin(hosts[i].ID, hosts[j].ID)
				}
				continue
			}
			if matches[0] < i {
				continue
			}
			rel := Relation{From: hosts[i].ID, To: hosts[matches[0]].ID, Kind: RelSameAs}
			if _, exists := g.relations[rel]; exists {
				continue
			}
			g.relations[rel] = struct{}{}
			added++
		}
	}
	placed, placementWarnings := linkPlacement(g)
	return added + placed, append(warnings, placementWarnings...)
}

func idsOf(hosts []Entity, indexes []int) []ID {
	out := make([]ID, len(indexes))
	for k, i := range indexes {
		out[k] = hosts[i].ID
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// addressVRFs maps each address of a host to the VRFs it lives in. An address
// without an entry has no known VRF.
type addressVRFs map[netip.Addr][]string

// hostVRFs derives the VRF of each host address from the IP address entities
// that belong, through an interface, to the host. Hosts only carry plain
// addresses; the VRF lives on the IP address entity of the source.
func hostVRFs(g *Graph, hosts []Entity) []addressVRFs {
	byHost := map[ID]addressVRFs{}
	for _, ip := range g.ByKind(KindIPAddress) {
		vrf := ip.Attrs[AttrVRF]
		if vrf == "" {
			continue
		}
		for _, nicRel := range g.RelationsFrom(ip.ID) {
			if nicRel.Kind != RelMemberOf {
				continue
			}
			for _, hostRel := range g.RelationsFrom(nicRel.To) {
				if hostRel.Kind != RelMemberOf {
					continue
				}
				if byHost[hostRel.To] == nil {
					byHost[hostRel.To] = addressVRFs{}
				}
				for _, addr := range ip.Addresses {
					if !slices.Contains(byHost[hostRel.To][addr], vrf) {
						byHost[hostRel.To][addr] = append(byHost[hostRel.To][addr], vrf)
					}
				}
			}
		}
	}
	out := make([]addressVRFs, len(hosts))
	for i, h := range hosts {
		out[i] = byHost[h.ID]
	}
	return out
}

// vrfsCompatible reports whether the two address occurrences can be the same
// address: unknown VRF on either side matches anything, otherwise the VRFs
// must overlap.
func vrfsCompatible(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	return slices.ContainsFunc(a, func(v string) bool { return slices.Contains(b, v) })
}

func sameMachine(a, b Entity, namesA, namesB []string, vrfsA, vrfsB addressVRFs) bool {
	for _, addr := range a.Addresses {
		if slices.Contains(b.Addresses, addr) && vrfsCompatible(vrfsA[addr], vrfsB[addr]) {
			return true
		}
	}
	return slices.ContainsFunc(namesA, func(n string) bool {
		return slices.ContainsFunc(namesB, func(m string) bool { return hostnamesMatch(n, m) })
	})
}

// hostnamesMatch reports whether two normalized names denote the same host:
// they are equal, or one is a bare short name equal to the first label of the
// other.
func hostnamesMatch(a, b string) bool {
	if a == b {
		return true
	}
	return !strings.Contains(a, ".") && strings.HasPrefix(b, a+".") ||
		!strings.Contains(b, ".") && strings.HasPrefix(a, b+".")
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
