package youtrack

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

// hostTree is a document tree with one host document per entry of hosts (name
// to summary text), the VLAN overview and one Kubernetes workload.
func hostTree(hosts map[string]string) render.Document {
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	children := []render.Document{
		{ID: "overview:vlans", Title: "VLANs", Sections: []render.Section{section("table", "vlans")}},
	}
	for _, name := range names {
		children = append(children, render.Document{
			ID: "netbox:host:" + name, Title: name + ".example.com",
			Sections: []render.Section{section("summary", hosts[name])},
		})
	}
	children = append(children, render.Document{
		ID: "k8s:workload:app", Title: "app", Sections: []render.Section{section("summary", "deployment")},
	})
	return render.Document{
		ID: "overview:root", Title: "Infrastructure documentation",
		Sections: []render.Section{section("contents", "index")},
		Children: children,
	}
}

func fourHosts() map[string]string {
	return map[string]string{"web-1": "a", "web-2": "a", "web-3": "a", "web-4": "a"}
}

func newSinkWith(t *testing.T, api API, mod func(*Config)) *Sink {
	t.Helper()
	cfg := Config{ProjectID: testProjectID}
	if mod != nil {
		mod(&cfg)
	}
	s, err := New(api, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newPlannerWith(t *testing.T, api Reader, mod func(*Config)) *Planner {
	t.Helper()
	cfg := Config{ProjectID: testProjectID}
	if mod != nil {
		mod(&cfg)
	}
	p, err := NewPlanner(api, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// publishedAPI returns a fake that already holds the articles of tree.
func publishedAPI(t *testing.T, tree render.Document) *fakeAPI {
	t.Helper()
	api := &fakeAPI{}
	if _, err := newSink(t, api).Publish(context.Background(), tree); err != nil {
		t.Fatal(err)
	}
	return api
}

func TestPublishRefusesToMarkMostOfTheKnowledgeBaseStale(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	before := api.writeCalls()

	res, err := newSink(t, api).Publish(context.Background(), hostTree(nil))
	if !errors.Is(err, ErrMassStale) {
		t.Fatalf("err = %v, want ErrMassStale", err)
	}
	if got := api.writeCalls(); len(got) != len(before) {
		t.Errorf("the run wrote before aborting: %v", got[len(before):])
	}
	if res.Stale != 0 || res.Created != 0 || res.Updated != 0 {
		t.Errorf("result = %+v, want nothing done", res)
	}
	for _, key := range []string{"netbox:host:web-1", "k8s:workload:app"} {
		if a := api.byKey(t, key); hasStaleTag(*a) {
			t.Errorf("%s was marked stale", key)
		}
	}
}

func TestMassStaleErrorNamesTheCountsAndTheOverride(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	_, err := newSink(t, api).Publish(context.Background(), hostTree(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"5 of 9", "allow-mass-stale"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestAllowMassStaleLetsTheRunProceed(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	s := newSinkWith(t, api, func(c *Config) { c.AllowMassStale = true })

	res, err := s.Publish(context.Background(), hostTree(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stale != 5 {
		t.Errorf("Stale = %d, want 5", res.Stale)
	}
	if a := api.byKey(t, "netbox:host:web-1"); !hasStaleTag(*a) {
		t.Error("web-1 was not marked stale")
	}
}

func TestStaleBelowTheThresholdProceeds(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	hosts := fourHosts()
	delete(hosts, "web-4")

	res, err := newSink(t, api).Publish(context.Background(), hostTree(hosts))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stale != 1 {
		t.Errorf("Stale = %d, want 1", res.Stale)
	}
}

func TestMaxStaleRatioIsConfigurable(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	hosts := fourHosts()
	delete(hosts, "web-4")
	s := newSinkWith(t, api, func(c *Config) { c.MaxStaleRatio = 0.05 })

	if _, err := s.Publish(context.Background(), hostTree(hosts)); !errors.Is(err, ErrMassStale) {
		t.Fatalf("err = %v, want ErrMassStale for 1 of 9 with a ratio of 0.05", err)
	}
}

func TestStaleOnlyAppliesToActiveSources(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	before := api.writeCalls()
	s := newSinkWith(t, api, func(c *Config) { c.ActiveSources = []string{"k8s"} })

	// The NetBox collector did not run: its hosts are absent, not vanished.
	res, err := s.Publish(context.Background(), hostTree(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stale != 0 {
		t.Errorf("Stale = %d, want 0", res.Stale)
	}
	for _, key := range []string{"netbox:host:web-1", "group:hosts"} {
		if a := api.byKey(t, key); hasStaleTag(*a) || strings.Contains(a.Content, staleBlockID) {
			t.Errorf("%s was touched although its source was not active", key)
		}
	}
	if got := api.writeCalls(); len(got) != len(before) {
		t.Errorf("unexpected writes: %v", got[len(before):])
	}
}

func TestStaleArticleLosesGeneratedBlocksButKeepsManualText(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	for i := range api.articles {
		if k, _ := documentKey(api.articles[i].Content); k == "netbox:host:web-4" {
			api.articles[i].Content += "\nMy own notes.\n"
		}
	}
	hosts := fourHosts()
	delete(hosts, "web-4")
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), hostTree(hosts)); err != nil {
		t.Fatal(err)
	}

	a := api.byKey(t, "netbox:host:web-4")
	if !hasStaleTag(*a) || !hasBlock(a.Content, staleBlockID) {
		t.Fatalf("web-4 is not marked stale:\n%s", a.Content)
	}
	if strings.Contains(a.Content, "docgen:begin summary") || strings.Contains(a.Content, "SUMMARY") {
		t.Errorf("generated sections are still in the stale article:\n%s", a.Content)
	}
	if !strings.Contains(a.Content, "My own notes.") {
		t.Errorf("manual text was lost:\n%s", a.Content)
	}

	// A second run on the same data is idempotent.
	before := api.writeCalls()
	res, err := s.Publish(context.Background(), hostTree(hosts))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stale != 0 || len(api.writeCalls()) != len(before) {
		t.Errorf("second run: result %+v, new writes %v", res, api.writeCalls()[len(before):])
	}

	// The object comes back: blocks return, the marking goes, notes stay.
	res, err = s.Publish(context.Background(), hostTree(fourHosts()))
	if err != nil {
		t.Fatal(err)
	}
	a = api.byKey(t, "netbox:host:web-4")
	if res.Restored != 1 || hasStaleTag(*a) || hasBlock(a.Content, staleBlockID) {
		t.Errorf("restore: result %+v, article:\n%s", res, a.Content)
	}
	if !strings.Contains(a.Content, "docgen:begin summary") || !strings.Contains(a.Content, "My own notes.") {
		t.Errorf("restored article lost content:\n%s", a.Content)
	}
}

// TestPlanPredictsExactlyThePublishWrites runs a change that creates, updates,
// marks stale and restores, and compares the write calls the planner names
// with the ones Publish makes.
func TestPlanPredictsExactlyThePublishWrites(t *testing.T) {
	ctx := context.Background()
	api := publishedAPI(t, hostTree(map[string]string{"web-1": "a", "web-2": "a", "web-3": "a", "web-4": "a"}))
	// web-4 vanishes in one run, so it carries the stale marking afterwards.
	if _, err := newSink(t, api).Publish(ctx, hostTree(map[string]string{"web-1": "a", "web-2": "a", "web-3": "a"})); err != nil {
		t.Fatal(err)
	}

	// web-4 returns, web-1 changes, web-2 vanishes, web-5 is new.
	next := hostTree(map[string]string{"web-1": "changed", "web-3": "a", "web-4": "a", "web-5": "a"})
	plan, err := newPlanner(t, api).Plan(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Created() != 1 || plan.Stale() != 1 || plan.Restored() != 1 {
		t.Fatalf("plan = %s", plan.Summary())
	}
	planned := plan.Writes()

	mark := len(api.writeCalls())
	res, err := newSink(t, api).Publish(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	got := api.writeCalls()[mark:]
	if strings.Join(planned, "\n") != strings.Join(got, "\n") {
		t.Errorf("planned writes:\n%s\nactual writes:\n%s", strings.Join(planned, "\n"), strings.Join(got, "\n"))
	}
	if plan.Created() != res.Created || plan.Updated() != res.Updated || plan.Unchanged() != res.Unchanged ||
		plan.Stale() != res.Stale || plan.Restored() != res.Restored {
		t.Errorf("plan = %s, result = %+v", plan.Summary(), res)
	}
}

func TestPlanShowsStaleAndRestoreChanges(t *testing.T) {
	ctx := context.Background()
	api := publishedAPI(t, hostTree(fourHosts()))
	hosts := fourHosts()
	delete(hosts, "web-4")
	if _, err := newSink(t, api).Publish(ctx, hostTree(hosts)); err != nil {
		t.Fatal(err)
	}

	stalePlan, err := newPlanner(t, api).Plan(ctx, hostTree(map[string]string{"web-1": "a", "web-2": "a"}))
	if err != nil {
		t.Fatal(err)
	}
	diff := stalePlan.Diff()
	for _, want := range []string{"netbox:host:web-3", "Tags: docgen-stale", "docgen:begin stale-banner"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff does not show %q:\n%s", want, diff)
		}
	}
	if stalePlan.Stale() != 1 {
		t.Errorf("plan = %s, want 1 to mark stale", stalePlan.Summary())
	}

	restorePlan, err := newPlanner(t, api).Plan(ctx, hostTree(fourHosts()))
	if err != nil {
		t.Fatal(err)
	}
	if restorePlan.Restored() != 1 || !strings.Contains(restorePlan.Summary(), "1 to restore") {
		t.Errorf("plan = %s, want 1 to restore", restorePlan.Summary())
	}
	if !strings.Contains(restorePlan.Diff(), "-Tags: docgen-stale") {
		t.Errorf("restore diff does not show the tag removal:\n%s", restorePlan.Diff())
	}
}

func TestPlanReportsTheMassStaleGuardAndStillReturnsThePlan(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	plan, err := newPlanner(t, api).Plan(context.Background(), hostTree(nil))
	if !errors.Is(err, ErrMassStale) {
		t.Fatalf("err = %v, want ErrMassStale", err)
	}
	if plan.Stale() != 5 {
		t.Errorf("plan = %s, want the 5 stale changes to be listed", plan.Summary())
	}

	plan, err = newPlannerWith(t, api, func(c *Config) { c.AllowMassStale = true }).Plan(context.Background(), hostTree(nil))
	if err != nil || plan.Stale() != 5 {
		t.Errorf("with the flag: plan = %s, err = %v", plan.Summary(), err)
	}
}

// A host that was denied after it had been published disappears from the
// tree like a vanished one. Its name and address must not stay in YouTrack.
func TestStaleArticleDoesNotKeepNamesAndAddressesOfADeniedHost(t *testing.T) {
	const fakeAddress, fakeSecret = "192.0.2.77", "fake-secret-value-for-test"
	withHost := hostTree(fourHosts())
	withHost.Children[1].Sections = []render.Section{section("summary", "denied-host "+fakeAddress+" "+fakeSecret)}
	api := publishedAPI(t, withHost)

	hosts := fourHosts()
	delete(hosts, "web-1")
	if _, err := newSink(t, api).Publish(context.Background(), hostTree(hosts)); err != nil {
		t.Fatal(err)
	}
	a := api.byKey(t, "netbox:host:web-1")
	if !hasStaleTag(*a) {
		t.Fatal("the host was not marked stale")
	}
	for _, leaked := range []string{fakeAddress, fakeSecret, "denied-host"} {
		if strings.Contains(a.Content, leaked) {
			t.Errorf("%q is still in the stale article:\n%s", leaked, a.Content)
		}
	}
}

// TestGuardCountsOnlyLiveArticles checks that articles already carrying tag
// and banner do not dilute the stale limit.
func TestGuardCountsOnlyLiveArticles(t *testing.T) {
	tests := []struct {
		name      string
		firstRun  map[string]string // hosts kept by the preparatory run, which may stale the rest
		secondRun map[string]string // hosts of the run under test
		ratio     float64
		wantErr   bool
	}{
		{"many already stale, few live", map[string]string{"web-1": "a"}, nil, 0.3, true},
		{"all live, below the limit", fourHosts(), map[string]string{"web-1": "a", "web-2": "a", "web-3": "a"}, 0.3, false},
		{"all live, above the limit", fourHosts(), nil, 0.3, true},
		{"empty live set, nothing new goes stale", nil, nil, 0.3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := publishedAPI(t, hostTree(fourHosts()))
			prepare := newSinkWith(t, api, func(c *Config) { c.AllowMassStale = true })
			if _, err := prepare.Publish(context.Background(), hostTree(tt.firstRun)); err != nil {
				t.Fatal(err)
			}
			s := newSinkWith(t, api, func(c *Config) { c.MaxStaleRatio = tt.ratio })
			_, err := s.Publish(context.Background(), hostTree(tt.secondRun))
			if got := errors.Is(err, ErrMassStale); got != tt.wantErr {
				t.Fatalf("err = %v, want ErrMassStale = %v", err, tt.wantErr)
			}
		})
	}
}

func TestGuardAbortsWhenNoLiveArticleIsLeft(t *testing.T) {
	stale := client.Article{
		Tags:    []client.TagRef{{ID: "5-1", Name: StaleTag}},
		Content: staleContent("<!-- docgen:doc netbox:host:web-1 -->"),
	}
	b := &opBuilder{
		existing: map[string]client.Article{"netbox:host:web-1": stale, "netbox:host:web-2": stale},
		ops:      []op{{kind: opMarkStale, newlyStale: true}},
	}
	err := b.guard(Config{})
	if !errors.Is(err, ErrMassStale) {
		t.Fatalf("err = %v, want ErrMassStale for an empty live set", err)
	}
	if err := b.guard(Config{AllowMassStale: true}); err != nil {
		t.Fatalf("err = %v, want nil with AllowMassStale", err)
	}
}
