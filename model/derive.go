package model

// Attribute keys the NetBox collector sets on hosts; see its mapping.
const (
	attrMachineType    = "type"
	attrMachineCluster = "cluster"
)

// DeriveDependencies adds depends_on relations that follow from relations the
// sources already report. It runs after collection and linking, on the graph
// that survived the filter, so a dependency never mentions an entity that was
// rejected. It derives exactly these edges and no others:
//
//   - Ingress -> Service: an ingress that routes_to a service depends on it.
//   - Service -> Workload: a service that routes_to a workload depends on it.
//   - Workload -> Host: a workload that runs_on a host depends on it.
//   - Host -> Host: a host that runs_on another host (a virtual machine on its
//     hypervisor) depends on it.
//   - Host -> Cluster: a virtual machine (type "vm") depends on the cluster of
//     its source that is named by its "cluster" attribute.
//
// Only the endpoint kinds above count; a routes_to or runs_on between other
// kinds is ignored. The edges are not chained transitively. Deriving twice
// adds nothing new. DeriveDependencies returns the number of relations added.
func DeriveDependencies(g *Graph) int {
	added := 0
	add := func(from, to ID) {
		rel := Relation{From: from, To: to, Kind: RelDependsOn}
		if _, exists := g.relations[rel]; exists {
			return
		}
		g.relations[rel] = struct{}{}
		added++
	}

	for _, rel := range g.Relations() {
		from, okFrom := g.Entity(rel.From)
		to, okTo := g.Entity(rel.To)
		if !okFrom || !okTo {
			continue
		}
		switch {
		case rel.Kind == RelRoutesTo && from.Kind == KindIngress && to.Kind == KindService,
			rel.Kind == RelRoutesTo && from.Kind == KindService && to.Kind == KindWorkload,
			rel.Kind == RelRunsOn && from.Kind == KindWorkload && to.Kind == KindHost,
			rel.Kind == RelRunsOn && from.Kind == KindHost && to.Kind == KindHost:
			add(from.ID, to.ID)
		}
	}

	clusters := map[[2]string]ID{}
	for _, c := range g.ByKind(KindCluster) {
		clusters[[2]string{c.Source, c.Name}] = c.ID
	}
	for _, h := range g.ByKind(KindHost) {
		if h.Attrs[attrMachineType] != "vm" || h.Attrs[attrMachineCluster] == "" {
			continue
		}
		if id, ok := clusters[[2]string{h.Source, h.Attrs[attrMachineCluster]}]; ok {
			add(h.ID, id)
		}
	}
	return added
}
