package kubernetes

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/collector/mapbuild"
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
	*mapbuild.Builder

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
		Builder:    mapbuild.New(Source),
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
			addManifestLabels(b.Entities[id], o.Metadata.Labels)
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
	e, err := b.Add(model.KindNamespace, name, name)
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
	e, err := b.Add(model.KindWorkload, ns+"/"+kind+"/"+o.Metadata.Name, o.Metadata.Name)
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
	mapbuild.SetAttr(e, "replicas", string(o.Spec.Replicas))
	if o.Spec.Schedule != "" {
		e.Attrs["schedule"] = o.Spec.Schedule
	}
	mapbuild.SetAttr(e, "images", strings.Join(images(tmpl), ", "))
	b.podLabels[e.ID] = tmpl.Metadata.Labels
	if node := pinnedNode(tmpl); node != "" {
		nodeID, err := b.nodeHost(node)
		if err != nil {
			return err
		}
		b.Relate(e.ID, nodeID, model.RelRunsOn)
	}
	b.workloads[ns] = append(b.workloads[ns], e.ID)
	b.Relate(e.ID, nsID, model.RelMemberOf)
	return nil
}

// pinnedNode returns the node a pod template is pinned to by nodeName or by a
// kubernetes.io/hostname nodeSelector, or "" when it is not pinned.
func pinnedNode(t podTemplate) string {
	return mapbuild.FirstNonEmpty(t.Spec.NodeName, t.Spec.NodeSelector[hostnameLabel])
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
	e := b.Entities[id]
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
	e, err := b.Add(model.KindHost, name, name)
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
	e, err := b.Add(model.KindService, ns+"/"+o.Metadata.Name, o.Metadata.Name)
	if err != nil {
		return fmt.Errorf("service %s/%s: %w", ns, o.Metadata.Name, err)
	}
	applyLabels(e, o, ns)
	e.Attrs["namespace"] = ns
	mapbuild.SetAttr(e, "type", mapbuild.FirstNonEmpty(o.Spec.Type, "ClusterIP"))
	mapbuild.SetAttr(e, "ports", strings.Join(servicePorts(o.Spec.Ports), ", "))
	published := slices.Clone(o.Spec.ExternalIPs)
	for _, in := range o.Status.LoadBalancer.Ingress {
		published = append(published, in.IP)
	}
	var external []string
	for _, raw := range published {
		if addr, err := netip.ParseAddr(raw); err == nil && !slices.Contains(external, addr.String()) {
			external = append(external, addr.String())
		}
	}
	mapbuild.SetAttr(e, model.AttrExternalIPs, strings.Join(external, ","))
	for _, raw := range append(append([]string{o.Spec.ClusterIP}, o.Spec.ClusterIPs...), published...) {
		addr, err := netip.ParseAddr(raw)
		if err != nil || slices.Contains(e.Addresses, addr) {
			continue // "None", "" and duplicates carry no address
		}
		e.Addresses = append(e.Addresses, addr)
	}
	b.services[ns+"/"+o.Metadata.Name] = e.ID
	b.Relate(e.ID, nsID, model.RelMemberOf)
	if len(o.serviceSelector) == 0 {
		return nil
	}
	for _, wid := range b.workloads[ns] {
		if subset(o.serviceSelector, b.podLabels[wid]) {
			b.Relate(e.ID, wid, model.RelRoutesTo)
		}
	}
	return nil
}

func servicePorts(ports []servicePort) []string {
	var out []string
	for _, p := range ports {
		s := strconv.Itoa(p.Port) + "/" + mapbuild.FirstNonEmpty(p.Protocol, "TCP")
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
	e, err := b.Add(model.KindIngress, ns+"/"+o.Metadata.Name, o.Metadata.Name)
	if err != nil {
		return fmt.Errorf("ingress %s/%s: %w", ns, o.Metadata.Name, err)
	}
	applyLabels(e, o, ns)
	e.Attrs["namespace"] = ns
	mapbuild.SetAttr(e, "class", o.Spec.IngressClassName)
	b.Relate(e.ID, nsID, model.RelMemberOf)

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
	mapbuild.SetAttr(e, "hosts", strings.Join(hosts, ", "))
	seen := map[model.ID]bool{}
	for _, name := range backends {
		if id, ok := b.services[ns+"/"+name]; ok && !seen[id] {
			seen[id] = true
			b.Relate(e.ID, id, model.RelRoutesTo)
		}
	}
	return nil
}

// manifestLabelPrefix namespaces manifest labels. Entity labels also carry the
// collector's own filter dimensions (namespace, role, tenant, tag.*); a manifest
// must not be able to set those, so its labels live under their own prefix and
// rules match them as "label.<key>".
const manifestLabelPrefix = "label."

// addManifestLabels copies metadata.labels onto e under manifestLabelPrefix.
func addManifestLabels(e *model.Entity, labels map[string]string) {
	for k, v := range labels {
		e.Labels[manifestLabelPrefix+k] = v
	}
}

// applyLabels sets the namespace label and the prefixed manifest labels.
func applyLabels(e *model.Entity, o parsed, ns string) {
	e.Labels["namespace"] = ns
	addManifestLabels(e, o.Metadata.Labels)
}
