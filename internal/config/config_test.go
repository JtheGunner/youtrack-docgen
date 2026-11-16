package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
      tags: [documented]
    deny:
      roles: [lab, test]
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
	if got := nb.Allow["tags"]; len(got) != 1 || got[0] != "documented" {
		t.Errorf("netbox allow = %v", nb.Allow)
	}
	if got := nb.Deny["roles"]; len(got) != 2 {
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
			name: "unknown key in youtrack",
			yaml: strings.Replace(validYAML, "project: INFRA", "project: INFRA\n  projct: X", 1), env: fullEnv,
			want: []string{"projct"},
		},
		{
			name: "typo in a built-in module setting",
			yaml: strings.Replace(validYAML, "    allow:\n      tags: [documented]", "    alow:\n      tags: [documented]", 1), env: fullEnv,
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
			name: "empty filter list",
			yaml: strings.Replace(validYAML, "tags: [documented]", "tags: []", 1), env: fullEnv,
			want: []string{"collectors.netbox.allow.tags: must not be empty"},
		},
		{
			name: "filter is not a map of lists",
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
