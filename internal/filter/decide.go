package filter

import (
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
// carry already rules it out. Hostnames or addresses that a denied host drags
// along are not considered. An ID whose key the redactor would change is
// denied.
func (f *Filter) DecideID(id model.ID) Verdict {
	source, kind, key, err := model.ParseID(id)
	if err != nil {
		return Undecidable
	}
	// Same rule as Apply: a key the redactor would change is removed, whatever
	// the rules say.
	if f.redactor.Text(key) != key {
		return Denied
	}
	rules := f.rules[source]
	known := knownFields{kind: string(kind)}
	if source == kubernetesSource {
		known.namespace, known.hasNamespace = namespaceFromKey(kind, key)
	}
	deny := anyMatch(rules.Deny, known)
	allow := triTrue
	if len(rules.Allow) > 0 {
		allow = anyMatch(rules.Allow, known)
	}
	switch {
	case deny == triTrue || allow == triFalse:
		return Denied
	case deny == triFalse && allow == triTrue:
		return Permitted
	}
	return Undecidable
}

// knownFields are the match dimensions an ID carries.
type knownFields struct {
	kind         string
	namespace    string
	hasNamespace bool
}

// namespaceFromKey returns the namespace a Kubernetes key names.
func namespaceFromKey(kind model.Kind, key string) (string, bool) {
	switch kind {
	case model.KindNamespace:
		return key, true
	case model.KindWorkload, model.KindService, model.KindIngress:
		ns, _, found := strings.Cut(key, "/")
		return ns, found
	}
	return "", false
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
