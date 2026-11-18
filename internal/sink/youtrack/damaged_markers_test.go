package youtrack

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

const manualNote = "Manual note: restart order is db, then app.\n"

// damagedContent returns a hand-edited article: the end marker of a, or its
// begin marker, was deleted, with manual text below the section.
func damagedContent(t *testing.T, a block, dropBegin bool) string {
	t.Helper()
	rendered := a.render()
	if dropBegin {
		rendered = strings.Replace(rendered, "<!-- docgen:begin a hash="+a.hash+" -->\n", "", 1)
	} else {
		rendered = strings.Replace(rendered, "<!-- docgen:end a -->\n", "", 1)
	}
	return "<!-- docgen:doc k -->\n\n" + rendered + "\n" + manualNote
}

func TestMergeContentKeepsManualTextWhenAMarkerWasDeleted(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dropBegin bool
	}{
		{"end marker deleted", false},
		{"begin marker deleted", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := mustBlock(t, "a", "old\n")
			changed := mustBlock(t, "a", "new\n")
			content := damagedContent(t, old, tc.dropBegin)
			for run := 1; run <= 3; run++ {
				wanted := old
				if run >= 2 {
					wanted = changed // the hash differs: a replace is attempted
				}
				content = mergeContent(content, []block{wanted})
				if !strings.Contains(content, manualNote) {
					t.Fatalf("run %d lost the manual text:\n%s", run, content)
				}
				if n := strings.Count(content, "<!-- docgen:end a -->"); (n != 1 && !tc.dropBegin) || n > 2 {
					t.Fatalf("run %d: %d end markers, want the appended copy only:\n%s", run, n, content)
				}
			}
			if again := mergeContent(content, []block{changed}); again != content {
				t.Errorf("a further run changed the content:\n%s", again)
			}
			if n := strings.Count(content, changed.render()); n != 1 {
				t.Errorf("%d copies of the section, want 1:\n%s", n, content)
			}
		})
	}
}

func TestParseBlocksNeverPairsAnOrphanBeginWithALaterCopy(t *testing.T) {
	content := "<!-- docgen:begin a hash=00 -->\nmanual\n<!-- docgen:begin a hash=11 -->\nbody\n<!-- docgen:end a -->\n"
	spans := parseBlocks(content)
	if len(spans) != 2 || spans[0].closed || !spans[1].closed {
		t.Fatalf("spans = %+v, want an unclosed orphan and a closed block", spans)
	}
	if got := strayMarkers(content); len(got) != 1 || got[0] != "begin a without end" {
		t.Errorf("strayMarkers = %q", got)
	}
}

func TestStrayMarkers(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          []string
	}{
		{"intact", "<!-- docgen:begin a hash=00 -->\nx\n<!-- docgen:end a -->\n", nil},
		{"no markers", "manual\n", nil},
		{"missing end", "<!-- docgen:begin a hash=00 -->\nx\n", []string{"begin a without end"}},
		{"missing begin", "x\n<!-- docgen:end a -->\n", []string{"end a without begin"}},
		{"end of another ID inside a block is body", "<!-- docgen:begin a hash=00 -->\n<!-- docgen:end b -->\n<!-- docgen:end a -->\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strayMarkers(tc.content)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The flow of the issue over real runs: the end marker of a section is deleted
// by hand, the data changes, and the manual text must survive every run.
func TestPublishKeepsManualTextWhenAMarkerWasDeleted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(content string) string
	}{
		{"end marker deleted", func(c string) string {
			return strings.Replace(c, "<!-- docgen:end summary -->\n", "", 1)
		}},
		{"begin marker deleted", func(c string) string {
			return beginSummaryLine.ReplaceAllString(c, "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{}
			s := newSink(t, api)
			ctx := context.Background()
			if _, err := s.Publish(ctx, tree("web-1.example.com", "a")); err != nil {
				t.Fatal(err)
			}
			host := -1
			for i, a := range api.articles {
				if a.Summary == "web-1.example.com" {
					host = i
				}
			}
			content := tc.damage(api.articles[host].Content)
			// Manual text sits right below the damaged section.
			api.articles[host].Content = strings.Replace(content, "<!-- docgen:begin links", manualNote+"\n<!-- docgen:begin links", 1)

			var contents []string
			for _, summary := range []string{"a", "changed", "changed"} {
				doc := tree("web-1.example.com", summary)
				plan, err := newPlanner(t, api).Plan(ctx, doc)
				if err != nil {
					t.Fatal(err)
				}
				res, err := s.Publish(ctx, doc)
				if err != nil {
					t.Fatal(err)
				}
				got := api.articles[host].Content
				if !strings.Contains(got, manualNote) {
					t.Fatalf("manual text lost after publishing %q:\n%s", summary, got)
				}
				if n := strings.Count(got, "<!-- docgen:end summary -->"); n > 2 {
					t.Errorf("%d end markers for summary:\n%s", n, got)
				}
				if summary == "changed" && !strings.Contains(got, "changed") {
					t.Errorf("the section was not refreshed:\n%s", got)
				}
				if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "damaged docgen markers") {
					t.Errorf("warnings = %q, want one about the damaged markers", res.Warnings)
				}
				if len(plan.Warnings) != 1 || plan.Warnings[0] != res.Warnings[0] {
					t.Errorf("plan warnings %q differ from publish warnings %q", plan.Warnings, res.Warnings)
				}
				contents = append(contents, got)
			}
			if contents[1] != contents[2] {
				t.Errorf("the third run changed the article:\n%s\n---\n%s", contents[1], contents[2])
			}
			updates := api.updates
			if _, err := s.Publish(ctx, tree("web-1.example.com", "changed")); err != nil {
				t.Fatal(err)
			}
			if api.updates != updates {
				t.Error("a run on unchanged data wrote to the article")
			}
		})
	}
}

var beginSummaryLine = regexp.MustCompile("(?m)^<!-- docgen:begin summary hash=\\S+ -->\n")
