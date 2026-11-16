// Package markdown writes rendered documents as Markdown files into a
// directory, so the result can be inspected before anything is sent to
// YouTrack.
package markdown

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JtheGunner/youtrackt-docgen/internal/render"
	"github.com/JtheGunner/youtrackt-docgen/internal/sink"
)

// Name is the sink name used in the config.
const Name = "markdown"

// rootFile is the file name of the root document.
const rootFile = "index.md"

// Sink implements sink.Sink by writing one .md file per document.
type Sink struct {
	dir string
}

var _ sink.Sink = (*Sink)(nil)

// New builds a sink that writes into dir. The directory is created on Publish.
func New(dir string) (*Sink, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("markdown: dir is required")
	}
	return &Sink{dir: dir}, nil
}

// Name returns the sink name.
func (*Sink) Name() string { return Name }

// Publish writes root as index.md and every other document as its own file
// named after its ID. A document with children gets a "Pages" list linking to
// them. Existing files with the same name are overwritten; other files in the
// directory are left alone.
func (s *Sink) Publish(ctx context.Context, root render.Document) error {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("markdown: create output directory: %w", err)
	}
	names := fileNames(root)
	var err error
	root.Walk(func(d render.Document) {
		if err != nil {
			return
		}
		if err = ctx.Err(); err != nil {
			return
		}
		body := d.Markdown() + pages(d, names)
		path := filepath.Join(s.dir, names[d.ID])
		if werr := os.WriteFile(path, []byte(body), 0o644); werr != nil { //nolint:gosec // generated docs are meant to be readable
			err = fmt.Errorf("markdown: write %s: %w", names[d.ID], werr)
		}
	})
	return err
}

// fileNames assigns every document of the tree a unique file name. The root is
// always index.md; the others derive from their ID, with a short hash added
// when two IDs would otherwise produce the same name.
func fileNames(root render.Document) map[string]string {
	names := map[string]string{root.ID: rootFile}
	used := map[string]bool{rootFile: true}
	root.Walk(func(d render.Document) {
		if _, ok := names[d.ID]; ok {
			return
		}
		name := slug(d.ID) + ".md"
		if used[name] {
			sum := sha256.Sum256([]byte(d.ID))
			name = slug(d.ID) + "-" + hex.EncodeToString(sum[:4]) + ".md"
		}
		names[d.ID] = name
		used[name] = true
	})
	return names
}

// slug reduces id to lowercase ASCII letters, digits and single hyphens, so an
// ID can never escape the output directory or produce an odd file name.
func slug(id string) string {
	var b strings.Builder
	dash := true // suppresses a leading hyphen
	for _, r := range strings.ToLower(id) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "document"
	}
	return out
}

// pages renders the list of links to the children of d, or "" without children.
func pages(d render.Document, names map[string]string) string {
	if len(d.Children) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Pages\n\n")
	for _, c := range d.Children {
		fmt.Fprintf(&b, "- [%s](%s)\n", linkText.Replace(strings.Join(strings.Fields(c.Title), " ")), names[c.ID])
	}
	return b.String()
}

var linkText = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "\\\\",
	"[", "\\[", "]", "\\]", "`", "\\`", "*", "\\*", "_", "\\_",
)
