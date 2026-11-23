package docgen_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/collector"
	"github.com/JtheGunner/youtrack-docgen/docgen"
	"github.com/JtheGunner/youtrack-docgen/model"
)

const (
	fakePassword = "hunter2-example"
	fakeBearer   = "fake0bearer0token0abcdefghijklmnop"
	fakeAPIKey   = "fake-attribute-secret-value"
)

// seededCollector emits the given entities.
type seededCollector struct {
	name     string
	entities []model.Entity
}

func (c seededCollector) Name() string { return c.name }

func (c seededCollector) Collect(_ context.Context, g *model.Graph) error {
	for _, e := range c.entities {
		if err := g.AddEntity(e); err != nil {
			return err
		}
	}
	return nil
}

func seededHost(t *testing.T, name string, labels, attrs map[string]string) model.Entity {
	t.Helper()
	id, err := model.NewID("inventory", model.KindHost, name)
	if err != nil {
		t.Fatal(err)
	}
	return model.Entity{ID: id, Kind: model.KindHost, Source: "inventory", Name: name, Labels: labels, Attrs: attrs}
}

// runGenerate runs `generate` for a registered "inventory" collector and
// returns the concatenated content of every generated file keyed by path.
func runGenerate(t *testing.T, rules string, entities []model.Entity) map[string]string {
	t.Helper()
	path, outDir := writeConfig(t, "  inventory:\n    url: https://inventory.example.com/api\n"+rules)
	app := docgen.New()
	factory := func(collector.Settings) (collector.Collector, error) {
		return seededCollector{name: "inventory", entities: entities}, nil
	}
	if err := app.Register("inventory", factory); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := app.Run(context.Background(), []string{"generate", "--config", path}, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr.String())
	}
	files := map[string]string{}
	err := filepath.WalkDir(outDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, readErr := os.ReadFile(p)
		files[p] = string(b)
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func joined(files map[string]string) string {
	var sb strings.Builder
	for p, c := range files {
		sb.WriteString(p + "\n" + c + "\n")
	}
	return sb.String()
}

func TestRegisteredCollectorSecretsNeverReachTheSink(t *testing.T) {
	tests := []struct {
		name         string
		attrs        map[string]string
		leaks        []string
		wantRedacted bool
	}{
		{
			name: "secret in description",
			attrs: map[string]string{
				model.AttrDescription: "managed by ops password=" + fakePassword + " and Authorization: Bearer " + fakeBearer,
			},
			leaks:        []string{fakePassword, fakeBearer},
			wantRedacted: true,
		},
		{
			name:  "secret under a secret-looking attribute key",
			attrs: map[string]string{"api_token": fakeAPIKey, "rack": "r1"},
			leaks: []string{fakeAPIKey},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := runGenerate(t, "", []model.Entity{seededHost(t, "web01.example.com", nil, tt.attrs)})
			all := joined(files)
			if !strings.Contains(all, "web01.example.com") {
				t.Fatalf("host missing from output:\n%s", all)
			}
			for _, leak := range tt.leaks {
				if strings.Contains(all, leak) {
					t.Errorf("output leaks %q", leak)
				}
			}
			// The markdown sink escapes the brackets of the placeholder.
			if tt.wantRedacted && !strings.Contains(all, `\[REDACTED\]`) {
				t.Errorf("output has no redaction placeholder:\n%s", all)
			}
		})
	}
}

func TestAllowAndDenyRulesApplyToRegisteredCollector(t *testing.T) {
	hosts := func(t *testing.T) []model.Entity {
		return []model.Entity{
			seededHost(t, "web01.example.com", map[string]string{"env": "prod"}, nil),
			seededHost(t, "lab01.example.com", map[string]string{"env": "lab"}, nil),
			seededHost(t, "db01.example.com", map[string]string{"env": "prod", "tier": "db"}, nil),
		}
	}
	tests := []struct {
		name  string
		rules string
		want  []string
		gone  []string
	}{
		{
			name:  "no rules keeps everything",
			rules: "",
			want:  []string{"web01.example.com", "lab01.example.com", "db01.example.com"},
		},
		{
			name:  "deny removes matching entity",
			rules: "    deny:\n      - labels:\n          env: lab\n",
			want:  []string{"web01.example.com", "db01.example.com"},
			gone:  []string{"lab01.example.com"},
		},
		{
			name:  "allow removes entities it does not match",
			rules: "    allow:\n      - labels:\n          env: prod\n",
			want:  []string{"web01.example.com", "db01.example.com"},
			gone:  []string{"lab01.example.com"},
		},
		{
			name:  "deny wins over allow",
			rules: "    allow:\n      - labels:\n          env: prod\n    deny:\n      - labels:\n          tier: db\n",
			want:  []string{"web01.example.com"},
			gone:  []string{"lab01.example.com", "db01.example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := runGenerate(t, tt.rules, hosts(t))
			all := joined(files)
			for _, w := range tt.want {
				if !strings.Contains(all, w) {
					t.Errorf("output lacks %q", w)
				}
			}
			for _, g := range tt.gone {
				if strings.Contains(all, g) {
					t.Errorf("output still contains %q", g)
				}
			}
		})
	}
}
