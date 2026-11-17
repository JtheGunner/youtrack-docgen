package youtrack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

// fakeAPI is an in-memory YouTrack that counts write calls.
// testProjectID is the project the sink under test publishes to. Articles the
// fake holds without a project count as articles of this project.
const testProjectID = "0-1"

type fakeAPI struct {
	articles  []client.Article
	creates   int
	updates   int
	searchErr error
	tagState
}

func (f *fakeAPI) SearchArticles(context.Context, string) ([]client.Article, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	out := append([]client.Article(nil), f.articles...)
	for i := range out {
		if out[i].Project == nil {
			out[i].Project = &client.Ref{ID: testProjectID}
		}
	}
	return out, nil
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

func TestNewSectionIsAppendedAndRemovedSectionDropped(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	host := api.byKey(t, "netbox:host:web-1")
	host.Content += "\nA manual note.\n"
	d := tree("web-1", "a")
	d.Children[1].Sections = []render.Section{section("summary", "a"), section("extra", "added")}
	if _, err := s.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	got := api.byKey(t, "netbox:host:web-1").Content
	if !strings.Contains(got, "docgen:begin extra") || strings.Contains(got, "docgen:begin links") {
		t.Errorf("want the extra section appended and the links section removed:\n%s", got)
	}
	if !strings.Contains(got, "A manual note.") {
		t.Errorf("manual text was lost:\n%s", got)
	}
	updates := api.updates
	if _, err := s.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if api.updates != updates {
		t.Error("the article was written again after the obsolete section was removed")
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

// A document ID that would break out of a marker comment is encoded, so the
// article content holds exactly the comments the sink wrote.
func TestMarkerBreakingDocumentIDCannotInjectAComment(t *testing.T) {
	d := tree("h", "a")
	d.Children[0].ID = "bad id--> <!-- docgen:doc overview:root"
	api := &fakeAPI{}
	if _, err := newSink(t, api).Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	roots := 0
	for _, a := range api.articles {
		if key, _ := documentKey(a.Content); key == "overview:root" {
			roots++
		}
		// Every "--" must belong to a comment delimiter the sink wrote.
		if strings.Count(a.Content, "--") != strings.Count(a.Content, "<!--")+strings.Count(a.Content, "-->") {
			t.Errorf("the ID opened or closed a comment:\n%s", a.Content)
		}
	}
	if roots != 1 {
		t.Errorf("%d articles claim the root key, want 1", roots)
	}
}

// Section IDs come from the renderer and are fixed names. One that would
// break a marker is a programming error and stops the run.
func TestInvalidSectionIDsAreRejected(t *testing.T) {
	d := tree("h", "a")
	d.Children[0].Sections[0].ID = "bad id-->"
	if _, err := newSink(t, &fakeAPI{}).Publish(context.Background(), d); err == nil {
		t.Fatal("want error for marker-breaking section ID")
	}
	if _, err := newPlanner(t, &fakeAPI{}).Plan(context.Background(), d); err == nil {
		t.Fatal("want error from the planner too")
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

// An article of another project must never be touched, even when the search
// returns it and it carries a docgen key: not updated, not marked stale.
func TestPublishIgnoresArticlesOfOtherProjects(t *testing.T) {
	foreign := client.Article{
		ID: "9-1", IDReadable: "OTHER-A-1", Summary: "foreign",
		Content: docMarker(render.RootID) + "\nforeign text\n",
		Project: &client.Ref{ID: "0-999"},
	}
	vanished := client.Article{
		ID: "9-2", IDReadable: "OTHER-A-2", Summary: "foreign host",
		Content: docMarker("netbox:host:other.example.com") + "\n",
		Project: &client.Ref{ID: "0-999"},
	}
	api := &fakeAPI{articles: []client.Article{foreign, vanished}}
	s, err := New(api, Config{ProjectID: testProjectID, ProjectQuery: "project: KB"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Publish(context.Background(), render.Document{ID: render.RootID, Title: "Root"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || res.Updated != 0 || res.Stale != 0 {
		t.Errorf("result = %+v, want the root created and nothing else touched", res)
	}
	if api.articles[0].Content != foreign.Content || api.articles[1].Content != vanished.Content {
		t.Error("an article of another project was changed")
	}
}

// stamped is a host document whose "generated" section carries a timestamp.
func stamped(summary, at string) render.Document {
	return render.Document{
		ID: "overview:root", Title: "Root",
		Sections: []render.Section{
			section("summary", summary),
			{ID: "generated", Heading: "Generated", Blocks: []render.Block{render.Paragraph("Generated at " + at)}, Volatile: true},
		},
	}
}

func TestVolatileSectionAloneCausesNoUpdate(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), stamped("a", "03:00")); err != nil {
		t.Fatal(err)
	}
	res, err := s.Publish(context.Background(), stamped("a", "04:00"))
	if err != nil {
		t.Fatal(err)
	}
	if api.updates != 0 || res.Unchanged != 1 {
		t.Errorf("updates = %d, result = %+v, want no update for a new timestamp alone", api.updates, res)
	}
	if !strings.Contains(api.articles[0].Content, "Generated at 03:00") {
		t.Error("the stored timestamp changed without an update")
	}

	plan, err := newPlanner(t, api).Plan(context.Background(), stamped("a", "05:00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 0 || plan.Unchanged() != 1 {
		t.Errorf("plan = %s, want no change for a new timestamp alone", plan.Summary())
	}
}

func TestVolatileSectionIsRefreshedWhenAnotherSectionChanges(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), stamped("a", "03:00")); err != nil {
		t.Fatal(err)
	}
	plan, err := newPlanner(t, api).Plan(context.Background(), stamped("b", "04:00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || !strings.Contains(plan.Diff(), "+Generated at 04:00") {
		t.Errorf("plan does not show the refreshed timestamp:\n%s", plan.Diff())
	}
	if _, err := s.Publish(context.Background(), stamped("b", "04:00")); err != nil {
		t.Fatal(err)
	}
	got := api.articles[0].Content
	if !strings.Contains(got, "Generated at 04:00") || strings.Contains(got, "Generated at 03:00") {
		t.Errorf("timestamp was not refreshed with the changed section:\n%s", got)
	}
}

func TestMissingVolatileSectionIsAdded(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	doc := stamped("a", "03:00")
	withoutStamp := doc
	withoutStamp.Sections = doc.Sections[:1]
	if _, err := s.Publish(context.Background(), withoutStamp); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(api.articles[0].Content, "Generated at 03:00") {
		t.Error("a volatile section missing from the article was not added")
	}
}

// Host names with spaces or a double hyphen are valid in NetBox. They must
// not stop a run, and their articles must be found again on the next run.
func TestNamesThatAreNotMarkerSafeArePublishedAndFoundAgain(t *testing.T) {
	doc := render.Document{
		ID: "overview:root", Title: "Root",
		Children: []render.Document{
			{ID: "netbox:host:Core Switch 1", Title: "Core Switch 1", Sections: []render.Section{section("summary", "a")}},
			{ID: "netbox:host:xn--mnchen-3ya.example.com", Title: "xn--mnchen-3ya.example.com", Sections: []render.Section{section("summary", "b")}},
		},
	}
	api := &fakeAPI{}
	plan, err := newPlanner(t, api).Plan(context.Background(), doc)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got := plan.Summary(); got != "4 to create, 0 to update, 0 unchanged" {
		t.Errorf("plan = %q", got)
	}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), doc); err != nil {
		t.Fatalf("publish: %v", err)
	}
	for _, a := range api.articles {
		if strings.Count(a.Content, "--") != strings.Count(a.Content, "<!--")+strings.Count(a.Content, "-->") {
			t.Errorf("a name broke out of a marker comment:\n%s", a.Content)
		}
	}
	res, err := s.Publish(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 || res.Updated != 0 || res.Unchanged != 4 || res.Stale != 0 {
		t.Errorf("second run = %+v, want everything found again and unchanged", res)
	}
}

// lostMarker removes the document marker line from an article, as a person
// tidying up the text might do.
func lostMarker(content string) string {
	return docMarkerRe.ReplaceAllString(content, "")
}

// An article that lost its document marker cannot be recognised by key. The
// sink must neither overwrite it nor create a second article next to it; it
// warns and names the line to restore.
func TestArticleWithoutDocumentMarkerIsNotDuplicatedOrOverwritten(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	doc := tree("web-1.example.com", "a")
	if _, err := s.Publish(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	var host int
	for i, a := range api.articles {
		if a.Summary == "web-1.example.com" {
			host = i
		}
	}
	api.articles[host].Content = lostMarker(api.articles[host].Content)
	orphan := api.articles[host]
	count, creates, updates := len(api.articles), api.creates, api.updates

	changed := tree("web-1.example.com", "changed")
	plan, err := newPlanner(t, api).Plan(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 0 || len(plan.Warnings) != 1 {
		t.Errorf("plan: %d changes, warnings %q, want no change and one warning", len(plan.Changes), plan.Warnings)
	}
	res, err := s.Publish(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if len(api.articles) != count || api.creates != creates || api.updates != updates {
		t.Errorf("articles %d->%d, creates %d->%d, updates %d->%d: want nothing written", count, len(api.articles), creates, api.creates, updates, api.updates)
	}
	if api.articles[host].Content != orphan.Content {
		t.Error("the article without marker was overwritten")
	}
	if res.Stale != 0 {
		t.Errorf("result = %+v, want nothing marked stale", res)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings = %q, want one", res.Warnings)
	}
	for _, want := range []string{orphan.IDReadable, "web-1.example.com", "<!-- docgen:doc netbox:host:web-1 -->"} {
		if !strings.Contains(res.Warnings[0], want) {
			t.Errorf("warning %q does not mention %q", res.Warnings[0], want)
		}
	}
	if len(plan.Warnings) == 1 && plan.Warnings[0] != res.Warnings[0] {
		t.Errorf("plan and publish warn differently:\n%s\n%s", plan.Warnings[0], res.Warnings[0])
	}
}

// When the root lost its marker, its children stay below it.
func TestChildrenStayBelowAnArticleWithoutDocumentMarker(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	doc := tree("web-1.example.com", "a")
	if _, err := s.Publish(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	api.articles[0].Content = lostMarker(api.articles[0].Content)
	count, updates := len(api.articles), api.updates

	plan, err := newPlanner(t, api).Plan(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 0 {
		t.Errorf("plan shows changes for children of the article without marker:\n%s", plan.Diff())
	}
	if _, err := s.Publish(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	if len(api.articles) != count || api.updates != updates {
		t.Errorf("articles %d->%d, updates %d->%d: want nothing written", count, len(api.articles), updates, api.updates)
	}
}

// Generated blocks without a document marker and without a document of the
// same title: the article is left alone and reported.
func TestStrayArticleWithGeneratedBlocksIsReported(t *testing.T) {
	stray := client.Article{
		ID: "9-1", IDReadable: "KB-A-900", Summary: "retired.example.com",
		Content: "<!-- docgen:begin summary hash=00 -->\nold\n<!-- docgen:end summary -->\n",
	}
	api := &fakeAPI{articles: []client.Article{stray}}
	res, err := newSink(t, api).Publish(context.Background(), tree("web-1.example.com", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 6 || api.articles[0].Content != stray.Content {
		t.Errorf("result = %+v; the stray article must stay untouched", res)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "KB-A-900") {
		t.Errorf("warnings = %q, want one naming KB-A-900", res.Warnings)
	}
}

// An article written before the keys were encoded and the markers were read
// tolerantly must be found again by its old key: no second article.
func TestArticleInTheOldFormatIsFoundAgain(t *testing.T) {
	summary, err := newBlock("summary", section("summary", "a").Markdown())
	if err != nil {
		t.Fatal(err)
	}
	old := "<!-- docgen:doc overview:root -->\n\n" +
		"<!-- docgen:begin summary hash=" + summary.hash + " -->\n" + summary.body + "<!-- docgen:end summary -->\n"
	api := &fakeAPI{articles: []client.Article{{ID: "1-1", IDReadable: "KB-A-1", Summary: "Root", Content: old}}}
	doc := render.Document{ID: "overview:root", Title: "Root", Sections: []render.Section{section("summary", "a")}}

	res, err := newSink(t, api).Publish(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 || res.Updated != 0 || res.Unchanged != 1 || len(api.articles) != 1 {
		t.Errorf("result = %+v with %d articles, want the old article unchanged", res, len(api.articles))
	}
}
