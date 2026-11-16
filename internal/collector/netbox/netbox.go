// Package netbox reads sites, networks, IP addresses, devices, virtual machines
// and interfaces from the NetBox REST API (read-only) into the neutral model.
package netbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/JtheGunner/youtrackt-docgen/internal/config"
	"github.com/JtheGunner/youtrackt-docgen/internal/model"
)

// Source is the source name used in every entity ID created by this collector.
const Source = "netbox"

const defaultTimeout = 30 * time.Second

// Collector implements collector.Collector for NetBox.
type Collector struct {
	client *client
}

// Option customizes a Collector.
type Option func(*Collector)

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Collector) { c.client.http = h }
}

// New builds a collector from the "netbox" collector module config. The URL
// and token are required; the token is only ever sent to that URL's host.
func New(m config.Module, opts ...Option) (*Collector, error) {
	if m.URL == "" {
		return nil, errors.New("netbox: url is required")
	}
	if !m.Token.IsSet() {
		return nil, errors.New("netbox: token is required")
	}
	base, err := url.Parse(m.URL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("netbox: url must be an absolute http(s) URL")
	}
	c := &Collector{client: &client{
		base:  base,
		token: m.Token.Reveal(),
		http:  &http.Client{Timeout: defaultTimeout},
	}}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Name returns the source name.
func (*Collector) Name() string { return Source }

// Collect reads NetBox and adds its entities and relations to g. Nothing is
// added when reading fails, so a partial NetBox view never reaches the graph.
func (c *Collector) Collect(ctx context.Context, g *model.Graph) error {
	data, err := c.fetch(ctx)
	if err != nil {
		return err
	}
	b := newBuilder()
	if err := b.build(data); err != nil {
		return fmt.Errorf("netbox: %w", err)
	}
	return b.commit(g)
}

type snapshot struct {
	sites      []site
	vlans      []vlan
	prefixes   []prefix
	ips        []ipAddress
	devices    []machine
	vms        []machine
	deviceNICs []iface
	vmNICs     []iface
}

func (c *Collector) fetch(ctx context.Context) (snapshot, error) {
	var s snapshot
	var err error
	steps := []func() error{
		func() (e error) { s.sites, e = fetchAll[site](ctx, c.client, "api/dcim/sites/"); return },
		func() (e error) { s.vlans, e = fetchAll[vlan](ctx, c.client, "api/ipam/vlans/"); return },
		func() (e error) { s.prefixes, e = fetchAll[prefix](ctx, c.client, "api/ipam/prefixes/"); return },
		func() (e error) { s.devices, e = fetchAll[machine](ctx, c.client, "api/dcim/devices/"); return },
		func() (e error) {
			s.vms, e = fetchAll[machine](ctx, c.client, "api/virtualization/virtual-machines/")
			return
		},
		func() (e error) { s.deviceNICs, e = fetchAll[iface](ctx, c.client, "api/dcim/interfaces/"); return },
		func() (e error) {
			s.vmNICs, e = fetchAll[iface](ctx, c.client, "api/virtualization/interfaces/")
			return
		},
		func() (e error) { s.ips, e = fetchAll[ipAddress](ctx, c.client, "api/ipam/ip-addresses/"); return },
	}
	for _, step := range steps {
		if err = step(); err != nil {
			return snapshot{}, err
		}
	}
	return s, nil
}
