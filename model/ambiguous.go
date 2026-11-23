package model

import "slices"

// AmbiguousTwin is a pair of hosts of different sources that match each other
// but could not be linked because at least one of them has several candidates.
// It is not a published relation: it never appears in Relations(). The filter
// uses it to fail closed, so a host whose possible twin is denied is withheld.
type AmbiguousTwin struct {
	A, B ID
}

func (g *Graph) addAmbiguousTwin(a, b ID) {
	if a > b {
		a, b = b, a
	}
	if g.ambiguous == nil {
		g.ambiguous = make(map[AmbiguousTwin]struct{})
	}
	g.ambiguous[AmbiguousTwin{A: a, B: b}] = struct{}{}
}

// AmbiguousTwins returns the ambiguous host pairs recorded by Link, sorted.
func (g *Graph) AmbiguousTwins() []AmbiguousTwin {
	out := make([]AmbiguousTwin, 0, len(g.ambiguous))
	for p := range g.ambiguous {
		out = append(out, p)
	}
	slices.SortFunc(out, func(x, y AmbiguousTwin) int {
		return cmpChain(compareIDs(x.A, y.A), compareIDs(x.B, y.B))
	})
	return out
}

func compareIDs(a, b ID) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
