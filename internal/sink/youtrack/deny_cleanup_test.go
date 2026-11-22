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
			s := newSinkWith(t, api, func(c *Config) { k8sOnly(c); c.IncompleteSources = []string{"netbox"}; c.DenyCheck = tt.check })

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
		c.IncompleteSources = []string{"netbox"}
		c.DenyCheck = func(string) (bool, bool) { return false, false }
	})
	if _, err := s.Publish(context.Background(), hostTree(nil)); err != nil {
		t.Fatalf("err = %v, want no decision for an already stale article", err)
	}
	if got := api.writeCalls(); len(got) != before {
		t.Errorf("unexpected writes: %v", got[before:])
	}
}

func incompleteNetBox(c *Config) { k8sOnly(c); c.IncompleteSources = []string{"netbox"} }

// A switched-off source is not an incomplete one: its articles are left alone
// whatever the rules look like.
func TestDenyCheckIgnoresSourcesThatAreSwitchedOff(t *testing.T) {
	api := publishedAPI(t, hostTree(map[string]string{"old": "a"}))
	before := len(api.writeCalls())
	asked := false
	s := newSinkWith(t, api, func(c *Config) {
		k8sOnly(c)
		c.DenyCheck = func(string) (bool, bool) { asked = true; return false, false }
	})
	if _, err := s.Publish(context.Background(), hostTree(nil)); err != nil {
		t.Fatalf("err = %v, want the switched-off source untouched", err)
	}
	if asked || len(api.writeCalls()) != before {
		t.Errorf("asked = %v, writes = %v", asked, api.writeCalls()[before:])
	}
}

func TestIDFromKeyInvertsDocKey(t *testing.T) {
	for _, id := range []string{"netbox:host:web-1", "netbox:host:a--b", "netbox:host:a b", "netbox:host:100%", "esc:netbox:host:x", "kubernetes:workload:ns--1/deployment/web"} {
		got, ok := idFromKey(docKey(id))
		if !ok || got != id {
			t.Errorf("idFromKey(docKey(%q)) = %q, %v", id, got, ok)
		}
	}
	for _, key := range []string{"esc:netbox:host:a%ZZ", "esc:netbox:host:a--b", "esc:netbox:host:a%41"} {
		if got, ok := idFromKey(key); ok {
			t.Errorf("idFromKey(%q) = %q, want failure", key, got)
		}
	}
}

// The rules must see the real ID, not the encoded key.
func TestDenyCheckReceivesTheDecodedIDOfEscapedKeys(t *testing.T) {
	for _, verdict := range []struct {
		name            string
		denied, decided bool
		wantStale       int
	}{{"deny", true, true, 1}, {"allow", false, true, 0}} {
		t.Run(verdict.name, func(t *testing.T) {
			api := publishedAPI(t, hostTree(map[string]string{"a--b": "x"}))
			var got []string
			s := newSinkWith(t, api, func(c *Config) {
				incompleteNetBox(c)
				c.DenyCheck = func(id string) (bool, bool) { got = append(got, id); return verdict.denied, verdict.decided }
			})
			res, err := s.Publish(context.Background(), hostTree(nil))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, []string{"netbox:host:a--b"}) {
				t.Errorf("DenyCheck saw %v, want the decoded ID", got)
			}
			if res.Stale != verdict.wantStale {
				t.Errorf("Stale = %d, want %d", res.Stale, verdict.wantStale)
			}
		})
	}
}

func TestUndecodableKeyOfAnIncompleteSourceBlocks(t *testing.T) {
	api := publishedAPI(t, hostTree(map[string]string{"old": "a"}))
	api.byKey(t, "netbox:host:old").Content = docMarker("esc:netbox:host:a%ZZ") + "\n"
	s := newSinkWith(t, api, func(c *Config) {
		incompleteNetBox(c)
		c.DenyCheck = func(string) (bool, bool) { return false, true }
	})
	if _, err := s.Publish(context.Background(), hostTree(nil)); !errors.Is(err, ErrDenyCleanup) {
		t.Fatalf("err = %v, want ErrDenyCleanup", err)
	}
}

// A key that is no entity ID cannot match a rule: it warns and does not block.
func TestKeyThatIsNoEntityIDWarnsWithoutBlocking(t *testing.T) {
	api := publishedAPI(t, hostTree(map[string]string{"a:b": "x"}))
	before := len(api.writeCalls())
	s := newSinkWith(t, api, func(c *Config) {
		incompleteNetBox(c)
		c.DenyCheck = func(string) (bool, bool) { return false, false }
	})
	res, err := s.Publish(context.Background(), hostTree(nil))
	if err != nil {
		t.Fatalf("err = %v, want no block", err)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "not an entity ID") }) {
		t.Errorf("warnings = %v, want a note", res.Warnings)
	}
	if len(api.writeCalls()) != before {
		t.Errorf("unexpected writes: %v", api.writeCalls()[before:])
	}
}

// An article that looks clean but still holds an interrupted block is not
// clean: it needs a decision like any other.
func TestUnterminatedBlockKeepsAStaleArticleFromCountingAsClean(t *testing.T) {
	api := publishedAPI(t, hostTree(map[string]string{"old": "a"}))
	deny := func(c *Config) { c.ForceStaleIDs = []string{"netbox:host:old"} }
	if _, err := newSinkWith(t, api, deny).Publish(context.Background(), hostTree(nil)); err != nil {
		t.Fatal(err)
	}
	article := api.byKey(t, "netbox:host:old")
	article.Content += "\n<!-- docgen:begin summary hash=abc -->\n" + fakeLeak + "\n"
	s := newSinkWith(t, api, func(c *Config) {
		incompleteNetBox(c)
		c.DenyCheck = func(string) (bool, bool) { return false, false }
	})
	if _, err := s.Publish(context.Background(), hostTree(nil)); !errors.Is(err, ErrDenyCleanup) {
		t.Fatalf("err = %v, want ErrDenyCleanup", err)
	}
}

// A denied article found by ID passes the same unterminated-block gate.
func TestDeniedByIDWithUnterminatedBlockBlocks(t *testing.T) {
	api := publishedAPI(t, hostTree(map[string]string{"old": "a"}))
	api.byKey(t, "netbox:host:old").Content = docMarker("netbox:host:old") + "\n<!-- docgen:begin summary hash=abc -->\n" + fakeLeak + "\n"
	before := len(api.writeCalls())
	s := newSinkWith(t, api, func(c *Config) {
		incompleteNetBox(c)
		c.DenyCheck = func(string) (bool, bool) { return true, true }
	})
	if _, err := s.Publish(context.Background(), hostTree(nil)); !errors.Is(err, ErrDenyCleanup) {
		t.Fatalf("err = %v, want ErrDenyCleanup", err)
	}
	if len(api.writeCalls()) != before {
		t.Errorf("writes before the abort: %v", api.writeCalls()[before:])
	}
}
