// Package netbox reads sites, networks, IP addresses, devices, virtual machines
// and interfaces from the NetBox REST API (read-only) into the neutral model.
package netbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// Source is the source name used in every entity ID created by this collector.
const Source = "netbox"

const defaultTimeout = 30 * time.Second

// Collector implements collector.Collector for NetBox.
type Collector struct {
	client   *client
	warnings io.Writer
	// incomplete holds the reasons the last Collect left objects out.
	incomplete []string
}

// Option customizes a Collector.
type Option func(*Collector)

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Collector) { c.client.http = h }
}

// WithWarnings sends warnings about skipped objects to w instead of standard
// error.
func WithWarnings(w io.Writer) Option {
	return func(c *Collector) { c.warnings = w }
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
	}, warnings: os.Stderr}
	for _, opt := range opts {
		opt(c)
	}
	c.client.http = restrictRedirects(c.client.http, base)
	return c, nil
}

// restrictRedirects returns a copy of h that follows a redirect only when it
// stays on the configured scheme and host, so the token is never sent to
// another host or over a downgraded scheme. It replaces any CheckRedirect the
// caller set, and the caller's client is left unmodified.
func restrictRedirects(h *http.Client, base *url.URL) *http.Client {
	restricted := *h
	restricted.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		if req.URL.Scheme != base.Scheme || req.URL.Host != base.Host {
			return fmt.Errorf("refusing redirect to %s://%s: only %s://%s is allowed",
				req.URL.Scheme, req.URL.Host, base.Scheme, base.Host)
		}
		return nil
	}
	return &restricted
}

// Name returns the source name.
func (*Collector) Name() string { return Source }

// Collect reads NetBox and adds its entities and relations to g. Nothing is
// added when reading fails, so a partial NetBox view never reaches the graph.
func (c *Collector) Collect(ctx context.Context, g *model.Graph) error {
	c.incomplete = nil
	data, err := c.fetch(ctx)
	if err != nil {
		return err
	}
	b := newBuilder(c.client.base, c.warn)
	err = b.build(data)
	c.incomplete = b.skipped
	if err != nil {
		return fmt.Errorf("netbox: %w", err)
	}
	return b.Commit(g)
}

// Incomplete returns the reasons the last Collect left NetBox objects out
// (objects that could not be mapped). It is empty for a complete read.
func (c *Collector) Incomplete() []string { return slices.Clone(c.incomplete) }

func (c *Collector) warn(msg string) {
	fmt.Fprintf(c.warnings, "warning: %s\n", msg) //nolint:errcheck // a failing warning stream must not stop the run
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
