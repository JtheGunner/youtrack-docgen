package netbox_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/collector/netbox"
	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// onlyServe makes the fake answer every endpoint with an empty list except
// the given ones, so a test controls the whole NetBox view.
func (f *fakeNetBox) onlyServe(bodies map[string]string) {
	for _, path := range []string{
		"/api/dcim/sites/", "/api/ipam/vlans/", "/api/ipam/prefixes/", "/api/dcim/devices/",
		"/api/virtualization/virtual-machines/", "/api/dcim/interfaces/",
		"/api/virtualization/interfaces/", "/api/ipam/ip-addresses/",
	} {
		f.serveJSON(path, bodies[path])
	}
}

func collectWith(t *testing.T, f *fakeNetBox) (*model.Graph, *netbox.Collector, string) {
	t.Helper()
	var warnings bytes.Buffer
	c, err := netbox.New(config.Module{URL: f.URL, Token: config.Secret(fakeToken)}, netbox.WithWarnings(&warnings))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	g := model.NewGraph()
	if err := c.Collect(context.Background(), g); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return g, c, warnings.String()
}

func TestCollectDisambiguatesDuplicateNames(t *testing.T) {
	tests := []struct {
		name   string
		bodies map[string]string
		want   []model.ID
		absent []model.ID
	}{
		{
			name: "same device name on two sites",
			bodies: map[string]string{
				"/api/dcim/devices/": `{"id": 3, "name": "sw01.example.com"}, {"id": 4, "name": "sw01.example.com"}, {"id": 5, "name": "other.example.com"}`,
			},
			want:   []model.ID{"netbox:host:sw01.example.com~device3", "netbox:host:sw01.example.com~device4", "netbox:host:other.example.com"},
			absent: []model.ID{"netbox:host:sw01.example.com"},
		},
		{
			name: "device and VM with the same name",
			bodies: map[string]string{
				"/api/dcim/devices/":                    `{"id": 3, "name": "app01.example.com"}`,
				"/api/virtualization/virtual-machines/": `{"id": 3, "name": "app01.example.com"}`,
			},
			want: []model.ID{"netbox:host:app01.example.com~device3", "netbox:host:app01.example.com~vm3"},
		},
		{
			name: "anycast addresses",
			bodies: map[string]string{
				"/api/ipam/ip-addresses/": `{"id": 11, "address": "192.0.2.53/32"}, {"id": 12, "address": "192.0.2.53/32"}, {"id": 13, "address": "192.0.2.54/32"}`,
			},
			want: []model.ID{
				"netbox:ipaddress:192.0.2.53/32~ip11", "netbox:ipaddress:192.0.2.53/32~ip12", "netbox:ipaddress:192.0.2.54/32",
			},
		},
		{
			name: "same VLAN ID in two groups",
			bodies: map[string]string{
				"/api/ipam/vlans/": `{"id": 21, "vid": 100, "name": "a"}, {"id": 22, "vid": 100, "name": "b"}`,
			},
			want: []model.ID{"netbox:network:vlan-100~vlan21", "netbox:network:vlan-100~vlan22"},
		},
		{
			name: "same prefix twice",
			bodies: map[string]string{
				"/api/ipam/prefixes/": `{"id": 31, "prefix": "198.51.100.0/24"}, {"id": 32, "prefix": "198.51.100.0/24"}`,
			},
			want: []model.ID{"netbox:network:198.51.100.0/24~prefix31", "netbox:network:198.51.100.0/24~prefix32"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeNetBox(t)
			f.onlyServe(tt.bodies)
			g, c, _ := collectWith(t, f)
			for _, id := range tt.want {
				if _, ok := g.Entity(id); !ok {
					t.Errorf("entity %s missing", id)
				}
			}
			for _, id := range tt.absent {
				if _, ok := g.Entity(id); ok {
					t.Errorf("entity %s must not exist", id)
				}
			}
			if got := c.Incomplete(); len(got) != 0 {
				t.Errorf("Incomplete = %v, want none: disambiguated objects are not lost", got)
			}
		})
	}
}

func TestCollectDisambiguatedIDsAreIndependentOfResponseOrder(t *testing.T) {
	ids := func(body string) map[model.ID]string {
		f := newFakeNetBox(t)
		f.onlyServe(map[string]string{"/api/dcim/devices/": body})
		g, _, _ := collectWith(t, f)
		out := map[model.ID]string{}
		for _, e := range g.ByKind(model.KindHost) {
			out[e.ID] = e.Name
		}
		return out
	}
	a := ids(`{"id": 3, "name": "dup"}, {"id": 4, "name": "dup"}`)
	b := ids(`{"id": 4, "name": "dup"}, {"id": 3, "name": "dup"}`)
	if len(a) != 2 {
		t.Fatalf("got %d hosts, want 2", len(a))
	}
	for id, u := range a {
		if v, ok := b[id]; !ok || v != u {
			t.Errorf("%s differs between runs", id)
		}
	}
}

func TestCollectKeepsUniqueKeysUnchanged(t *testing.T) {
	g := collectFixtures(t)
	for _, id := range []model.ID{"netbox:host:srv01.example.com", "netbox:network:192.0.2.0/24", "netbox:site:example-hq"} {
		if _, ok := g.Entity(id); !ok {
			t.Errorf("entity %s changed or missing", id)
		}
	}
}

func TestCollectSkipsUnresolvableObjectsWithWarning(t *testing.T) {
	f := newFakeNetBox(t)
	f.onlyServe(map[string]string{
		"/api/ipam/prefixes/":     `{"id": 1, "prefix": "not-a-prefix"}, {"id": 2, "prefix": "198.51.100.0/24"}`,
		"/api/ipam/ip-addresses/": `{"id": 5, "address": "bogus"}, {"id": 6, "address": "198.51.100.7/24"}`,
		"/api/dcim/devices/":      `{"id": 8, "name": "dup"}, {"id": 8, "name": "dup"}`,
	})
	g, c, warnings := collectWith(t, f)
	if _, ok := g.Entity("netbox:network:198.51.100.0/24"); !ok {
		t.Error("valid prefix missing")
	}
	if _, ok := g.Entity("netbox:ipaddress:198.51.100.7/24"); !ok {
		t.Error("valid address missing")
	}
	for _, want := range []string{"prefix 1", "ip address 5"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings %q lack %q", warnings, want)
		}
	}
	if len(c.Incomplete()) != 3 {
		t.Errorf("Incomplete = %v, want 3 reasons", c.Incomplete())
	}
	if strings.Contains(warnings, fakeToken) {
		t.Error("warnings leak the token")
	}
}

func TestCollectSkipsInterfacesOfSkippedHosts(t *testing.T) {
	f := newFakeNetBox(t)
	f.onlyServe(map[string]string{
		// The same NetBox ID twice yields one key twice: one device is skipped.
		"/api/dcim/devices/":    `{"id": 8, "name": "dup"}, {"id": 8, "name": "dup"}`,
		"/api/dcim/interfaces/": `{"id": 1, "name": "eth0", "device": {"id": 8}}`,
	})
	_, c, warnings := collectWith(t, f)
	if !strings.Contains(warnings, "skipping device 8") {
		t.Errorf("warnings %q lack the skipped device", warnings)
	}
	if len(c.Incomplete()) == 0 {
		t.Error("Incomplete is empty after skipping objects")
	}
}

func TestCollectFollowsNextLinkWithDifferentScheme(t *testing.T) {
	secondPage := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/sites/" {
			_, _ = w.Write([]byte(`{"results": []}`))
			return
		}
		if r.URL.Query().Get("offset") == "1" {
			secondPage = true
			_, _ = w.Write([]byte(`{"next": null, "results": [{"id": 2, "name": "B", "slug": "b"}]}`))
			return
		}
		// A proxy-fronted NetBox reports the other scheme.
		_, _ = fmt.Fprintf(w, `{"next": "https://%s/api/dcim/sites/?limit=100&offset=1", "results": [{"id": 1, "name": "A", "slug": "a"}]}`, r.Host)
	}))
	t.Cleanup(srv.Close)
	g, err := collect(t, srv.URL, fakeToken)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := len(g.ByKind(model.KindSite)); got != 2 || !secondPage {
		t.Errorf("sites = %d, second page requested = %v, want both pages read", got, secondPage)
	}
}

func TestCollectNextLinkSchemeChangeStillRejectsForeignHost(t *testing.T) {
	f := newFakeNetBox(t)
	f.override["/api/dcim/sites/"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"next": "http://other.example.com/api/dcim/sites/?offset=1", "results": []}`))
	}
	if _, err := collect(t, f.URL, fakeToken); err == nil || !strings.Contains(err.Error(), "different host") {
		t.Fatalf("error = %v, want different host", err)
	}
}

func TestCollectPrefixSiteFromSiteOrScope(t *testing.T) {
	const sites = `{"id": 1, "name": "Example HQ", "slug": "example-hq"}, {"id": 2, "name": "Branch", "slug": "branch"}`
	tests := []struct {
		name     string
		prefix   string
		wantSite model.ID // empty: no site relation
	}{
		{"site field (NetBox < 4.2)", `"site": {"id": 2, "name": "Branch", "slug": "branch"}`, "netbox:site:branch"},
		{"scope pointing to a site (NetBox >= 4.2)", `"site": null, "scope_type": "dcim.site", "scope_id": 1, "scope": {"id": 1, "name": "Example HQ"}`, "netbox:site:example-hq"},
		{"scope without scope_id", `"scope_type": "dcim.site", "scope": {"id": 2, "name": "Branch"}`, "netbox:site:branch"},
		{"scope pointing to a region", `"scope_type": "dcim.region", "scope_id": 1, "scope": {"id": 1, "name": "EU"}`, ""},
		{"scope pointing to a location", `"scope_type": "dcim.location", "scope_id": 2`, ""},
		{"no site and no scope", `"site": null, "scope_type": null, "scope_id": null`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeNetBox(t)
			f.onlyServe(map[string]string{
				"/api/dcim/sites/":    sites,
				"/api/ipam/prefixes/": `{"id": 7, "prefix": "198.51.100.0/24", ` + tt.prefix + `}`,
			})
			g, _, _ := collectWith(t, f)
			var got model.ID
			for _, r := range g.Relations() {
				if r.From == "netbox:network:198.51.100.0/24" && strings.HasPrefix(string(r.To), "netbox:site:") {
					got = r.To
				}
			}
			if got != tt.wantSite {
				t.Errorf("site relation = %q, want %q", got, tt.wantSite)
			}
		})
	}
}
