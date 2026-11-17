package filter

import (
	"maps"
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

// dependents decides which entities depend on denied hosts.
//
// An interface that is a member of a denied host is removed. An IP address
// assigned only to such interfaces is removed as well. An IP address that is
// also assigned elsewhere stays, but scrubbed of the denied hosts' names.
type dependents struct {
	removed map[model.ID]bool
	// names are the denied hostnames (names and aliases) that must not leak.
	names []string
	// nameRE matches any of names case-insensitively; nil when names is empty.
	nameRE *regexp.Regexp
	// masked marks the surviving entities that referenced a denied owner.
	masked map[model.ID]bool
}

func newDependents(g *model.Graph, denied map[model.ID]model.Entity) dependents {
	d := dependents{removed: map[model.ID]bool{}, masked: map[model.ID]bool{}}
	if len(denied) == 0 {
		return d
	}
	for _, h := range denied {
		d.removed[h.ID] = true
		d.names = append(d.names, h.Name)
		d.names = append(d.names, h.Aliases...)
	}
	d.names = slices.DeleteFunc(d.names, func(s string) bool { return strings.TrimSpace(s) == "" })
	if len(d.names) > 0 {
		parts := make([]string, len(d.names))
		for i, n := range d.names {
			parts[i] = regexp.QuoteMeta(n)
		}
		d.nameRE = regexp.MustCompile("(?i)" + strings.Join(parts, "|"))
	}

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
		named := d.namesDenied(ip)
		switch {
		case (denies > 0 || named) && others == 0:
			d.removed[ip.ID] = true
		case denies > 0 || named:
			d.masked[ip.ID] = true
		}
	}
	return d
}

// namesDenied reports whether the DNS name or an alias of e is a denied hostname.
func (d dependents) namesDenied(e model.Entity) bool {
	if d.nameRE == nil {
		return false
	}
	return d.nameRE.MatchString(e.Attrs["dns_name"]) || slices.ContainsFunc(e.Aliases, d.nameRE.MatchString)
}

// mask removes the denied hostnames from the free-text fields of e.
func (d dependents) mask(e model.Entity) model.Entity {
	re := d.nameRE
	if re == nil {
		return e
	}

	scrub := func(in map[string]string) map[string]string {
		if in == nil {
			return nil
		}
		out := maps.Clone(in)
		for k, v := range out {
			out[k] = re.ReplaceAllString(v, Placeholder)
		}
		return out
	}
	e.Labels = scrub(e.Labels)
	e.Attrs = scrub(e.Attrs)
	if e.Aliases != nil {
		e.Aliases = slices.DeleteFunc(slices.Clone(e.Aliases), re.MatchString)
	}
	return e
}
