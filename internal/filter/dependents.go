package filter

import (
	"fmt"
	"maps"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/model"
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
	type edge struct {
		from, to  model.ID
		ambiguous bool
	}
	var edges []edge
	for _, rel := range g.Relations() {
		if rel.Kind == model.RelSameAs {
			edges = append(edges, edge{from: rel.From, to: rel.To}, edge{from: rel.To, to: rel.From})
		}
	}
	// An ambiguous match is no published relation, but one of the candidates
	// may be the same machine as a denied host: fail closed and withhold the
	// other end. Such a host is only a possible twin, so its other candidates,
	// which cannot be the same machine as the denied one, are not denied as twins.
	for _, pair := range g.AmbiguousTwins() {
		edges = append(edges, edge{from: pair.A, to: pair.B, ambiguous: true}, edge{from: pair.B, to: pair.A, ambiguous: true})
	}
	possibleTwin := map[model.ID]bool{}
	for changed := len(denied) > 0; changed; {
		changed = false
		for _, e := range edges {
			if _, isDenied := denied[e.from]; !isDenied || (e.ambiguous && possibleTwin[e.from]) {
				continue
			}
			if _, already := denied[e.to]; already {
				continue
			}
			if twin, ok := g.Entity(e.to); ok && twin.Kind == model.KindHost {
				denied[twin.ID] = twin
				possibleTwin[twin.ID] = e.ambiguous
				changed = true
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
// of. Every window inside such a run is parsed, never compared as text, so every
// way of writing an address is caught: compressed, expanded, upper case,
// IPv4-mapped, NAT64, next to a port or any other text.
var addressTokenRE = regexp.MustCompile(`[0-9A-Fa-f:.]+`)

// maxAddressLen is the longest address literal ("0000:...:0000" is 39
// characters, an IPv4-mapped one with dotted quad 45). It bounds the window.
const maxAddressLen = 45

// embeddingPrefixes are the IPv6 ranges that carry an IPv4 address in their
// low 32 bits: NAT64 (RFC 6052) and the deprecated IPv4-compatible range.
var embeddingPrefixes = []netip.Prefix{
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("::/96"),
}

const (
	digitChars = "0123456789"
	hexChars   = "0123456789abcdefABCDEF"
)

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
	// names are the denied hostnames, aliases and short names.
	names []string
	// addrs are the (unmapped) addresses of the denied hosts and of the IP
	// entities removed with them.
	addrs map[netip.Addr]bool
}

// minShortNameLen is the shortest short name (first label of a denied FQDN)
// that is removed from free text. Shorter ones ("db", "web") are too generic:
// removing them would damage unrelated text.
const minShortNameLen = 4

// shortName returns the first label of a dotted hostname, or "" when name is no
// dotted hostname, is an address literal, or the label is shorter than
// minShortNameLen.
func shortName(name string) string {
	label, _, dotted := strings.Cut(strings.TrimSpace(name), ".")
	if !dotted || len(label) < minShortNameLen {
		return ""
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return ""
	}
	return label
}

// reverseNames returns the reverse-DNS name of addr (in-addr.arpa or ip6.arpa).
func reverseNames(addr netip.Addr) []string {
	if addr.Is4() {
		b := addr.As4()
		return []string{fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", b[3], b[2], b[1], b[0])}
	}
	if !addr.Is6() {
		return nil
	}
	raw := addr.As16()
	var nibbles []string
	for i := len(raw) - 1; i >= 0; i-- {
		nibbles = append(nibbles, fmt.Sprintf("%x", raw[i]&0x0f), fmt.Sprintf("%x", raw[i]>>4))
	}
	return []string{strings.Join(nibbles, ".") + ".ip6.arpa"}
}

// buildNameRE compiles the denied names plus the reverse-DNS names of every
// denied address.
func (d *dependents) buildNameRE() {
	names := slices.Clone(d.names)
	for addr := range d.addrs {
		names = append(names, reverseNames(addr)...)
	}
	d.nameRE = boundaryRegexp(names, hostnameChars)
}

// newDependents collects what depends on the denied entities. denied are the
// hosts, deniedIPs the IP address entities, deniedNamespaces the namespaces
// and deniedClusters the Kubernetes clusters that are removed whatever their
// namespaces do.
func newDependents(g *model.Graph, denied map[model.ID]model.Entity, deniedIPs, deniedNamespaces, deniedClusters []model.Entity) dependents {
	d := dependents{removed: map[model.ID]bool{}, addrs: map[netip.Addr]bool{}}
	var members []model.Entity
	for _, c := range deniedClusters {
		members = append(members, d.removeClusterContents(g, c)...)
	}
	for _, ns := range deniedNamespaces {
		members = append(members, d.removeNamespaceMembers(g, ns)...)
	}
	for _, ip := range deniedIPs {
		d.addAddresses(ip)
		d.addIPNames(ip)
	}
	d.addMemberValues(g, members, denied, deniedIPs)
	for _, h := range denied {
		d.removed[h.ID] = true
		d.names = append(d.names, h.Name, shortName(h.Name))
		for _, alias := range h.Aliases {
			d.names = append(d.names, alias, shortName(alias))
		}
		for _, addr := range h.Addresses {
			d.addAddress(addr)
		}
	}
	d.buildNameRE()
	if !d.active() {
		return d
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
		if (denies > 0 || d.namesDenied(ip)) && others == 0 {
			d.removed[ip.ID] = true
			d.addAddresses(ip)
			d.addIPNames(ip)
		}
	}
	d.buildNameRE()
	return d
}

// removeNamespaceMembers marks the workloads, services and ingresses of a
// denied namespace as removed: through their member_of relation and, for
// objects of the same source, through the cluster and namespace in their key.
// A rule on a namespace name denies the namespace of that name in every
// cluster, so its members go in every cluster too. It returns the members it
// marked.
func (d dependents) removeNamespaceMembers(g *model.Graph, ns model.Entity) []model.Entity {
	var members []model.Entity
	mark := func(e model.Entity) {
		if !d.removed[e.ID] {
			d.removed[e.ID] = true
			members = append(members, e)
		}
	}
	for _, rel := range g.RelationsTo(ns.ID) {
		if member, ok := g.Entity(rel.From); ok && rel.Kind == model.RelMemberOf {
			mark(member)
		}
	}
	_, _, nsKey, err := model.ParseID(ns.ID)
	if err != nil {
		return members
	}
	for _, kind := range []model.Kind{model.KindWorkload, model.KindService, model.KindIngress} {
		for _, e := range g.ByKind(kind) {
			if e.Source != ns.Source {
				continue
			}
			if _, _, key, err := model.ParseID(e.ID); err == nil {
				if keyNS, found := namespaceKeyFromKey(kind, key); found && keyNS == nsKey {
					mark(e)
				}
			}
		}
	}
	return members
}

// removeClusterContents marks the namespaces of a denied cluster and their
// workloads, services and ingresses as removed, so nothing outlives its parent:
// through member_of relations and, for objects of the same source, through
// the cluster in their key. It returns the workloads, services and ingresses
// it marked; a namespace name is no value to scrub from free text.
func (d dependents) removeClusterContents(g *model.Graph, cluster model.Entity) []model.Entity {
	_, _, clusterKey, err := model.ParseID(cluster.ID)
	if err != nil {
		return nil
	}
	inCluster := func(e model.Entity) bool {
		if e.Source != cluster.Source {
			return false
		}
		_, _, key, err := model.ParseID(e.ID)
		if err != nil {
			return false
		}
		parts, ok := splitKubernetesKey(e.Kind, key)
		return ok && parts[0] == clusterKey
	}
	var namespaces []model.Entity
	for _, ns := range g.ByKind(model.KindNamespace) {
		if inCluster(ns) {
			namespaces = append(namespaces, ns)
		}
	}
	for _, ns := range memberSources(g, cluster.ID, model.KindNamespace) {
		if !inCluster(ns) {
			namespaces = append(namespaces, ns)
		}
	}
	var members []model.Entity
	for _, ns := range namespaces {
		d.removed[ns.ID] = true
		members = append(members, d.removeNamespaceMembers(g, ns)...)
	}
	for _, kind := range []model.Kind{model.KindWorkload, model.KindService, model.KindIngress} {
		for _, e := range g.ByKind(kind) {
			if !d.removed[e.ID] && inCluster(e) {
				d.removed[e.ID] = true
				members = append(members, e)
			}
		}
	}
	return members
}

// clusterDomains are the suffixes under which a Kubernetes service name
// resolves inside a cluster, below "<name>.<namespace>".
var clusterDomains = []string{"", ".svc", ".svc.cluster.local"}

// addMemberValues records what identifies the removed members of denied
// namespaces in free text: their names, their cluster-internal DNS names, their
// hosts and their addresses. A value that an entity which stays also owns
// (its name, an alias or an address) is not recorded: removing it would damage
// that entity's own text.
func (d *dependents) addMemberValues(g *model.Graph, members []model.Entity, deniedHosts map[model.ID]model.Entity, deniedIPs []model.Entity) {
	if len(members) == 0 {
		return
	}
	owned := ownedValues(g, d.removed, deniedHosts, deniedIPs)
	for _, m := range members {
		var names []string
		if _, _, key, err := model.ParseID(m.ID); err == nil {
			ns, found := namespaceFromKey(m.Kind, key)
			if !found {
				ns = m.Labels[LabelNamespace]
			}
			name := key[strings.LastIndex(key, "/")+1:]
			names = append(names, m.Name)
			if len(name) >= minShortNameLen {
				names = append(names, name)
			}
			if name != "" && ns != "" {
				for _, suffix := range clusterDomains {
					names = append(names, name+"."+ns+suffix)
				}
			}
		}
		names = append(names, m.Aliases...)
		for _, name := range names {
			if !owned[strings.ToLower(strings.TrimSpace(name))] {
				d.names = append(d.names, name)
			}
		}
		for _, addr := range m.Addresses {
			if !owned[addr.Unmap().WithZone("").String()] {
				d.addAddress(addr)
			}
		}
	}
}

// ownedValues returns the lower-cased names, aliases and addresses of every
// entity that is not removed with its namespace, a denied host or a denied IP.
// Those are scrubbed anyway or are the denied value itself.
func ownedValues(g *model.Graph, removed map[model.ID]bool, deniedHosts map[model.ID]model.Entity, deniedIPs []model.Entity) map[string]bool {
	skip := map[model.ID]bool{}
	for id := range removed {
		skip[id] = true
	}
	for id := range deniedHosts {
		skip[id] = true
	}
	for _, ip := range deniedIPs {
		skip[ip.ID] = true
	}
	owned := map[string]bool{}
	add := func(s string) {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			owned[s] = true
		}
	}
	for _, kind := range allKinds {
		for _, e := range g.ByKind(kind) {
			if skip[e.ID] {
				continue
			}
			add(e.Name)
			for _, a := range e.Aliases {
				add(a)
			}
			for _, a := range e.Addresses {
				add(a.Unmap().WithZone("").String())
			}
			if _, _, key, err := model.ParseID(e.ID); err == nil {
				add(key[strings.LastIndex(key, "/")+1:])
			}
		}
	}
	return owned
}

// addIPNames records the DNS name and aliases of a removed IP entity, so a host
// that carries them as an alias does not keep them.
func (d *dependents) addIPNames(ip model.Entity) {
	d.names = append(d.names, ip.Attrs[model.AttrDNSName])
	d.names = append(d.names, ip.Aliases...)
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
	// Go alternations are leftmost-first, so a word that is a prefix of another
	// must come after it: longest first, ties in alphabetical order.
	slices.SortFunc(words, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
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
	return d.nameRE.MatchString(e.Attrs[model.AttrDNSName]) || slices.ContainsFunc(e.Aliases, d.nameRE.MatchString)
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

// scrubAddressToken replaces every denied address inside one candidate token.
// Windows are tried longest first at each position, so a whole IPv4-mapped
// literal is replaced rather than only its dotted tail. A token is never
// skipped because of its length: a bounded window slides over it.
func (d dependents) scrubAddressToken(tok string) string {
	if !strings.ContainsAny(tok, ".:") {
		return tok
	}
	var b strings.Builder
	changed := false
	for i := 0; i < len(tok); {
		if end := d.deniedWindowEnd(tok, i); end > i {
			b.WriteString(Placeholder)
			i, changed = end, true
			continue
		}
		b.WriteByte(tok[i])
		i++
	}
	if !changed {
		return tok
	}
	return b.String()
}

// deniedWindowEnd returns the end of the longest window of tok starting at
// start that is a denied address standing on its own, or start if there is none.
func (d dependents) deniedWindowEnd(tok string, start int) int {
	for end := min(len(tok), start+maxAddressLen); end > start+1; end-- {
		// The cheap check first: only a denied address needs a boundary check.
		if d.isDeniedAddress(tok[start:end]) && standsAlone(tok, start, end) {
			return end
		}
	}
	return start
}

// standsAlone reports whether tok[start:end] may be replaced. Digits glued to
// the window only protect it when the longer run is itself a valid, different
// address ("2001:db8::50ab" is not "2001:db8::50"). Glued junk such as
// "a2001:db8::50" or "1192.0.2.50" is not an address, so the denied part is
// hidden: the scrubber may hide too much but must not leak. A colon or dot next
// to the window is a separator ("ip:", "addr:port", a trailing ":") and never
// extends it. The look-around is capped at the longest address literal, so a
// long run of digits cannot make the scan expensive.
func standsAlone(tok string, start, end int) bool {
	set := digitChars
	if strings.Contains(tok[start:end], ":") {
		set = hexChars
	}
	from, to := start, end
	for from > 0 && strings.ContainsRune(set, rune(tok[from-1])) {
		from--
		if to-from > maxAddressLen {
			return true // longer than any address literal, so not a different address
		}
	}
	for to < len(tok) && strings.ContainsRune(set, rune(tok[to])) {
		to++
		if to-from > maxAddressLen {
			return true
		}
	}
	if from == start && to == end {
		return true
	}
	_, err := netip.ParseAddr(tok[from:to])
	return err != nil
}

// isDeniedAddress parses s and checks it, unmapped and with any embedded IPv4
// address extracted, against the denied set.
func (d dependents) isDeniedAddress(s string) bool {
	if !strings.ContainsAny(s, ".:") {
		return false
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	addr = addr.Unmap().WithZone("")
	if d.addrs[addr] {
		return true
	}
	for _, prefix := range embeddingPrefixes {
		if addr.Is6() && prefix.Contains(addr) {
			raw := addr.As16()
			if d.addrs[netip.AddrFrom4([4]byte(raw[12:]))] {
				return true
			}
		}
	}
	return false
}

// namesDeniedValue reports whether the name or the ID key of e is, or contains,
// a denied hostname or address. Name and ID are the article title and key, so
// such an entity cannot be masked without changing its identity: it is dropped.
func (d dependents) namesDeniedValue(e model.Entity) bool {
	if !d.active() {
		return false
	}
	if d.scrubText(e.Name) != e.Name {
		return true
	}
	_, _, key, err := model.ParseID(e.ID)
	return err != nil || d.scrubText(key) != key
}

// mask removes the denied hostnames and addresses from e. The entity ID and
// name are identity and stay untouched (an entity whose identity names a denied
// value is dropped by namesDeniedValue instead); every other field is scrubbed.
// The input is not modified.
func (d dependents) mask(e model.Entity) model.Entity {
	if !d.active() {
		return e
	}
	// Keys are scrubbed too: a key such as "uplink.lab01.example.com" names the
	// host. Keys are visited in sorted order so a collision after scrubbing is
	// resolved deterministically (the first original key wins).
	scrub := func(in map[string]string) map[string]string {
		if in == nil {
			return nil
		}
		out := make(map[string]string, len(in))
		for _, k := range slices.Sorted(maps.Keys(in)) {
			scrubbed := d.scrubText(k)
			if _, taken := out[scrubbed]; !taken {
				out[scrubbed] = d.scrubText(in[k])
			}
		}
		return out
	}
	e.Labels = scrub(e.Labels)
	e.Attrs = scrub(e.Attrs)
	if e.Aliases != nil {
		// An alias that mentions a denied hostname or address is dropped.
		e.Aliases = slices.DeleteFunc(slices.Clone(e.Aliases), func(a string) bool {
			return d.scrubText(a) != a
		})
	}
	if e.Addresses != nil {
		e.Addresses = slices.DeleteFunc(slices.Clone(e.Addresses), func(a netip.Addr) bool { return d.addrs[a.Unmap().WithZone("")] })
	}
	return e
}
