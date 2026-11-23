package model

import (
	"slices"
	"strings"
)

// RelationKind is the type of a Relation.
type RelationKind string

// Relation kinds.
const (
	RelRunsOn    RelationKind = "runs_on"
	RelMemberOf  RelationKind = "member_of"
	RelExposes   RelationKind = "exposes"
	RelDependsOn RelationKind = "depends_on"
	RelRoutesTo  RelationKind = "routes_to"
	// RelSameAs links entities of different sources that describe the same
	// real-world object, for example a Kubernetes node and its NetBox VM.
	RelSameAs RelationKind = "same_as"
)

var relationKinds = []RelationKind{RelRunsOn, RelMemberOf, RelExposes, RelDependsOn, RelRoutesTo, RelSameAs}

func (k RelationKind) valid() bool { return slices.Contains(relationKinds, k) }

// Relation is a directed edge between two entities.
type Relation struct {
	From ID
	To   ID
	Kind RelationKind
}

func compareRelations(a, b Relation) int {
	return cmpChain(
		strings.Compare(string(a.From), string(b.From)),
		strings.Compare(string(a.To), string(b.To)),
		strings.Compare(string(a.Kind), string(b.Kind)),
	)
}

// cmpChain returns the first non-zero comparison result.
func cmpChain(results ...int) int {
	for _, r := range results {
		if r != 0 {
			return r
		}
	}
	return 0
}
