package youtrack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
)

var (
	docMarkerRe = regexp.MustCompile(`<!-- docgen:doc (\S+) -->`)
	beginRe     = regexp.MustCompile(`<!-- docgen:begin (\S+) hash=([0-9a-f]+) -->\n`)
)

// block is one generated section as it appears in article content.
type block struct {
	id   string
	hash string
	body string // Markdown of the section
	// volatile blocks differ on every run; see render.Section.Volatile.
	volatile bool
}

// sectionBlocks turns the sections of a document into blocks.
func sectionBlocks(d render.Document) ([]block, error) {
	blocks := make([]block, 0, len(d.Sections))
	for _, sec := range d.Sections {
		b, err := newBlock(sec.ID, sec.Markdown())
		if err != nil {
			return nil, err
		}
		b.volatile = sec.Volatile
		blocks = append(blocks, b)
	}
	return blocks, nil
}

func newBlock(id, markdown string) (block, error) {
	if err := checkMarkerToken(id); err != nil {
		return block{}, err
	}
	sum := sha256.Sum256([]byte(markdown))
	return block{id: id, hash: hex.EncodeToString(sum[:8]), body: markdown}, nil
}

func (b block) render() string {
	return fmt.Sprintf("<!-- docgen:begin %s hash=%s -->\n%s<!-- docgen:end %s -->\n", b.id, b.hash, b.body, b.id)
}

func endMarker(id string) string { return "<!-- docgen:end " + id + " -->\n" }

// checkMarkerToken rejects IDs that could break out of a marker comment.
func checkMarkerToken(s string) error {
	if s == "" || strings.ContainsAny(s, " \t\r\n") || strings.Contains(s, "--") {
		return fmt.Errorf("youtrack sink: %q cannot be used in a docgen marker", s)
	}
	return nil
}

func docMarker(key string) string { return "<!-- docgen:doc " + key + " -->" }

// documentKey extracts the stable document key from article content.
func documentKey(content string) (string, bool) {
	m := docMarkerRe.FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// buildContent renders content for a new article.
func buildContent(key string, blocks []block) string {
	var b strings.Builder
	b.WriteString(docMarker(key) + "\n")
	for _, bl := range blocks {
		b.WriteString("\n" + bl.render())
	}
	return b.String()
}

// mergeContent returns the content an article with the given content should
// have. A volatile block alone never changes an article: it is only refreshed
// when another block changes too, or added when the article lacks it. Publish
// and Plan both decide through this function.
func mergeContent(existing string, wanted []block) string {
	if merge(existing, wanted, false) == existing {
		return existing
	}
	return merge(existing, wanted, true)
}

// merge replaces the marked blocks of existing whose hash differs from the
// wanted block with the same ID and appends wanted blocks that are not present
// yet. Volatile blocks are only replaced with refreshVolatile. Everything
// outside the markers is kept verbatim. Blocks in existing that are no longer
// wanted are left alone.
func merge(existing string, wanted []block, refreshVolatile bool) string {
	byID := make(map[string]block, len(wanted))
	for _, w := range wanted {
		byID[w.id] = w
	}
	seen := make(map[string]bool, len(wanted))
	var out strings.Builder
	rest := existing
	for {
		loc := beginRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			break
		}
		id, hash := rest[loc[2]:loc[3]], rest[loc[4]:loc[5]]
		end := strings.Index(rest[loc[1]:], endMarker(id))
		if end < 0 {
			// Unterminated block: keep the marker as manual text.
			out.WriteString(rest[:loc[1]])
			rest = rest[loc[1]:]
			continue
		}
		blockEnd := loc[1] + end + len(endMarker(id))
		out.WriteString(rest[:loc[0]])
		w, wanted := byID[id]
		if wanted && !seen[id] && w.hash != hash && (refreshVolatile || !w.volatile) {
			out.WriteString(w.render())
		} else {
			out.WriteString(rest[loc[0]:blockEnd])
		}
		seen[id] = seen[id] || wanted
		rest = rest[blockEnd:]
	}
	out.WriteString(rest)
	merged := out.String()
	for _, w := range wanted {
		if !seen[w.id] {
			merged = strings.TrimRight(merged, "\n") + "\n\n" + w.render()
		}
	}
	return merged
}
