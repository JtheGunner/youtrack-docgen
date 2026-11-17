package youtrack

import (
	"context"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrackt-docgen/internal/testutil/golden"
)

func newPlanner(t *testing.T, api Reader) *Planner {
	t.Helper()
	p, err := NewPlanner(api, "project: KB")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanOnEmptyProjectCreatesEverything(t *testing.T) {
	api := &fakeAPI{}
	plan, err := newPlanner(t, api).Plan(context.Background(), tree("web-1.example.com", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Summary(); got != "6 to create, 0 to update, 0 unchanged" {
		t.Errorf("summary = %q", got)
	}
	golden.Assert(t, "plan-create", []byte(plan.Diff()))
}

func TestPlanMatchesPublish(t *testing.T) {
	api := &fakeAPI{}
	s := newSink(t, api)
	if _, err := s.Publish(context.Background(), tree("web-1.example.com", "a")); err != nil {
		t.Fatal(err)
	}
	writes := api.creates + api.updates

	plan, err := newPlanner(t, api).Plan(context.Background(), tree("web-1.example.com", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if api.creates+api.updates != writes {
		t.Fatal("planning wrote to the API")
	}
	golden.Assert(t, "plan-update", []byte(plan.Diff()))

	res, err := s.Publish(context.Background(), tree("web-1.example.com", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Created() != res.Created || plan.Updated() != res.Updated || plan.Unchanged() != res.Unchanged {
		t.Errorf("plan = %s, publish result = %+v", plan.Summary(), res)
	}
}

func TestPlanOnUnchangedDataIsEmpty(t *testing.T) {
	api := &fakeAPI{}
	if _, err := newSink(t, api).Publish(context.Background(), tree("web-1.example.com", "a")); err != nil {
		t.Fatal(err)
	}
	plan, err := newPlanner(t, api).Plan(context.Background(), tree("web-1.example.com", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Diff() != "" || plan.Summary() != "0 to create, 0 to update, 6 unchanged" {
		t.Errorf("diff = %q, summary = %q", plan.Diff(), plan.Summary())
	}
}

func TestPlanKeepsManualContentOutOfTheDiff(t *testing.T) {
	api := &fakeAPI{}
	if _, err := newSink(t, api).Publish(context.Background(), tree("web-1.example.com", "a")); err != nil {
		t.Fatal(err)
	}
	host := api.byKey(t, "netbox:host:web-1")
	host.Content += "\nManual note by an engineer.\n"

	plan, err := newPlanner(t, api).Plan(context.Background(), tree("web-1.example.com", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Diff(), "-Manual note") || strings.Contains(plan.Diff(), "+Manual note") {
		t.Errorf("manual text must stay untouched:\n%s", plan.Diff())
	}
}

func TestPlanReportsTitleChange(t *testing.T) {
	api := &fakeAPI{}
	if _, err := newSink(t, api).Publish(context.Background(), tree("web-1.example.com", "a")); err != nil {
		t.Fatal(err)
	}
	plan, err := newPlanner(t, api).Plan(context.Background(), tree("web-1-renamed.example.com", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Diff(), "-Title: web-1.example.com") || !strings.Contains(plan.Diff(), "+Title: web-1-renamed.example.com") {
		t.Errorf("title change missing:\n%s", plan.Diff())
	}
}

func TestPlanPropagatesReadErrors(t *testing.T) {
	api := &fakeAPI{searchErr: context.DeadlineExceeded}
	if _, err := newPlanner(t, api).Plan(context.Background(), tree("h", "a")); err == nil {
		t.Fatal("want an error")
	}
}

func TestNewPlannerValidates(t *testing.T) {
	if _, err := NewPlanner(nil, "project: KB"); err == nil {
		t.Error("nil reader accepted")
	}
	if _, err := NewPlanner(&fakeAPI{}, ""); err == nil {
		t.Error("empty query accepted")
	}
}
