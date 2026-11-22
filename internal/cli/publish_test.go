package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
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
		"project": map[string]any{"id": fakeProjectID},
	}}
	yt.nextID = 900

	stdout, stderr, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "publish", "--allow-mass-stale")
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

// The preview must fail where the real run would fail.
func TestDryRunFailsForUnknownProject(t *testing.T) {
	for _, args := range [][]string{{"diff"}, {"publish", "--dry-run"}} {
		publishEnv(t)
		nb := fakeNetBox(t)
		yt := newFakeYouTrack(t)
		yt.projects = nil

		_, _, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), args...)
		if err == nil || !strings.Contains(err.Error(), `"INFRA"`) {
			t.Errorf("%v: error = %v, want one naming the project", args, err)
		}
	}
}

// The search query is only a hint. An article of another project that carries
// a docgen key must not be updated, tagged, or shown in the preview.
func TestArticlesOfOtherProjectsAreNeverTouched(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	foreign := "<!-- docgen:doc netbox:host:other.example.com -->\nforeign text\n"
	yt.articles = []map[string]any{{
		"id": "1-900", "summary": "other.example.com", "content": foreign,
		"project": map[string]any{"id": "0-999"},
	}}
	yt.nextID = 900
	cfg := diffConfig(nb.URL, yt.srv.URL, t.TempDir())

	preview, _, err := runCLI(t, cfg, "diff")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview, "other.example.com") {
		t.Errorf("the preview shows an article of another project:\n%s", preview)
	}
	stdout, _, err := runCLI(t, cfg, "publish")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, " 0 marked stale,") {
		t.Errorf("stdout = %q, want no article marked stale", stdout)
	}
	for _, w := range yt.writes() {
		if strings.Contains(w, "1-900") {
			t.Errorf("an article of another project was written: %s", w)
		}
	}
	if yt.articles[0]["content"] != foreign {
		t.Error("the content of an article of another project changed")
	}
}

func TestProjectQueryUsesTheResolvedKeyInBraces(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	yt.projects = []map[string]any{{"id": fakeProjectID, "shortName": "Infra"}}

	if _, _, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "publish"); err != nil {
		t.Fatal(err)
	}
	if len(yt.queries) != 1 || yt.queries[0] != "project: {Infra}" {
		t.Errorf("queries = %q, want one listing with the project's own key in braces", yt.queries)
	}
}

// The preview and the real run must agree, for creations and for updates.
func TestDryRunCountsMatchWhatPublishWrites(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	cfg := diffConfig(nb.URL, yt.srv.URL, t.TempDir())

	preview, _, err := runCLI(t, cfg, "publish", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLI(t, cfg, "publish"); err != nil {
		t.Fatal(err)
	}
	created := strconv.Itoa(len(yt.articles))
	if !strings.HasSuffix(preview, "sink youtrack (dry run): "+created+" to create, 0 to update, 0 unchanged, 0 to mark stale, 0 to restore\n") {
		t.Errorf("preview does not announce the %s articles publish created:\n%s", created, preview)
	}

	yt.articles[0]["summary"] = "renamed by hand"
	preview, _, err = runCLI(t, cfg, "publish", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview, " 0 to create, 1 to update, ") {
		t.Errorf("preview does not announce one update:\n%s", preview)
	}
	stdout, _, err := runCLI(t, cfg, "publish")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "sink youtrack: 0 created, 1 updated, ") {
		t.Errorf("stdout = %q, want one article updated", stdout)
	}
}

// A later run on unchanged data must not rewrite articles just because the
// "Generated at" line shows a new time.
func TestPublishLaterOnSameDataWritesNothing(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	cfg := diffConfig(nb.URL, yt.srv.URL, t.TempDir())
	if _, _, err := runCLI(t, cfg, "publish"); err != nil {
		t.Fatal(err)
	}
	created := strconv.Itoa(len(yt.articles))
	firstWrites := len(yt.writes())
	later := generatedAt.Add(24 * time.Hour)

	preview, _, err := runCLIAt(t, later, cfg, "diff")
	if err != nil {
		t.Fatal(err)
	}
	if want := "sink youtrack (dry run): 0 to create, 0 to update, " + created + " unchanged, 0 to mark stale, 0 to restore\n"; preview != want {
		t.Errorf("diff = %q, want %q", preview, want)
	}
	stdout, _, err := runCLIAt(t, later, cfg, "publish")
	if err != nil {
		t.Fatal(err)
	}
	if extra := yt.writes()[firstWrites:]; len(extra) > 0 {
		t.Errorf("the later run made write requests: %v", extra)
	}
	if want := "sink youtrack: 0 created, 0 updated, " + created + " unchanged, 0 marked stale, 0 restored\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// The Markdown sink writes whole files, so it keeps showing the time of the run.
func TestGenerateWritesTheTimeOfTheRun(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	nb := fakeNetBox(t)
	out := filepath.Join(t.TempDir(), "out")
	later := generatedAt.Add(24 * time.Hour)
	if _, _, err := runCLIAt(t, later, generateConfig(nb.URL, out, ""), "generate"); err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(out, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "Generated at "+later.Format(time.RFC3339)) {
		t.Errorf("index.md does not show the time of the run:\n%s", index)
	}
}

// An article whose document marker was removed is reported on stderr by the
// preview and by the real run, and no second article is created for it.
func TestArticleWithoutDocumentMarkerIsReportedOnStderr(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	cfg := diffConfig(nb.URL, yt.srv.URL, t.TempDir())
	if _, _, err := runCLI(t, cfg, "publish"); err != nil {
		t.Fatal(err)
	}
	var title string
	for _, a := range yt.articles {
		content, _ := a["content"].(string)
		if strings.Contains(content, "docgen:doc netbox:host:srv01.example.com") {
			a["content"] = strings.Replace(content, "<!-- docgen:doc netbox:host:srv01.example.com -->\n", "", 1)
			title, _ = a["summary"].(string)
		}
	}
	if title == "" {
		t.Fatal("fixture host article not found")
	}
	// A second article without marker whose title matches no document, and
	// whose title tries to drive the terminal.
	yt.articles = append(yt.articles, map[string]any{
		"id": "1-800", "summary": "stray\x1b]0;pwned\x07",
		"content": "<!-- docgen:begin summary hash=00 -->\nold\n<!-- docgen:end summary -->\n",
		"project": map[string]any{"id": fakeProjectID},
	})
	count, writes := len(yt.articles), len(yt.writes())

	for _, args := range [][]string{{"diff"}, {"publish"}} {
		stdout, stderr, err := runCLI(t, cfg, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(stderr, "warning: ") || !strings.Contains(stderr, "<!-- docgen:doc netbox:host:srv01.example.com -->") {
			t.Errorf("%v: stderr does not warn about the article without marker: %q", args, stderr)
		}
		if strings.Count(stderr, "warning: ") != 2 {
			t.Errorf("%v: want two warnings, got %q", args, stderr)
		}
		if strings.ContainsAny(stderr, "\x1b\x07") {
			t.Errorf("%v: a control character from an article title reached the terminal: %q", args, stderr)
		}
		if strings.Contains(stdout, "warning") {
			t.Errorf("%v: the warning went to stdout", args)
		}
	}
	if len(yt.articles) != count || len(yt.writes()) != writes {
		t.Errorf("articles %d->%d, writes %d->%d: want nothing written", count, len(yt.articles), writes, len(yt.writes()))
	}
}

// seedVanishedArticle puts the article of a host that the sources do not
// list into the fake YouTrack: the only article of the project, so a run marks
// all of the managed articles as stale.
func seedVanishedArticle(yt *fakeYouTrack) {
	yt.articles = []map[string]any{{
		"id":      "1-900",
		"summary": "gone.example.com",
		"content": "<!-- docgen:doc netbox:host:gone.example.com -->\n",
		"project": map[string]any{"id": fakeProjectID},
	}}
	yt.nextID = 900
}

func TestPublishRefusesMassStaleBeforeWriting(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	seedVanishedArticle(yt)

	_, _, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "publish")
	if err == nil || !strings.Contains(err.Error(), "--allow-mass-stale") {
		t.Fatalf("error = %v, want the hint for --allow-mass-stale", err)
	}
	if w := yt.writes(); len(w) > 0 {
		t.Errorf("publish wrote before refusing: %v", w)
	}
}

func TestPublishHonoursMaxStaleRatioFromTheConfig(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	seedVanishedArticle(yt)
	cfg := strings.Replace(diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "  project: INFRA\n", "  project: INFRA\n  max_stale_ratio: 1\n", 1)

	if _, stderr, err := runCLI(t, cfg, "publish"); err != nil {
		t.Fatalf("error = %v\nstderr: %s", err, stderr)
	}
}

func TestDryRunShowsStaleChangesAndTheGuard(t *testing.T) {
	for _, cmd := range [][]string{{"diff"}, {"publish", "--dry-run"}} {
		t.Run(strings.Join(cmd, " "), func(t *testing.T) {
			publishEnv(t)
			nb := fakeNetBox(t)
			yt := newFakeYouTrack(t)
			seedVanishedArticle(yt)
			cfg := diffConfig(nb.URL, yt.srv.URL, t.TempDir())

			stdout, _, err := runCLI(t, cfg, cmd...)
			if err == nil || !strings.Contains(err.Error(), "--allow-mass-stale") {
				t.Fatalf("error = %v, want the stale guard", err)
			}
			for _, want := range []string{"netbox:host:gone.example.com", "+Tags: docgen-stale", "1 to mark stale"} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout does not show %q:\n%s", want, stdout)
				}
			}

			stdout, stderr, err := runCLI(t, cfg, append(cmd, "--allow-mass-stale")...)
			if err != nil {
				t.Fatalf("error = %v\nstderr: %s", err, stderr)
			}
			if !strings.Contains(stdout, "1 to mark stale") {
				t.Errorf("stdout = %s", stdout)
			}
			if w := yt.writes(); len(w) > 0 {
				t.Errorf("dry run made write requests: %v", w)
			}
		})
	}
}
