package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateReportsAmbiguousLinksOnlyForKeptEntities has a Kubernetes node
// with the short name web01 that matches two NetBox hosts by name. The warning
// is shown while all hosts are published. Once a deny rule removes one
// candidate, the node is withheld too (it may be the denied machine) and
// neither the warning nor the denied host may reach any output.
func TestGenerateReportsAmbiguousLinksOnlyForKeptEntities(t *testing.T) {
	const devices = `{"count": 2, "next": null, "previous": null, "results": [
  {"id": 41, "name": "web01.example.com", "status": {"value": "active"},
   "primary_ip4": null, "primary_ip6": null, "description": "", "tags": []},
  {"id": 42, "name": "web01.example.org", "status": {"value": "active"},
   "primary_ip4": null, "primary_ip6": null, "description": "", "tags": [{"slug": "lab"}]}
]}`
	const node = `apiVersion: v1
kind: Node
metadata:
  name: web01
`
	tests := []struct {
		name        string
		deny        string
		wantWarning bool
	}{
		{name: "every entity kept", wantWarning: true},
		{name: "one candidate denied", deny: "\n    deny:\n      - tags: [lab]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := emptyNetBoxPage
				if r.URL.Path == "/api/dcim/devices/" {
					body = devices
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, body)
			}))
			t.Cleanup(srv.Close)
			manifest := filepath.Join(t.TempDir(), "node.yaml")
			if err := os.WriteFile(manifest, []byte(node), 0o600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "out")
			cfg := fmt.Sprintf(`
collectors:
  netbox:
    url: %s
    token: ${NETBOX_TOKEN}%s
  kubernetes:
    paths: [%s]
sinks:
  markdown:
    dir: %s
`, srv.URL, tt.deny, manifest, out)

			stdout, stderr, err := runGenerate(t, cfg)
			if err != nil {
				t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
			}
			if got := strings.Contains(stderr, "ambiguous match"); got != tt.wantWarning {
				t.Errorf("stderr has the ambiguity warning = %v, want %v:\n%s", got, tt.wantWarning, stderr)
			}
			if tt.wantWarning {
				return
			}
			outputs := map[string]string{"stdout": stdout, "stderr": stderr}
			for name, body := range readDir(t, out) {
				outputs[name] = string(body)
			}
			for name, body := range outputs {
				text := strings.ToLower(name + "\n" + body)
				for _, leaked := range []string{"web01.example.org", "netbox-host-web01-example-org", "kubernetes:host:web01", "kubernetes-host-web01"} {
					if strings.Contains(text, leaked) {
						t.Errorf("%s reveals %q:\n%s", name, leaked, body)
					}
				}
			}
		})
	}
}
