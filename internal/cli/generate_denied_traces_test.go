package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Whatever a deny rule rejects appears in no form in the output: a denied
// host's short name and reverse-DNS name, and every member of a denied
// namespace. The kept service carries no denied value.
func TestGenerateRemovesEveryTraceOfDeniedEntities(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeTaggedLinkNetBox(t)
	k8s := filepath.Join("..", "collector", "kubernetes", "testdata")
	manifest := filepath.Join(t.TempDir(), "traces.yaml")
	const objects = `apiVersion: v1
kind: Service
metadata:
  name: edge
  namespace: infra
  labels:
    note: moved from node-a, ptr 21.2.0.192.in-addr.arpa, was node-a11
spec:
  clusterIP: 10.96.0.10
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hidden-workload
  namespace: quarantine
spec:
  replicas: 1
---
apiVersion: v1
kind: Service
metadata:
  name: hidden-service
  namespace: quarantine
spec:
  clusterIP: 10.96.0.99
`
	if err := os.WriteFile(manifest, []byte(objects), 0o600); err != nil {
		t.Fatal(err)
	}
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
    deny:
      - namespaces: [quarantine]
sinks:
  markdown:
    dir: %s
`, srv.URL, filepath.Join(k8s, "nodes"), manifest, out)

	stdout, stderr, err := runGenerate(t, cfg)
	if err != nil {
		t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
	}
	outputs := map[string]string{"stdout": stdout, "stderr": stderr}
	for name, body := range readDir(t, out) {
		outputs[name] = string(body)
	}
	for name, body := range outputs {
		lower := strings.ToLower(name + "\n" + body)
		for _, denied := range []string{"node-a", "21.2.0.192", "192.0.2.21", "quarantine", "hidden-workload", "hidden-service", "10.96.0.99"} {
			if strings.Contains(lower, denied) {
				t.Errorf("%s reveals the denied value %q:\n%s", name, denied, body)
			}
		}
	}
}
