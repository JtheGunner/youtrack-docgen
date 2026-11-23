package filter

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/model"
)

// Label keys that collectors use for the well-known match dimensions.
const (
	LabelTagPrefix = model.LabelTagPrefix // one "tag.<slug>" label per tag
	LabelRole      = "role"               // device or service role
	LabelTenant    = model.LabelTenant    // owning tenant
	LabelNamespace = "namespace"          // Kubernetes namespace
)

// Match selects entities. Every populated field must match (AND); the values
// inside one field are alternatives (OR). Values are glob patterns as understood
// by path.Match, compared case-insensitively. An empty Match matches nothing, so
// a mistyped rule never allows or denies everything by accident.
type Match struct {
	Kinds      []string          `yaml:"kinds"`
	Tags       []string          `yaml:"tags"`
	Roles      []string          `yaml:"roles"`
	Tenants    []string          `yaml:"tenants"`
	Namespaces []string          `yaml:"namespaces"`
	Labels     map[string]string `yaml:"labels"`
}

func (m Match) isEmpty() bool {
	return len(m.Kinds) == 0 && len(m.Tags) == 0 && len(m.Roles) == 0 &&
		len(m.Tenants) == 0 && len(m.Namespaces) == 0 && len(m.Labels) == 0
}

// Validate reports an empty Match or a malformed glob, so a bad rule fails at
// load time instead of silently never matching.
func (m Match) Validate() error {
	if m.isEmpty() {
		return errors.New("match has no criteria")
	}
	patterns := slices.Concat(m.Kinds, m.Tags, m.Roles, m.Tenants, m.Namespaces)
	for _, p := range m.Labels {
		patterns = append(patterns, p)
	}
	for _, p := range patterns {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("invalid pattern %q: %w", p, err)
		}
	}
	return nil
}

// matches reports whether e satisfies every populated field.
func (m Match) matches(e model.Entity) bool {
	if m.isEmpty() {
		return false
	}
	if len(m.Kinds) > 0 && !anyGlob(m.Kinds, string(e.Kind)) {
		return false
	}
	if len(m.Tags) > 0 && !anyGlob(m.Tags, tagsOf(e)...) {
		return false
	}
	if len(m.Roles) > 0 && !anyGlob(m.Roles, e.Labels[LabelRole]) {
		return false
	}
	if len(m.Tenants) > 0 && !anyGlob(m.Tenants, e.Labels[LabelTenant]) {
		return false
	}
	if len(m.Namespaces) > 0 && !anyGlob(m.Namespaces, namespaceOf(e)) {
		return false
	}
	for key, pattern := range m.Labels {
		value, ok := e.Labels[key]
		if !ok || !globMatch(pattern, value) {
			return false
		}
	}
	return true
}

// namespaceOf returns the namespace an entity lives in; a namespace entity
// lives in itself.
func namespaceOf(e model.Entity) string {
	if e.Kind == model.KindNamespace {
		return e.Name
	}
	return e.Labels[LabelNamespace]
}

// Rules is the allow/deny policy of one collector. Deny wins over allow. When
// Allow is empty everything not denied passes; otherwise an entity must match
// at least one Allow entry.
type Rules struct {
	Allow []Match `yaml:"allow"`
	Deny  []Match `yaml:"deny"`
}

func (r Rules) permits(e model.Entity) bool { return !r.denies(e) && r.allows(e) }

// denies reports whether a deny rule matches e.
func (r Rules) denies(e model.Entity) bool {
	return slices.ContainsFunc(r.Deny, func(m Match) bool { return m.matches(e) })
}

// allows reports whether the allow rules let e pass: always without allow
// rules, otherwise when one of them matches.
func (r Rules) allows(e model.Entity) bool {
	return len(r.Allow) == 0 || slices.ContainsFunc(r.Allow, func(m Match) bool { return m.matches(e) })
}

func anyGlob(patterns []string, values ...string) bool {
	for _, v := range values {
		for _, p := range patterns {
			if globMatch(p, v) {
				return true
			}
		}
	}
	return false
}

// globMatch compares case-insensitively; a malformed pattern never matches.
func globMatch(pattern, value string) bool {
	ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(value))
	return err == nil && ok
}

// tagsOf returns the tag slugs of e, taken from its "tag.<slug>" labels.
func tagsOf(e model.Entity) []string {
	var tags []string
	for key := range e.Labels {
		if slug, ok := strings.CutPrefix(key, LabelTagPrefix); ok && slug != "" {
			tags = append(tags, slug)
		}
	}
	return tags
}
