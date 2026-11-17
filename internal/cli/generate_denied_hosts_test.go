package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTaggedLinkNetBox serves the link fixtures with both hosts tagged "lab".
func fakeTaggedLinkNetBox(t *testing.T) *httptest.Server {
	t.Helper()
	devices := strings.ReplaceAll(linkNetBoxDevices, `"tags": []`, `"tags": [{"slug": "lab"}]`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := map[string]string{
			"/api/dcim/devices/":      devices,
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

// Denied hosts are referenced by Kubernetes objects through their hostnames and
// addresses (ingress hosts, service external IPs, node pinning). None of it may
// reach a file, stdout or stderr, while the objects themselves stay documented.
func TestGenerateScrubsDeniedHostsFromAllEntityKinds(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeTaggedLinkNetBox(t)
	k8s := filepath.Join("..", "collector", "kubernetes", "testdata")
	out := filepath.Join(t.TempDir(), "out")
	cfg := fmt.Sprintf(`
collectors:
  netbox:
    url: %s
    token: ${NETBOX_TOKEN}
    deny:
      - tags: [lab]
  kubernetes:
    paths: [%s, %s]
sinks:
  markdown:
    dir: %s
`, srv.URL, filepath.Join(k8s, "nodes"), filepath.Join(k8s, "manifests", "apps", "web.yaml"), out)

	stdout, stderr, err := runGenerate(t, cfg)
	if err != nil {
		t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
	}
	outputs := map[string]string{"stdout": stdout, "stderr": stderr}
	for name, body := range readDir(t, out) {
		outputs[name] = string(body)
	}
	for name, body := range outputs {
		for _, denied := range []string{"node-a.example.com", "shop.example.com", "192.0.2.21", "192.0.2.30"} {
			if strings.Contains(strings.ToLower(name+"\n"+body), denied) {
				t.Errorf("%s reveals the denied value %q:\n%s", name, denied, body)
			}
		}
	}
	const keptRow = "| 10.96.0.10 | edge | service |"
	var documented bool
	for _, body := range outputs {
		if strings.Contains(body, keptRow) {
			documented = true
		}
	}
	if !documented {
		t.Errorf("the service must stay documented with its cluster IP (%q), only the denied host data is scrubbed", keptRow)
	}
}
