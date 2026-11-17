package filter

import (
	"maps"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// deniedHosts collects the hosts that the rules exclude, keyed by ID, so that
// everything that only exists because of them can be removed with them.
func (f *Filter) deniedHosts(g *model.Graph) map[model.ID]model.Entity {
	denied := make(map[model.ID]model.Entity)
	for _, h := range g.ByKind(model.KindHost) {
		if !f.rules[h.Source].permits(h) {
			denied[h.ID] = h
		}
	}
	// A same_as twin from another source is the same machine: deny it too,
	// transitively and in either direction.
	for changed := len(denied) > 0; changed; {
		changed = false
		for _, rel := range g.Relations() {
			if rel.Kind != model.RelSameAs {
				continue
			}
			for _, pair := range [][2]model.ID{{rel.From, rel.To}, {rel.To, rel.From}} {
				if _, isDenied := denied[pair[0]]; !isDenied {
					continue
				}
				if _, already := denied[pair[1]]; already {
					continue
				}
				if twin, ok := g.Entity(pair[1]); ok && twin.Kind == model.KindHost {
					denied[twin.ID] = twin
					changed = true
				}
			}
		}
	}
	return denied
}

// hostnameChars are the characters a hostname is made of. A denied name only
// matches when it is not embedded in a longer hostname, so denying "db" hides
// "db.example.com" but keeps "db2.example.com" and "mongodb.example.com".
const hostnameChars = `a-z0-9-`

// addressChars are the characters an IP address literal is made of.
const addressChars = `0-9a-f:`

// maxScrubPasses bounds the repeated replacement that is needed because
// neighbouring matches share their boundary character.
const maxScrubPasses = 8

// dependents decides which entities depend on denied hosts.
//
// An interface that is a member of a denied host is removed. An IP address
// assigned only to such interfaces is removed as well. Every surviving entity
// is scrubbed of the denied hosts' names and addresses.
type dependents struct {
	removed map[model.ID]bool
	// nameRE matches any denied hostname (names and aliases) at hostname
	// boundaries, case-insensitively; nil when there are none.
	nameRE *regexp.Regexp
	// addrs are the addresses of the denied hosts and of the IP entities removed
	// with them; addrRE matches them at address boundaries.
	addrs  map[netip.Addr]bool
	addrRE *regexp.Regexp
}

func newDependents(g *model.Graph, denied map[model.ID]model.Entity) dependents {
	d := dependents{removed: map[model.ID]bool{}, addrs: map[netip.Addr]bool{}}
	if len(denied) == 0 {
		return d
	}
	var names []string
	for _, h := range denied {
		d.removed[h.ID] = true
		names = append(names, h.Name)
		names = append(names, h.Aliases...)
		for _, addr := range h.Addresses {
			d.addrs[addr] = true
		}
	}
	d.nameRE = boundaryRegexp(names, hostnameChars)

	deniedIfaces := map[model.ID]bool{}
	for _, nic := range g.ByKind(model.KindInterface) {
		for _, rel := range g.RelationsFrom(nic.ID) {
			if _, ok := denied[rel.To]; ok && rel.Kind == model.RelMemberOf {
				deniedIfaces[nic.ID] = true
				d.removed[nic.ID] = true
			}
		}
	}
	for _, ip := range g.ByKind(model.KindIPAddress) {
		var denies, others int
		for _, rel := range g.RelationsFrom(ip.ID) {
			if rel.Kind != model.RelMemberOf {
				continue
			}
			if deniedIfaces[rel.To] {
				denies++
			} else if to, ok := g.Entity(rel.To); ok && to.Kind == model.KindInterface {
				others++
			}
		}
		if (denies > 0 || d.namesDenied(ip)) && others == 0 {
			d.removed[ip.ID] = true
			d.addAddresses(ip)
		}
	}
	d.addrRE = boundaryRegexp(addrStrings(d.addrs), addressChars)
	return d
}

// addAddresses records the addresses of a removed IP entity: its name is the
// address, and it may own further ones.
func (d dependents) addAddresses(ip model.Entity) {
	if addr, err := netip.ParseAddr(ip.Name); err == nil {
		d.addrs[addr] = true
	}
	for _, addr := range ip.Addresses {
		d.addrs[addr] = true
	}
}

// addrStrings renders the addresses in a stable order.
func addrStrings(addrs map[netip.Addr]bool) []string {
	out := make([]string, 0, len(addrs))
	for addr := range addrs {
		out = append(out, addr.String())
	}
	slices.Sort(out)
	return out
}

// boundaryRegexp matches any of words case-insensitively, but only when the
// neighbouring characters are not in chars. It returns nil when there is
// nothing to match. Group 1 and 3 hold the boundaries, group 2 the match.
func boundaryRegexp(words []string, chars string) *regexp.Regexp {
	words = slices.DeleteFunc(slices.Clone(words), func(s string) bool { return strings.TrimSpace(s) == "" })
	if len(words) == 0 {
		return nil
	}
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = regexp.QuoteMeta(w)
	}
	return regexp.MustCompile(`(?i)(^|[^` + chars + `])(` + strings.Join(parts, "|") + `)($|[^` + chars + `])`)
}

// namesDenied reports whether the DNS name or an alias of e is a denied hostname.
func (d dependents) namesDenied(e model.Entity) bool {
	if d.nameRE == nil {
		return false
	}
	return d.nameRE.MatchString(e.Attrs["dns_name"]) || slices.ContainsFunc(e.Aliases, d.nameRE.MatchString)
}

// active reports whether there is anything to scrub.
func (d dependents) active() bool { return d.nameRE != nil || d.addrRE != nil }

// scrubText replaces the denied hostnames and addresses in s.
func (d dependents) scrubText(s string) string {
	for _, re := range []*regexp.Regexp{d.nameRE, d.addrRE} {
		if re == nil {
			continue
		}
		// Neighbouring matches share a boundary character, so repeat until stable.
		for range maxScrubPasses {
			next := re.ReplaceAllString(s, "${1}"+Placeholder+"${3}")
			if next == s {
				break
			}
			s = next
		}
	}
	return s
}

// mask removes the denied hostnames and addresses from e. The entity ID and
// name are identity and stay untouched; every other field is scrubbed. The
// input is not modified.
func (d dependents) mask(e model.Entity) model.Entity {
	if !d.active() {
		return e
	}
	scrub := func(in map[string]string) map[string]string {
		if in == nil {
			return nil
		}
		out := maps.Clone(in)
		for k, v := range out {
			out[k] = d.scrubText(v)
		}
		return out
	}
	e.Labels = scrub(e.Labels)
	e.Attrs = scrub(e.Attrs)
	if e.Aliases != nil {
		e.Aliases = slices.DeleteFunc(slices.Clone(e.Aliases), func(a string) bool {
			return d.nameRE != nil && d.nameRE.MatchString(a)
		})
	}
	if e.Addresses != nil {
		e.Addresses = slices.DeleteFunc(slices.Clone(e.Addresses), func(a netip.Addr) bool { return d.addrs[a] })
	}
	return e
}
