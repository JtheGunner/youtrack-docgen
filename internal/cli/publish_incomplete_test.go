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

// An object a deny rule removes must be cleaned from the Knowledge Base even
// when the source was read incompletely: denied content must not stay
// published because another file of the source was broken.
func TestIncompleteReadStillCleansDeniedObjects(t *testing.T) {
	gone := strings.Replace(incompleteTestDeployment, "name: web", "name: gone", 1)
	cfg, yt := kubernetesOnlyRun(t, map[string]string{"gone.yaml": gone, "bad.yaml": "a: [unclosed\n"})
	cfg = strings.Replace(cfg, "\nsinks:", "\n    deny:\n      - namespaces: [shop]\nsinks:", 1)

	_, stderr, err := runCLI(t, cfg, "publish", "--allow-mass-stale")
	if err != nil {
		t.Fatalf("error = %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "kubernetes: collection is incomplete") {
		t.Errorf("stderr = %q, want a note about the incomplete collection", stderr)
	}
	if !slices.Contains(yt.writes(), "POST /api/articles/1-900/tags") {
		t.Errorf("the denied object's article was not cleaned and tagged: %v", yt.writes())
	}
}

// The article of an object that only lives in a skipped file is not in the
// graph. Whether it is denied is decided from its document ID, and an
// undecidable case stops publish before the first write.
func TestIncompleteReadChecksDenyRulesForUncollectedArticles(t *testing.T) {
	tests := []struct {
		name      string
		deny      string
		wantClean bool
		wantFail  bool
	}{
		{name: "denied by namespace is cleaned", deny: "      - namespaces: [shop]\n", wantClean: true},
		{name: "other namespace is left alone", deny: "      - namespaces: [other]\n"},
		{name: "label rule is undecidable", deny: "      - labels: {label.team: payments}\n", wantFail: true},
	}
	broken := map[string]string{"ok.yaml": incompleteTestDeployment, "bad.yaml": "a: [unclosed\n"}
	for _, tt := range tests {
		t.Run("publish/"+tt.name, func(t *testing.T) {
			cfg, yt := kubernetesOnlyRun(t, broken)
			cfg = strings.Replace(cfg, "\nsinks:", "\n    deny:\n"+tt.deny+"sinks:", 1)

			_, stderr, err := runCLI(t, cfg, "publish", "--allow-mass-stale")
			if (err != nil) != tt.wantFail {
				t.Fatalf("error = %v, want failure %v\nstderr: %s", err, tt.wantFail, stderr)
			}
			if tt.wantFail && !strings.Contains(err.Error(), "cannot clean denied articles") {
				t.Errorf("error = %q, want the deny cleanup error", err)
			}
			if tagged := slices.Contains(yt.writes(), "POST /api/articles/1-900/tags"); tagged != tt.wantClean {
				t.Errorf("tagged = %v, want %v: %v", tagged, tt.wantClean, yt.writes())
			}
			if tt.wantFail && len(yt.writes()) != 0 {
				t.Errorf("writes before the abort: %v", yt.writes())
			}
		})
	}
	t.Run("diff warns only", func(t *testing.T) {
		cfg, yt := kubernetesOnlyRun(t, broken)
		cfg = strings.Replace(cfg, "\nsinks:", "\n    deny:\n      - labels: {label.team: payments}\nsinks:", 1)

		_, stderr, err := runCLI(t, cfg, "diff")
		if err != nil {
			t.Fatalf("error = %v, want diff to stay tolerant\nstderr: %s", err, stderr)
		}
		if !strings.Contains(stderr, "publish would fail") {
			t.Errorf("stderr = %q, want a warning", stderr)
		}
		if len(yt.writes()) != 0 {
			t.Errorf("diff wrote: %v", yt.writes())
		}
	})
}

// A collector that is switched off is not an incomplete one: its articles stay
// as they are, even when a rule could not be decided from their IDs.
func TestSwitchedOffSourceIsNotCheckedAgainstDenyRules(t *testing.T) {
	cfg, yt := kubernetesOnlyRun(t, map[string]string{"ok.yaml": incompleteTestDeployment})
	yt.articles[0]["content"] = "<!-- docgen:doc netbox:host:old -->\n"
	cfg = strings.Replace(cfg, "\nsinks:",
		"\n  netbox:\n    enabled: false\n    deny:\n      - tags: [lab]\n      - labels: {label.team: payments}\nsinks:", 1)

	_, stderr, err := runCLI(t, cfg, "publish")
	if err != nil {
		t.Fatalf("error = %v, want the switched-off source left alone\nstderr: %s", err, stderr)
	}
	for _, w := range yt.writes() {
		if strings.Contains(w, "/api/articles/1-900") {
			t.Errorf("the switched-off source's article was written: %v", yt.writes())
		}
	}
}
