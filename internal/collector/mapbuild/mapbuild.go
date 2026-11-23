// Package mapbuild holds the entity-building helpers shared by the collectors:
// a Builder that accumulates entities and relations in insertion order and the
// small attribute helpers every mapping needs.
package mapbuild

import (
	"fmt"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/model"
)

// Builder accumulates the entities and relations of one collector run.
// Entities keeps the entities by ID so mappings can amend them after creation.
type Builder struct {
	source   string
	Entities map[model.ID]*model.Entity
	order    []model.ID
	rels     []model.Relation
}

// New returns a Builder that stamps every entity with source.
func New(source string) *Builder {
	return &Builder{source: source, Entities: map[model.ID]*model.Entity{}}
}

// Add creates an entity of the given kind. The key is sanitized for use in
// the ID. Adding the same ID twice returns model.ErrDuplicateEntity.
func (b *Builder) Add(kind model.Kind, key, name string) (*model.Entity, error) {
	id, err := model.NewID(b.source, kind, SanitizeKey(key))
	if err != nil {
		return nil, err
	}
	if _, dup := b.Entities[id]; dup {
		return nil, fmt.Errorf("%w: %s", model.ErrDuplicateEntity, id)
	}
	e := &model.Entity{
		ID: id, Kind: kind, Name: name, Source: b.source,
		Labels: map[string]string{}, Attrs: map[string]string{},
	}
	b.Entities[id] = e
	b.order = append(b.order, id)
	return e, nil
}

// Relate records a relation between two entities.
func (b *Builder) Relate(from, to model.ID, kind model.RelationKind) {
	b.rels = append(b.rels, model.Relation{From: from, To: to, Kind: kind})
}

// Commit writes the accumulated entities and relations into g.
func (b *Builder) Commit(g *model.Graph) error {
	for _, id := range b.order {
		if err := g.AddEntity(*b.Entities[id]); err != nil {
			return fmt.Errorf("%s: %w", b.source, err)
		}
	}
	for _, r := range b.rels {
		if err := g.AddRelation(r); err != nil {
			return fmt.Errorf("%s: %w", b.source, err)
		}
	}
	return nil
}

// SanitizeKey removes the ID separator, which IPv6 addresses contain.
func SanitizeKey(k string) string { return strings.ReplaceAll(k, ":", "_") }

// FirstNonEmpty returns the first non-empty value, or "".
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// SetAttr sets an attribute unless the value is empty.
func SetAttr(e *model.Entity, k, v string) {
	if v != "" {
		e.Attrs[k] = v
	}
}
