package docgen_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/collector"
	"github.com/JtheGunner/youtrack-docgen/docgen"
	"github.com/JtheGunner/youtrack-docgen/model"
)

const fakeToken = "seeded-fake-inventory-token"

// inventory is an overlay collector that records the settings it was built
// from and adds one host.
type inventory struct{ settings collector.Settings }

func (*inventory) Name() string { return "inventory" }

func (c *inventory) Collect(_ context.Context, g *model.Graph) error {
	id, err := model.NewID("inventory", model.KindHost, "web01.example.com")
	if err != nil {
		return err
	}
	return g.AddEntity(model.Entity{ID: id, Kind: model.KindHost, Source: "inventory", Name: "web01.example.com"})
}

func inventoryFactory(built **inventory) collector.Factory {
	return func(s collector.Settings) (collector.Collector, error) {
		c := &inventory{settings: s}
		if built != nil {
			*built = c
		}
		return c, nil
	}
}

func TestRegister(t *testing.T) {
	tests := []struct {
		name    string
		reg     string
		factory collector.Factory
		wantErr string
	}{
		{name: "valid name", reg: "inventory", factory: inventoryFactory(nil)},
		{name: "digits dash underscore", reg: "cmdb-2_lab", factory: inventoryFactory(nil)},
		{name: "empty name", reg: "", factory: inventoryFactory(nil), wantErr: `invalid collector name ""`},
		{name: "upper case", reg: "Inventory", factory: inventoryFactory(nil), wantErr: `invalid collector name "Inventory"`},
		{name: "leading digit", reg: "1inventory", factory: inventoryFactory(nil), wantErr: `invalid collector name "1inventory"`},
		{name: "dot", reg: "inv.entory", factory: inventoryFactory(nil), wantErr: `invalid collector name "inv.entory"`},
		{name: "nil factory", reg: "inventory", factory: nil, wantErr: `collector "inventory": factory is nil`},
		{name: "built-in netbox", reg: "netbox", factory: inventoryFactory(nil), wantErr: `collector "netbox" is built in and cannot be replaced`},
		{name: "built-in kubernetes", reg: "kubernetes", factory: inventoryFactory(nil), wantErr: `collector "kubernetes" is built in and cannot be replaced`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := docgen.New().Register(tt.reg, tt.factory)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Register: %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want prefix %q", err, tt.wantErr)
			}
		})
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	app := docgen.New()
	if err := app.Register("inventory", inventoryFactory(nil)); err != nil {
		t.Fatal(err)
	}
	err := app.Register("inventory", inventoryFactory(nil))
	if err == nil || err.Error() != `collector "inventory" is already registered` {
		t.Fatalf("error = %v", err)
	}
}

func writeConfig(t *testing.T, collectors string) (path, outDir string) {
	t.Helper()
	dir := t.TempDir()
	outDir = filepath.Join(dir, "out")
	path = filepath.Join(dir, "docgen.yaml")
	content := "collectors:\n" + collectors + "sinks:\n  markdown:\n    dir: " + outDir + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, outDir
}

func TestRunBuildsRegisteredCollectorFromConfig(t *testing.T) {
	t.Setenv("INVENTORY_TOKEN", fakeToken)
	path, outDir := writeConfig(t, `  inventory:
    url: https://inventory.example.com/api
    token: ${INVENTORY_TOKEN}
    region: lab
`)
	app := docgen.New()
	var built *inventory
	if err := app.Register("inventory", inventoryFactory(&built)); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := app.Run(context.Background(), []string{"generate", "--config", path}, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr.String())
	}
	if built == nil {
		t.Fatal("the registered factory was not called")
	}
	if built.settings.URL != "https://inventory.example.com/api" || built.settings.Token.Value() != fakeToken || built.settings.Options["region"] != "lab" {
		t.Fatalf("settings = %+v", built.settings)
	}
	if !strings.Contains(stdout.String(), "sink markdown: wrote") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	found := false
	walkErr := filepath.WalkDir(outDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(filepath.Base(p), "web01") {
			found = true
		}
		return err
	})
	if walkErr != nil || !found {
		t.Fatalf("no article for the overlay host in %s (walk error: %v)", outDir, walkErr)
	}
	if strings.Contains(stdout.String()+stderr.String(), fakeToken) {
		t.Fatal("output leaks the token")
	}
}

func TestRunRejectsUnregisteredCollector(t *testing.T) {
	path, _ := writeConfig(t, "  inventory:\n    url: https://inventory.example.com\n")
	var stdout, stderr bytes.Buffer
	err := docgen.New().Run(context.Background(), []string{"generate", "--config", path}, &stdout, &stderr)
	want := `collector "inventory" is not registered (registered: kubernetes, netbox); disable it with enabled: false`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestRunUsesOnlyTheGivenArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"nil args", nil},
		{"empty args", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := docgen.New().Run(context.Background(), tt.args, &stdout, &stderr); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Fatalf("stdout = %q, want the help text", stdout.String())
			}
		})
	}
}

func TestRunHonoursCancelledContext(t *testing.T) {
	path, _ := writeConfig(t, "  inventory:\n    url: https://inventory.example.com\n")
	app := docgen.New()
	if err := app.Register("inventory", func(collector.Settings) (collector.Collector, error) { return cancelAware{}, nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	err := app.Run(ctx, []string{"generate", "--config", path}, &stdout, &stderr)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

type cancelAware struct{}

func (cancelAware) Name() string { return "inventory" }

func (cancelAware) Collect(ctx context.Context, _ *model.Graph) error { return ctx.Err() }

func TestRegisterAfterRunTakesEffect(t *testing.T) {
	path, _ := writeConfig(t, "  inventory:\n    url: https://inventory.example.com\n")
	app := docgen.New()
	var stdout, stderr bytes.Buffer
	if err := app.Run(context.Background(), []string{"generate", "--config", path}, &stdout, &stderr); err == nil {
		t.Fatal("Run without the registration succeeded")
	}
	if err := app.Register("inventory", inventoryFactory(nil)); err != nil {
		t.Fatal(err)
	}
	if err := app.Run(context.Background(), []string{"generate", "--config", path}, &stdout, &stderr); err != nil {
		t.Fatalf("Run after Register: %v", err)
	}
}
