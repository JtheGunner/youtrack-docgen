package render

import (
	"slices"
	"strconv"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// kubernetesSource is the source name of the Kubernetes collector. Only its
// clusters become Kubernetes documents; clusters of other sources (NetBox) are
// not rendered as documents of their own.
const kubernetesSource = "kubernetes"

// kubernetesClusters returns the clusters reported by the Kubernetes collector,
// sorted by ID.
func kubernetesClusters(g *model.Graph) []model.Entity {
	var out []model.Entity
	for _, c := range g.ByKind(model.KindCluster) {
		if c.Source == kubernetesSource {
			out = append(out, c)
		}
	}
	return out
}

// cluster builds the article for one Kubernetes cluster. Its children are the
// articles of the cluster's namespaces, sorted by ID.
func (r *renderer) cluster(c model.Entity) Document {
	namespaces := r.membersOf(c.ID, model.KindNamespace)
	children := make([]Document, len(namespaces))
	for i, ns := range namespaces {
		children[i] = r.namespace(ns, c)
	}
	sections := []Section{
		{ID: "summary", Heading: "Summary", Blocks: []Block{List{"Cluster: " + c.Name, "Source: " + c.Source}}},
		{ID: "namespaces", Heading: "Namespaces", Blocks: []Block{r.namespaceTable(namespaces)}},
		r.generated(sources([]model.Entity{c}, namespaces)),
	}
	return Document{ID: string(c.ID), Title: c.Name, Sections: sections, Children: children}
}

func (r *renderer) namespaceTable(namespaces []model.Entity) Block {
	if len(namespaces) == 0 {
		return Paragraph("No namespaces are known.")
	}
	t := Table{Header: []string{"Namespace", "Workloads", "Services", "Ingresses"}}
	for _, ns := range namespaces {
		t.Rows = append(t.Rows, []string{
			ns.Name,
			strconv.Itoa(len(r.membersOf(ns.ID, model.KindWorkload))),
			strconv.Itoa(len(r.membersOf(ns.ID, model.KindService))),
			strconv.Itoa(len(r.membersOf(ns.ID, model.KindIngress))),
		})
	}
	return t
}

// namespace builds the article for one namespace of cluster c. The title is the
// namespace name; the summary repeats cluster and namespace as plain text so
// search finds them, as it does the service names, ingress names and ingress
// hostnames in the tables.
func (r *renderer) namespace(ns, c model.Entity) Document {
	workloads := r.membersOf(ns.ID, model.KindWorkload)
	services := r.membersOf(ns.ID, model.KindService)
	ingresses := r.membersOf(ns.ID, model.KindIngress)

	sections := []Section{
		{ID: "summary", Heading: "Summary", Blocks: []Block{List{
			"Cluster: " + c.Name, "Namespace: " + ns.Name, "Source: " + ns.Source,
		}}},
		{ID: "workloads", Heading: "Workloads", Blocks: []Block{r.workloadTable(workloads)}},
		{ID: "services", Heading: "Services", Blocks: []Block{r.serviceTable(services)}},
		{ID: "ingresses", Heading: "Ingresses", Blocks: []Block{r.ingressTable(ingresses)}},
		{ID: "links", Heading: "Links", Blocks: []Block{namespaceLinks(ns, workloads, services, ingresses)}},
		r.generated(sources([]model.Entity{ns}, workloads, services, ingresses)),
	}
	return Document{ID: string(ns.ID), Title: ns.Name, Sections: sections, Notes: ManualNotes()}
}

// membersOf returns the entities of the given kind that are members of id,
// sorted by ID.
func (r *renderer) membersOf(id model.ID, kind model.Kind) []model.Entity {
	var out []model.Entity
	for _, rel := range r.g.RelationsTo(id) {
		if rel.Kind != model.RelMemberOf {
			continue
		}
		if e, ok := r.g.Entity(rel.From); ok && e.Kind == kind {
			out = append(out, e)
		}
	}
	sortEntities(&out)
	return out
}

// targetNames returns the sorted distinct names of the entities of the given
// kind that e points to with a relation of the given kind.
func (r *renderer) targetNames(e model.Entity, rel model.RelationKind, kind model.Kind) []string {
	var names []string
	for _, x := range r.g.RelationsFrom(e.ID) {
		if x.Kind != rel {
			continue
		}
		if t, ok := r.g.Entity(x.To); ok && t.Kind == kind {
			names = append(names, t.Name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func (r *renderer) workloadTable(workloads []model.Entity) Block {
	if len(workloads) == 0 {
		return Paragraph("No workloads are known.")
	}
	t := Table{Header: []string{"Name", "Kind", "Replicas", "Images", "Runs on"}}
	for _, w := range workloads {
		t.Rows = append(t.Rows, []string{
			w.Name, dash(w.Attrs["kind"]), dash(w.Attrs["replicas"]), dash(w.Attrs["images"]),
			joinOrDash(r.targetNames(w, model.RelRunsOn, model.KindHost)),
		})
	}
	return t
}

func (r *renderer) serviceTable(services []model.Entity) Block {
	if len(services) == 0 {
		return Paragraph("No services are known.")
	}
	t := Table{Header: []string{"Name", "Type", "Ports", "Addresses", "Routes to"}}
	for _, s := range services {
		t.Rows = append(t.Rows, []string{
			s.Name, dash(s.Attrs["type"]), dash(s.Attrs["ports"]), joinOrDash(addrStrings(s.Addresses)),
			joinOrDash(r.targetNames(s, model.RelRoutesTo, model.KindWorkload)),
		})
	}
	return t
}

func (r *renderer) ingressTable(ingresses []model.Entity) Block {
	if len(ingresses) == 0 {
		return Paragraph("No ingresses are known.")
	}
	t := Table{Header: []string{"Name", "Hosts", "Routes to"}}
	for _, i := range ingresses {
		t.Rows = append(t.Rows, []string{
			i.Name, dash(i.Attrs["hosts"]),
			joinOrDash(r.targetNames(i, model.RelRoutesTo, model.KindService)),
		})
	}
	return t
}

// namespaceLinks lists the distinct repositories the namespace's objects were
// read from.
func namespaceLinks(ns model.Entity, groups ...[]model.Entity) Block {
	var urls []string
	for _, group := range append([][]model.Entity{{ns}}, groups...) {
		for _, e := range group {
			if u := safeURL(e.Attrs[model.AttrRepoURL]); u != "" {
				urls = append(urls, u)
			}
		}
	}
	slices.Sort(urls)
	urls = slices.Compact(urls)
	if len(urls) == 0 {
		return Paragraph("No links are known.")
	}
	items := make(List, len(urls))
	for i, u := range urls {
		items[i] = "Repository for " + ns.Source + ": " + u
	}
	return items
}
