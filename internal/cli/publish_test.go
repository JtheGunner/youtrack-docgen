package cli

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const fakeProjectID = "0-42"

func publishEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
}

func TestPublishCreatesArticlesInTheConfiguredProject(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	out := filepath.Join(t.TempDir(), "out")

	stdout, stderr, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, out), "publish")
	if err != nil {
		t.Fatalf("error = %v\nstderr: %s", err, stderr)
	}
	if len(yt.articles) == 0 {
		t.Fatal("publish created no article")
	}
	for _, a := range yt.articles {
		project, _ := a["project"].(map[string]any)
		if project["id"] != fakeProjectID {
			t.Errorf("article %q created in project %v, want %s", a["summary"], project["id"], fakeProjectID)
		}
	}
	want := "sink youtrack: " + strconv.Itoa(len(yt.articles)) + " created, 0 updated, 0 unchanged, 0 marked stale, 0 restored\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if files := readDirIfExists(out); len(files) > 0 {
		t.Errorf("publish wrote Markdown files: %v", files)
	}
}

func TestPublishSecondRunOnSameDataWritesNothing(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	cfg := diffConfig(nb.URL, yt.srv.URL, t.TempDir())
	if _, _, err := runCLI(t, cfg, "publish"); err != nil {
		t.Fatal(err)
	}
	created := len(yt.articles)
	firstWrites := len(yt.writes())

	stdout, _, err := runCLI(t, cfg, "publish")
	if err != nil {
		t.Fatal(err)
	}
	if extra := yt.writes()[firstWrites:]; len(extra) > 0 {
		t.Errorf("second run made write requests: %v", extra)
	}
	want := "sink youtrack: 0 created, 0 updated, " + strconv.Itoa(created) + " unchanged, 0 marked stale, 0 restored\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestPublishReportsArticlesMarkedStale(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	yt.articles = []map[string]any{{
		"id":      "1-900",
		"summary": "gone.example.com",
		"content": "<!-- docgen:doc netbox:host:gone.example.com -->\n",
	}}
	yt.nextID = 900

	stdout, stderr, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "publish")
	if err != nil {
		t.Fatalf("error = %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, " 1 marked stale, 0 restored\n") {
		t.Errorf("stdout = %q, want one article marked stale", stdout)
	}
	if !slices.Contains(yt.writes(), "POST /api/articles/1-900/tags") {
		t.Errorf("the vanished article was not tagged: %v", yt.writes())
	}
}

func TestPublishNeverSendsOrPrintsSeededSecrets(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)

	stdout, stderr, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "publish")
	if err != nil {
		t.Fatal(err)
	}
	if len(yt.bodies) == 0 {
		t.Fatal("publish sent no request body")
	}
	sent := strings.Join(yt.bodies, "\n")
	for _, secret := range []string{seededSecret, fakeNetBoxToken, fakeYouTrackToken} {
		if strings.Contains(sent, secret) {
			t.Errorf("secret %q was sent to YouTrack", secret)
		}
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("secret %q reached the output", secret)
		}
	}
}

func TestPublishFailsForUnknownProjectBeforeWriting(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	yt.projects = nil

	_, _, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "publish")
	if err == nil || !strings.Contains(err.Error(), `"INFRA"`) {
		t.Fatalf("error = %v, want one naming the project", err)
	}
	if w := yt.writes(); len(w) > 0 {
		t.Errorf("publish wrote despite the unknown project: %v", w)
	}
}

func TestPublishRequiresYouTrackSink(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	nb := fakeNetBox(t)
	_, _, err := runCLI(t, generateConfig(nb.URL, t.TempDir(), ""), "publish")
	if err == nil || !strings.Contains(err.Error(), "enable the youtrack sink") {
		t.Errorf("error = %v, want a hint to enable the youtrack sink", err)
	}
}

// A failed collection must never reach YouTrack: publishing a partial tree
// would mark every missing article as stale.
func TestPublishDoesNotContactYouTrackWhenCollectionFails(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	nb.Close()
	yt := newFakeYouTrack(t)

	_, _, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "publish")
	if err == nil {
		t.Fatal("want an error")
	}
	if len(yt.requests) > 0 {
		t.Errorf("YouTrack was contacted after a failed collection: %v", yt.requests)
	}
}

func TestGenerateHasNoDryRunFlag(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	nb := fakeNetBox(t)
	_, _, err := runCLI(t, generateConfig(nb.URL, t.TempDir(), ""), "generate", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("error = %v, want an unknown flag error", err)
	}
}
