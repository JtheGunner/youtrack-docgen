package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// k8sSecretFixtures are the fake secrets seeded into the Kubernetes fixtures:
// the shared web.yaml (annotation, env values, container argument) and the
// extra manifest written by writeSecretManifest.
var k8sSecretFixtures = []string{
	"FAKE-SECRET-ANNOTATION-3", "FAKE-SECRET-ENV-4", "FAKE-SECRET-ARG-5", "FAKE-SECRET-ENV-6",
	"FAKE-NAME-SECRET-7", "FAKE-LABEL-SECRET-8", "FAKE-IDENTITY-SECRET-9",
}

// writeSecretManifest returns the path of a manifest with secrets in a label,
// in the name of one object and in the name of another one.
func writeSecretManifest(t *testing.T) string {
	t.Helper()
	const manifest = `apiVersion: v1
kind: Service
metadata:
  name: worker
  namespace: shop
  labels:
    dsn: "postgres://app:FAKE-LABEL-SECRET-8@db.example.com/app"
spec:
  clusterIP: 10.96.0.50
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: "password=FAKE-NAME-SECRET-7"
  namespace: shop
spec:
  template:
    spec:
      containers:
        - name: main
          image: registry.example.com/shop/main:1.0.0
---
apiVersion: v1
kind: Service
metadata:
  name: "api_key=FAKE-IDENTITY-SECRET-9"
  namespace: shop
spec:
  clusterIP: 10.96.0.77
`
	path := filepath.Join(t.TempDir(), "secrets.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func k8sFixturePaths(t *testing.T) string {
	t.Helper()
	k8s := filepath.Join("..", "collector", "kubernetes", "testdata")
	return fmt.Sprintf("[{path: %s, cluster: test}, {path: %s, cluster: test}]", filepath.Join(k8s, "manifests", "apps", "web.yaml"), writeSecretManifest(t))
}

func TestGenerateKubernetesFixturesLeakNoSecret(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	cfg := fmt.Sprintf(`
collectors:
  kubernetes:
    paths: %s
sinks:
  markdown:
    dir: %s
`, k8sFixturePaths(t), out)

	stdout, stderr, err := runGenerate(t, cfg)
	if err != nil {
		t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
	}
	outputs := map[string]string{"stdout": stdout, "stderr": stderr}
	for name, body := range readDir(t, out) {
		outputs[name] = string(body)
	}
	var documented bool
	for name, body := range outputs {
		for _, secret := range k8sSecretFixtures {
			if strings.Contains(name+"\n"+body, secret) {
				t.Errorf("%s contains the secret %q:\n%s", name, secret, body)
			}
		}
		if strings.Contains(body, "worker") {
			documented = true
		}
	}
	if !documented {
		t.Error("the harmless worker service must stay documented")
	}
}

func TestPublishKubernetesFixturesLeakNoSecretToYouTrack(t *testing.T) {
	t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
	yt := newFakeYouTrack(t)
	cfg := fmt.Sprintf(`
youtrack:
  url: %s
  token: ${YOUTRACK_TOKEN}
  project: INFRA
collectors:
  kubernetes:
    paths: %s
sinks:
  youtrack: {}
`, yt.srv.URL, k8sFixturePaths(t))

	stdout, stderr, err := runCLI(t, cfg, "publish")
	if err != nil {
		t.Fatalf("publish error = %v\nstderr: %s", err, stderr)
	}
	sent := strings.Join(yt.bodies, "\n")
	if !strings.Contains(sent, "worker") {
		t.Fatalf("publish sent no article for the worker service:\n%s", sent)
	}
	for _, secret := range append(k8sSecretFixtures, fakeYouTrackToken) {
		if strings.Contains(sent, secret) {
			t.Errorf("secret %q was sent to YouTrack", secret)
		}
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("secret %q reached the output", secret)
		}
	}
}
