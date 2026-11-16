package youtrack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrackt-docgen/internal/render"
	"github.com/JtheGunner/youtrackt-docgen/internal/sink/youtrack/client"
)

// fakeAPI is an in-memory YouTrack that counts write calls.
type fakeAPI struct {
	articles  []client.Article
	creates   int
	updates   int
	searchErr error
}

func (f *fakeAPI) SearchArticles(context.Context, string) ([]client.Article, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return append([]client.Article(nil), f.articles...), nil
}

func (f *fakeAPI) CreateArticle(_ context.Context, n client.NewArticle) (*client.Article, error) {
	f.creates++
	a := client.Article{
		ID:         fmt.Sprintf("1-%d", len(f.articles)+1),
		IDReadable: fmt.Sprintf("KB-A-%d", len(f.articles)+1),
		Summary:    n.Summary,
		Content:    n.Content,
	}
	if n.ParentArticleID != "" {
		a.ParentArticle = &client.Ref{ID: n.ParentArticleID}
	}
	f.articles = append(f.articles, a)
	return &a, nil
}

func (f *fakeAPI) UpdateArticle(_ context.Context, id string, u client.ArticleUpdate) (*client.Article, error) {
	f.updates++
	for i := range f.articles {
		if f.articles[i].ID != id {
			continue
		}
		a := &f.articles[i]
		if u.Summary != nil {
			a.Summary = *u.Summary
		}
		if u.Content != nil {
			a.Content = *u.Content
		}
		if u.ParentArticleID != nil {
			a.ParentArticle = &client.Ref{ID: *u.ParentArticleID}
		}
		cp := *a
		return &cp, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeAPI) byKey(t *testing.T, key string) *client.Article {
	t.Helper()
	for i := range f.articles {
		if k, _ := documentKey(f.articles[i].Content); k == key {
			return &f.articles[i]
		}
	}
	t.Fatalf("no article with key %q", key)
	return nil
}

func section(id, text string) render.Section {
	return render.Section{ID: id, Heading: strings.ToUpper(id), Blocks: []render.Block{render.Paragraph(text)}}
}

func tree(hostTitle, summary string) render.Document {
	return render.Document{
		ID: "overview:root", Title: "Infrastructure documentation",
		Sections: []render.Section{section("contents", "index")},
		Children: []render.Document{
			{ID: "overview:vlans", Title: "VLANs", Sections: []render.Section{section("table", "vlans")}},
			{ID: "netbox:host:web-1", Title: hostTitle, Sections: []render.Section{section("summary", summary), section("links", "none")}},
			{ID: "k8s:workload:app", Title: "app", Sections: []render.Section{section("summary", "deployment")}},
		},
	}
}

func newSink(t *testing.T, api API) *Sink {
	t.Helper()
	s, err := New(api, Config{ProjectID: "0-1", ProjectQuery: "project: KB"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPublishBuildsHierarchy(t *testing.T) {
	api := &fakeAPI{}
	res, err := newSink(t, api).Publish(context.Background(), tree("web-1.example.com", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 6 || res.Updated != 0 {
		t.Fatalf("result = %+v, want 6 created (root, vlans, hosts, host, kubernetes, workload)", res)
	}
	parentKey := func(key string) string {
		a := api.byKey(t, key)
		if a.ParentArticle == nil {
			return ""
		}
		for _, p := range api.articles {
			if p.ID == a.ParentArticle.ID {
				k, _ := documentKey(p.Content)
				return k
			}
		}
		return "?"
	}
	want := map[string]string{
		"overview:root":     "",
		"overview:vlans":    "overview:root",
		"group:hosts":       "overview:root",
		"group:kubernetes":  "overview:root",
		"netbox:host:web-1": "group:hosts",
		"k8s:workload:app":  "group:kubernetes",
	}
	for key, parent := range want {
		if got := parentKey(key); got != parent {
			t.Errorf("parent of %s = %q, want %q", key, got, parent)
		}
	}
}

func TestSecondRunMakesNoWriteCalls(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	creates, updates := api.creates, api.updates
	res, err := s.Publish(context.Background(), tree("web-1", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if api.creates != creates || api.updates != updates {
		t.Errorf("second run wrote: creates %d->%d, updates %d->%d", creates, api.creates, updates, api.updates)
	}
	if res.Created != 0 || res.Updated != 0 || res.Unchanged != 6 {
		t.Errorf("result = %+v", res)
	}
}

func TestManualContentIsPreservedAndOnlyChangedSectionReplaced(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "old")); err != nil {
		t.Fatal(err)
	}
	host := api.byKey(t, "netbox:host:web-1")
	host.Content = "Manual intro.\n\n" + host.Content + "\n## Operator notes\nKeep me.\n"
	linksBefore := between(t, host.Content, "links")

	if _, err := s.Publish(context.Background(), tree("web-1", "new")); err != nil {
		t.Fatal(err)
	}
	got := api.byKey(t, "netbox:host:web-1").Content
	for _, want := range []string{"Manual intro.", "## Operator notes\nKeep me.", "new"} {
		if !strings.Contains(got, want) {
			t.Errorf("content lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old") {
		t.Errorf("old section content still present:\n%s", got)
	}
	if between(t, got, "links") != linksBefore {
		t.Error("unchanged section was rewritten")
	}
	if api.updates != 1 {
		t.Errorf("updates = %d, want 1 (only the host)", api.updates)
	}
}

func TestRenamedTitleIsRecognizedByKey(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	n := len(api.articles)
	res, err := s.Publish(context.Background(), tree("web-one", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(api.articles) != n || res.Created != 0 || res.Updated != 1 {
		t.Fatalf("articles %d->%d, result %+v", n, len(api.articles), res)
	}
	if got := api.byKey(t, "netbox:host:web-1").Summary; got != "web-one" {
		t.Errorf("summary = %q", got)
	}
}

func TestNewSectionIsAppendedAndRemovedSectionKept(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	d := tree("web-1", "a")
	d.Children[1].Sections = []render.Section{section("summary", "a"), section("extra", "added")}
	if _, err := s.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	got := api.byKey(t, "netbox:host:web-1").Content
	if !strings.Contains(got, "docgen:begin extra") || !strings.Contains(got, "docgen:begin links") {
		t.Errorf("want appended extra section and untouched links section:\n%s", got)
	}
}

func TestMisplacedParentIsCorrected(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	api.byKey(t, "netbox:host:web-1").ParentArticle = nil
	res, err := s.Publish(context.Background(), tree("web-1", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 {
		t.Errorf("result = %+v, want one parent fix", res)
	}
}

func TestDuplicateKeyFails(t *testing.T) {
	c := buildContent("overview:root", nil)
	api := &fakeAPI{articles: []client.Article{{ID: "1", Content: c}, {ID: "2", Content: c}}}
	if _, err := newSink(t, api).Publish(context.Background(), tree("h", "a")); err == nil {
		t.Fatal("want error for duplicate key")
	}
}

func TestSearchErrorPropagates(t *testing.T) {
	api := &fakeAPI{searchErr: errors.New("boom")}
	if _, err := newSink(t, api).Publish(context.Background(), tree("h", "a")); !errors.Is(err, api.searchErr) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvalidIDsAreRejected(t *testing.T) {
	d := tree("h", "a")
	d.Children[0].ID = "bad id-->"
	if _, err := newSink(t, &fakeAPI{}).Publish(context.Background(), d); err == nil {
		t.Fatal("want error for marker-breaking ID")
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(nil, Config{ProjectID: "1", ProjectQuery: "q"}); err == nil {
		t.Error("nil API accepted")
	}
	if _, err := New(&fakeAPI{}, Config{}); err == nil {
		t.Error("empty config accepted")
	}
}

func TestMergeContentIgnoresUnterminatedBlock(t *testing.T) {
	existing := "<!-- docgen:doc k -->\n<!-- docgen:begin a hash=00 -->\nno end\n"
	w, _ := newBlock("a", "x\n")
	got := mergeContent(existing, []block{w})
	if !strings.HasPrefix(got, existing) || !strings.Contains(got, "docgen:end a") {
		t.Errorf("got %q", got)
	}
}

// between returns the marked block with the given section ID.
func between(t *testing.T, content, id string) string {
	t.Helper()
	start := strings.Index(content, "<!-- docgen:begin "+id+" ")
	end := strings.Index(content, "<!-- docgen:end "+id+" -->")
	if start < 0 || end < start {
		t.Fatalf("block %q not found in:\n%s", id, content)
	}
	return content[start:end]
}
