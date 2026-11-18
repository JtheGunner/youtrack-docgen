package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

const validYAML = `
youtrack:
  url: https://youtrack.example.com
  token: ${YOUTRACK_TOKEN}
  project: INFRA
collectors:
  netbox:
    url: https://netbox.example.com
    token: ${NETBOX_TOKEN}
    allow:
      - tags: [documented]
    deny:
      - roles: [lab, test]
      - kinds: [host]
        tenants: [lab]
  kubernetes:
    paths: [./kubernetes-config, ./gitops]
sinks:
  youtrack:
    root_article: Infrastructure
  markdown:
    dir: ./out
`

func envMap(m map[string]string) LookupFunc {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

var fullEnv = map[string]string{
	"YOUTRACK_TOKEN": "fake-youtrack-token",
	"NETBOX_TOKEN":   "fake-netbox-token",
}

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(validYAML), envMap(fullEnv))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.YouTrack.URL != "https://youtrack.example.com" || cfg.YouTrack.Project != "INFRA" {
		t.Errorf("youtrack = %+v", cfg.YouTrack)
	}
	if got := cfg.YouTrack.Token.Reveal(); got != "fake-youtrack-token" {
		t.Errorf("youtrack token = %q", got)
	}
	nb, ok := cfg.Collectors["netbox"]
	if !ok {
		t.Fatal("collector netbox missing")
	}
	if nb.URL != "https://netbox.example.com" || nb.Token.Reveal() != "fake-netbox-token" {
		t.Errorf("netbox = %+v", nb)
	}
	if len(nb.Allow) != 1 || len(nb.Allow[0].Tags) != 1 || nb.Allow[0].Tags[0] != "documented" {
		t.Errorf("netbox allow = %v", nb.Allow)
	}
	if len(nb.Deny) != 2 || len(nb.Deny[0].Roles) != 2 || nb.Deny[1].Tenants[0] != "lab" {
		t.Errorf("netbox deny = %v", nb.Deny)
	}
	if !nb.IsEnabled() {
		t.Error("netbox should default to enabled")
	}
	if got := cfg.Sinks["youtrack"].Settings["root_article"]; got != "Infrastructure" {
		t.Errorf("sink youtrack root_article = %v", got)
	}
	if got := cfg.Sinks["markdown"].Settings["dir"]; got != "./out" {
		t.Errorf("sink markdown dir = %v", got)
	}
}

func TestParseDoesNotLeakSecretsWhenFormatted(t *testing.T) {
	cfg, err := Parse([]byte(validYAML), envMap(fullEnv))
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg)} {
		for _, secret := range fullEnv {
			if strings.Contains(out, secret) {
				t.Errorf("formatted config leaks %q", secret)
			}
		}
	}
}

func TestConfigFilterAppliesRulesAndRedaction(t *testing.T) {
	yamlText := validYAML + "redact:\n  deny_patterns: ['CORP-[A-Z]+-[0-9]+']\n"
	cfg, err := Parse([]byte(yamlText), envMap(fullEnv))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	f, err := cfg.Filter()
	if err != nil {
		t.Fatalf("Filter() error = %v", err)
	}

	g := model.NewGraph()
	for _, e := range []model.Entity{
		{ID: "netbox:host:web01", Kind: model.KindHost, Name: "web01", Source: "netbox",
			Labels: map[string]string{model.LabelTagPrefix + "documented": "true"}, Attrs: map[string]string{"note": "see CORP-FAKE-42"}},
		{ID: "netbox:host:lab01", Kind: model.KindHost, Name: "lab01", Source: "netbox",
			Labels: map[string]string{model.LabelTagPrefix + "documented": "true", "role": "lab"}},
		{ID: "netbox:host:untagged", Kind: model.KindHost, Name: "untagged", Source: "netbox"},
	} {
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	hosts := out.ByKind(model.KindHost)
	if len(hosts) != 1 || hosts[0].Name != "web01" {
		t.Fatalf("hosts = %+v, want only web01", hosts)
	}
	if got := hosts[0].Attrs["note"]; strings.Contains(got, "CORP-FAKE-42") {
		t.Errorf("configured deny pattern not applied: %q", got)
	}
}

func TestParseDisabledModules(t *testing.T) {
	const yamlText = `
collectors:
  netbox:
    enabled: false
  kubernetes:
    paths: [./k8s]
sinks:
  markdown:
    dir: ./out
`
	cfg, err := Parse([]byte(yamlText), envMap(nil))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Collectors["netbox"].IsEnabled() {
		t.Error("netbox should be disabled")
	}
	if got := cfg.EnabledCollectors(); len(got) != 1 || got[0] != "kubernetes" {
		t.Errorf("EnabledCollectors() = %v", got)
	}
	if got := cfg.EnabledSinks(); len(got) != 1 || got[0] != "markdown" {
		t.Errorf("EnabledSinks() = %v", got)
	}
}

func TestParseEnvDefaultsAndEscapes(t *testing.T) {
	const yamlText = `
collectors:
  my-overlay:
    note: cost $$5
sinks:
  markdown:
    dir: ${OUT_DIR:-./out}
`
	cfg, err := Parse([]byte(yamlText), envMap(nil))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := cfg.Sinks["markdown"].Settings["dir"]; got != "./out" {
		t.Errorf("dir = %v", got)
	}
	if got := cfg.Collectors["my-overlay"].Settings["note"]; got != "cost $5" {
		t.Errorf("note = %v", got)
	}
}

func TestParseExpandedValueIsTakenLiterally(t *testing.T) {
	const yamlText = `
collectors:
  my-overlay:
    value: ${SPECIAL}
sinks:
  markdown:
    dir: ./out
`
	// Characters that are significant in YAML must not change the structure.
	const special = "a: b # c, [d] {e} - f"
	cfg, err := Parse([]byte(yamlText), envMap(map[string]string{"SPECIAL": special}))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := cfg.Collectors["my-overlay"].Settings["value"]; got != special {
		t.Errorf("value = %q, want %q", got, special)
	}
	if len(cfg.Collectors["my-overlay"].Settings) != 1 {
		t.Errorf("settings = %v, the value was split into several keys", cfg.Collectors["my-overlay"].Settings)
	}
}

func TestParseExpandsBooleansAndIgnoresComments(t *testing.T) {
	const yamlText = `
collectors:
  kubernetes:
    enabled: ${K8S_ENABLED:-true}
    paths: [./k8s]
sinks:
  markdown:
    dir: ./out # ${NOT_SET_BUT_COMMENTED}
`
	cfg, err := Parse([]byte(yamlText), envMap(nil))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !cfg.Collectors["kubernetes"].IsEnabled() {
		t.Error("kubernetes should be enabled")
	}
}

func TestParseOverlayModuleIsNotKeyChecked(t *testing.T) {
	const yamlText = `
collectors:
  my-overlay:
    whatever: 1
    nested: { a: b }
sinks:
  markdown:
    dir: ./out
`
	if _, err := Parse([]byte(yamlText), envMap(nil)); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		env  map[string]string
		want []string // every entry must appear in the error text
	}{
		{name: "empty file", yaml: "", want: []string{"config is empty"}},
		{name: "yaml syntax error", yaml: "collectors: [unclosed", want: []string{"cannot parse"}},
		{
			name: "unknown top-level key",
			yaml: "colectors: {}\n" + validYAML, env: fullEnv,
			want: []string{"colectors"},
		},
		{
			name: "max stale ratio above one",
			yaml: strings.Replace(validYAML, "project: INFRA", "project: INFRA\n  max_stale_ratio: 1.5", 1), env: fullEnv,
			want: []string{"youtrack.max_stale_ratio: must be between 0 and 1"},
		},
		{
			name: "max stale ratio below zero",
			yaml: strings.Replace(validYAML, "project: INFRA", "project: INFRA\n  max_stale_ratio: -0.1", 1), env: fullEnv,
			want: []string{"youtrack.max_stale_ratio: must be between 0 and 1"},
		},
		{
			name: "unknown key in youtrack",
			yaml: strings.Replace(validYAML, "project: INFRA", "project: INFRA\n  projct: X", 1), env: fullEnv,
			want: []string{"projct"},
		},
		{
			name: "typo in a built-in module setting",
			yaml: strings.Replace(validYAML, "    allow:\n      - tags: [documented]", "    alow:\n      - tags: [documented]", 1), env: fullEnv,
			want: []string{`collectors.netbox: unknown setting "alow"`},
		},
		{
			name: "missing environment variable reports its path",
			yaml: validYAML, env: map[string]string{"YOUTRACK_TOKEN": "fake-youtrack-token"},
			want: []string{"collectors.netbox.token: environment variable NETBOX_TOKEN is not set or empty"},
		},
		{
			name: "all missing variables are reported together",
			yaml: validYAML, env: nil,
			want: []string{
				"youtrack.token: environment variable YOUTRACK_TOKEN is not set or empty",
				"collectors.netbox.token: environment variable NETBOX_TOKEN is not set or empty",
			},
		},
		{
			name: "youtrack url without scheme",
			yaml: strings.Replace(validYAML, "https://youtrack.example.com", "youtrack.example.com", 1), env: fullEnv,
			want: []string{"youtrack.url:"},
		},
		{
			name: "collector url with wrong scheme",
			yaml: strings.Replace(validYAML, "https://netbox.example.com", "ftp://netbox.example.com", 1), env: fullEnv,
			want: []string{"collectors.netbox.url:"},
		},
		{
			name: "url without host",
			yaml: strings.Replace(validYAML, "https://netbox.example.com", "https://", 1), env: fullEnv,
			want: []string{"collectors.netbox.url:"},
		},
		{
			name: "youtrack project missing while youtrack sink is active",
			yaml: strings.Replace(validYAML, "  project: INFRA\n", "", 1), env: fullEnv,
			want: []string{"youtrack.project: required"},
		},
		{
			name: "no enabled collector",
			yaml: "collectors:\n  netbox:\n    enabled: false\nsinks:\n  markdown:\n    dir: ./out\n",
			want: []string{"at least one collector must be enabled"},
		},
		{
			name: "no enabled sink",
			yaml: "collectors:\n  kubernetes:\n    paths: [./k8s]\n",
			want: []string{"at least one sink must be enabled"},
		},
		{
			name: "invalid module name",
			yaml: "collectors:\n  Net Box:\n    paths: [./k8s]\nsinks:\n  markdown:\n    dir: ./out\n",
			want: []string{`collectors."Net Box": invalid module name`},
		},
		{
			name: "empty match",
			yaml: strings.Replace(validYAML, "- tags: [documented]", "- tags: []", 1), env: fullEnv,
			want: []string{"collectors.netbox.allow[0]: match has no criteria"},
		},
		{
			name: "malformed glob in match",
			yaml: strings.Replace(validYAML, "tags: [documented]", "tags: ['[doc']", 1), env: fullEnv,
			want: []string{`collectors.netbox.allow[0]: invalid pattern "[doc"`},
		},
		{
			name: "invalid redact deny pattern",
			yaml: validYAML + "redact:\n  deny_patterns: ['(']\n", env: fullEnv,
			want: []string{"redact: filter: invalid deny pattern"},
		},
		{
			name: "filter is not a list of matches",
			yaml: strings.Replace(validYAML, "tags: [documented]", "tags: documented", 1), env: fullEnv,
			want: []string{"cannot unmarshal"},
		},
		{
			name: "netbox needs url and token",
			yaml: "collectors:\n  netbox: {}\nsinks:\n  markdown:\n    dir: ./out\n",
			want: []string{"collectors.netbox.url: required", "collectors.netbox.token: required"},
		},
		{
			name: "kubernetes needs paths",
			yaml: "collectors:\n  kubernetes: {}\nsinks:\n  markdown:\n    dir: ./out\n",
			want: []string{"collectors.kubernetes.paths: required"},
		},
		{
			name: "kubernetes paths must be strings",
			yaml: "collectors:\n  kubernetes:\n    paths: [1, {a: b}]\nsinks:\n  markdown:\n    dir: ./out\n",
			want: []string{"collectors.kubernetes.paths: must be a list of non-empty strings"},
		},
		{
			name: "markdown sink needs dir",
			yaml: "collectors:\n  kubernetes:\n    paths: [./k8s]\nsinks:\n  markdown: {}\n",
			want: []string{"sinks.markdown.dir: required"},
		},
		{
			name: "several problems at once",
			yaml: "collectors:\n  netbox: {}\n  kubernetes: {}\nsinks:\n  markdown: {}\n",
			want: []string{"collectors.netbox.url", "collectors.kubernetes.paths", "sinks.markdown.dir"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), envMap(tt.env))
			if err == nil {
				t.Fatal("Parse() error = nil, want an error")
			}
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error %T is not a *ValidationError", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not mention %q:\n%s", want, err)
				}
			}
			for _, secret := range tt.env {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaks the value %q", secret)
				}
			}
		})
	}
}

func TestParseTypeErrorsDoNotLeakExpandedValues(t *testing.T) {
	const secret = "fake-secret-value-123"
	// The secret lands in a field that expects a list, so YAML reports a type error.
	const yamlText = `
collectors:
  netbox:
    url: https://netbox.example.com
    token: ${NETBOX_TOKEN}
    allow:
      - tags: ${NETBOX_TOKEN}
sinks:
  markdown:
    dir: ./out
`
	_, err := Parse([]byte(yamlText), envMap(map[string]string{"NETBOX_TOKEN": secret}))
	if err == nil {
		t.Fatal("Parse() error = nil, want an error")
	}
	// YAML shortens values in type errors, so a prefix is already a leak.
	if strings.Contains(err.Error(), secret[:6]) {
		t.Errorf("error leaks (part of) the expanded value:\n%s", err)
	}
	if !strings.Contains(err.Error(), "into []string") {
		t.Errorf("error lost the expected type:\n%s", err)
	}
}

func TestRedactDecodeError(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "scalar into list", in: "cannot unmarshal !!str `fake-se...` into []string", want: "cannot unmarshal a !!str value into []string"},
		{name: "scalar into bool", in: "cannot unmarshal !!str `maybe` into bool", want: "cannot unmarshal a !!str value into bool"},
		{name: "value with backtick", in: "cannot unmarshal !!str `a`b...` into []string", want: "cannot unmarshal a !!str value into []string"},
		{name: "value containing the word into", in: "cannot unmarshal !!str `x into y` into []string", want: "cannot unmarshal a !!str value into []string"},
		{name: "no value in message", in: "field colectors not found in type config.Config", want: "field colectors not found in type config.Config"},
		{name: "collection into scalar", in: "cannot unmarshal !!map into string", want: "cannot unmarshal !!map into string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactDecodeError(tt.in); got != tt.want {
				t.Errorf("redactDecodeError(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docgen.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOUTRACK_TOKEN", "fake-youtrack-token")
	t.Setenv("NETBOX_TOKEN", "fake-netbox-token")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.YouTrack.Project != "INFRA" {
		t.Errorf("project = %q", cfg.YouTrack.Project)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load() error = %v, want os.ErrNotExist", err)
	}
}

func TestParseMaxStaleRatio(t *testing.T) {
	cfg, err := Parse([]byte(strings.Replace(validYAML, "project: INFRA", "project: INFRA\n  max_stale_ratio: 0.25", 1)), envMap(fullEnv))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.YouTrack.MaxStaleRatio != 0.25 {
		t.Errorf("MaxStaleRatio = %v, want 0.25", cfg.YouTrack.MaxStaleRatio)
	}
	cfg, err = Parse([]byte(validYAML), envMap(fullEnv))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.YouTrack.MaxStaleRatio != 0 {
		t.Errorf("MaxStaleRatio = %v, want 0 (the sink default) when unset", cfg.YouTrack.MaxStaleRatio)
	}
}
