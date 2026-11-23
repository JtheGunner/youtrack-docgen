package filter

import (
	"slices"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// Verdict is the outcome of checking an entity ID against the rules.
type Verdict int

// Verdicts of Filter.DecideID.
const (
	// Permitted: the rules let every entity with this ID pass.
	Permitted Verdict = iota
	// Denied: the rules remove every entity with this ID.
	Denied
	// Undecidable: the outcome depends on labels the ID does not carry.
	Undecidable
)

// tri is a three-valued truth: a rule field that needs a label the ID does
// not carry is unknown, not false.
type tri int

const (
	triFalse tri = iota
	triUnknown
	triTrue
)

// kubernetesSource is the source whose keys carry the namespace.
const kubernetesSource = "kubernetes"

// DecideID checks an entity that is only known by its ID, such as the article
// of an object that was not collected. Only what the ID carries is used: the
// source, the kind and, for Kubernetes, the namespace. A rule that needs more
// (tags, roles, tenants, labels) is undecidable unless a field the ID does
// carry already rules it out. Interfaces, IP addresses, networks and sites that
// only fail the allow rules stay undecided, because an allowed host may bring
// them along as references. Hostnames or addresses that a denied host drags
// along are not considered. An ID whose key the redactor would change is
// denied. A Kubernetes member (workload, service, ingress) follows the verdict
// of its namespace. Rules see the plain namespace, never the cluster prefix of
// the key, so a namespace rule decides that namespace in every cluster. A
// Kubernetes cluster lives in no namespace and follows kind rules only.
func (f *Filter) DecideID(id model.ID) Verdict {
	source, kind, key, err := model.ParseID(id)
	if err != nil {
		return Undecidable
	}
	verdict := f.decideKey(source, kind, key)
	if source != kubernetesSource || verdict == Denied {
		return verdict
	}
	// Apply removes the members of a denied namespace with it, so an ID that
	// names a member is denied when its namespace is, and undecided when the
	// namespace is.
	nsKey, found := namespaceKeyFromKey(kind, key)
	if !found || kind == model.KindNamespace {
		return verdict
	}
	switch f.decideKey(source, model.KindNamespace, nsKey) {
	case Denied:
		return Denied
	case Undecidable:
		return Undecidable
	}
	return verdict
}

// decideKey checks the rules against the fields the ID parts carry.
func (f *Filter) decideKey(source string, kind model.Kind, key string) Verdict {
	// Same rule as Apply: a key the redactor would change is removed, whatever
	// the rules say.
	if f.redactor.Text(key) != key {
		return Denied
	}
	rules := f.rules[source]
	known := knownFields{kind: string(kind)}
	switch {
	case source == kubernetesSource && kind == model.KindCluster:
		// A cluster lives in no namespace: like Apply, a namespace rule sees
		// the empty namespace, so only kind rules decide it.
		known.hasNamespace = true
	case source == kubernetesSource:
		known.namespace, known.hasNamespace = namespaceFromKey(kind, key)
	}
	deny := anyMatch(rules.Deny, known)
	allow := triTrue
	if len(rules.Allow) > 0 {
		allow = anyMatch(rules.Allow, known)
	}
	// Apply admits these kinds as references of an allowed host although no
	// allow rule matches them. An ID cannot tell, so a failed allow match is
	// undecided; only a deny rule denies them for certain.
	if allow == triFalse && deny != triTrue && isReferenceKind(kind) {
		allow = triUnknown
	}
	switch {
	case deny == triTrue || allow == triFalse:
		return Denied
	case deny == triFalse && allow == triTrue:
		return Permitted
	}
	return Undecidable
}

// isReferenceKind reports whether an allowed host can bring entities of kind
// along as references.
func isReferenceKind(kind model.Kind) bool {
	switch kind {
	case model.KindInterface, model.KindIPAddress, model.KindNetwork, model.KindSite:
		return true
	}
	return false
}

// knownFields are the match dimensions an ID carries.
type knownFields struct {
	kind         string
	namespace    string
	hasNamespace bool
}

// kubernetesKeySegments is the number of "/"-separated segments of each
// cluster-scoped Kubernetes key: cluster/namespace, cluster/namespace/kind/name
// and cluster/namespace/name.
var kubernetesKeySegments = map[model.Kind]int{
	model.KindNamespace: 2,
	model.KindWorkload:  4,
	model.KindService:   3,
	model.KindIngress:   3,
}

// splitKubernetesKey returns the segments of a cluster-scoped Kubernetes key.
// A key with the wrong number of segments or an empty one is not split, so a
// key that may be ambiguous never names a namespace.
func splitKubernetesKey(kind model.Kind, key string) ([]string, bool) {
	want, ok := kubernetesKeySegments[kind]
	if !ok {
		return nil, false
	}
	parts := strings.Split(key, "/")
	if len(parts) != want || slices.Contains(parts, "") {
		return nil, false
	}
	return parts, true
}

// namespaceFromKey returns the plain namespace a Kubernetes key names, without
// its cluster. Rules match the plain namespace, so a namespace rule decides the
// namespace of that name in every cluster.
func namespaceFromKey(kind model.Kind, key string) (string, bool) {
	parts, ok := splitKubernetesKey(kind, key)
	if !ok {
		return "", false
	}
	return parts[1], true
}

// namespaceKeyFromKey returns the key of the namespace entity a Kubernetes
// key lives in ("cluster/namespace"), or the key itself for a namespace.
func namespaceKeyFromKey(kind model.Kind, key string) (string, bool) {
	parts, ok := splitKubernetesKey(kind, key)
	if !ok {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

// anyMatch is the OR over the matches; with no match it is false.
func anyMatch(matches []Match, k knownFields) tri {
	result := triFalse
	for _, m := range matches {
		switch m.matchesKnown(k) {
		case triTrue:
			return triTrue
		case triUnknown:
			result = triUnknown
		}
	}
	return result
}

// matchesKnown is Match.matches for an entity known by its ID only.
func (m Match) matchesKnown(k knownFields) tri {
	if m.isEmpty() {
		return triFalse
	}
	result := triTrue
	if len(m.Kinds) > 0 && !anyGlob(m.Kinds, k.kind) {
		return triFalse
	}
	if len(m.Namespaces) > 0 {
		if !k.hasNamespace {
			result = triUnknown
		} else if !anyGlob(m.Namespaces, k.namespace) {
			return triFalse
		}
	}
	if len(m.Tags)+len(m.Roles)+len(m.Tenants)+len(m.Labels) > 0 {
		result = triUnknown
	}
	return result
}
