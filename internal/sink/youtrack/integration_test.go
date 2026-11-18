package youtrack_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

// Environment variables that enable the real-YouTrack integration test. When
// any of them is unset the test is skipped, so default CI never runs it.
const (
	envURL          = "YOUTRACK_IT_URL"
	envToken        = "YOUTRACK_IT_TOKEN"
	envProjectID    = "YOUTRACK_IT_PROJECT_ID"
	envProjectQuery = "YOUTRACK_IT_PROJECT_QUERY"
)

// titlePrefix marks every article this test creates.
const titlePrefix = "docgen-it-"

const indexTimeout = 60 * time.Second

// TestIntegrationPublishAgainstRealYouTrack publishes a small throwaway tree
// to a real YouTrack instance and checks create, idempotent re-run and update.
//
// The sink marks every article matched by the project query that it did not
// publish as stale, so the test refuses to write unless the query matches only
// articles of earlier runs of this test. Point it at a dedicated sandbox
// project. The client has no delete operation (docgen never deletes
// articles), so the created articles are logged for manual removal.
func TestIntegrationPublishAgainstRealYouTrack(t *testing.T) {
	baseURL, token := os.Getenv(envURL), os.Getenv(envToken)
	projectID, query := os.Getenv(envProjectID), os.Getenv(envProjectQuery)
	for name, v := range map[string]string{
		envURL: baseURL, envToken: token, envProjectID: projectID, envProjectQuery: query,
	} {
		if v == "" {
			t.Skipf("real-YouTrack integration test disabled: %s is not set", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	api, err := client.New(baseURL, token)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	sink, err := youtrack.New(api, youtrack.Config{
		ProjectID: projectID, ProjectQuery: query,
		// Every run leaves its articles behind and the next run marks them as
		// stale: in a sandbox that is most of the project, by design.
		AllowMassStale: true,
	})
	if err != nil {
		t.Fatalf("youtrack.New: %v", err)
	}

	before, err := api.SearchArticles(ctx, query)
	if err != nil {
		t.Fatalf("search before run: %v", err)
	}
	for _, a := range before {
		if !strings.HasPrefix(a.Summary, titlePrefix) {
			t.Fatalf("project query matches article %q that this test did not create; "+
				"use a dedicated sandbox project (refusing to write)", a.Summary)
		}
	}

	rootTitle := titlePrefix + strconv.FormatInt(time.Now().UnixNano(), 10)
	// Registered before the first write so it also runs when the test fails.
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		found, err := api.SearchArticles(cleanupCtx, query)
		if err != nil {
			t.Logf("cleanup: search failed (%v); remove articles titled %q* manually", err, rootTitle)
			return
		}
		for _, a := range found {
			if strings.HasPrefix(a.Summary, rootTitle) {
				t.Logf("cleanup: docgen has no delete path; remove article %s (%q) manually", a.IDReadable, a.Summary)
			}
		}
	})

	tree := func(body string) render.Document {
		return render.Document{
			ID:    "overview:" + rootTitle,
			Title: rootTitle,
			Sections: []render.Section{{
				ID: "intro", Heading: "Intro", Blocks: []render.Block{render.Paragraph(body)},
			}},
			Children: []render.Document{{
				ID:    "overview:" + rootTitle + "-child",
				Title: rootTitle + "-child",
				Sections: []render.Section{{
					ID: "details", Heading: "Details", Blocks: []render.Block{render.Paragraph("child " + body)},
				}},
			}},
		}
	}

	res, err := sink.Publish(ctx, tree("first version"))
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if res.Created != 2 {
		t.Fatalf("first publish: created %d articles, want 2 (%+v)", res.Created, res)
	}

	waitForIndexed(ctx, t, api, query, rootTitle, 2)

	res, err = sink.Publish(ctx, tree("first version"))
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if res.Created != 0 || res.Updated != 0 || res.Stale != 0 || res.Unchanged != 2 {
		t.Fatalf("second publish must be a no-op, got %+v", res)
	}

	res, err = sink.Publish(ctx, tree("second version"))
	if err != nil {
		t.Fatalf("third publish: %v", err)
	}
	if res.Created != 0 || res.Updated != 2 {
		t.Fatalf("third publish: want 2 updates and no creates, got %+v", res)
	}
}

// waitForIndexed polls until the search index lists want articles of this run,
// because YouTrack indexes new articles asynchronously and an early re-run
// would otherwise create duplicates.
func waitForIndexed(ctx context.Context, t *testing.T, api *client.Client, query, prefix string, want int) {
	t.Helper()
	deadline := time.Now().Add(indexTimeout)
	for {
		found, err := api.SearchArticles(ctx, query)
		if err != nil {
			t.Fatalf("search while waiting for index: %v", err)
		}
		n := 0
		for _, a := range found {
			if strings.HasPrefix(a.Summary, prefix) {
				n++
			}
		}
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("search index shows %d of %d articles after %s", n, want, indexTimeout)
		}
		time.Sleep(2 * time.Second)
	}
}
