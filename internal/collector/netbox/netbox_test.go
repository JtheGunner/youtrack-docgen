package netbox_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/collector"
	"github.com/JtheGunner/youtrack-docgen/internal/collector/netbox"
	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

const fakeToken = "fake-netbox-token-0123456789"

var _ collector.Collector = (*netbox.Collector)(nil)

// fakeNetBox serves the synthetic fixtures and records requests.
type fakeNetBox struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	override map[string]func(w http.ResponseWriter, r *http.Request)
}

func newFakeNetBox(t *testing.T) *fakeNetBox {
	t.Helper()
	f := &fakeNetBox{override: map[string]func(http.ResponseWriter, *http.Request){}}
	files := map[string]string{
		"/api/dcim/sites/":                      "sites.json",
		"/api/ipam/vlans/":                      "vlans.json",
		"/api/ipam/prefixes/":                   "prefixes.json",
		"/api/virtualization/virtual-machines/": "vms.json",
		"/api/dcim/interfaces/":                 "device-interfaces.json",
		"/api/virtualization/interfaces/":       "vm-interfaces.json",
		"/api/ipam/ip-addresses/":               "ip-addresses.json",
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Clone(r.Context()))
		f.mu.Unlock()

		if h, ok := f.override[r.URL.Path]; ok {
			h(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Token "+fakeToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		name, ok := files[r.URL.Path]
		if r.URL.Path == "/api/dcim/devices/" {
			name, ok = "devices-1.json", true
			if r.URL.Query().Get("offset") == "2" {
				name = "devices-2.json"
			}
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		body, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.ReplaceAll(string(body), "{{BASE}}", f.URL)))
	}))
	t.Cleanup(f.Close)
	return f
}

func collect(t *testing.T, url, token string) (*model.Graph, error) {
	t.Helper()
	c, err := netbox.New(config.Module{URL: url, Token: config.Secret(token)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	g := model.NewGraph()
	return g, c.Collect(context.Background(), g)
}

func collectFixtures(t *testing.T) *model.Graph {
	t.Helper()
	g, err := collect(t, newFakeNetBox(t).URL, fakeToken)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return g
}

func TestCollectMapsEntities(t *testing.T) {
	g := collectFixtures(t)

	tests := []struct {
		id     model.ID
		name   string
		attrs  map[string]string
		labels map[string]string
	}{
		{"netbox:site:example-hq", "Example HQ",
			map[string]string{"status": "active", "region": "EMEA", "description": "Synthetic primary site"},
			map[string]string{"tag.prod": "true"}},
		{"netbox:network:vlan-example-hq-100", "servers",
			map[string]string{"type": "vlan", "vlan_id": "100", "status": "active"}, nil},
		{"netbox:network:192.0.2.0/24", "192.0.2.0/24",
			map[string]string{"type": "prefix", "cidr": "192.0.2.0/24", "status": "active"},
			map[string]string{"role": "servers"}},
		{"netbox:network:2001_db8__/32", "2001:db8::/32", map[string]string{"cidr": "2001:db8::/32"}, nil},
		{"netbox:host:sw01.example.com", "sw01.example.com",
			map[string]string{"type": "device", "model": "EX-1000"}, map[string]string{"role": "core-switch", "tenant": "example-corp"}},
		{"netbox:host:srv01.example.com", "srv01.example.com",
			map[string]string{"primary_ip4": "192.0.2.10/24", "platform": "Linux"},
			map[string]string{"role": "server", "tag.prod": "true"}},
		{"netbox:host:device-3", "device-3", map[string]string{"status": "planned"}, nil},
		{"netbox:host:vm01.example.com", "vm01.example.com",
			map[string]string{"type": "vm", "cluster": "k8s-example"}, map[string]string{"role": "k8s-node"}},
		{"netbox:interface:srv01.example.com/eth0", "srv01.example.com eth0",
			map[string]string{"enabled": "true", "mac": "02:00:00:00:00:01", "mtu": "1500", "type": "1000base-t"}, nil},
		{"netbox:interface:sw01.example.com/mgmt0", "sw01.example.com mgmt0",
			map[string]string{"enabled": "false", "description": "Management"}, nil},
		{"netbox:interface:vm01.example.com/ens3", "vm01.example.com ens3",
			map[string]string{"mac": "02:00:00:00:00:02"}, nil},
		{"netbox:ipaddress:192.0.2.10/24@mgmt", "192.0.2.10/24", map[string]string{"vrf": "Mgmt"}, nil},
		{"netbox:ipaddress:2001_db8__10/64", "2001:db8::10/64", map[string]string{"status": "reserved"}, nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.id), func(t *testing.T) {
			e, ok := g.Entity(tt.id)
			if !ok {
				t.Fatalf("entity %s missing", tt.id)
			}
			if e.Name != tt.name {
				t.Errorf("name = %q, want %q", e.Name, tt.name)
			}
			for k, v := range tt.attrs {
				if e.Attrs[k] != v {
					t.Errorf("attr %s = %q, want %q", k, e.Attrs[k], v)
				}
			}
			for k, v := range tt.labels {
				if e.Labels[k] != v {
					t.Errorf("label %s = %q, want %q", k, e.Labels[k], v)
				}
			}
		})
	}
}

func TestCollectEntityCounts(t *testing.T) {
	g := collectFixtures(t)
	want := map[model.Kind]int{
		model.KindSite: 1, model.KindNetwork: 4, model.KindHost: 4,
		model.KindInterface: 3, model.KindIPAddress: 5,
	}
	for kind, n := range want {
		if got := len(g.ByKind(kind)); got != n {
			t.Errorf("%s count = %d, want %d", kind, got, n)
		}
	}
}

func TestCollectAddressesAndAliases(t *testing.T) {
	g := collectFixtures(t)
	addr := netip.MustParseAddr("192.0.2.10")

	host, _ := g.Entity("netbox:host:srv01.example.com")
	if len(host.Addresses) != 1 || host.Addresses[0] != addr {
		t.Errorf("host addresses = %v, want [%s]", host.Addresses, addr)
	}
	if len(host.Aliases) != 1 || host.Aliases[0] != "web.example.com" {
		t.Errorf("host aliases = %v, want [web.example.com]", host.Aliases)
	}
	if got := g.ByHostname("web.example.com"); len(got) != 2 {
		t.Errorf("ByHostname(web.example.com) = %d entities, want host and ip", len(got))
	}
	nic, _ := g.Entity("netbox:interface:srv01.example.com/eth0")
	if len(nic.Addresses) != 1 || nic.Addresses[0] != addr {
		t.Errorf("interface addresses = %v", nic.Addresses)
	}
	vm, _ := g.Entity("netbox:host:vm01.example.com")
	if len(vm.Addresses) != 1 || vm.Addresses[0] != netip.MustParseAddr("192.0.2.20") {
		t.Errorf("vm addresses = %v", vm.Addresses)
	}
}

func TestCollectRelations(t *testing.T) {
	g := collectFixtures(t)
	want := []model.Relation{
		{From: "netbox:host:srv01.example.com", To: "netbox:site:example-hq", Kind: model.RelMemberOf},
		{From: "netbox:interface:srv01.example.com/eth0", To: "netbox:host:srv01.example.com", Kind: model.RelMemberOf},
		{From: "netbox:interface:vm01.example.com/ens3", To: "netbox:host:vm01.example.com", Kind: model.RelMemberOf},
		{From: "netbox:ipaddress:192.0.2.10/24", To: "netbox:interface:srv01.example.com/eth0", Kind: model.RelMemberOf},
		{From: "netbox:ipaddress:192.0.2.20/24", To: "netbox:interface:vm01.example.com/ens3", Kind: model.RelMemberOf},
		{From: "netbox:ipaddress:192.0.2.10/24", To: "netbox:network:192.0.2.0/24", Kind: model.RelMemberOf},
		{From: "netbox:ipaddress:10.0.5.1/24", To: "netbox:network:10.0.0.0/16", Kind: model.RelMemberOf},
		{From: "netbox:network:192.0.2.0/24", To: "netbox:site:example-hq", Kind: model.RelMemberOf},
		{From: "netbox:network:192.0.2.0/24", To: "netbox:network:vlan-example-hq-100", Kind: model.RelMemberOf},
		{From: "netbox:network:vlan-example-hq-100", To: "netbox:site:example-hq", Kind: model.RelMemberOf},
	}
	have := map[model.Relation]bool{}
	for _, r := range g.Relations() {
		have[r] = true
	}
	for _, r := range want {
		if !have[r] {
			t.Errorf("missing relation %s -[%s]-> %s", r.From, r.Kind, r.To)
		}
	}
	// A VRF address must not attach to a prefix of the default VRF.
	for _, r := range g.RelationsFrom("netbox:ipaddress:192.0.2.10/24@mgmt") {
		t.Errorf("VRF address has unexpected relation %v", r)
	}
}

func TestCollectPaginatesAndAuthenticates(t *testing.T) {
	f := newFakeNetBox(t)
	g, err := collect(t, f.URL, fakeToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Entity("netbox:host:device-3"); !ok {
		t.Error("device from the second page is missing")
	}
	deviceRequests := 0
	for _, r := range f.requests {
		if got := r.Header.Get("Authorization"); got != "Token "+fakeToken {
			t.Errorf("%s: Authorization = %q", r.URL.Path, got)
		}
		if r.Method != http.MethodGet {
			t.Errorf("%s: method %s, collector must be read-only", r.URL.Path, r.Method)
		}
		if r.URL.Path == "/api/dcim/devices/" {
			deviceRequests++
		}
	}
	if deviceRequests != 2 {
		t.Errorf("device requests = %d, want 2", deviceRequests)
	}
}

func TestCollectFailures(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		handler func(w http.ResponseWriter, r *http.Request)
		wantErr string
	}{
		{name: "wrong token", token: "other-fake-token", wantErr: "403"},
		{name: "server error", token: fakeToken, wantErr: "500",
			handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }},
		{name: "invalid json", token: fakeToken, wantErr: "decode",
			handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }},
		{name: "next link to other host", token: fakeToken, wantErr: "different host",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"next": "https://other.example.com/api/dcim/sites/?offset=1", "results": []}`))
			}},
		{name: "pagination loop", token: fakeToken, wantErr: "loop",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"next": "http://%s%s", "results": []}`, r.Host, r.URL.RequestURI())
			}},
		{name: "unknown interface owner", token: fakeToken, wantErr: "unknown owner",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"results": [{"id": 1, "name": "eth0", "device": {"id": 99}}]}`))
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeNetBox(t)
			if tt.handler != nil {
				switch tt.name {
				case "unknown interface owner":
					f.override["/api/dcim/interfaces/"] = tt.handler
				case "pagination loop", "next link to other host":
					f.override["/api/dcim/sites/"] = tt.handler
				default:
					f.override["/api/ipam/vlans/"] = tt.handler
				}
			}
			g, err := collect(t, f.URL, tt.token)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), tt.token) {
				t.Errorf("error leaks the token: %v", err)
			}
			if n := len(g.ByKind(model.KindSite)) + len(g.ByKind(model.KindHost)); n != 0 {
				t.Errorf("graph has %d entities after a failed collect, want 0", n)
			}
		})
	}
}

func TestCollectNeverLeaksTokenIntoGraph(t *testing.T) {
	g := collectFixtures(t)
	for _, kind := range []model.Kind{model.KindSite, model.KindNetwork, model.KindHost, model.KindInterface, model.KindIPAddress} {
		for _, e := range g.ByKind(kind) {
			if s := fmt.Sprintf("%+v", e); strings.Contains(s, fakeToken) {
				t.Errorf("entity %s contains the token", e.ID)
			}
		}
	}
}

func TestCollectDuplicateHostFails(t *testing.T) {
	f := newFakeNetBox(t)
	f.override["/api/virtualization/virtual-machines/"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results": [{"id": 9, "name": "sw01.example.com"}]}`))
	}
	if _, err := collect(t, f.URL, fakeToken); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v, want duplicate entity", err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Module
		ok   bool
	}{
		{"valid", config.Module{URL: "https://netbox.example.com", Token: "x"}, true},
		{"missing url", config.Module{Token: "x"}, false},
		{"missing token", config.Module{URL: "https://netbox.example.com"}, false},
		{"relative url", config.Module{URL: "netbox.example.com", Token: "x"}, false},
		{"non-http scheme", config.Module{URL: "ftp://netbox.example.com", Token: "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := netbox.New(tt.cfg)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
			if tt.ok && c.Name() != "netbox" {
				t.Errorf("Name = %q", c.Name())
			}
		})
	}
}

// redirectTarget records whether any request reached it.
type redirectTarget struct {
	*httptest.Server
	mu   sync.Mutex
	hits int
	auth []string
}

func newRedirectTarget(t *testing.T, useTLS bool) *redirectTarget {
	t.Helper()
	rt := &redirectTarget{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt.mu.Lock()
		rt.hits++
		rt.auth = append(rt.auth, r.Header.Get("Authorization"))
		rt.mu.Unlock()
		_, _ = w.Write([]byte(`{"results": []}`))
	})
	if useTLS {
		rt.Server = httptest.NewTLSServer(h)
	} else {
		rt.Server = httptest.NewServer(h)
	}
	t.Cleanup(rt.Close)
	return rt
}

func TestCollectRefusesRedirectsOffTheConfiguredOrigin(t *testing.T) {
	tests := []struct {
		name   string
		tls    bool
		client func(target *redirectTarget) *http.Client
	}{
		{"other host, default client", false, func(*redirectTarget) *http.Client { return nil }},
		{"other scheme, default client", true, func(*redirectTarget) *http.Client { return nil }},
		{"custom client that follows redirects", false, func(*redirectTarget) *http.Client {
			return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
		}},
		{"custom TLS client", true, func(target *redirectTarget) *http.Client { return target.Client() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := newRedirectTarget(t, tt.tls)
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
			}))
			t.Cleanup(origin.Close)

			var opts []netbox.Option
			if hc := tt.client(target); hc != nil {
				opts = append(opts, netbox.WithHTTPClient(hc))
			}
			c, err := netbox.New(config.Module{URL: origin.URL, Token: fakeToken}, opts...)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Collect(context.Background(), model.NewGraph())
			if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
				t.Fatalf("error = %v, want a refused redirect", err)
			}
			if strings.Contains(err.Error(), fakeToken) {
				t.Errorf("error leaks the token: %v", err)
			}
			target.mu.Lock()
			defer target.mu.Unlock()
			if target.hits != 0 {
				t.Errorf("redirect target received %d request(s) with Authorization %q", target.hits, target.auth)
			}
		})
	}
}

func TestCollectFollowsRedirectsOnTheSameOrigin(t *testing.T) {
	f := newFakeNetBox(t)
	f.override["/api/dcim/sites/"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved/sites/", http.StatusTemporaryRedirect)
	}
	f.override["/moved/sites/"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token "+fakeToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"results": []}`))
	}
	if _, err := collect(t, f.URL, fakeToken); err != nil {
		t.Fatalf("same-origin redirect should be followed: %v", err)
	}
}
