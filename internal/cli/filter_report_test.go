package cli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The run prints per-collector counts of what allow and deny removed, a warning
// when allow rules leave nothing, and never names a removed entity.
func TestGeneratePrintsFilterCountsWithoutNames(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeTaggedLinkNetBox(t)
	service := writeKeptService(t)

	tests := []struct {
		name       string
		netboxRule string
		wantLine   *regexp.Regexp
		wantWarn   bool
	}{
		{
			name:       "deny removes hosts",
			netboxRule: "deny:\n      - tags: [lab]",
			wantLine:   regexp.MustCompile(`(?m)^filter netbox: kept \d+, removed 0 by allow, [1-9]\d* by deny$`),
		},
		{
			name:       "allow that matches nothing warns",
			netboxRule: "allow:\n      - tags: [no-such-tag]",
			wantLine:   regexp.MustCompile(`(?m)^filter netbox: kept 0, removed [1-9]\d* by allow, 0 by deny$`),
			wantWarn:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			cfg := fmt.Sprintf(`
collectors:
  netbox:
    url: %s
    token: ${NETBOX_TOKEN}
    %s
  kubernetes:
    paths: [%s]
sinks:
  markdown:
    dir: %s
`, srv.URL, tc.netboxRule, service, out)

			stdout, stderr, err := runGenerate(t, cfg)
			if err != nil {
				t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
			}
			if !tc.wantLine.MatchString(stderr) {
				t.Errorf("stderr lacks the netbox counts %s:\n%s", tc.wantLine, stderr)
			}
			if !regexp.MustCompile(`(?m)^filter kubernetes: kept \d+, removed 0 by allow, 0 by deny$`).MatchString(stderr) {
				t.Errorf("stderr lacks the kubernetes counts:\n%s", stderr)
			}
			if got := strings.Contains(stderr, "warning: collector netbox: no entity passed the allow rules"); got != tc.wantWarn {
				t.Errorf("allow warning present = %v, want %v:\n%s", got, tc.wantWarn, stderr)
			}
			for _, denied := range []string{"node-a.example.com", "192.0.2.21", "192.0.2.30"} {
				if strings.Contains(strings.ToLower(stdout+stderr), denied) {
					t.Errorf("output names the removed value %q:\n%s%s", denied, stdout, stderr)
				}
			}
		})
	}
}
