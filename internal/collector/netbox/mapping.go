package netbox

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/JtheGunner/youtrackt-docgen/internal/model"
)

// objRef identifies a NetBox object by API family and numeric ID.
type objRef struct {
	family string
	id     int
}

const (
	famDevice = "device"
	famVM     = "vm"
	famDevNIC = "dcim.interface"
	famVMNIC  = "virtualization.vminterface"
)

type builder struct {
	entities map[model.ID]*model.Entity
	order    []model.ID
	rels     []model.Relation

	sites    map[int]model.ID
	vlans    map[int]model.ID
	hosts    map[objRef]model.ID
	ifaces   map[objRef]model.ID
	ifaceOwn map[model.ID]model.ID
}

func newBuilder() *builder {
	return &builder{
		entities: map[model.ID]*model.Entity{},
		sites:    map[int]model.ID{},
		vlans:    map[int]model.ID{},
		hosts:    map[objRef]model.ID{},
		ifaces:   map[objRef]model.ID{},
		ifaceOwn: map[model.ID]model.ID{},
	}
}

func (b *builder) build(s snapshot) error {
	steps := []func() error{
		func() error { return b.addSites(s.sites) },
		func() error { return b.addVLANs(s.vlans) },
		func() error { return b.addPrefixes(s.prefixes) },
		func() error { return b.addMachines(s.devices, famDevice) },
		func() error { return b.addMachines(s.vms, famVM) },
		func() error { return b.addInterfaces(s.deviceNICs, famDevNIC) },
		func() error { return b.addInterfaces(s.vmNICs, famVMNIC) },
		func() error { return b.addIPs(s.ips, s.prefixes) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) commit(g *model.Graph) error {
	for _, id := range b.order {
		if err := g.AddEntity(*b.entities[id]); err != nil {
			return fmt.Errorf("netbox: %w", err)
		}
	}
	for _, r := range b.rels {
		if err := g.AddRelation(r); err != nil {
			return fmt.Errorf("netbox: %w", err)
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

func (b *builder) addSites(sites []site) error {
	for _, s := range sites {
		key := firstNonEmpty(s.Slug, s.Name)
		e, err := b.add(model.KindSite, key, firstNonEmpty(s.Name, s.Slug))
		if err != nil {
			return fmt.Errorf("site %d: %w", s.ID, err)
		}
		setAttr(e, "status", s.Status.Value)
		setAttr(e, "description", s.Description)
		if s.Region != nil {
			setAttr(e, "region", s.Region.Name)
		}
		setTags(e, s.Tags)
		setTenant(e, s.Tenant)
		b.sites[s.ID] = e.ID
	}
	return nil
}

func (b *builder) addVLANs(vlans []vlan) error {
	for _, v := range vlans {
		key := "vlan-" + strconv.Itoa(v.VID)
		if v.Site != nil {
			key = "vlan-" + firstNonEmpty(v.Site.Slug, v.Site.Name) + "-" + strconv.Itoa(v.VID)
		}
		e, err := b.add(model.KindNetwork, key, firstNonEmpty(v.Name, key))
		if err != nil {
			return fmt.Errorf("vlan %d: %w", v.ID, err)
		}
		setAttr(e, "type", "vlan")
		setAttr(e, "vlan_id", strconv.Itoa(v.VID))
		setAttr(e, "status", v.Status.Value)
		setTags(e, v.Tags)
		setTenant(e, v.Tenant)
		b.vlans[v.ID] = e.ID
		if v.Site != nil {
			b.relateIf(e.ID, b.sites, v.Site.ID, model.RelMemberOf)
		}
	}
	return nil
}

func (b *builder) addPrefixes(prefixes []prefix) error {
	for _, p := range prefixes {
		e, err := b.add(model.KindNetwork, withVRF(p.Prefix, p.VRF), p.Prefix)
		if err != nil {
			return fmt.Errorf("prefix %d: %w", p.ID, err)
		}
		setAttr(e, "type", "prefix")
		setAttr(e, "cidr", p.Prefix)
		setAttr(e, "status", p.Status.Value)
		setAttr(e, "description", p.Description)
		if p.VRF != nil {
			setAttr(e, "vrf", p.VRF.Name)
		}
		if p.Role != nil {
			e.Labels["role"] = firstNonEmpty(p.Role.Slug, p.Role.Name)
		}
		setTags(e, p.Tags)
		setTenant(e, p.Tenant)
		if p.Site != nil {
			b.relateIf(e.ID, b.sites, p.Site.ID, model.RelMemberOf)
		}
		if p.VLAN != nil {
			b.relateIf(e.ID, b.vlans, p.VLAN.ID, model.RelMemberOf)
		}
	}
	return nil
}

func (b *builder) addMachines(ms []machine, family string) error {
	for _, m := range ms {
		name := ""
		if m.Name != nil {
			name = *m.Name
		}
		name = firstNonEmpty(name, fmt.Sprintf("%s-%d", family, m.ID))
		e, err := b.add(model.KindHost, name, name)
		if err != nil {
			return fmt.Errorf("%s %d: %w", family, m.ID, err)
		}
		setAttr(e, "type", family)
		setAttr(e, "status", m.Status.Value)
		setAttr(e, "description", m.Description)
		if m.PrimaryIP4 != nil {
			setAttr(e, "primary_ip4", m.PrimaryIP4.Address)
		}
		if m.PrimaryIP6 != nil {
			setAttr(e, "primary_ip6", m.PrimaryIP6.Address)
		}
		if m.Platform != nil {
			setAttr(e, "platform", m.Platform.Name)
		}
		if m.Cluster != nil {
			setAttr(e, "cluster", m.Cluster.Name)
		}
		if m.DeviceType != nil {
			setAttr(e, "model", m.DeviceType.Model)
		}
		role := m.Role
		if role == nil {
			role = m.DeviceRole
		}
		if role != nil {
			e.Labels["role"] = firstNonEmpty(role.Slug, role.Name)
		}
		setTags(e, m.Tags)
		setTenant(e, m.Tenant)
		b.hosts[objRef{family, m.ID}] = e.ID
		if m.Site != nil {
			b.relateIf(e.ID, b.sites, m.Site.ID, model.RelMemberOf)
		}
	}
	return nil
}

func (b *builder) addInterfaces(nics []iface, family string) error {
	hostFamily, owner := famDevice, func(i iface) *ref { return i.Device }
	if family == famVMNIC {
		hostFamily, owner = famVM, func(i iface) *ref { return i.VirtualMachine }
	}
	for _, n := range nics {
		o := owner(n)
		if o == nil {
			return fmt.Errorf("interface %d (%s) has no owner", n.ID, n.Name)
		}
		hostID, ok := b.hosts[objRef{hostFamily, o.ID}]
		if !ok {
			return fmt.Errorf("interface %d (%s): unknown owner %d", n.ID, n.Name, o.ID)
		}
		_, _, hostKey, err := model.ParseID(hostID)
		if err != nil {
			return err
		}
		e, err := b.add(model.KindInterface, hostKey+"/"+n.Name, hostKeyName(hostKey, n.Name))
		if err != nil {
			return fmt.Errorf("interface %d: %w", n.ID, err)
		}
		setAttr(e, "enabled", strconv.FormatBool(n.Enabled))
		setAttr(e, "description", n.Description)
		if n.Type != nil {
			setAttr(e, "type", n.Type.Value)
		}
		if n.MACAddress != nil {
			setAttr(e, "mac", *n.MACAddress)
		}
		if n.MTU != nil {
			setAttr(e, "mtu", strconv.Itoa(*n.MTU))
		}
		setTags(e, n.Tags)
		b.ifaces[objRef{family, n.ID}] = e.ID
		b.ifaceOwn[e.ID] = hostID
		b.relate(e.ID, hostID, model.RelMemberOf)
	}
	return nil
}

func (b *builder) addIPs(ips []ipAddress, prefixes []prefix) error {
	nets, err := b.networks(prefixes)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		addr, err := netip.ParsePrefix(ip.Address)
		if err != nil {
			return fmt.Errorf("ip address %d: %w", ip.ID, err)
		}
		e, err := b.add(model.KindIPAddress, withVRF(ip.Address, ip.VRF), ip.Address)
		if err != nil {
			return fmt.Errorf("ip address %d: %w", ip.ID, err)
		}
		setAttr(e, "status", ip.Status.Value)
		setAttr(e, "description", ip.Description)
		setAttr(e, "dns_name", ip.DNSName)
		if ip.VRF != nil {
			setAttr(e, "vrf", ip.VRF.Name)
		}
		setTags(e, ip.Tags)
		setTenant(e, ip.Tenant)
		e.Addresses = []netip.Addr{addr.Addr()}
		if ip.DNSName != "" {
			e.Aliases = []string{ip.DNSName}
		}
		if best, ok := nets.mostSpecific(addr.Addr(), vrfID(ip.VRF)); ok {
			b.relate(e.ID, best, model.RelMemberOf)
		}
		b.assign(e, ip, addr.Addr())
	}
	return nil
}

// assign links an IP to the interface it is assigned to and copies the address
// and DNS name to that interface and its host.
func (b *builder) assign(ipEntity *model.Entity, ip ipAddress, addr netip.Addr) {
	if ip.AssignedObjectID == nil {
		return
	}
	nicID, ok := b.ifaces[objRef{ip.AssignedObjectType, *ip.AssignedObjectID}]
	if !ok {
		return
	}
	b.relate(ipEntity.ID, nicID, model.RelMemberOf)
	for _, id := range []model.ID{nicID, b.ifaceOwn[nicID]} {
		owner := b.entities[id]
		if !slices.Contains(owner.Addresses, addr) {
			owner.Addresses = append(owner.Addresses, addr)
		}
	}
	host := b.entities[b.ifaceOwn[nicID]]
	if ip.DNSName != "" && !slices.Contains(host.Aliases, ip.DNSName) && !strings.EqualFold(host.Name, ip.DNSName) {
		host.Aliases = append(host.Aliases, ip.DNSName)
	}
}

type prefixIndex []indexedPrefix

type indexedPrefix struct {
	prefix netip.Prefix
	vrf    int
	id     model.ID
}

func (b *builder) networks(prefixes []prefix) (prefixIndex, error) {
	var idx prefixIndex
	for _, p := range prefixes {
		pfx, err := netip.ParsePrefix(p.Prefix)
		if err != nil {
			return nil, fmt.Errorf("prefix %d: %w", p.ID, err)
		}
		id, err := model.NewID(Source, model.KindNetwork, sanitizeKey(withVRF(p.Prefix, p.VRF)))
		if err != nil {
			return nil, err
		}
		idx = append(idx, indexedPrefix{pfx.Masked(), vrfID(p.VRF), id})
	}
	return idx, nil
}

func (idx prefixIndex) mostSpecific(addr netip.Addr, vrf int) (model.ID, bool) {
	var best indexedPrefix
	found := false
	for _, p := range idx {
		if p.vrf != vrf || !p.prefix.Contains(addr) {
			continue
		}
		if !found || p.prefix.Bits() > best.prefix.Bits() {
			best, found = p, true
		}
	}
	return best.id, found
}

func (b *builder) relateIf(from model.ID, targets map[int]model.ID, key int, kind model.RelationKind) {
	if to, ok := targets[key]; ok {
		b.relate(from, to, kind)
	}
}

func vrfID(v *ref) int {
	if v == nil {
		return 0
	}
	return v.ID
}

func withVRF(key string, v *ref) string {
	if v == nil {
		return key
	}
	return key + "@" + firstNonEmpty(v.Slug, v.Name)
}

// sanitizeKey removes the ID separator, which IPv6 addresses contain.
func sanitizeKey(k string) string { return strings.ReplaceAll(k, ":", "_") }

func hostKeyName(hostKey, nic string) string { return hostKey + " " + nic }

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

func setTags(e *model.Entity, tags []tag) {
	for _, t := range tags {
		e.Labels[model.LabelTagPrefix+t.Slug] = "true"
	}
}

func setTenant(e *model.Entity, tenant *ref) {
	if tenant != nil {
		e.Labels[model.LabelTenant] = firstNonEmpty(tenant.Slug, tenant.Name)
	}
}
