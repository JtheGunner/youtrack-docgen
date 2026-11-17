package markdown_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/markdown"
	"github.com/JtheGunner/youtrack-docgen/internal/testutil/golden"
)

func sampleTree() render.Document {
	return render.Document{
		ID:    "overview:root",
		Title: "Infrastructure documentation",
		Sections: []render.Section{
			{ID: "contents", Heading: "Contents", Blocks: []render.Block{render.List{"web01.example.com"}}},
		},
		Children: []render.Document{
			{
				ID:    "netbox:host:web01",
				Title: "web01.example.com",
				Sections: []render.Section{
					{ID: "addresses", Heading: "Addresses", Blocks: []render.Block{
						render.Table{Header: []string{"Address"}, Rows: [][]string{{"192.0.2.10"}}},
					}},
				},
			},
		},
	}
}

func publish(t *testing.T, root render.Document) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "out")
	s, err := markdown.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(context.Background(), root); err != nil {
		t.Fatalf("Publish error = %v", err)
	}
	return dir
}

func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestPublishWritesOneFilePerDocument(t *testing.T) {
	dir := publish(t, sampleTree())

	if got, want := listFiles(t, dir), []string{"index.md", "netbox-host-web01.md"}; !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for _, name := range []string{"index", "netbox-host-web01"} {
		body, err := os.ReadFile(filepath.Join(dir, name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, name, body)
	}
}

func TestPublishIsIdempotent(t *testing.T) {
	root := sampleTree()
	dir := publish(t, root)
	first, _ := os.ReadFile(filepath.Join(dir, "index.md"))

	s, _ := markdown.New(dir)
	if err := s.Publish(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	if string(first) != string(second) {
		t.Errorf("second run changed index.md")
	}
}

func TestPublishKeepsFilesInsideDirectory(t *testing.T) {
	root := render.Document{ID: "overview:root", Title: "Root", Children: []render.Document{
		{ID: "../../etc/passwd", Title: "evil"},
		{ID: "a:b", Title: "one"},
		{ID: "a-b", Title: "two"},
	}}
	dir := publish(t, root)

	files := listFiles(t, dir)
	if len(files) != 4 {
		t.Fatalf("files = %v, want 4 distinct files", files)
	}
	for _, f := range files {
		if strings.ContainsAny(f, "/\\") || strings.HasPrefix(f, ".") {
			t.Errorf("unsafe file name %q", f)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "etc")); err == nil {
		t.Error("a document escaped the output directory")
	}
}

func TestPublishLinksChildren(t *testing.T) {
	dir := publish(t, sampleTree())
	body, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	if !strings.Contains(string(body), "[web01.example.com](netbox-host-web01.md)") {
		t.Errorf("index.md has no link to the host page:\n%s", body)
	}
}

func TestPublishLeavesOtherFilesAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(manual, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := markdown.New(dir)
	if err := s.Publish(context.Background(), sampleTree()); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(manual); err != nil || string(got) != "keep" {
		t.Errorf("notes.txt = %q, %v; want it untouched", got, err)
	}
}

func TestPublishHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, _ := markdown.New(filepath.Join(t.TempDir(), "out"))
	if err := s.Publish(ctx, sampleTree()); err == nil {
		t.Fatal("Publish error = nil, want context error")
	}
}

func TestNewRequiresDir(t *testing.T) {
	if _, err := markdown.New("  "); err == nil {
		t.Fatal("New error = nil, want an error")
	}
}

func TestPublishFailsWhenDirIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := markdown.New(filepath.Join(file, "sub"))
	if err := s.Publish(context.Background(), sampleTree()); err == nil {
		t.Fatal("Publish error = nil, want an error")
	}
}
