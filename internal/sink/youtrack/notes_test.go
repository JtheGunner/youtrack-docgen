package youtrack

import (
	"context"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
)

func treeWithNotes(summary string) render.Document {
	d := tree("web-1", summary)
	for i := range d.Children {
		if d.Children[i].ID == "netbox:host:web-1" {
			d.Children[i].Notes = render.ManualNotes()
		}
	}
	return d
}

func TestNewArticleGetsManualNotesOnceOutsideTheMarkers(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), treeWithNotes("first")); err != nil {
		t.Fatal(err)
	}
	host := api.byKey(t, "netbox:host:web-1").Content
	if got := strings.Count(host, "## Manual notes"); got != 1 {
		t.Fatalf("manual notes heading count = %d, want 1:\n%s", got, host)
	}
	notesAt := strings.Index(host, "## Manual notes")
	for _, id := range []string{"summary", "links"} {
		if end := strings.Index(host, "<!-- docgen:end "+id+" -->"); end > notesAt {
			t.Errorf("notes come before the %s block", id)
		}
	}
	for _, key := range []string{"overview:root", "overview:vlans", "k8s:workload:app"} {
		if strings.Contains(api.byKey(t, key).Content, "Manual notes") {
			t.Errorf("article %s has a notes section but its document has none", key)
		}
	}
}

func TestLaterRunsNeverChangeTheNotes(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), treeWithNotes("first")); err != nil {
		t.Fatal(err)
	}

	// Unchanged data: no write at all, notes included.
	writesBefore := len(api.writeCalls())
	if _, err := s.Publish(context.Background(), treeWithNotes("first")); err != nil {
		t.Fatal(err)
	}
	if got := len(api.writeCalls()); got != writesBefore {
		t.Errorf("second run made %d write calls", got-writesBefore)
	}

	// A person writes into the notes, then a generated section changes.
	host := api.byKey(t, "netbox:host:web-1")
	host.Content = strings.Replace(host.Content, "Write notes here.", "Rack 4, ask the network team before reboots.", 1)
	if _, err := s.Publish(context.Background(), treeWithNotes("second")); err != nil {
		t.Fatal(err)
	}
	got := api.byKey(t, "netbox:host:web-1").Content
	if !strings.Contains(got, "Rack 4, ask the network team before reboots.") {
		t.Errorf("edited notes were lost:\n%s", got)
	}
	if n := strings.Count(got, "## Manual notes"); n != 1 {
		t.Errorf("manual notes heading count = %d, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "second") {
		t.Errorf("generated section was not updated:\n%s", got)
	}
}

func TestExistingArticleWithoutNotesDoesNotGainThem(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "first")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(context.Background(), treeWithNotes("second")); err != nil {
		t.Fatal(err)
	}
	if got := api.byKey(t, "netbox:host:web-1").Content; strings.Contains(got, "Manual notes") {
		t.Errorf("notes were added to an existing article:\n%s", got)
	}
}
