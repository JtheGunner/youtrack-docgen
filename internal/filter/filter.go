package filter

import (
	"errors"
	"fmt"

	"github.com/JtheGunner/youtrackt-docgen/internal/model"
)

// Config is the filter configuration: allow/deny rules per collector (keyed by
// the entity source name) and the global redaction settings.
type Config struct {
	Collectors map[string]Rules `yaml:"collectors"`
	Redact     RedactConfig     `yaml:"redact"`
}

// Filter drops excluded entities and redacts secrets.
type Filter struct {
	rules    map[string]Rules
	redactor *Redactor
}

// New validates cfg and builds a Filter.
func New(cfg Config) (*Filter, error) {
	redactor, err := NewRedactor(cfg.Redact)
	if err != nil {
		return nil, err
	}
	return &Filter{rules: cfg.Collectors, redactor: redactor}, nil
}

// Redactor exposes the redactor so renderers can scrub free text they compose.
func (f *Filter) Redactor() *Redactor { return f.redactor }

// allKinds lists every entity kind; model.Graph has no enumeration of its own.
var allKinds = []model.Kind{
	model.KindSite, model.KindNetwork, model.KindIPAddress, model.KindHost,
	model.KindInterface, model.KindCluster, model.KindNamespace,
	model.KindWorkload, model.KindService, model.KindIngress,
}

// Apply returns a new graph holding only the permitted entities, redacted, and
// the relations whose both ends survived. The input graph is not modified.
// Sources without rules pass everything.
func (f *Filter) Apply(g *model.Graph) (*model.Graph, error) {
	out := model.NewGraph()
	for _, kind := range allKinds {
		for _, e := range g.ByKind(kind) {
			if !f.rules[e.Source].permits(e) {
				continue
			}
			if err := out.AddEntity(f.redactor.Entity(e)); err != nil {
				return nil, fmt.Errorf("filter: %w", err)
			}
		}
	}
	for _, rel := range g.Relations() {
		err := out.AddRelation(rel)
		if errors.Is(err, model.ErrUnknownEntity) {
			continue // one end was filtered out
		}
		if err != nil {
			return nil, fmt.Errorf("filter: %w", err)
		}
	}
	return out, nil
}
