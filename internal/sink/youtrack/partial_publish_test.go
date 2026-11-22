package youtrack

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

var errServerDown = errors.New("fake server down")

// flakyAPI fails the n-th create call, or every update call, while armed.
type flakyAPI struct {
	*fakeAPI
	failCreateAt int // 1-based number of the create call that fails; 0 disables
	failUpdates  bool
	createCalls  int
}

func (f *flakyAPI) CreateArticle(ctx context.Context, n client.NewArticle) (*client.Article, error) {
	f.createCalls++
	if f.failCreateAt != 0 && f.createCalls == f.failCreateAt {
		return nil, errServerDown
	}
	return f.fakeAPI.CreateArticle(ctx, n)
}

func (f *flakyAPI) UpdateArticle(ctx context.Context, id string, u client.ArticleUpdate) (*client.Article, error) {
	if f.failUpdates {
		return nil, errServerDown
	}
	return f.fakeAPI.UpdateArticle(ctx, id, u)
}

func countKeys(t *testing.T, api *fakeAPI) map[string]int {
	t.Helper()
	keys := map[string]int{}
	for _, a := range api.articles {
		key, ok := documentKey(a.Content)
		if !ok {
			t.Fatalf("article %s has no document key", a.ID)
		}
		keys[key]++
	}
	return keys
}

func TestAbortMidPublishLeavesAStateTheNextRunContinuesWithoutDuplicates(t *testing.T) {
	api := &flakyAPI{fakeAPI: &fakeAPI{}, failCreateAt: 4}
	s := newSink(t, api)

	res, err := s.Publish(context.Background(), tree("web-1.example.com", "a"))
	if !errors.Is(err, errServerDown) {
		t.Fatalf("error = %v, want the server failure", err)
	}
	if res.Created != 3 || len(api.articles) != 3 {
		t.Fatalf("after the abort: result %+v, %d articles, want 3 created", res, len(api.articles))
	}

	api.failCreateAt = 0
	res, err = s.Publish(context.Background(), tree("web-1.example.com", "a"))
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if res.Created != 3 || res.Unchanged != 3 {
		t.Errorf("retry result = %+v, want the 3 missing articles created and 3 left alone", res)
	}
	keys := countKeys(t, api.fakeAPI)
	if len(keys) != 6 {
		t.Errorf("%d distinct documents, want 6: %v", len(keys), keys)
	}
	for key, n := range keys {
		if n != 1 {
			t.Errorf("document %s exists %d times", key, n)
		}
	}
	// The children created by the retry hang below the parents of the first run.
	host := api.byKey(t, "netbox:host:web-1")
	group := api.byKey(t, "group:hosts")
	if host.ParentArticle == nil || host.ParentArticle.ID != group.ID {
		t.Errorf("host parent = %+v, want the hosts group %s", host.ParentArticle, group.ID)
	}

	before := len(api.writeCalls())
	if _, err := s.Publish(context.Background(), tree("web-1.example.com", "a")); err != nil {
		t.Fatal(err)
	}
	if got := len(api.writeCalls()); got != before {
		t.Errorf("a run after the recovery wrote %d times, want 0", got-before)
	}
}

func TestAbortedUpdateKeepsManualTextAndTheRetryFinishesIt(t *testing.T) {
	api := &flakyAPI{fakeAPI: &fakeAPI{}}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1.example.com", "old")); err != nil {
		t.Fatal(err)
	}
	host := api.byKey(t, "netbox:host:web-1")
	host.Content += "\nManual note.\n"

	api.failUpdates = true
	if _, err := s.Publish(context.Background(), tree("web-1.example.com", "new")); !errors.Is(err, errServerDown) {
		t.Fatalf("error = %v, want the server failure", err)
	}
	if got := api.byKey(t, "netbox:host:web-1").Content; !strings.Contains(got, "Manual note.") || !strings.Contains(got, "old") {
		t.Errorf("the failed run changed the article:\n%s", got)
	}

	api.failUpdates = false
	if _, err := s.Publish(context.Background(), tree("web-1.example.com", "new")); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	got := api.byKey(t, "netbox:host:web-1").Content
	if !strings.Contains(got, "Manual note.") || !strings.Contains(got, "new") || strings.Contains(got, "old") {
		t.Errorf("retry result is wrong:\n%s", got)
	}
	if n := len(countKeys(t, api.fakeAPI)); n != 6 {
		t.Errorf("%d documents, want 6", n)
	}
}

func TestDryRunAgainstAKnowledgeBaseWithManuallyEditedArticles(t *testing.T) {
	api := &fakeAPI{}
	if _, err := newSink(t, api).Publish(context.Background(), tree("web-1.example.com", "a")); err != nil {
		t.Fatal(err)
	}
	// An engineer adds notes around the generated blocks of one article and
	// edits the text inside a generated block of another. A block whose
	// generated input did not change is not rewritten, so that edit stays.
	host := api.byKey(t, "netbox:host:web-1")
	host.Content = "Manual intro.\n\n" + host.Content + "\n## Operator notes\nKeep me.\n"
	vlans := api.byKey(t, "overview:vlans")
	vlans.Content = strings.Replace(vlans.Content, "\nvlans\n", "\nhand edited vlans\n", 1)
	// A page of its own that docgen never created.
	api.articles = append(api.articles, client.Article{ID: "9-1", IDReadable: "KB-A-99", Summary: "Runbook", Content: "Hand written runbook.\n"})
	writes := len(api.writeCalls())

	plan, err := newPlanner(t, api).Plan(context.Background(), tree("web-1.example.com", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.Summary(), "0 to create, 1 to update, 5 unchanged, 0 to mark stale, 0 to restore"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if len(plan.Writes()) != 1 {
		t.Errorf("plan lists %d writes, want 1: %v", len(plan.Writes()), plan.Writes())
	}
	diff := plan.Diff()
	for _, mustNot := range []string{"Manual intro", "Operator notes", "Keep me", "Runbook"} {
		if strings.Contains(diff, mustNot) {
			t.Errorf("diff touches manual content %q:\n%s", mustNot, diff)
		}
	}
	if strings.Contains(diff, "hand edited vlans") {
		t.Errorf("diff rewrites a generated block whose input did not change:\n%s", diff)
	}
	if !strings.Contains(diff, "+b") {
		t.Errorf("diff lacks the changed summary:\n%s", diff)
	}
	if got := len(api.writeCalls()); got != writes {
		t.Errorf("the dry run made %d write calls", got-writes)
	}
	if api.byKey(t, "netbox:host:web-1").Content != host.Content {
		t.Error("the dry run changed an article")
	}
}
