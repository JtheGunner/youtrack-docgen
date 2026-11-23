package youtrack

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/filter"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
	"github.com/JtheGunner/youtrack-docgen/internal/render"
)

// clusterTree is the overview root with one cluster document per entry of
// clusters (cluster name to namespace names), each holding its namespaces as
// children, the way the renderer emits them.
func clusterTree(clusters map[string][]string) render.Document {
	names := make([]string, 0, len(clusters))
	for name := range clusters {
		names = append(names, name)
	}
	sort.Strings(names)
	children := []render.Document{
		{ID: "overview:vlans", Title: "VLANs", Sections: []render.Section{section("table", "vlans")}},
	}
	for _, name := range names {
		cluster := render.Document{
			ID: "kubernetes:cluster:" + name, Title: name,
			Sections: []render.Section{section("summary", "cluster "+name)},
		}
		for _, ns := range clusters[name] {
			cluster.Children = append(cluster.Children, render.Document{
				ID: "kubernetes:namespace:" + name + "/" + ns, Title: ns,
				Sections: []render.Section{section("summary", "namespace "+ns+" in "+name)},
			})
		}
		children = append(children, cluster)
	}
	return render.Document{
		ID: "overview:root", Title: "Infrastructure documentation",
		Sections: []render.Section{section("contents", "index")},
		Children: children,
	}
}

func twoClusters() map[string][]string {
	return map[string][]string{"prod": {"blog", "shop"}, "stage": {"shop"}}
}

// parentKeyOf returns the document key of the parent article of key.
func (f *fakeAPI) parentKeyOf(t *testing.T, key string) string {
	t.Helper()
	a := f.byKey(t, key)
	if a.ParentArticle == nil {
		return ""
	}
	for _, p := range f.articles {
		if p.ID == a.ParentArticle.ID {
			k, _ := documentKey(p.Content)
			return k
		}
	}
	return "?"
}

// allKeys returns the document keys of every article the fake holds.
func (f *fakeAPI) allKeys() []string {
	var out []string
	for _, a := range f.articles {
		if k, ok := documentKey(a.Content); ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func TestKubernetesArticlesLandBelowTheirCluster(t *testing.T) {
	api := &fakeAPI{}
	res, err := newSink(t, api).Publish(context.Background(), clusterTree(twoClusters()))
	if err != nil {
		t.Fatal(err)
	}
	// root, vlans, group, 2 clusters, 3 namespaces
	if res.Created != 8 {
		t.Fatalf("result = %+v, want 8 created", res)
	}
	tests := []struct{ key, title, parent string }{
		{"group:kubernetes", "Kubernetes", "overview:root"},
		{"kubernetes:cluster:prod", "prod", "group:kubernetes"},
		{"kubernetes:cluster:stage", "stage", "group:kubernetes"},
		{"kubernetes:namespace:prod/blog", "blog", "kubernetes:cluster:prod"},
		{"kubernetes:namespace:prod/shop", "shop", "kubernetes:cluster:prod"},
		{"kubernetes:namespace:stage/shop", "shop", "kubernetes:cluster:stage"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := api.byKey(t, tt.key).Summary; got != tt.title {
				t.Errorf("title = %q, want %q", got, tt.title)
			}
			if got := api.parentKeyOf(t, tt.key); got != tt.parent {
				t.Errorf("parent = %q, want %q", got, tt.parent)
			}
		})
	}
	if slices.Contains(api.allKeys(), "group:hosts") {
		t.Error("a hosts group was created without hosts")
	}
}

func TestKubernetesSecondRunMakesNoWriteCalls(t *testing.T) {
	api := publishedAPI(t, clusterTree(twoClusters()))
	before := len(api.writeCalls())
	res, err := newSink(t, api).Publish(context.Background(), clusterTree(twoClusters()))
	if err != nil {
		t.Fatal(err)
	}
	if got := api.writeCalls(); len(got) != before {
		t.Errorf("second run wrote: %v", got[before:])
	}
	if res.Created != 0 || res.Updated != 0 || res.Stale != 0 || res.Unchanged != 8 {
		t.Errorf("result = %+v, want 8 unchanged", res)
	}
}

func TestRemovedNamespaceIsMarkedStaleAndItsClusterStays(t *testing.T) {
	api := publishedAPI(t, clusterTree(twoClusters()))
	n := len(api.articles)
	gone := map[string][]string{"prod": {"blog"}, "stage": {"shop"}}

	res, err := newSink(t, api).Publish(context.Background(), clusterTree(gone))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stale != 1 || len(api.articles) != n {
		t.Fatalf("result = %+v, articles %d->%d; want one stale article, none deleted", res, n, len(api.articles))
	}
	if got := api.byKey(t, "kubernetes:namespace:prod/shop"); !isStale(got) {
		t.Errorf("removed namespace is not stale: tags=%v", got.Tags)
	}
	for _, key := range api.allKeys() {
		if key == "kubernetes:namespace:prod/shop" {
			continue
		}
		if a := api.byKey(t, key); hasStaleTag(*a) || strings.Contains(a.Content, "docgen:begin stale-banner") {
			t.Errorf("%s was marked stale", key)
		}
	}
}

func TestDisappearedClusterAndItsNamespacesAreMarkedStale(t *testing.T) {
	api := publishedAPI(t, clusterTree(twoClusters()))
	n := len(api.articles)

	res, err := newSink(t, api).Publish(context.Background(), clusterTree(map[string][]string{"prod": {"blog", "shop"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(api.articles) != n {
		t.Fatalf("articles %d->%d; none may be deleted", n, len(api.articles))
	}
	want := []string{"kubernetes:cluster:stage", "kubernetes:namespace:stage/shop"}
	if got := staleKeys(api); !slices.Equal(got, want) || res.Stale != len(want) {
		t.Errorf("stale = %v (result %+v), want %v", got, res, want)
	}
	for _, key := range want {
		if a := api.byKey(t, key); !strings.Contains(a.Content, "docgen:begin stale-banner") {
			t.Errorf("%s has no stale banner:\n%s", key, a.Content)
		}
	}
}

func TestNamespaceReturnsBelowItsClusterAndLosesTheStaleMark(t *testing.T) {
	api := publishedAPI(t, clusterTree(twoClusters()))
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), clusterTree(map[string][]string{"prod": {"blog"}, "stage": {"shop"}})); err != nil {
		t.Fatal(err)
	}
	res, err := s.Publish(context.Background(), clusterTree(twoClusters()))
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored != 1 {
		t.Errorf("result = %+v, want one restored", res)
	}
	got := api.byKey(t, "kubernetes:namespace:prod/shop")
	if isStale(got) || strings.Contains(got.Content, "docgen:begin stale-banner") || !strings.Contains(got.Content, "docgen:begin summary") {
		t.Errorf("namespace not restored: tags=%v content=\n%s", got.Tags, got.Content)
	}
	if parent := api.parentKeyOf(t, "kubernetes:namespace:prod/shop"); parent != "kubernetes:cluster:prod" {
		t.Errorf("parent = %q", parent)
	}
}

func kubernetesFilter(t *testing.T, deny ...filter.Match) *filter.Filter {
	t.Helper()
	f, err := filter.New(filter.Config{Collectors: map[string]filter.Rules{"kubernetes": {Deny: deny}}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func kubernetesAllowFilter(t *testing.T, allow ...filter.Match) *filter.Filter {
	t.Helper()
	f, err := filter.New(filter.Config{Collectors: map[string]filter.Rules{"kubernetes": {Allow: allow}}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// decideWith is the DenyCheck the CLI builds from a filter.
func decideWith(f *filter.Filter) func(string) (bool, bool) {
	return func(id string) (bool, bool) {
		switch f.DecideID(model.ID(id)) {
		case filter.Denied:
			return true, true
		case filter.Permitted:
			return false, true
		}
		return false, false
	}
}

func staleKeys(api *fakeAPI) []string {
	var out []string
	for _, a := range api.articles {
		if k, _ := documentKey(a.Content); hasStaleTag(a) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// A namespace that a deny rule removes between runs is cleaned in every
// cluster; the cluster articles stay.
func TestDeniedNamespaceIsCleanedInEveryCluster(t *testing.T) {
	denyShop := filter.Match{Namespaces: []string{"shop"}}
	tests := []struct {
		name string
		mod  func(*Config)
	}{
		{"collected source, forced by ID", func(c *Config) {
			c.ActiveSources = []string{"kubernetes"}
			c.ForceStaleIDs = []string{"kubernetes:namespace:prod/shop", "kubernetes:namespace:stage/shop"}
		}},
		{"uncollected namespace of an incomplete source", func(c *Config) {
			c.ActiveSources = []string{"netbox"}
			c.IncompleteSources = []string{"kubernetes"}
			c.DenyCheck = decideWith(kubernetesFilter(t, denyShop))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := publishedAPI(t, clusterTree(twoClusters()))
			after := map[string][]string{"prod": {"blog"}, "stage": nil}
			res, err := newSinkWith(t, api, tt.mod).Publish(context.Background(), clusterTree(after))
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"kubernetes:namespace:prod/shop", "kubernetes:namespace:stage/shop"}
			if got := staleKeys(api); !slices.Equal(got, want) || res.Stale != 2 {
				t.Errorf("stale = %v (result %+v), want %v", got, res, want)
			}
			for _, key := range want {
				a := api.byKey(t, key)
				if !isStale(a) || strings.Contains(a.Content, "docgen:begin summary") {
					t.Errorf("%s not cleaned and marked: tags=%v content=\n%s", key, a.Tags, a.Content)
				}
			}
		})
	}
}

// With an incomplete source, the rules decide each unpublished article by ID:
// a permitted one stays untouched, a denied one is cleaned, and an old-format
// ID that the rules cannot decide blocks the run before the first write.
func TestDenyCheckOfKubernetesIDsOfAnIncompleteSource(t *testing.T) {
	tests := []struct {
		name      string
		filter    *filter.Filter
		oldFormat bool
		wantStale []string
		wantErr   error
		// after is the tree of this run; nil means both shop articles are
		// unpublished and both clusters are regenerated.
		after map[string][]string
	}{
		{"nothing denied", kubernetesFilter(t, filter.Match{Namespaces: []string{"other"}}), false, nil, nil, nil},
		{"denied cluster-prefixed namespace", kubernetesFilter(t, filter.Match{Namespaces: []string{"shop"}}), false,
			[]string{"kubernetes:namespace:prod/shop", "kubernetes:namespace:stage/shop"}, nil, nil},
		// The old format names no cluster, so a cluster-wide effect of any
		// namespace rule cannot be ruled out: such an ID blocks, it is neither
		// cleaned nor silently kept.
		{"old-format ID under a namespace deny", kubernetesFilter(t, filter.Match{Namespaces: []string{"shop"}}), true, nil, ErrDenyCleanup, nil},
		{"old-format ID under a rule that needs labels", kubernetesFilter(t, filter.Match{Labels: map[string]string{"team": "a"}}), true, nil, ErrDenyCleanup, nil},
		{"old-format ID without any deny rule", kubernetesFilter(t), true, nil, nil, nil},
		// The cluster article of stage is not regenerated. It lists its
		// namespaces by name, so a rule that may hide a namespace blocks.
		{"unregenerated cluster under a namespace deny", kubernetesFilter(t, filter.Match{Namespaces: []string{"quarantine"}}), false, nil, ErrDenyCleanup,
			map[string][]string{"prod": {"blog", "shop"}}},
		{"unregenerated cluster under a label deny", kubernetesFilter(t, filter.Match{Labels: map[string]string{"label.team": "a"}}), false, nil, ErrDenyCleanup,
			map[string][]string{"prod": {"blog", "shop"}}},
		{"unregenerated cluster under a member kind deny", kubernetesFilter(t, filter.Match{Kinds: []string{"service"}}), false, nil, nil,
			map[string][]string{"prod": {"blog", "shop"}}},
		{"unregenerated cluster under an allow list that may leave out a namespace",
			kubernetesAllowFilter(t, filter.Match{Kinds: []string{"cluster"}}, filter.Match{Namespaces: []string{"public"}}), false, nil, ErrDenyCleanup,
			map[string][]string{"prod": {"blog", "shop"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := publishedAPI(t, clusterTree(twoClusters()))
			if tt.oldFormat {
				a := api.byKey(t, "kubernetes:namespace:prod/shop")
				a.Content = strings.Replace(a.Content, "kubernetes:namespace:prod/shop", "kubernetes:namespace:shop", 1)
			}
			before := len(api.writeCalls())
			mod := func(c *Config) {
				c.ActiveSources = []string{"netbox"}
				c.IncompleteSources = []string{"kubernetes"}
				c.DenyCheck = decideWith(tt.filter)
			}
			// Both shop articles are unpublished: the run collected nothing of them.
			after := map[string][]string{"prod": {"blog"}, "stage": nil}
			if tt.after != nil {
				after = tt.after
			}
			_, err := newSinkWith(t, api, mod).Publish(context.Background(), clusterTree(after))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if got := api.writeCalls(); len(got) != before {
					t.Errorf("writes before the abort: %v", got[before:])
				}
				return
			}
			if got := staleKeys(api); !slices.Equal(got, tt.wantStale) {
				t.Errorf("stale = %v, want %v", got, tt.wantStale)
			}
			if len(tt.wantStale) == 0 {
				if got := api.writeCalls(); len(got) != before {
					t.Errorf("new write calls although nothing is denied: %v", got[before:])
				}
			}
		})
	}
}

// Keys with a slash, dots and hyphens are their own document keys and survive
// the escape round trip, so the rules see the real ID.
func TestKubernetesIDsAreTheirOwnKeys(t *testing.T) {
	for _, id := range []string{
		"kubernetes:cluster:prod-eu.example",
		"kubernetes:namespace:prod-eu.example/shop-front",
		"kubernetes:workload:prod/shop/deployment/web-1.v2",
	} {
		if got := docKey(id); got != id {
			t.Errorf("docKey(%q) = %q, want the ID itself", id, got)
		}
		if got, ok := idFromKey(docKey(id)); !ok || got != id {
			t.Errorf("idFromKey(docKey(%q)) = %q, %v", id, got, ok)
		}
	}
	// A double hyphen is escaped and still round-trips.
	id := "kubernetes:namespace:prod/a--b"
	if got, ok := idFromKey(docKey(id)); !ok || got != id || docKey(id) == id {
		t.Errorf("docKey(%q) = %q, round trip = %q, %v", id, docKey(id), got, ok)
	}
}

func TestGroupForRoutesKubernetesIDs(t *testing.T) {
	tests := []struct{ id, key string }{
		{"kubernetes:cluster:prod", kubernetesKey},
		{"kubernetes:namespace:prod/shop", kubernetesKey},
		{"kubernetes:workload:prod/shop/deployment/web", kubernetesKey},
		{"kubernetes:ingress:prod/shop/web", kubernetesKey},
		{"kubernetes:namespace:shop", kubernetesKey}, // old format
		{"netbox:host:web-1", hostsKey},
		{"overview:vlans", ""},
	}
	for _, tt := range tests {
		if got, _ := groupFor(tt.id); got != tt.key {
			t.Errorf("groupFor(%q) = %q, want %q", tt.id, got, tt.key)
		}
	}
}

// A namespace document placed directly below the root (the renderer never does
// this) is still grouped under Kubernetes.
func TestNamespaceAsRootChildStaysUnderTheKubernetesGroup(t *testing.T) {
	root := clusterTree(nil)
	root.Children = append(root.Children, render.Document{
		ID: "kubernetes:namespace:prod/shop", Title: "shop", Sections: []render.Section{section("summary", "x")},
	})
	api := &fakeAPI{}
	if _, err := newSink(t, api).Publish(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if got := api.parentKeyOf(t, "kubernetes:namespace:prod/shop"); got != kubernetesKey {
		t.Errorf("parent = %q, want %q", got, kubernetesKey)
	}
}
