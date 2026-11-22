package netbox

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/collector/mapbuild"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
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

// gatewayKey names both the prefix custom field and the IP address tag that
// mark a default gateway. NetBox has no built-in field for it.
const gatewayKey = "gateway"

type builder struct {
	base *url.URL // NetBox UI root, for links to objects

	*mapbuild.Builder

	sites    map[int]model.ID
	vlans    map[int]model.ID
	hosts    map[objRef]model.ID
	ifaces   map[objRef]model.ID
	ifaceOwn map[model.ID]model.ID

	keys         keyCounter
	nets         prefixIndex
	skippedHosts map[objRef]bool
	warn         func(string)
	skipped      []string // reasons objects were left out
}

func newBuilder(base *url.URL, warn func(string)) *builder {
	return &builder{
		base:         base,
		warn:         warn,
		keys:         keyCounter{},
		skippedHosts: map[objRef]bool{},
		Builder:      mapbuild.New(Source),
		sites:        map[int]model.ID{},
		vlans:        map[int]model.ID{},
		hosts:        map[objRef]model.ID{},
		ifaces:       map[objRef]model.ID{},
		ifaceOwn:     map[model.ID]model.ID{},
	}
}

// skip drops an object that cannot be mapped: it warns and records the reason.
func (b *builder) skip(msg string) {
	b.warn(msg)
	b.skipped = append(b.skipped, msg)
}

// addKeyed adds an entity under key, made unique when several NetBox objects
// share it. It skips the object with a warning when the entity cannot be added.
func (b *builder) addKeyed(kind model.Kind, key, name, label string, id int) (*model.Entity, bool) {
	e, err := b.Add(kind, b.keys.resolve(kind, key, label, id), name)
	if err != nil {
		b.skip(fmt.Sprintf("netbox: skipping %s %d: %v", label, id, err))
		return nil, false
	}
	return e, true
}

func siteKey(s site) string { return mapbuild.FirstNonEmpty(s.Slug, s.Name) }

func vlanKey(v vlan) string {
	if v.Site != nil {
		return "vlan-" + mapbuild.FirstNonEmpty(v.Site.Slug, v.Site.Name) + "-" + strconv.Itoa(v.VID)
	}
	return "vlan-" + strconv.Itoa(v.VID)
}

func machineName(m machine, family string) string {
	name := ""
	if m.Name != nil {
		name = *m.Name
	}
	return mapbuild.FirstNonEmpty(name, fmt.Sprintf("%s-%d", family, m.ID))
}

// countKeys registers every key before mapping so that colliding objects can
// all be disambiguated, whatever order they arrive in.
func (b *builder) countKeys(s snapshot) {
	for _, x := range s.sites {
		b.keys.count(model.KindSite, siteKey(x))
	}
	for _, x := range s.vlans {
		b.keys.count(model.KindNetwork, vlanKey(x))
	}
	for _, x := range s.prefixes {
		b.keys.count(model.KindNetwork, withVRF(x.Prefix, x.VRF))
	}
	for _, x := range s.devices {
		b.keys.count(model.KindHost, machineName(x, famDevice))
	}
	for _, x := range s.vms {
		b.keys.count(model.KindHost, machineName(x, famVM))
	}
	for _, x := range s.ips {
		b.keys.count(model.KindIPAddress, withVRF(x.Address, x.VRF))
	}
}

func (b *builder) build(s snapshot) error {
	b.countKeys(s)
	steps := []func() error{
		func() error { return b.addSites(s.sites) },
		func() error { return b.addVLANs(s.vlans) },
		func() error { return b.addPrefixes(s.prefixes) },
		func() error { return b.addMachines(s.devices, famDevice) },
		func() error { return b.addMachines(s.vms, famVM) },
		func() error { return b.addInterfaces(s.deviceNICs, famDevNIC) },
		func() error { return b.addInterfaces(s.vmNICs, famVMNIC) },
		func() error { b.addIPs(s.ips); return nil },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) addSites(sites []site) error {
	for _, s := range sites {
		e, ok := b.addKeyed(model.KindSite, siteKey(s), mapbuild.FirstNonEmpty(s.Name, s.Slug), "site", s.ID)
		if !ok {
			continue
		}
		mapbuild.SetAttr(e, "status", s.Status.Value)
		mapbuild.SetAttr(e, model.AttrDescription, s.Description)
		if s.Region != nil {
			mapbuild.SetAttr(e, "region", s.Region.Name)
		}
		setTags(e, s.Tags)
		setTenant(e, s.Tenant)
		b.sites[s.ID] = e.ID
	}
	return nil
}

func (b *builder) addVLANs(vlans []vlan) error {
	for _, v := range vlans {
		key := vlanKey(v)
		e, ok := b.addKeyed(model.KindNetwork, key, mapbuild.FirstNonEmpty(v.Name, key), "vlan", v.ID)
		if !ok {
			continue
		}
		mapbuild.SetAttr(e, "type", "vlan")
		mapbuild.SetAttr(e, model.AttrVLAN, strconv.Itoa(v.VID))
		mapbuild.SetAttr(e, model.AttrURL, b.recordURL(v.DisplayURL, "ipam/vlans", v.ID))
		mapbuild.SetAttr(e, "status", v.Status.Value)
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
		pfx, err := netip.ParsePrefix(p.Prefix)
		if err != nil {
			b.skip(fmt.Sprintf("netbox: skipping prefix %d: invalid prefix %q", p.ID, p.Prefix))
			continue
		}
		e, ok := b.addKeyed(model.KindNetwork, withVRF(p.Prefix, p.VRF), p.Prefix, "prefix", p.ID)
		if !ok {
			continue
		}
		b.nets = append(b.nets, indexedPrefix{pfx.Masked(), vrfID(p.VRF), e.ID})
		mapbuild.SetAttr(e, "type", "prefix")
		mapbuild.SetAttr(e, model.AttrPrefix, p.Prefix)
		mapbuild.SetAttr(e, model.AttrGateway, customGateway(p.CustomFields))
		mapbuild.SetAttr(e, model.AttrURL, b.recordURL(p.DisplayURL, "ipam/prefixes", p.ID))
		mapbuild.SetAttr(e, "status", p.Status.Value)
		mapbuild.SetAttr(e, model.AttrDescription, p.Description)
		if p.VRF != nil {
			mapbuild.SetAttr(e, "vrf", p.VRF.Name)
		}
		if p.Role != nil {
			e.Labels["role"] = mapbuild.FirstNonEmpty(p.Role.Slug, p.Role.Name)
		}
		setTags(e, p.Tags)
		setTenant(e, p.Tenant)
		if siteID, ok := p.siteID(); ok {
			b.relateIf(e.ID, b.sites, siteID, model.RelMemberOf)
		}
		if p.VLAN != nil {
			b.relateIf(e.ID, b.vlans, p.VLAN.ID, model.RelMemberOf)
			if vlanID, ok := b.vlans[p.VLAN.ID]; ok {
				mapbuild.SetAttr(e, model.AttrVLAN, b.Entities[vlanID].Attrs[model.AttrVLAN])
			}
		}
	}
	return nil
}

func (b *builder) addMachines(ms []machine, family string) error {
	for _, m := range ms {
		name := machineName(m, family)
		e, ok := b.addKeyed(model.KindHost, name, name, family, m.ID)
		if !ok {
			b.skippedHosts[objRef{family, m.ID}] = true
			continue
		}
		mapbuild.SetAttr(e, "type", family)
		mapbuild.SetAttr(e, "status", m.Status.Value)
		mapbuild.SetAttr(e, model.AttrDescription, m.Description)
		mapbuild.SetAttr(e, model.AttrURL, b.recordURL(m.DisplayURL, uiPath[family], m.ID))
		if isFQDN(name) {
			mapbuild.SetAttr(e, model.AttrFQDN, name)
		}
		if m.PrimaryIP4 != nil {
			mapbuild.SetAttr(e, "primary_ip4", m.PrimaryIP4.Address)
		}
		if m.PrimaryIP6 != nil {
			mapbuild.SetAttr(e, "primary_ip6", m.PrimaryIP6.Address)
		}
		if m.Platform != nil {
			mapbuild.SetAttr(e, "platform", m.Platform.Name)
		}
		if m.Cluster != nil {
			mapbuild.SetAttr(e, "cluster", m.Cluster.Name)
		}
		if m.DeviceType != nil {
			mapbuild.SetAttr(e, "model", m.DeviceType.Model)
		}
		role := m.Role
		if role == nil {
			role = m.DeviceRole
		}
		if role != nil {
			e.Labels["role"] = mapbuild.FirstNonEmpty(role.Slug, role.Name)
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
		if !ok && b.skippedHosts[objRef{hostFamily, o.ID}] {
			b.skip(fmt.Sprintf("netbox: skipping interface %d (%s): its owner was skipped", n.ID, n.Name))
			continue
		}
		if !ok {
			return fmt.Errorf("interface %d (%s): unknown owner %d", n.ID, n.Name, o.ID)
		}
		_, _, hostKey, err := model.ParseID(hostID)
		if err != nil {
			return err
		}
		e, err := b.Add(model.KindInterface, hostKey+"/"+n.Name, hostKeyName(hostKey, n.Name))
		if err != nil {
			b.skip(fmt.Sprintf("netbox: skipping interface %d: %v", n.ID, err))
			continue
		}
		mapbuild.SetAttr(e, "enabled", strconv.FormatBool(n.Enabled))
		mapbuild.SetAttr(e, model.AttrDescription, n.Description)
		if n.Type != nil {
			mapbuild.SetAttr(e, "type", n.Type.Value)
		}
		if n.MACAddress != nil {
			mapbuild.SetAttr(e, "mac", *n.MACAddress)
		}
		if n.MTU != nil {
			mapbuild.SetAttr(e, "mtu", strconv.Itoa(*n.MTU))
		}
		setTags(e, n.Tags)
		b.ifaces[objRef{family, n.ID}] = e.ID
		b.ifaceOwn[e.ID] = hostID
		b.Relate(e.ID, hostID, model.RelMemberOf)
	}
	return nil
}

func (b *builder) addIPs(ips []ipAddress) {
	for _, ip := range ips {
		addr, err := netip.ParsePrefix(ip.Address)
		if err != nil {
			b.skip(fmt.Sprintf("netbox: skipping ip address %d: invalid address %q", ip.ID, ip.Address))
			continue
		}
		e, ok := b.addKeyed(model.KindIPAddress, withVRF(ip.Address, ip.VRF), ip.Address, "ip", ip.ID)
		if !ok {
			continue
		}
		mapbuild.SetAttr(e, "status", ip.Status.Value)
		mapbuild.SetAttr(e, model.AttrDescription, ip.Description)
		mapbuild.SetAttr(e, model.AttrDNSName, ip.DNSName)
		if ip.VRF != nil {
			mapbuild.SetAttr(e, "vrf", ip.VRF.Name)
		}
		setTags(e, ip.Tags)
		setTenant(e, ip.Tenant)
		e.Addresses = []netip.Addr{addr.Addr()}
		if ip.DNSName != "" {
			e.Aliases = []string{ip.DNSName}
		}
		if best, ok := b.nets.mostSpecific(addr.Addr(), vrfID(ip.VRF)); ok {
			b.Relate(e.ID, best, model.RelMemberOf)
			if hasTag(ip.Tags, gatewayKey) {
				// The custom field of the prefix wins; then the first tagged address.
				if network := b.Entities[best]; network.Attrs[model.AttrGateway] == "" {
					mapbuild.SetAttr(network, model.AttrGateway, addr.Addr().String())
				}
			}
		}
		b.assign(e, ip, addr.Addr())
	}
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
	b.Relate(ipEntity.ID, nicID, model.RelMemberOf)
	for _, id := range []model.ID{nicID, b.ifaceOwn[nicID]} {
		owner := b.Entities[id]
		if !slices.Contains(owner.Addresses, addr) {
			owner.Addresses = append(owner.Addresses, addr)
		}
	}
	host := b.Entities[b.ifaceOwn[nicID]]
	isPrimary := ip.Address == host.Attrs["primary_ip4"] || ip.Address == host.Attrs["primary_ip6"]
	if isPrimary && host.Attrs[model.AttrFQDN] == "" && isFQDN(ip.DNSName) {
		mapbuild.SetAttr(host, model.AttrFQDN, ip.DNSName)
	}
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
		b.Relate(from, to, kind)
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
	return key + "@" + mapbuild.FirstNonEmpty(v.Slug, v.Name)
}

func hostKeyName(hostKey, nic string) string { return hostKey + " " + nic }

// uiPath maps a machine family to its path in the NetBox UI.
var uiPath = map[string]string{
	famDevice: "dcim/devices",
	famVM:     "virtualization/virtual-machines",
}

// recordURL returns the address of an object in the NetBox UI: the one NetBox
// reports, or else one built from the configured URL and the object ID.
// Credentials, query and fragment never end up in it.
func (b *builder) recordURL(reported, path string, id int) string {
	if u, err := url.Parse(reported); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return stripURL(u).String()
	}
	return stripURL(b.base).JoinPath(path, strconv.Itoa(id), "/").String()
}

func stripURL(u *url.URL) *url.URL {
	out := *u
	out.User, out.RawQuery, out.Fragment, out.RawFragment = nil, "", "", ""
	return &out
}

// customGateway reads the gateway custom field of a prefix. The field is
// site-defined free input, so anything but an IP address is ignored.
func customGateway(fields map[string]json.RawMessage) string {
	var raw string
	if err := json.Unmarshal(fields[gatewayKey], &raw); err != nil {
		return ""
	}
	// An interface address such as 192.0.2.1/24 is accepted too.
	host, _, _ := strings.Cut(strings.TrimSpace(raw), "/")
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	return addr.String()
}

// isFQDN reports whether name looks like a multi-label DNS name.
func isFQDN(name string) bool {
	if !strings.Contains(strings.Trim(name, "."), ".") || strings.ContainsAny(name, " /:") {
		return false
	}
	_, err := netip.ParseAddr(name)
	return err != nil
}

func hasTag(tags []tag, slug string) bool {
	return slices.ContainsFunc(tags, func(t tag) bool { return t.Slug == slug })
}

func setTags(e *model.Entity, tags []tag) {
	for _, t := range tags {
		e.Labels[model.LabelTagPrefix+t.Slug] = "true"
	}
}

func setTenant(e *model.Entity, tenant *ref) {
	if tenant != nil {
		e.Labels[model.LabelTenant] = mapbuild.FirstNonEmpty(tenant.Slug, tenant.Name)
	}
}
