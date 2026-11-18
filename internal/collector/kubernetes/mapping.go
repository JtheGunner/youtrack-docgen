package kubernetes

import (
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

const (
	defaultNamespace = "default"
	// hostnameLabel is the well-known node label a nodeSelector can pin a pod with.
	hostnameLabel = "kubernetes.io/hostname"
)

// workloadKinds are the manifest kinds mapped to model workloads.
var workloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"Job":         true,
	"CronJob":     true,
}

type builder struct {
	entities map[model.ID]*model.Entity
	order    []model.ID
	rels     []model.Relation

	namespaces map[string]model.ID
	declared   map[string]bool // namespaces with their own manifest
	nodes      map[string]model.ID
	nodeSeen   map[string]bool // nodes with their own manifest
	// podLabels holds the pod template labels per workload, for service selectors.
	podLabels map[model.ID]map[string]string
	workloads map[string][]model.ID // namespace -> workload IDs
	services  map[string]model.ID   // namespace/name -> service ID
}

func newBuilder() *builder {
	return &builder{
		entities:   map[model.ID]*model.Entity{},
		namespaces: map[string]model.ID{},
		declared:   map[string]bool{},
		nodes:      map[string]model.ID{},
		nodeSeen:   map[string]bool{},
		podLabels:  map[model.ID]map[string]string{},
		workloads:  map[string][]model.ID{},
		services:   map[string]model.ID{},
	}
}

func (b *builder) build(objects []parsed) error {
	// Namespaces first so every namespaced object can link to its namespace.
	for _, o := range objects {
		if o.Kind == "Namespace" {
			id, err := b.addNamespace(o.Metadata.Name, true)
			if err != nil {
				return err
			}
			maps.Copy(b.entities[id].Labels, o.Metadata.Labels)
		}
	}
	// Nodes before workloads so a workload can point at its node.
	for _, o := range objects {
		if o.Kind == "Node" {
			if err := b.addNode(o); err != nil {
				return err
			}
		}
	}
	for _, o := range objects {
		if workloadKinds[o.Kind] {
			if err := b.addWorkload(o); err != nil {
				return err
			}
		}
	}
	for _, o := range objects {
		if o.Kind == "Service" {
			if err := b.addService(o); err != nil {
				return err
			}
		}
	}
	for _, o := range objects {
		if o.Kind == "Ingress" {
			if err := b.addIngress(o); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *builder) commit(g *model.Graph) error {
	for _, id := range b.order {
		if err := g.AddEntity(*b.entities[id]); err != nil {
			return fmt.Errorf("kubernetes: %w", err)
		}
	}
	for _, r := range b.rels {
		if err := g.AddRelation(r); err != nil {
			return fmt.Errorf("kubernetes: %w", err)
		}
	}
	return nil
}

func (b *builder) add(kind model.Kind, key, name string) (*model.Entity, error) {
	id, err := model.NewID(Source, kind, sanitizeKey(key))
	if err != nil {
		return nil, err
	}
	if _, dup := b.entities[id]; dup {
		return nil, fmt.Errorf("%w: %s", model.ErrDuplicateEntity, id)
	}
	e := &model.Entity{
		ID: id, Kind: kind, Name: name, Source: Source,
		Labels: map[string]string{}, Attrs: map[string]string{},
	}
	b.entities[id] = e
	b.order = append(b.order, id)
	return e, nil
}

func (b *builder) relate(from, to model.ID, kind model.RelationKind) {
	b.rels = append(b.rels, model.Relation{From: from, To: to, Kind: kind})
}

// addNamespace creates the namespace once. An explicit Namespace manifest may
// follow an implicit one created from a referencing object; it is then a no-op.
func (b *builder) addNamespace(name string, explicit bool) (model.ID, error) {
	if name == "" {
		return "", errors.New("namespace manifest has no name")
	}
	if id, ok := b.namespaces[name]; ok {
		if explicit && b.declared[name] {
			return "", fmt.Errorf("%w: namespace %s", model.ErrDuplicateEntity, name)
		}
		b.declared[name] = b.declared[name] || explicit
		return id, nil
	}
	e, err := b.add(model.KindNamespace, name, name)
	if err != nil {
		return "", err
	}
	b.declared[name] = explicit
	b.namespaces[name] = e.ID
	return e.ID, nil
}

func (b *builder) namespaceOf(o parsed) (string, model.ID, error) {
	ns := o.Metadata.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	id, err := b.addNamespace(ns, false)
	return ns, id, err
}

func (b *builder) addWorkload(o parsed) error {
	if o.Metadata.Name == "" {
		return fmt.Errorf("%s manifest has no name", o.Kind)
	}
	ns, nsID, err := b.namespaceOf(o)
	if err != nil {
		return err
	}
	kind := strings.ToLower(o.Kind)
	e, err := b.add(model.KindWorkload, ns+"/"+kind+"/"+o.Metadata.Name, o.Metadata.Name)
	if err != nil {
		return fmt.Errorf("%s %s/%s: %w", o.Kind, ns, o.Metadata.Name, err)
	}
	tmpl := o.Spec.Template
	if o.Kind == "CronJob" {
		tmpl = o.Spec.JobTemplate.Spec.Template
	}
	applyLabels(e, o, ns)
	e.Attrs["kind"] = o.Kind
	e.Attrs["namespace"] = ns
	setAttr(e, "replicas", string(o.Spec.Replicas))
	if o.Spec.Schedule != "" {
		e.Attrs["schedule"] = o.Spec.Schedule
	}
	setAttr(e, "images", strings.Join(images(tmpl), ", "))
	b.podLabels[e.ID] = tmpl.Metadata.Labels
	if node := pinnedNode(tmpl); node != "" {
		nodeID, err := b.nodeHost(node)
		if err != nil {
			return err
		}
		b.relate(e.ID, nodeID, model.RelRunsOn)
	}
	b.workloads[ns] = append(b.workloads[ns], e.ID)
	b.relate(e.ID, nsID, model.RelMemberOf)
	return nil
}

// pinnedNode returns the node a pod template is pinned to by nodeName or by a
// kubernetes.io/hostname nodeSelector, or "" when it is not pinned.
func pinnedNode(t podTemplate) string {
	return firstNonEmpty(t.Spec.NodeName, t.Spec.NodeSelector[hostnameLabel])
}

// addNode maps a Node manifest to a host. Only the name and the reported
// addresses are read; labels, annotations and node info are not.
func (b *builder) addNode(o parsed) error {
	name := o.Metadata.Name
	if name == "" {
		return errors.New("node manifest has no name")
	}
	if b.nodeSeen[name] {
		return fmt.Errorf("%w: node %s", model.ErrDuplicateEntity, name)
	}
	b.nodeSeen[name] = true
	id, err := b.nodeHost(name)
	if err != nil {
		return err
	}
	e := b.entities[id]
	for _, a := range o.Status.Addresses {
		switch a.Type {
		case "InternalIP", "ExternalIP":
			if addr, err := netip.ParseAddr(a.Address); err == nil && !slices.Contains(e.Addresses, addr) {
				e.Addresses = append(e.Addresses, addr)
			}
		case "Hostname", "InternalDNS", "ExternalDNS":
			if a.Address != "" && a.Address != name && !slices.Contains(e.Aliases, a.Address) {
				e.Aliases = append(e.Aliases, a.Address)
			}
		}
	}
	return nil
}

// nodeHost returns the host for a node name, creating a name-only host when a
// workload references a node that has no manifest of its own.
func (b *builder) nodeHost(name string) (model.ID, error) {
	if id, ok := b.nodes[name]; ok {
		return id, nil
	}
	e, err := b.add(model.KindHost, name, name)
	if err != nil {
		return "", err
	}
	e.Attrs["type"] = "kubernetes-node"
	b.nodes[name] = e.ID
	return e.ID, nil
}

func images(t podTemplate) []string {
	var out []string
	for _, c := range append(slices.Clone(t.Spec.InitContainers), t.Spec.Containers...) {
		if c.Image != "" && !slices.Contains(out, c.Image) {
			out = append(out, c.Image)
		}
	}
	return out
}

func (b *builder) addService(o parsed) error {
	if o.Metadata.Name == "" {
		return errors.New("service manifest has no name")
	}
	ns, nsID, err := b.namespaceOf(o)
	if err != nil {
		return err
	}
	e, err := b.add(model.KindService, ns+"/"+o.Metadata.Name, o.Metadata.Name)
	if err != nil {
		return fmt.Errorf("service %s/%s: %w", ns, o.Metadata.Name, err)
	}
	applyLabels(e, o, ns)
	e.Attrs["namespace"] = ns
	setAttr(e, "type", firstNonEmpty(o.Spec.Type, "ClusterIP"))
	setAttr(e, "ports", strings.Join(servicePorts(o.Spec.Ports), ", "))
	var external []string
	for _, raw := range o.Spec.ExternalIPs {
		if addr, err := netip.ParseAddr(raw); err == nil && !slices.Contains(external, addr.String()) {
			external = append(external, addr.String())
		}
	}
	setAttr(e, model.AttrExternalIPs, strings.Join(external, ","))
	for _, raw := range append(append([]string{o.Spec.ClusterIP}, o.Spec.ClusterIPs...), o.Spec.ExternalIPs...) {
		addr, err := netip.ParseAddr(raw)
		if err != nil || slices.Contains(e.Addresses, addr) {
			continue // "None", "" and duplicates carry no address
		}
		e.Addresses = append(e.Addresses, addr)
	}
	b.services[ns+"/"+o.Metadata.Name] = e.ID
	b.relate(e.ID, nsID, model.RelMemberOf)
	if len(o.serviceSelector) == 0 {
		return nil
	}
	for _, wid := range b.workloads[ns] {
		if subset(o.serviceSelector, b.podLabels[wid]) {
			b.relate(e.ID, wid, model.RelRoutesTo)
		}
	}
	return nil
}

func servicePorts(ports []servicePort) []string {
	var out []string
	for _, p := range ports {
		s := strconv.Itoa(p.Port) + "/" + firstNonEmpty(p.Protocol, "TCP")
		if t := fmt.Sprint(p.TargetPort); p.TargetPort != nil && t != strconv.Itoa(p.Port) {
			s += "->" + t
		}
		out = append(out, s)
	}
	return out
}

// subset reports whether every selector pair is present in labels.
func subset(selector, labels map[string]string) bool {
	for k, v := range selector {
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}
	return true
}

func (b *builder) addIngress(o parsed) error {
	if o.Metadata.Name == "" {
		return errors.New("ingress manifest has no name")
	}
	ns, nsID, err := b.namespaceOf(o)
	if err != nil {
		return err
	}
	e, err := b.add(model.KindIngress, ns+"/"+o.Metadata.Name, o.Metadata.Name)
	if err != nil {
		return fmt.Errorf("ingress %s/%s: %w", ns, o.Metadata.Name, err)
	}
	applyLabels(e, o, ns)
	e.Attrs["namespace"] = ns
	setAttr(e, "class", o.Spec.IngressClassName)
	b.relate(e.ID, nsID, model.RelMemberOf)

	var hosts []string
	addHost := func(h string) {
		if h != "" && !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	var backends []string
	if d := o.Spec.DefaultBackend; d != nil && d.Service != nil {
		backends = append(backends, d.Service.Name)
	}
	for _, r := range o.Spec.Rules {
		addHost(r.Host)
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			if p.Backend.Service != nil {
				backends = append(backends, p.Backend.Service.Name)
			}
		}
	}
	for _, t := range o.Spec.TLS {
		for _, h := range t.Hosts {
			addHost(h)
		}
	}
	e.Aliases = hosts
	setAttr(e, "hosts", strings.Join(hosts, ", "))
	seen := map[model.ID]bool{}
	for _, name := range backends {
		if id, ok := b.services[ns+"/"+name]; ok && !seen[id] {
			seen[id] = true
			b.relate(e.ID, id, model.RelRoutesTo)
		}
	}
	return nil
}

// applyLabels copies metadata.labels onto e so label rules can match them. The
// namespace label is set last: it is the collector's own and a manifest label
// must not override it.
func applyLabels(e *model.Entity, o parsed, ns string) {
	maps.Copy(e.Labels, o.Metadata.Labels)
	e.Labels["namespace"] = ns
}

func sanitizeKey(k string) string { return strings.ReplaceAll(k, ":", "_") }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func setAttr(e *model.Entity, k, v string) {
	if v != "" {
		e.Attrs[k] = v
	}
}
