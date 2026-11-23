package render_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/collector/kubernetes"
	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/filter"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
	"github.com/JtheGunner/youtrack-docgen/internal/render"
)

func dumpTree(root render.Document) string {
	var b strings.Builder
	root.Walk(func(d render.Document) { b.WriteString(d.Markdown()) })
	return b.String()
}

// Seeded fake secrets in a Secret manifest, environment values, annotations and
// the credentials of a repository URL must reach no rendered document, whatever
// the collector reads and the filter lets through.
func TestKubernetesSeededSecretsNeverReachDocuments(t *testing.T) {
	const manifests = `apiVersion: v1
kind: Namespace
metadata:
  name: shop
---
apiVersion: v1
kind: Secret
metadata:
  name: shop-credentials
  namespace: shop
stringData:
  password: FAKE-SECRET-PASSWORD-1
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
  annotations:
    note: FAKE-SECRET-ANNOTATION-3
  labels:
    owner-token: FAKE-SECRET-LABEL-7
spec:
  replicas: 2
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
        - name: web
          image: registry.example.com/shop/web:1.4.0
          args: ["--api-key=FAKE-SECRET-ARG-5"]
          env:
            - name: API_TOKEN
              value: FAKE-SECRET-ENV-4
---
apiVersion: v1
kind: Service
metadata:
  name: web
  namespace: shop
spec:
  clusterIP: 10.96.0.20
  selector:
    app: web
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
  namespace: shop
spec:
  rules:
    - host: shop.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: web
                port:
                  number: 80
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shop.yaml"), []byte(manifests), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := make([]any, 0, 2)
	for _, cluster := range []string{"alpha", "beta"} {
		paths = append(paths, map[string]any{
			"path": dir, "cluster": cluster,
			"repo_url": "https://admin:FAKE-SECRET-URLPASS-8@git.example.com/shop/manifests?private_token=FAKE-SECRET-URLTOKEN-9#FAKE-SECRET-FRAGMENT-10",
		})
	}
	col, err := kubernetes.New(config.Module{Settings: map[string]any{"paths": paths}})
	if err != nil {
		t.Fatal(err)
	}
	g := model.NewGraph()
	if err := col.Collect(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	f, err := filter.New(filter.Config{})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}

	root := render.Render(filtered, render.Options{GeneratedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)})
	out := dumpTree(root)
	var seen int
	root.Walk(func(d render.Document) {
		if strings.HasPrefix(d.ID, "kubernetes:") {
			seen++
		}
	})
	if seen != 4 { // two clusters, two namespaces
		t.Fatalf("rendered %d kubernetes documents, want 4:\n%s", seen, out)
	}
	for _, secret := range []string{"FAKE-SECRET", "admin:", "private_token", "shop-credentials"} {
		if strings.Contains(out, secret) {
			t.Errorf("rendered documents contain %q", secret)
		}
	}
}
