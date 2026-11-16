package render

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/JtheGunner/youtrackt-docgen/internal/model"
)

// host builds the article for one host. The title is the hostname; the
// summary repeats FQDN, aliases and IPs as plain text so search finds them.
func (r *renderer) host(h model.Entity) Document {
	runsHere, dependsOn, neededBy, sameAs := r.hostRelations(h)

	sections := []Section{
		{ID: "summary", Heading: "Summary", Blocks: []Block{r.hostSummary(h)}},
		{ID: "network", Heading: "Network", Blocks: []Block{r.hostNetwork(h)}},
		{ID: "runs-here", Heading: "Runs here", Blocks: []Block{entityTable(runsHere, "Nothing is known to run here.")}},
		{ID: "depends-on", Heading: "Depends on", Blocks: []Block{entityTable(dependsOn, "No dependencies are known.")}},
		{ID: "needed-by", Heading: "Needed by", Blocks: []Block{entityTable(neededBy, "Nothing is known to depend on this host.")}},
		{ID: "links", Heading: "Links", Blocks: []Block{hostLinks(h, sameAs)}},
		r.generated(sources([]model.Entity{h}, sameAs)),
	}
	return Document{ID: string(h.ID), Title: h.Name, Sections: sections}
}

func (r *renderer) hostSummary(h model.Entity) Block {
	items := List{"Hostname: " + h.Name}
	if fqdn := h.Attrs["fqdn"]; fqdn != "" {
		items = append(items, "FQDN: "+fqdn)
	}
	if len(h.Aliases) > 0 {
		items = append(items, "Aliases: "+strings.Join(sortedCopy(h.Aliases), ", "))
	}
	if len(h.Addresses) > 0 {
		items = append(items, "IP addresses: "+strings.Join(addrStrings(h.Addresses), ", "))
	}
	if role := h.Labels["role"]; role != "" {
		items = append(items, "Role: "+role)
	}
	if desc := h.Attrs["description"]; desc != "" {
		items = append(items, "Description: "+desc)
	}
	items = append(items, "Source: "+h.Source)
	return items
}

// hostNetwork lists each address with the networks (and VLANs) it belongs to.
func (r *renderer) hostNetwork(h model.Entity) Block {
	if len(h.Addresses) == 0 {
		return Paragraph("No IP addresses are known.")
	}
	t := Table{Header: []string{"IP address", "Network", "VLAN"}}
	for _, addr := range slices.SortedFunc(slices.Values(h.Addresses), netip.Addr.Compare) {
		var names, vlans []string
		for _, n := range r.nets {
			if n.prefix.IsValid() && n.prefix.Contains(addr) {
				names = append(names, n.entity.Name)
				if v := n.entity.Attrs["vlan"]; v != "" {
					vlans = append(vlans, v)
				}
			}
		}
		t.Rows = append(t.Rows, []string{addr.String(), joinOrDash(names), joinOrDash(vlans)})
	}
	return t
}

// hostRelations collects what runs on h, what h depends on, what depends on
// h and the same machine as reported by other sources. Results are sorted by ID.
func (r *renderer) hostRelations(h model.Entity) (runsHere, dependsOn, neededBy, sameAs []model.Entity) {
	for _, rel := range r.g.RelationsTo(h.ID) {
		switch rel.Kind {
		case model.RelRunsOn:
			runsHere = r.appendEntity(runsHere, rel.From)
		case model.RelDependsOn:
			neededBy = r.appendEntity(neededBy, rel.From)
		case model.RelSameAs:
			sameAs = r.appendEntity(sameAs, rel.From)
		case model.RelMemberOf, model.RelExposes, model.RelRoutesTo:
		}
	}
	for _, rel := range r.g.RelationsFrom(h.ID) {
		switch rel.Kind {
		case model.RelExposes:
			runsHere = r.appendEntity(runsHere, rel.To)
		case model.RelDependsOn:
			dependsOn = r.appendEntity(dependsOn, rel.To)
		case model.RelSameAs:
			sameAs = r.appendEntity(sameAs, rel.To)
		case model.RelRunsOn, model.RelMemberOf, model.RelRoutesTo:
		}
	}
	for _, list := range []*[]model.Entity{&runsHere, &dependsOn, &neededBy, &sameAs} {
		sortEntities(list)
	}
	return runsHere, dependsOn, neededBy, sameAs
}

func (r *renderer) appendEntity(list []model.Entity, id model.ID) []model.Entity {
	e, ok := r.g.Entity(id)
	if !ok {
		return list
	}
	return append(list, e)
}

// sortEntities sorts by ID and drops duplicates.
func sortEntities(list *[]model.Entity) {
	slices.SortFunc(*list, func(a, b model.Entity) int { return strings.Compare(string(a.ID), string(b.ID)) })
	*list = slices.CompactFunc(*list, func(a, b model.Entity) bool { return a.ID == b.ID })
}

func entityTable(entities []model.Entity, empty string) Block {
	if len(entities) == 0 {
		return Paragraph(empty)
	}
	t := Table{Header: []string{"Name", "Kind", "Addresses", "Source"}}
	for _, e := range entities {
		t.Rows = append(t.Rows, []string{e.Name, string(e.Kind), joinOrDash(addrStrings(e.Addresses)), e.Source})
	}
	return t
}

func hostLinks(h model.Entity, sameAs []model.Entity) Block {
	var items List
	if u := safeURL(h.Attrs["url"]); u != "" {
		items = append(items, "Source record ("+h.Source+"): "+u)
	}
	for _, e := range sameAs {
		line := "Same machine in " + e.Source + ": " + e.Name
		if u := safeURL(e.Attrs["url"]); u != "" {
			line += " (" + u + ")"
		}
		items = append(items, line)
	}
	if len(items) == 0 {
		return Paragraph("No links are known.")
	}
	return items
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
