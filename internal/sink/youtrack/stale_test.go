package youtrack

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrackt-docgen/internal/sink/youtrack/client"
)

// tagState gives fakeAPI in-memory tag support and counts tag writes.
type tagState struct {
	tags       []client.TagRef
	tagWrites  int
	addTagErr  error
	findTagErr error
}

func (f *fakeAPI) FindTag(_ context.Context, name string) (*client.TagRef, error) {
	if f.findTagErr != nil {
		return nil, f.findTagErr
	}
	for i := range f.tags {
		if f.tags[i].Name == name {
			return &f.tags[i], nil
		}
	}
	return nil, nil
}

func (f *fakeAPI) CreateTag(_ context.Context, name string) (*client.TagRef, error) {
	f.tagWrites++
	f.tags = append(f.tags, client.TagRef{ID: "5-" + name, Name: name})
	return &f.tags[len(f.tags)-1], nil
}

func (f *fakeAPI) AddTag(_ context.Context, articleID, tagID string) error {
	if f.addTagErr != nil {
		return f.addTagErr
	}
	f.tagWrites++
	for i := range f.articles {
		if f.articles[i].ID != articleID {
			continue
		}
		for _, t := range f.tags {
			if t.ID == tagID {
				f.articles[i].Tags = append(f.articles[i].Tags, t)
				return nil
			}
		}
	}
	return errors.New("not found")
}

func (f *fakeAPI) RemoveTag(_ context.Context, articleID, tagID string) error {
	f.tagWrites++
	for i := range f.articles {
		if f.articles[i].ID != articleID {
			continue
		}
		kept := f.articles[i].Tags[:0]
		for _, t := range f.articles[i].Tags {
			if t.ID != tagID {
				kept = append(kept, t)
			}
		}
		f.articles[i].Tags = kept
	}
	return nil
}

func isStale(a *client.Article) bool {
	return hasStaleTag(*a) && strings.Contains(a.Content, "docgen:begin stale-banner")
}

func TestVanishedObjectGetsTagAndBanner(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	host := api.byKey(t, "netbox:host:web-1")
	host.Content = "Manual intro.\n\n" + host.Content
	n := len(api.articles)

	gone := tree("web-1", "a")
	gone.Children = append(gone.Children[:1], gone.Children[2:]...)
	res, err := s.Publish(context.Background(), gone)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stale != 2 || len(api.articles) != n {
		t.Fatalf("result %+v, articles %d->%d; want host and its emptied group stale, none deleted", res, n, len(api.articles))
	}
	got := api.byKey(t, "netbox:host:web-1")
	if !isStale(got) {
		t.Fatalf("host not marked stale: tags=%v content=\n%s", got.Tags, got.Content)
	}
	for _, want := range []string{"Manual intro.", "docgen:begin summary", "docgen:doc netbox:host:web-1"} {
		if !strings.Contains(got.Content, want) {
			t.Errorf("content lost %q:\n%s", want, got.Content)
		}
	}
	if api.byKey(t, "overview:vlans").Tags != nil {
		t.Error("published article was tagged")
	}
}

func TestStaleMarkingIsIdempotent(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	gone := tree("web-1", "a")
	gone.Children = gone.Children[:1]
	if _, err := s.Publish(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	updates, tagWrites, creates := api.updates, api.tagWrites, api.creates
	res, err := s.Publish(context.Background(), gone)
	if err != nil {
		t.Fatal(err)
	}
	if api.updates != updates || api.tagWrites != tagWrites || api.creates != creates || res.Stale != 0 {
		t.Errorf("second run wrote: updates %d->%d, tag writes %d->%d, result %+v", updates, api.updates, tagWrites, api.tagWrites, res)
	}
}

func TestReappearingObjectLosesTagAndBanner(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	before := api.byKey(t, "netbox:host:web-1").Content
	gone := tree("web-1", "a")
	gone.Children = gone.Children[:1]
	if _, err := s.Publish(context.Background(), gone); err != nil {
		t.Fatal(err)
	}

	res, err := s.Publish(context.Background(), tree("web-1", "a"))
	if err != nil {
		t.Fatal(err)
	}
	got := api.byKey(t, "netbox:host:web-1")
	if res.Restored != 4 || hasStaleTag(*got) || strings.Contains(got.Content, "stale-banner") {
		t.Fatalf("result %+v, tags %v, content:\n%s", res, got.Tags, got.Content)
	}
	if got.Content != before {
		t.Errorf("content differs after round trip:\n%q\nwant\n%q", got.Content, before)
	}
	updates, tagWrites := api.updates, api.tagWrites
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	if api.updates != updates || api.tagWrites != tagWrites {
		t.Error("run after restore wrote again")
	}
}

func TestExistingStaleTagIsReused(t *testing.T) {
	api := &fakeAPI{tagState: tagState{tags: []client.TagRef{{ID: "5-x", Name: StaleTag}}}}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
		t.Fatal(err)
	}
	gone := tree("web-1", "a")
	gone.Children = gone.Children[:1]
	if _, err := s.Publish(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	if len(api.tags) != 1 {
		t.Errorf("tags = %v, want the existing tag reused", api.tags)
	}
}

func TestStaleTagErrorsPropagate(t *testing.T) {
	gone := tree("web-1", "a")
	gone.Children = gone.Children[:1]
	for name, st := range map[string]tagState{
		"find": {findTagErr: errors.New("boom")},
		"add":  {addTagErr: errors.New("boom")},
	} {
		t.Run(name, func(t *testing.T) {
			api := &fakeAPI{}
			s := newSink(t, api)
			if _, err := s.Publish(context.Background(), tree("web-1", "a")); err != nil {
				t.Fatal(err)
			}
			api.tagState = st
			if _, err := s.Publish(context.Background(), gone); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestRemoveBlockRoundTrip(t *testing.T) {
	for name, content := range map[string]string{
		"with marker": buildContent("k", nil) + "\nManual.\n",
		"no marker":   "Manual only.\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := removeBlock(insertBanner(content), staleBlockID); got != content {
				t.Errorf("got %q, want %q", got, content)
			}
		})
	}
}
