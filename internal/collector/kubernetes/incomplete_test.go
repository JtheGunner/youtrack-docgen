package kubernetes

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

const validDeployment = `apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: shop}
spec: {replicas: 1}
`

func TestIncompleteReportsSkippedInput(t *testing.T) {
	tests := []struct {
		name           string
		files          map[string]string
		wantIncomplete bool
	}{
		{name: "valid manifests", files: map[string]string{"ok.yaml": validDeployment}},
		{name: "duplicate only", files: map[string]string{"a.yaml": validDeployment, "b.yaml": validDeployment}},
		{name: "broken file", files: map[string]string{"ok.yaml": validDeployment, "bad.yaml": "a: [unclosed\n"}, wantIncomplete: true},
		{
			name:           "invalid document",
			files:          map[string]string{"ok.yaml": validDeployment + "---\napiVersion: apps/v1\nkind: Deployment\nmetadata: [not, a, map]\n"},
			wantIncomplete: true,
		},
		{
			name:           "nameless namespace",
			files:          map[string]string{"ok.yaml": validDeployment + "---\napiVersion: v1\nkind: Namespace\nmetadata: {}\n"},
			wantIncomplete: true,
		},
		{name: "empty path", wantIncomplete: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			c := NewFromPaths(dir).WithWarnings(io.Discard)
			if err := c.Collect(context.Background(), model.NewGraph()); err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if got := len(c.Incomplete()) > 0; got != tt.wantIncomplete {
				t.Errorf("Incomplete() = %v, want incomplete = %v", c.Incomplete(), tt.wantIncomplete)
			}
		})
	}
}

func TestIncompleteIsResetByEveryCollect(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("a: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewFromPaths(dir).WithWarnings(io.Discard)
	if err := c.Collect(context.Background(), model.NewGraph()); err != nil {
		t.Fatal(err)
	}
	if len(c.Incomplete()) == 0 {
		t.Fatal("broken file not reported")
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.yaml"), []byte(validDeployment), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Collect(context.Background(), model.NewGraph()); err != nil {
		t.Fatal(err)
	}
	if got := c.Incomplete(); len(got) != 0 {
		t.Errorf("Incomplete() = %v after a complete read, want none", got)
	}
}
