package filter

import (
	"errors"
	"fmt"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
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

// allKinds lists every entity kind; model.Graph has no enumeration of its own.
var allKinds = []model.Kind{
	model.KindSite, model.KindNetwork, model.KindIPAddress, model.KindHost,
	model.KindInterface, model.KindCluster, model.KindNamespace,
	model.KindWorkload, model.KindService, model.KindIngress,
}

// Apply returns a new graph holding only the permitted entities, redacted, and
// the relations whose both ends survived. The input graph is not modified.
// Interfaces and IP addresses that only belong to a denied host are dropped;
// every surviving entity loses the denied hostnames and addresses, and an entity
// that is itself named after one is dropped. An entity whose name or ID key
// contains a secret the redactor would remove is dropped too: masking it would
// still leak through references.
// Sources without rules pass everything.
func (f *Filter) Apply(g *model.Graph) (*model.Graph, error) {
	out := model.NewGraph()
	deps := newDependents(g, f.deniedHosts(g))
	for _, kind := range allKinds {
		for _, e := range g.ByKind(kind) {
			if !f.rules[e.Source].permits(e) || deps.removed[e.ID] || deps.namesDeniedValue(e) || f.redactor.IdentityChanged(e) {
				continue
			}
			if err := out.AddEntity(deps.mask(f.redactor.Entity(e))); err != nil {
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
