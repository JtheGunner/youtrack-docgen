package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const incompleteTestDeployment = `apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: shop}
spec: {replicas: 1}
`

// kubernetesOnlyRun prepares a Kubernetes-only config over the given manifest
// files and a YouTrack that already holds the article of an object the
// manifests do not list.
func kubernetesOnlyRun(t *testing.T, files map[string]string) (cfg string, yt *fakeYouTrack) {
	t.Helper()
	publishEnv(t)
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	yt = newFakeYouTrack(t)
	yt.articles = []map[string]any{{
		"id":      "1-900",
		"summary": "gone",
		"content": "<!-- docgen:doc kubernetes:workload:shop/deployment/gone -->\n",
		"project": map[string]any{"id": fakeProjectID},
	}}
	yt.nextID = 900
	cfg = "youtrack:\n  url: " + yt.srv.URL + "\n  token: ${YOUTRACK_TOKEN}\n  project: INFRA\n" +
		"collectors:\n  kubernetes:\n    paths: [" + dir + "]\nsinks:\n  youtrack: {}\n"
	return cfg, yt
}

func TestIncompleteKubernetesReadNeverMarksArticlesStale(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{name: "broken file", files: map[string]string{"ok.yaml": incompleteTestDeployment, "bad.yaml": "a: [unclosed\n"}},
		{name: "invalid document", files: map[string]string{"ok.yaml": incompleteTestDeployment + "---\napiVersion: apps/v1\nkind: Deployment\nmetadata: [x]\n"}},
		{name: "nameless namespace", files: map[string]string{"ok.yaml": incompleteTestDeployment + "---\napiVersion: v1\nkind: Namespace\nmetadata: {}\n"}},
		{name: "empty path", files: nil},
	}
	for _, args := range [][]string{{"publish", "--allow-mass-stale"}, {"diff"}} {
		for _, tt := range tests {
			t.Run(args[0]+"/"+tt.name, func(t *testing.T) {
				cfg, yt := kubernetesOnlyRun(t, tt.files)

				stdout, stderr, err := runCLI(t, cfg, args...)
				if err != nil {
					t.Fatalf("error = %v\nstderr: %s", err, stderr)
				}
				if !strings.Contains(stderr, "kubernetes: collection is incomplete") {
					t.Errorf("stderr = %q, want a note about the incomplete collection", stderr)
				}
				if !strings.Contains(stdout, "0 marked stale") && !strings.Contains(stdout, "0 to mark stale") {
					t.Errorf("stdout = %q, want no stale marking", stdout)
				}
				if slices.Contains(yt.writes(), "POST /api/articles/1-900/tags") {
					t.Errorf("the article was tagged stale: %v", yt.writes())
				}
			})
		}
	}
}

func TestCompleteKubernetesReadStillMarksVanishedArticlesStale(t *testing.T) {
	cfg, yt := kubernetesOnlyRun(t, map[string]string{"ok.yaml": incompleteTestDeployment})

	if _, stderr, err := runCLI(t, cfg, "publish", "--allow-mass-stale"); err != nil {
		t.Fatalf("error = %v\nstderr: %s", err, stderr)
	}
	if !slices.Contains(yt.writes(), "POST /api/articles/1-900/tags") {
		t.Errorf("the vanished article was not tagged: %v", yt.writes())
	}
}
