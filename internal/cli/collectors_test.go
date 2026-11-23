package cli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/collector"
	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/model"
)

const fakeOverlayToken = "seeded-fake-overlay-token"

// testSinks enables a sink, which every valid config needs.
const testSinks = "sinks:\n  markdown:\n    dir: out\n"

// namedCollector is a collector with a fixed name that collects nothing.
type namedCollector struct{ name string }

func (c namedCollector) Name() string                              { return c.name }
func (namedCollector) Collect(context.Context, *model.Graph) error { return nil }

func parseConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml+testSinks), func(key string) (string, bool) {
		if key == "OVERLAY_TOKEN" {
			return fakeOverlayToken, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestBuildCollectorsErrors(t *testing.T) {
	factoryFor := func(name string) collector.Factory {
		return func(collector.Settings) (collector.Collector, error) { return namedCollector{name: name}, nil }
	}
	tests := []struct {
		name      string
		yaml      string
		factories map[string]collector.Factory
		want      string
	}{
		{
			name:      "unknown collector lists the registered names",
			yaml:      "collectors:\n  inventory:\n    url: https://inventory.example.com\n",
			factories: map[string]collector.Factory{"netbox": factoryFor("netbox"), "kubernetes": factoryFor("kubernetes")},
			want:      `collector "inventory" is not registered (registered: kubernetes, netbox); disable it with enabled: false`,
		},
		{
			name:      "unknown collector without registrations",
			yaml:      "collectors:\n  inventory:\n    url: https://inventory.example.com\n",
			factories: map[string]collector.Factory{},
			want:      `collector "inventory" is not registered (registered: none); disable it with enabled: false`,
		},
		{
			name:      "name mismatch",
			yaml:      "collectors:\n  inventory:\n    url: https://inventory.example.com\n",
			factories: map[string]collector.Factory{"inventory": factoryFor("cmdb")},
			want:      `collector registered as "inventory" reports the name "cmdb"`,
		},
		{
			name: "factory error is returned",
			yaml: "collectors:\n  inventory:\n    url: https://inventory.example.com\n",
			factories: map[string]collector.Factory{"inventory": func(collector.Settings) (collector.Collector, error) {
				return nil, errors.New("collectors.inventory: url is unreachable")
			}},
			want: "collectors.inventory: url is unreachable",
		},
		{
			name: "factory returns no collector",
			yaml: "collectors:\n  inventory:\n    url: https://inventory.example.com\n",
			factories: map[string]collector.Factory{"inventory": func(collector.Settings) (collector.Collector, error) {
				return nil, nil
			}},
			want: `collector "inventory": factory returned no collector`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildCollectors(parseConfig(t, tt.yaml), tt.factories)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestBuildCollectorsSkipsDisabledUnknownCollector(t *testing.T) {
	cfg := parseConfig(t, "collectors:\n  inventory:\n    enabled: false\n  other:\n    url: https://other.example.com\n")
	got, err := buildCollectors(cfg, map[string]collector.Factory{"other": func(collector.Settings) (collector.Collector, error) {
		return namedCollector{name: "other"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name() != "other" {
		t.Fatalf("built %v, want only the enabled collector", got)
	}
}

func TestBuildCollectorsPassesSettingsToOverlayFactory(t *testing.T) {
	cfg := parseConfig(t, `
collectors:
  inventory:
    url: https://inventory.example.com/api
    token: ${OVERLAY_TOKEN}
    region: lab
    racks: [r1, r2]
    deny:
      - tags: [scratch]
`)
	var got collector.Settings
	calls := 0
	factories := map[string]collector.Factory{"inventory": func(s collector.Settings) (collector.Collector, error) {
		calls++
		got = s
		return namedCollector{name: "inventory"}, nil
	}}
	collectors, err := buildCollectors(cfg, factories)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(collectors) != 1 || collectors[0].Name() != "inventory" {
		t.Fatalf("calls = %d, collectors = %v", calls, collectors)
	}
	if got.URL != "https://inventory.example.com/api" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Token.Value() != fakeOverlayToken {
		t.Errorf("Token.Value() = %q, want the expanded env value", got.Token.Value())
	}
	wantOptions := map[string]any{"region": "lab", "racks": []any{"r1", "r2"}}
	if !reflect.DeepEqual(got.Options, wantOptions) {
		t.Errorf("Options = %#v, want %#v", got.Options, wantOptions)
	}
}

func TestBuiltinCollectorsAreRegisteredUnderTheirNames(t *testing.T) {
	builtins := BuiltinCollectors()
	var names []string
	for name := range builtins {
		names = append(names, name)
	}
	if len(builtins) != 2 || builtins["netbox"] == nil || builtins["kubernetes"] == nil {
		t.Fatalf("built-in collectors = %s, want kubernetes and netbox", strings.Join(names, ", "))
	}
}

func TestBuiltinFactoriesConvertSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings collector.Settings
		wantErr  string
	}{
		{name: "netbox", settings: collector.Settings{URL: "https://netbox.example.com", Token: collector.NewSecret(fakeOverlayToken)}},
		{name: "netbox", settings: collector.Settings{URL: "https://netbox.example.com"}, wantErr: "token"},
		{name: "kubernetes", settings: collector.Settings{Options: map[string]any{"paths": []any{map[string]any{"path": t.TempDir(), "cluster": "lab"}}}}},
		{name: "kubernetes", settings: collector.Settings{}, wantErr: "paths"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.wantErr, func(t *testing.T) {
			c, err := BuiltinCollectors()[tt.name](tt.settings)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Name() != tt.name {
				t.Fatalf("Name() = %q, want %q", c.Name(), tt.name)
			}
		})
	}
}
