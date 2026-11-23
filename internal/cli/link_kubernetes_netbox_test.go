package cli

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// linkNetBoxDevices are the synthetic NetBox hosts the Kubernetes fixtures
// are linked against: one owns the address and hostname of node-a, one is
// named like the ingress hostname, one is the fully qualified name of the node
// web01 (known by its short name only) and one owns the load balancer IP of a
// service. No host matches node-lost or node-ghost.
const linkNetBoxDevices = `{"count": 4, "next": null, "previous": null, "results": [
  {"id": 11, "name": "node-a.example.com", "status": {"value": "active"},
   "primary_ip4": {"id": 21, "address": "192.0.2.21/24"}, "primary_ip6": null, "description": "", "tags": []},
  {"id": 12, "name": "shop.example.com", "status": {"value": "active"},
   "primary_ip4": {"id": 22, "address": "192.0.2.30/24"}, "primary_ip6": null, "description": "", "tags": []},
  {"id": 13, "name": "web01.example.com", "status": {"value": "active"},
   "primary_ip4": null, "primary_ip6": null, "description": "", "tags": []},
  {"id": 14, "name": "lb01.example.com", "status": {"value": "active"},
   "primary_ip4": {"id": 23, "address": "192.0.2.40/24"}, "primary_ip6": null, "description": "", "tags": []}
]}`

const linkNetBoxInterfaces = `{"count": 2, "next": null, "previous": null, "results": [
  {"id": 31, "name": "eth0", "device": {"id": 11, "name": "node-a.example.com"}, "enabled": true,
   "type": {"value": "1000base-t"}, "mac_address": null, "mtu": null, "description": "", "tags": []},
  {"id": 32, "name": "eth0", "device": {"id": 14, "name": "lb01.example.com"}, "enabled": true,
   "type": {"value": "1000base-t"}, "mac_address": null, "mtu": null, "description": "", "tags": []}
]}`

const linkNetBoxIPAddresses = `{"count": 2, "next": null, "previous": null, "results": [
  {"id": 21, "address": "192.0.2.21/24", "status": {"value": "active"}, "vrf": null, "dns_name": "",
   "assigned_object_type": "dcim.interface", "assigned_object_id": 31, "description": "", "tags": []},
  {"id": 23, "address": "192.0.2.40/24", "status": {"value": "active"}, "vrf": null, "dns_name": "",
   "assigned_object_type": "dcim.interface", "assigned_object_id": 32, "description": "", "tags": []}
]}`

const emptyNetBoxPage = `{"count": 0, "next": null, "previous": null, "results": []}`

func fakeLinkNetBox(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := map[string]string{
			"/api/dcim/devices/":      linkNetBoxDevices,
			"/api/dcim/interfaces/":   linkNetBoxInterfaces,
			"/api/ipam/ip-addresses/": linkNetBoxIPAddresses,
		}[r.URL.Path]
		if !ok {
			body = emptyNetBoxPage
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCollectAndLinkKubernetesWithNetBox runs the real collectors on the
// synthetic Kubernetes manifests and a NetBox fake, then the linking step.
func TestCollectAndLinkKubernetesWithNetBox(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeLinkNetBox(t)
	k8s := filepath.Join("..", "collector", "kubernetes", "testdata")
	cfgPath := writeConfig(t, fmt.Sprintf(`
collectors:
  netbox:
    url: %s
    token: ${NETBOX_TOKEN}
  kubernetes:
    paths: [%s, %s, %s]
sinks:
  markdown:
    dir: %s
`, srv.URL, filepath.Join(k8s, "nodes"), filepath.Join(k8s, "manifests", "apps", "web.yaml"), filepath.Join(k8s, "linking"), t.TempDir()))
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	collectors, err := buildCollectors(cfg)
	if err != nil {
		t.Fatal(err)
	}
	graph := model.NewGraph()
	for _, c := range collectors {
		if err := c.Collect(context.Background(), graph); err != nil {
			t.Fatalf("collector %s: %v", c.Name(), err)
		}
	}
	model.Link(graph)

	const (
		netboxNode = model.ID("netbox:host:node-a.example.com")
		netboxShop = model.ID("netbox:host:shop.example.com")
	)
	const (
		netboxWeb = model.ID("netbox:host:web01.example.com")
		netboxLB  = model.ID("netbox:host:lb01.example.com")
	)
	for _, id := range []model.ID{netboxNode, netboxShop, netboxWeb, netboxLB} {
		if _, ok := graph.Entity(id); !ok {
			t.Fatalf("netbox host %s missing", id)
		}
	}
	relations := graph.Relations()
	want := []model.Relation{
		{From: "kubernetes:workload:shop/deployment/api", To: netboxNode, Kind: model.RelRunsOn},
		{From: netboxNode, To: "kubernetes:service:shop/edge", Kind: model.RelExposes},
		{From: netboxShop, To: "kubernetes:ingress:shop/web", Kind: model.RelExposes},
		// short node name, fully qualified NetBox name
		{From: "kubernetes:host:web01", To: netboxWeb, Kind: model.RelSameAs},
		{From: "kubernetes:workload:shop/deployment/report", To: netboxWeb, Kind: model.RelRunsOn},
		// load balancer IP
		{From: netboxLB, To: "kubernetes:service:shop/lb", Kind: model.RelExposes},
	}
	for _, rel := range want {
		if !slices.Contains(relations, rel) {
			t.Errorf("relation missing: %+v", rel)
		}
	}

	for _, name := range []string{"node-lost", "node-ghost"} {
		node := model.ID("kubernetes:host:" + name)
		if _, ok := graph.Entity(node); !ok {
			t.Fatalf("unmatched node %s missing from the graph", name)
		}
		for _, rel := range relations {
			if rel.Kind == model.RelSameAs && (rel.From == node || rel.To == node) {
				t.Errorf("%s must stay unlinked, got %+v", name, rel)
			}
		}
	}
	for _, rel := range relations {
		if rel.From == "kubernetes:workload:shop/deployment/worker" && rel.Kind == model.RelRunsOn {
			t.Errorf("an unpinned workload must not be placed on a host: %+v", rel)
		}
		if rel.Kind == model.RelRunsOn && rel.From == "kubernetes:workload:shop/deployment/batch" &&
			strings.HasPrefix(string(rel.To), "netbox:") {
			t.Errorf("workload pinned to the unmatched node-ghost must not run on a NetBox host: %+v", rel)
		}
	}
}

// TestGenerateDerivesDependencies runs the whole pipeline and checks that the
// host article of a NetBox host lists the workload pinned to it under
// "Needed by", while a host without any derived edge says it is not known.
func TestGenerateDerivesDependencies(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeLinkNetBox(t)
	k8s := filepath.Join("..", "collector", "kubernetes", "testdata")
	out := t.TempDir()
	cfg, err := config.Load(writeConfig(t, fmt.Sprintf(`
collectors:
  netbox:
    url: %s
    token: ${NETBOX_TOKEN}
  kubernetes:
    paths: [%s, %s]
sinks:
  markdown:
    dir: %s
`, srv.URL, filepath.Join(k8s, "nodes"), filepath.Join(k8s, "linking"), out)))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if err := generate(context.Background(), cfg, time.Unix(0, 0), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	article := func(name string) string {
		t.Helper()
		var found string
		walkErr := filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.Contains(filepath.Base(path), name) {
				return err
			}
			b, readErr := os.ReadFile(path)
			found = string(b)
			return readErr
		})
		if walkErr != nil || found == "" {
			t.Fatalf("article for %s not found: %v", name, walkErr)
		}
		return found
	}
	node := article("node-a-example-com")
	idx := strings.Index(node, "## Needed by")
	if idx < 0 || !strings.Contains(node[idx:], "| api | workload |") {
		t.Errorf("Needed by of node-a does not list the workload:\n%s", node)
	}
	if lb := article("lb01-example-com"); !strings.Contains(lb, "Not known: no source reports anything that depends on this host.") {
		t.Errorf("host without dependents must say it is not known:\n%s", lb)
	}
}
