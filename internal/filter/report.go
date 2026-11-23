package filter

import (
	"fmt"
	"maps"
	"slices"
)

// SourceReport counts what the filter did to the entities of one source. It
// holds numbers only, so it can be printed without leaking a removed entity.
type SourceReport struct {
	// Total is the number of entities the collector produced.
	Total int
	// RemovedByDeny counts entities removed by a deny rule or because they
	// depend on a denied entity or carry a secret in their identity.
	RemovedByDeny int
	// RemovedByAllow counts entities that no allow rule admitted.
	RemovedByAllow int
	// HasAllow reports whether the source has allow rules.
	HasAllow bool
}

// Kept is the number of entities that passed the filter.
func (s SourceReport) Kept() int { return s.Total - s.RemovedByAllow - s.RemovedByDeny }

// Report maps a source name to its counts.
type Report map[string]SourceReport

// Sources returns the source names in the report, sorted.
func (r Report) Sources() []string { return slices.Sorted(maps.Keys(r)) }

// Warnings returns one warning per source whose allow rules left no entity, which is almost always a misconfigured rule.
func (r Report) Warnings() []string {
	var out []string
	for _, source := range r.Sources() {
		s := r[source]
		if s.HasAllow && s.Total > 0 && s.Kept() == 0 && s.RemovedByAllow > 0 {
			out = append(out, fmt.Sprintf("collector %s: no entity passed the allow rules (%d removed by allow, %d by deny); check the rules", source, s.RemovedByAllow, s.RemovedByDeny))
		}
	}
	return out
}
