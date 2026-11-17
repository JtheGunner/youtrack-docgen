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

// addressTokenRE finds the runs of characters an IP address literal is made
// of. Candidates are parsed, never compared as text, so every way of writing an
// address is caught: compressed, expanded, upper case, IPv4-mapped, with a zone.
var addressTokenRE = regexp.MustCompile(`[0-9A-Fa-f:.]+`)

// maxAddressToken bounds the work per candidate; the longest address literal
// is 45 characters.
const maxAddressToken = 64

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
	// addrs are the (unmapped) addresses of the denied hosts and of the IP
	// entities removed with them.
	addrs map[netip.Addr]bool
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
			d.addAddress(addr)
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
	return d
}

// addAddresses records the addresses of a removed IP entity: its name is the
// address, and it may own further ones.
func (d dependents) addAddresses(ip model.Entity) {
	if addr, err := netip.ParseAddr(ip.Name); err == nil {
		d.addAddress(addr)
	}
	for _, addr := range ip.Addresses {
		d.addAddress(addr)
	}
}

// addAddress records addr in its unmapped form, so an IPv4-mapped IPv6 address
// and the plain IPv4 address count as the same one.
func (d dependents) addAddress(addr netip.Addr) {
	d.addrs[addr.Unmap().WithZone("")] = true
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
func (d dependents) active() bool { return d.nameRE != nil || len(d.addrs) > 0 }

// scrubText replaces the denied hostnames and addresses in s.
func (d dependents) scrubText(s string) string {
	if d.nameRE != nil {
		// Neighbouring matches share a boundary character, so repeat until stable.
		for range maxScrubPasses {
			next := d.nameRE.ReplaceAllString(s, "${1}"+Placeholder+"${3}")
			if next == s {
				break
			}
			s = next
		}
	}
	if len(d.addrs) == 0 {
		return s
	}
	return addressTokenRE.ReplaceAllStringFunc(s, d.scrubAddressToken)
}

// scrubAddressToken replaces the denied address inside one candidate token. The
// token may carry separators around the address: a trailing port (":8080"), a
// sentence dot, or the colons of a neighbouring literal.
func (d dependents) scrubAddressToken(tok string) string {
	if len(tok) > maxAddressToken {
		return tok
	}
	bases := []string{tok}
	if i := strings.LastIndexByte(tok, ':'); i > 0 && i < len(tok)-1 && isDigits(tok[i+1:]) {
		bases = append(bases, tok[:i]) // address without its port
	}
	for _, base := range bases {
		lead := len(base) - len(strings.TrimLeft(base, ":."))
		for l := 0; l <= lead; l++ {
			rest := base[l:]
			trail := len(rest) - len(strings.TrimRight(rest, ":."))
			for r := 0; r <= trail && r < len(rest); r++ {
				core := rest[:len(rest)-r]
				addr, err := netip.ParseAddr(core)
				if err != nil || !d.addrs[addr.Unmap().WithZone("")] {
					continue
				}
				return base[:l] + Placeholder + rest[len(core):] + tok[len(base):]
			}
		}
	}
	return tok
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
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
		e.Addresses = slices.DeleteFunc(slices.Clone(e.Addresses), func(a netip.Addr) bool { return d.addrs[a.Unmap().WithZone("")] })
	}
	return e
}
