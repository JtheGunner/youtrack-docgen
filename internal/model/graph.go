package model

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Errors returned by Graph mutations.
var (
	ErrInvalidEntity   = errors.New("invalid entity")
	ErrDuplicateEntity = errors.New("duplicate entity")
	ErrUnknownEntity   = errors.New("unknown entity")
	ErrInvalidRelation = errors.New("invalid relation")
)

// Graph holds entities and the relations between them. All query results are
// sorted, so output derived from a graph is deterministic.
type Graph struct {
	entities  map[ID]Entity
	relations map[Relation]struct{}
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		entities:  make(map[ID]Entity),
		relations: make(map[Relation]struct{}),
	}
}

// AddEntity stores a copy of e. The ID must be well-formed, its source and kind
// must match the entity's, and the ID must not exist yet.
func (g *Graph) AddEntity(e Entity) error {
	source, kind, _, err := ParseID(e.ID)
	if err != nil {
		return err
	}
	if kind != e.Kind || source != e.Source {
		return fmt.Errorf("%w: %s does not match source %q and kind %q", ErrInvalidEntity, e.ID, e.Source, e.Kind)
	}
	if e.Name == "" {
		return fmt.Errorf("%w: %s has no name", ErrInvalidEntity, e.ID)
	}
	if _, exists := g.entities[e.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateEntity, e.ID)
	}
	g.entities[e.ID] = e.clone()
	return nil
}

// AddRelation stores r. Both ends must exist, the kind must be known and the
// relation must not point at itself. Adding an existing relation is a no-op.
func (g *Graph) AddRelation(r Relation) error {
	if !r.Kind.valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidRelation, r.Kind)
	}
	if r.From == r.To {
		return fmt.Errorf("%w: %s relates to itself", ErrInvalidRelation, r.From)
	}
	for _, id := range []ID{r.From, r.To} {
		if _, ok := g.entities[id]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownEntity, id)
		}
	}
	g.relations[r] = struct{}{}
	return nil
}

// Entity returns a copy of the entity with the given ID.
func (g *Graph) Entity(id ID) (Entity, bool) {
	e, ok := g.entities[id]
	if !ok {
		return Entity{}, false
	}
	return e.clone(), true
}

// ByKind returns all entities of the given kind, sorted by ID.
func (g *Graph) ByKind(kind Kind) []Entity {
	return g.filter(func(e Entity) bool { return e.Kind == kind })
}

// ByAddress returns all entities that own addr, sorted by ID.
func (g *Graph) ByAddress(addr netip.Addr) []Entity {
	return g.filter(func(e Entity) bool { return slices.Contains(e.Addresses, addr) })
}

// ByHostname returns all entities whose name or alias equals name. The
// comparison ignores case and a trailing dot; short names do not match their
// fully qualified form.
func (g *Graph) ByHostname(name string) []Entity {
	want := normalizeHostname(name)
	if want == "" {
		return []Entity{}
	}
	return g.filter(func(e Entity) bool {
		if normalizeHostname(e.Name) == want {
			return true
		}
		return slices.ContainsFunc(e.Aliases, func(a string) bool { return normalizeHostname(a) == want })
	})
}

// Relations returns every relation, sorted.
func (g *Graph) Relations() []Relation {
	return g.relationsWhere(func(Relation) bool { return true })
}

// RelationsFrom returns the relations that start at id, sorted.
func (g *Graph) RelationsFrom(id ID) []Relation {
	return g.relationsWhere(func(r Relation) bool { return r.From == id })
}

// RelationsTo returns the relations that end at id, sorted.
func (g *Graph) RelationsTo(id ID) []Relation {
	return g.relationsWhere(func(r Relation) bool { return r.To == id })
}

func (g *Graph) filter(keep func(Entity) bool) []Entity {
	out := []Entity{}
	for _, e := range g.entities {
		if keep(e) {
			out = append(out, e.clone())
		}
	}
	slices.SortFunc(out, func(a, b Entity) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return out
}

func (g *Graph) relationsWhere(keep func(Relation) bool) []Relation {
	out := []Relation{}
	for r := range g.relations {
		if keep(r) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, compareRelations)
	return out
}

func normalizeHostname(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}
