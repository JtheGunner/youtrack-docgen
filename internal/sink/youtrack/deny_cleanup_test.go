package youtrack

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const fakeLeak = "fake-leaked-address-198.51.100.7"

// Only the k8s source ran; the NetBox articles are out of scope.
func k8sOnly(c *Config) { c.ActiveSources = []string{"k8s"} }

// An article with an interrupted generated block cannot be emptied safely: the
// text after the begin marker may be generated. The run must fail before any
// write, and the dry run must say so.
func TestForcedCleanupOfUnterminatedBlockFailsBeforeAnyWrite(t *testing.T) {
	api := publishedAPI(t, hostTree(map[string]string{"old": "a"}))
	article := api.byKey(t, "netbox:host:old")
	article.Content = docMarker("netbox:host:old") + "\n\n<!-- docgen:begin summary hash=abc -->\n" + fakeLeak + "\n"
	before := len(api.writeCalls())
	force := func(c *Config) { k8sOnly(c); c.ForceStaleIDs = []string{"netbox:host:old"} }

	res, err := newSinkWith(t, api, force).Publish(context.Background(), hostTree(nil))
	if !errors.Is(err, ErrDenyCleanup) {
		t.Fatalf("err = %v, want ErrDenyCleanup", err)
	}
	if got := api.writeCalls(); len(got) != before {
		t.Errorf("writes before the abort: %v", got[before:])
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "begin summary without end") }) {
		t.Errorf("warnings = %v, want the stray marker warning", res.Warnings)
	}
	if res.Stale != 0 {
		t.Errorf("Stale = %d, want 0: nothing may be reported as cleaned", res.Stale)
	}

	plan, err := newPlannerWith(t, api, force).Plan(context.Background(), hostTree(nil))
	if err != nil {
		t.Fatalf("Plan error = %v, a dry run only warns", err)
	}
	if !slices.ContainsFunc(plan.Warnings, func(w string) bool { return strings.Contains(w, "publish would fail") }) {
		t.Errorf("plan warnings = %v, want a note that publish would fail", plan.Warnings)
	}
}

// Denied objects are an intended cleanup: they must not count against the
// mass-stale limit, whatever share of the articles they are.
func TestForcedCleanupsAreNotCountedByTheMassStaleGuard(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	s := newSinkWith(t, api, func(c *Config) {
		k8sOnly(c)
		c.ForceStaleIDs = []string{"netbox:host:web-1", "netbox:host:web-2", "netbox:host:web-3", "netbox:host:web-4"}
	})

	res, err := s.Publish(context.Background(), hostTree(nil))
	if err != nil {
		t.Fatalf("err = %v, want the denied cleanup to pass the guard", err)
	}
	if res.Stale != 4 {
		t.Errorf("Stale = %d, want 4", res.Stale)
	}
}

// The guard still limits absence-based staling when forced cleanups run too.
func TestGuardStillLimitsAbsenceBasedStalingNextToForcedCleanups(t *testing.T) {
	api := publishedAPI(t, hostTree(fourHosts()))
	s := newSinkWith(t, api, func(c *Config) {
		c.ForceStaleIDs = []string{"netbox:host:web-1"}
		c.MaxStaleRatio = 0.2
	})

	// Every other host vanished from an active source.
	if _, err := s.Publish(context.Background(), hostTree(nil)); !errors.Is(err, ErrMassStale) {
		t.Fatalf("err = %v, want ErrMassStale for the absent hosts", err)
	}
}

func TestDenyCheckDecidesArticlesOfInactiveSources(t *testing.T) {
	tests := []struct {
		name      string
		check     func(string) (bool, bool)
		wantStale int
		wantErr   error
	}{
		{"denied is cleaned", func(string) (bool, bool) { return true, true }, 1, nil},
		{"permitted is left alone", func(string) (bool, bool) { return false, true }, 0, nil},
		{"undecidable fails before writing", func(string) (bool, bool) { return false, false }, 0, ErrDenyCleanup},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := publishedAPI(t, hostTree(map[string]string{"old": "a"}))
			before := len(api.writeCalls())
			s := newSinkWith(t, api, func(c *Config) { k8sOnly(c); c.DenyCheck = tt.check })

			res, err := s.Publish(context.Background(), hostTree(nil))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if res.Stale != tt.wantStale {
				t.Errorf("Stale = %d, want %d", res.Stale, tt.wantStale)
			}
			if tt.wantErr != nil && len(api.writeCalls()) != before {
				t.Errorf("writes before the abort: %v", api.writeCalls()[before:])
			}
			if tt.wantStale == 1 && !hasStaleTag(*api.byKey(t, "netbox:host:old")) {
				t.Error("the denied article has no stale tag")
			}
		})
	}
}

// An article that is clean and tagged already needs no decision.
func TestDenyCheckSkipsArticlesThatAreAlreadyStale(t *testing.T) {
	api := publishedAPI(t, hostTree(map[string]string{"old": "a"}))
	deny := func(c *Config) { c.ActiveSources = nil; c.ForceStaleIDs = []string{"netbox:host:old"} }
	if _, err := newSinkWith(t, api, deny).Publish(context.Background(), hostTree(nil)); err != nil {
		t.Fatal(err)
	}
	before := len(api.writeCalls())
	s := newSinkWith(t, api, func(c *Config) {
		k8sOnly(c)
		c.DenyCheck = func(string) (bool, bool) { return false, false }
	})
	if _, err := s.Publish(context.Background(), hostTree(nil)); err != nil {
		t.Fatalf("err = %v, want no decision for an already stale article", err)
	}
	if got := api.writeCalls(); len(got) != before {
		t.Errorf("unexpected writes: %v", got[before:])
	}
}
