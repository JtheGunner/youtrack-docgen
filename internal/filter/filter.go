package filter

import (
	"errors"
	"fmt"
	"slices"

	"github.com/JtheGunner/youtrack-docgen/model"
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

// AllKinds returns every entity kind, in a stable order.
func AllKinds() []model.Kind { return slices.Clone(allKinds) }

// Apply returns a new graph holding only the permitted entities, redacted, and
// the relations whose both ends survived. The input graph is not modified.
// Interfaces and IP addresses that only belong to a denied host are dropped;
// every surviving entity loses the denied hostnames, their short names (4 or
// more characters, at word boundaries), addresses and reverse-DNS names, and a
// denied IP entity's address is removed everywhere. The members of a denied
// namespace are dropped. An entity that is itself named after one is dropped. An entity whose name or ID key
// contains a secret the redactor would remove is dropped too: masking it would
// still leak through references.
// An allow rule that admits a host also admits what belongs to it: its
// interfaces and their IP addresses, plus the sites and networks they refer to.
// Sites and networks are only references and are not expanded further; a deny
// rule still wins over every one of these.
// Sources without rules pass everything.
func (f *Filter) Apply(g *model.Graph) (*model.Graph, error) {
	out, _, err := f.ApplyWithReport(g)
	return out, err
}

// ApplyWithReport is Apply plus the per-source counts of what was removed. The
// report holds counts only, never an entity name, ID or address.
func (f *Filter) ApplyWithReport(g *model.Graph) (*model.Graph, Report, error) {
	out := model.NewGraph()
	report := Report{}
	references, deps := f.settleReferences(g)
	for id := range f.parentClusters(g, references, deps) {
		references[id] = true
	}
	for _, kind := range allKinds {
		for _, e := range g.ByKind(kind) {
			rules := f.rules[e.Source]
			src := report[e.Source]
			src.Total++
			src.HasAllow = len(rules.Allow) > 0
			// A missing allow match is counted before a cascade: an entity that is
			// not allowed leaves because of the allow rules, whatever else hits it.
			notAllowed := !rules.allows(e) && !references[e.ID]
			cascaded := deps.removed[e.ID] || deps.namesDeniedValue(e) || f.redactor.IdentityChanged(e)
			switch {
			case rules.denies(e):
				src.RemovedByDeny++
			case notAllowed:
				src.RemovedByAllow++
			case cascaded:
				src.RemovedByDeny++
			}
			report[e.Source] = src
			if rules.denies(e) || notAllowed || cascaded {
				continue
			}
			if err := out.AddEntity(deps.mask(f.redactor.Entity(e))); err != nil {
				return nil, nil, fmt.Errorf("filter: %w", err)
			}
		}
	}
	for _, rel := range g.Relations() {
		err := out.AddRelation(rel)
		if errors.Is(err, model.ErrUnknownEntity) {
			continue // one end was filtered out
		}
		if err != nil {
			return nil, nil, fmt.Errorf("filter: %w", err)
		}
	}
	return out, report, nil
}

// deniedByRules returns the entities of kind that the rules exclude. An entity
// that an allowed host references is not excluded by a missing allow match.
func (f *Filter) deniedByRules(g *model.Graph, kind model.Kind, references map[model.ID]bool) []model.Entity {
	var denied []model.Entity
	for _, e := range g.ByKind(kind) {
		rules := f.rules[e.Source]
		if rules.denies(e) || (!rules.allows(e) && !references[e.ID]) {
			denied = append(denied, e)
		}
	}
	return denied
}

// deniedClusters returns the clusters that are removed whatever their
// namespaces do: a deny rule matches them or the redactor would change their
// identity. Their namespaces and members go with them. Like DecideID, only the
// clusters of the kubernetes source get this cascade.
func (f *Filter) deniedClusters(g *model.Graph) []model.Entity {
	var denied []model.Entity
	for _, c := range g.ByKind(model.KindCluster) {
		if c.Source != kubernetesSource {
			continue
		}
		if f.rules[c.Source].denies(c) || f.redactor.IdentityChanged(c) {
			denied = append(denied, c)
		}
	}
	return denied
}

// parentClusters returns the clusters that a surviving namespace is a
// member_of. They stay although no allow rule matches them, so a namespace
// never loses its parent; a deny rule still wins over them. Only clusters of
// the kubernetes source are kept this way.
func (f *Filter) parentClusters(g *model.Graph, references map[model.ID]bool, deps dependents) map[model.ID]bool {
	parents := map[model.ID]bool{}
	survives := func(ns model.Entity) bool {
		rules := f.rules[ns.Source]
		return !rules.denies(ns) && (rules.allows(ns) || references[ns.ID]) &&
			!deps.removed[ns.ID] && !deps.namesDeniedValue(ns) && !f.redactor.IdentityChanged(ns)
	}
	for _, c := range g.ByKind(model.KindCluster) {
		if c.Source != kubernetesSource || f.rules[c.Source].denies(c) {
			continue
		}
		if slices.ContainsFunc(memberSources(g, c.ID, model.KindNamespace), survives) {
			parents[c.ID] = true
		}
	}
	return parents
}

// settleReferences computes the allowed-host references together with the
// dependents of the denied entities. Whether a host, interface or IP survives depends on those
// dependents, and the dependents depend on which IPs are references, so it
// starts with every allowed host and drops the references of hosts the cascade
// removes (and what hangs below removed interfaces and IPs) until nothing changes. The set only shrinks, so this terminates.
func (f *Filter) settleReferences(g *model.Graph) (map[model.ID]bool, dependents) {
	deniedHosts := f.deniedHosts(g)
	deniedClusters := f.deniedClusters(g)
	references := f.allowedReferences(g, func(model.Entity) bool { return true })
	for {
		deps := newDependents(g, deniedHosts, f.deniedByRules(g, model.KindIPAddress, references), f.deniedByRules(g, model.KindNamespace, references), deniedClusters)
		next := f.allowedReferences(g, func(e model.Entity) bool {
			return !deps.removed[e.ID] && !deps.namesDeniedValue(e) && !f.redactor.IdentityChanged(e)
		})
		if len(next) == len(references) {
			return next, deps
		}
		references = next
	}
}

// allowedReferences returns the entities that an allowed host drags in although
// no allow rule matches them: its interfaces, their IP addresses, the networks
// of those addresses and the host's sites. Only sources with allow rules
// matter, and an entity a deny rule rejects is never included. Networks and
// sites are the end of the expansion.
func (f *Filter) allowedReferences(g *model.Graph, survives func(model.Entity) bool) map[model.ID]bool {
	refs := map[model.ID]bool{}
	admit := func(e model.Entity) bool {
		if f.rules[e.Source].denies(e) || !survives(e) {
			return false
		}
		refs[e.ID] = true
		return true
	}
	for _, host := range g.ByKind(model.KindHost) {
		rules := f.rules[host.Source]
		if len(rules.Allow) == 0 || !rules.permits(host) || !survives(host) {
			continue
		}
		for _, to := range memberTargets(g, host.ID, model.KindSite) {
			admit(to)
		}
		for _, nic := range memberSources(g, host.ID, model.KindInterface) {
			if !admit(nic) {
				continue
			}
			for _, ip := range memberSources(g, nic.ID, model.KindIPAddress) {
				if !admit(ip) {
					continue
				}
				for _, network := range memberTargets(g, ip.ID, model.KindNetwork) {
					admit(network)
				}
			}
		}
	}
	return refs
}

// memberTargets returns the entities of kind that id is a member_of.
func memberTargets(g *model.Graph, id model.ID, kind model.Kind) []model.Entity {
	var out []model.Entity
	for _, rel := range g.RelationsFrom(id) {
		if e, ok := g.Entity(rel.To); ok && rel.Kind == model.RelMemberOf && e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// memberSources returns the entities of kind that are a member_of id.
func memberSources(g *model.Graph, id model.ID, kind model.Kind) []model.Entity {
	var out []model.Entity
	for _, rel := range g.RelationsTo(id) {
		if e, ok := g.Entity(rel.From); ok && rel.Kind == model.RelMemberOf && e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}
