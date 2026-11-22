package markdown_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestPublishDoesNotFollowSymlinks(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "out")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "index.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	s, _ := markdown.New(dir)
	if err := s.Publish(context.Background(), sampleTree()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "keep" {
		t.Errorf("symlink target = %q, want it untouched", got)
	}
	info, err := os.Lstat(filepath.Join(dir, "index.md"))
	if err != nil || !info.Mode().IsRegular() {
		t.Errorf("index.md = %v, %v; want a regular file", info, err)
	}
}

func TestPublishLeavesNoTempFiles(t *testing.T) {
	dir := publish(t, sampleTree())
	for _, f := range listFiles(t, dir) {
		if strings.HasSuffix(f, ".tmp") {
			t.Errorf("temp file %q left behind", f)
		}
	}
}

func TestPublishKeepsExistingFileWhenAborted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "netbox-host-web01.md")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The context aborts after the root is written; the child must stay as it was.
	ctx := &abortAfter{Context: context.Background(), okCalls: 1}
	root := sampleTree()
	s, _ := markdown.New(dir)
	if err := s.Publish(ctx, root); err == nil {
		t.Fatal("Publish error = nil, want context error")
	}
	if got, _ := os.ReadFile(old); string(got) != "old" {
		t.Errorf("file = %q, want it untouched", got)
	}
	for _, f := range listFiles(t, dir) {
		if strings.HasSuffix(f, ".tmp") {
			t.Errorf("temp file %q left behind", f)
		}
	}
}

// abortAfter reports a cancelled context after okCalls calls to Err.
type abortAfter struct {
	context.Context
	okCalls int
}

func (c *abortAfter) Err() error {
	if c.okCalls > 0 {
		c.okCalls--
		return nil
	}
	return context.Canceled
}

func TestFileNamesAreIndependentOfOrder(t *testing.T) {
	names := func(ids ...string) map[string]string {
		root := render.Document{ID: "overview:root", Title: "Root"}
		for _, id := range ids {
			root.Children = append(root.Children, render.Document{ID: id, Title: id})
		}
		dir := publish(t, root)
		out := map[string]string{}
		for _, f := range listFiles(t, dir) {
			out[f] = ""
		}
		return out
	}
	a := names("a:b", "a-b", "index", "plain")
	b := names("plain", "index", "a-b", "a:b")
	if len(a) != 5 || len(b) != 5 {
		t.Fatalf("files = %v / %v, want 5 each", a, b)
	}
	for f := range a {
		if _, ok := b[f]; !ok {
			t.Errorf("file %q only present in one order", f)
		}
	}
	if _, ok := a["plain.md"]; !ok {
		t.Errorf("a non-colliding ID should keep its plain name: %v", a)
	}
}

// hashedName mirrors the sink's "<slug>-<8 hex of sha256(id)>.md" pattern so a
// test can build an ID whose slug equals another ID's generated name.
func hashedSlug(slug, id string) string {
	sum := sha256.Sum256([]byte(id))
	return slug + "-" + hex.EncodeToString(sum[:4])
}

func publishedNames(t *testing.T, ids ...string) []string {
	t.Helper()
	root := render.Document{ID: "overview:root", Title: "Root"}
	for _, id := range ids {
		root.Children = append(root.Children, render.Document{ID: id, Title: id})
	}
	return listFiles(t, publish(t, root))
}

func TestFileNamesAreUniqueAcrossAllIDs(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
	}{
		{"several IDs share a slug", []string{"a:b", "a-b", "a b"}},
		{"slug equals root file", []string{"index", "Index", "plain"}},
		{"slug equals generated name of colliding pair", []string{"a:b", "a-b", hashedSlug("a-b", "a:b")}},
		{"slug equals generated name of root collision", []string{"index", hashedSlug("index", "index")}},
		{"slug equals generated name of both of a pair", []string{"a:b", "a-b", hashedSlug("a-b", "a:b"), hashedSlug("a-b", "a-b")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := publishedNames(t, tt.ids...)
			if want := len(tt.ids) + 1; len(got) != want {
				t.Fatalf("files = %v (%d), want %d distinct files", got, len(got), want)
			}
			reversed := slices.Clone(tt.ids)
			slices.Reverse(reversed)
			if again := publishedNames(t, reversed...); !slices.Equal(got, again) {
				t.Errorf("names depend on order: %v vs %v", got, again)
			}
		})
	}
}

func TestFileNamesKeepNonCollidingNames(t *testing.T) {
	// The ID slugged like another ID's generated name keeps its plain name.
	taken := hashedSlug("a-b", "a:b")
	got := publishedNames(t, "a:b", "a-b", taken)
	if !slices.Contains(got, taken+".md") {
		t.Errorf("files = %v, want %q kept for the ID with that slug", got, taken+".md")
	}
	if !slices.Contains(got, "index.md") {
		t.Errorf("files = %v, want index.md", got)
	}
}
