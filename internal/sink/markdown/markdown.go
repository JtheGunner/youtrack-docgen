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
	"slices"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink"
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
// them. Existing files with the same name are replaced atomically (a symlink is replaced, never followed); other files in the
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
		if werr := writeFileAtomic(s.dir, names[d.ID], []byte(body)); werr != nil {
			err = fmt.Errorf("markdown: write %s: %w", names[d.ID], werr)
		}
	})
	return err
}

// writeFileAtomic writes data to dir/name through a temp file in dir and a
// rename, so a reader or an aborted run never sees a half-written file. The
// rename replaces a symlink at the target instead of following it, so nothing
// outside dir is ever written.
func writeFileAtomic(dir, name string, data []byte) (err error) {
	tmp, err := os.CreateTemp(dir, ".docgen-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(0o644); err != nil { //nolint:gosec // generated docs are meant to be readable
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

// minHashLen is the number of hex characters of the ID hash first tried as a
// disambiguating suffix.
const minHashLen = 8

// fileNames assigns every document of the tree a unique file name. The root is
// always index.md. An ID whose slug is unique keeps "<slug>.md". IDs sharing a
// slug (or slugged like the root file) get "<slug>-<hash>.md"; if that name is
// already taken by any other ID, the hash is extended until the name is free.
// Plain names are claimed first and the rest are resolved in sorted ID order,
// so the result does not depend on the order of the tree.
func fileNames(root render.Document) map[string]string {
	names := map[string]string{root.ID: rootFile}
	used := map[string]bool{rootFile: true}
	bySlug := map[string][]string{}
	root.Walk(func(d render.Document) {
		if _, ok := names[d.ID]; ok || slices.Contains(bySlug[slug(d.ID)], d.ID) {
			return
		}
		bySlug[slug(d.ID)] = append(bySlug[slug(d.ID)], d.ID)
	})
	var colliding []string
	for sl, ids := range bySlug {
		if len(ids) > 1 || used[sl+".md"] {
			colliding = append(colliding, ids...)
			continue
		}
		names[ids[0]] = sl + ".md"
		used[sl+".md"] = true
	}
	slices.Sort(colliding)
	for _, id := range colliding {
		name := uniqueHashedName(slug(id), id, used)
		names[id] = name
		used[name] = true
	}
	return names
}

// uniqueHashedName returns "<sl>-<hash>.md" with the shortest hash prefix of id
// that is not in used. The full hash is extended by a counter in the unlikely
// case that even it is taken.
func uniqueHashedName(sl, id string, used map[string]bool) string {
	sum := sha256.Sum256([]byte(id))
	full := hex.EncodeToString(sum[:])
	for n := minHashLen; n <= len(full); n++ {
		if name := sl + "-" + full[:n] + ".md"; !used[name] {
			return name
		}
	}
	for i := 2; ; i++ {
		if name := fmt.Sprintf("%s-%s-%d.md", sl, full, i); !used[name] {
			return name
		}
	}
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
