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
	envURL       = "YOUTRACK_IT_URL"
	envToken     = "YOUTRACK_IT_TOKEN"
	envProjectID = "YOUTRACK_IT_PROJECT_ID"
)

// titlePrefix marks every article this test creates.
const titlePrefix = "docgen-it-"

const listTimeout = 60 * time.Second

// TestIntegrationPublishAgainstRealYouTrack publishes a small throwaway tree
// to a real YouTrack instance and checks create, idempotent re-run, update,
// stale marking and restore.
//
// The sink marks every article of the project that it did not publish as
// stale, so the test refuses to write unless the project holds only articles
// of earlier runs of this test. Point it at a dedicated sandbox
// project. The client has no delete operation (docgen never deletes
// articles), so the created articles are logged for manual removal.
func TestIntegrationPublishAgainstRealYouTrack(t *testing.T) {
	baseURL, token := os.Getenv(envURL), os.Getenv(envToken)
	projectID := os.Getenv(envProjectID)
	for name, v := range map[string]string{
		envURL: baseURL, envToken: token, envProjectID: projectID,
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
		ProjectID: projectID,
		// Every run leaves its articles behind and the next run marks them as
		// stale: in a sandbox that is most of the project, by design.
		AllowMassStale: true,
	})
	if err != nil {
		t.Fatalf("youtrack.New: %v", err)
	}

	before, err := api.ListProjectArticles(ctx, projectID)
	if err != nil {
		t.Fatalf("list before run: %v", err)
	}
	for _, a := range before {
		if !strings.HasPrefix(a.Summary, titlePrefix) {
			t.Fatalf("project holds article %q that this test did not create; "+
				"use a dedicated sandbox project (refusing to write)", a.Summary)
		}
	}

	rootTitle := titlePrefix + strconv.FormatInt(time.Now().UnixNano(), 10)
	// Registered before the first write so it also runs when the test fails.
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		found, err := api.ListProjectArticles(cleanupCtx, projectID)
		if err != nil {
			t.Logf("cleanup: listing failed (%v); remove articles titled %q* manually", err, rootTitle)
			return
		}
		for _, a := range found {
			if strings.HasPrefix(a.Summary, rootTitle) {
				t.Logf("cleanup: docgen has no delete path; remove article %s (%q) manually", a.IDReadable, a.Summary)
			}
		}
	})

	tree := func(body string, withChild bool) render.Document {
		doc := render.Document{
			ID:    "overview:" + rootTitle,
			Title: rootTitle,
			Sections: []render.Section{{
				ID: "intro", Heading: "Intro", Blocks: []render.Block{render.Paragraph(body)},
			}},
		}
		if withChild {
			doc.Children = []render.Document{{
				ID:    "overview:" + rootTitle + "-child",
				Title: rootTitle + "-child",
				Sections: []render.Section{{
					ID: "details", Heading: "Details", Blocks: []render.Block{render.Paragraph("child " + body)},
				}},
			}}
		}
		return doc
	}

	res, err := sink.Publish(ctx, tree("first version", true))
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if res.Created != 2 {
		t.Fatalf("first publish: created %d articles, want 2 (%+v)", res.Created, res)
	}

	waitForListed(ctx, t, api, projectID, rootTitle, 2)

	res, err = sink.Publish(ctx, tree("first version", true))
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if res.Created != 0 || res.Updated != 0 || res.Stale != 0 || res.Unchanged != 2 {
		t.Fatalf("second publish must be a no-op, got %+v", res)
	}

	res, err = sink.Publish(ctx, tree("second version", true))
	if err != nil {
		t.Fatalf("third publish: %v", err)
	}
	if res.Created != 0 || res.Updated != 2 {
		t.Fatalf("third publish: want 2 updates and no creates, got %+v", res)
	}

	// The child vanishes: its article is marked as stale, never deleted.
	res, err = sink.Publish(ctx, tree("second version", false))
	if err != nil {
		t.Fatalf("stale publish: %v", err)
	}
	if res.Stale != 1 || res.Created != 0 {
		t.Fatalf("stale publish: want exactly 1 article marked stale, got %+v", res)
	}

	// The child returns: the stale marking is removed again.
	res, err = sink.Publish(ctx, tree("second version", true))
	if err != nil {
		t.Fatalf("restore publish: %v", err)
	}
	if res.Restored != 1 || res.Created != 0 {
		t.Fatalf("restore publish: want exactly 1 article restored, got %+v", res)
	}
}

// waitForListed polls until the project listing shows want articles of this
// run, so a re-run cannot start before the first run's articles are visible
// and create duplicates.
func waitForListed(ctx context.Context, t *testing.T, api *client.Client, projectID, prefix string, want int) {
	t.Helper()
	deadline := time.Now().Add(listTimeout)
	for {
		found, err := api.ListProjectArticles(ctx, projectID)
		if err != nil {
			t.Fatalf("list while waiting for articles: %v", err)
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
			t.Fatalf("project listing shows %d of %d articles after %s", n, want, listTimeout)
		}
		time.Sleep(2 * time.Second)
	}
}
