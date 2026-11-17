package youtrack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

// The markers are written in one canonical form but read tolerantly: an
// editor or the server may change line endings and spacing, and a person may
// drop the hash. A marker that is not recognised would make its block count
// as missing, and the block would be appended a second time.
var (
	docMarkerRe = regexp.MustCompile(`(?m)^[ \t]*<!--[ \t]*docgen:doc[ \t]+(\S+?)[ \t]*-->[ \t]*\r?$`)
	beginRe     = regexp.MustCompile(`<!--[ \t]*docgen:begin[ \t]+(\S+?)(?:[ \t]+hash=(\S*?))?[ \t]*-->[ \t]*(?:\r?\n)?`)
	endRe       = regexp.MustCompile(`<!--[ \t]*docgen:end[ \t]+(\S+?)[ \t]*-->[ \t]*(?:\r?\n)?`)
)

// span is one marked block found in article content.
type span struct {
	id, hash   string
	start, end int // the whole block, markers included
	closed     bool
}

// parseBlocks returns the marked blocks of content in order. A block runs
// from a begin marker to the next end marker with the same ID; begin markers
// inside it are part of its body. For a begin marker without end marker,
// closed is false and the span covers the begin marker only.
//
// Article text is written by other people, so this is one pass over the
// markers: the end markers are collected once and consumed in order.
func parseBlocks(content string) []span {
	begins := beginRe.FindAllStringSubmatchIndex(content, -1)
	if begins == nil {
		return nil
	}
	ends := map[string][][]int{}
	for _, loc := range endRe.FindAllStringSubmatchIndex(content, -1) {
		id := content[loc[2]:loc[3]]
		ends[id] = append(ends[id], loc)
	}
	var out []span
	pos := 0
	for _, loc := range begins {
		if loc[0] < pos {
			continue // inside the previous block
		}
		sp := span{id: content[loc[2]:loc[3]], start: loc[0], end: loc[1]}
		if loc[4] >= 0 {
			sp.hash = content[loc[4]:loc[5]]
		}
		// Drop end markers that lie before this begin marker; they can never
		// close a later block either.
		queue := ends[sp.id]
		for len(queue) > 0 && queue[0][0] < loc[1] {
			queue = queue[1:]
		}
		if len(queue) > 0 {
			sp.end, sp.closed = queue[0][1], true
			queue = queue[1:]
		}
		ends[sp.id] = queue
		out = append(out, sp)
		pos = sp.end
	}
	return out
}

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

// checkMarkerToken rejects IDs that could break out of a marker comment or
// that the marker patterns would not read back as one token.
func checkMarkerToken(s string) error {
	if s == "" || strings.Contains(s, "--") || strings.ContainsFunc(s, isMarkerUnsafe) {
		return fmt.Errorf("youtrack sink: %q cannot be used in a docgen marker", s)
	}
	return nil
}

func isMarkerUnsafe(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }

// escapedKeyPrefix marks a document key that docKey had to encode.
const escapedKeyPrefix = "esc:"

// docKey returns the stable key of the article for the document with the
// given ID. An ID that is safe inside a marker is its own key, so articles
// published before keep their key. Any other ID (a host name with spaces or a
// double hyphen, for example) is percent-encoded behind a prefix; the encoding
// is reversible, so two IDs never share a key.
//
// Use resolveKey to find the article of a document; docKey alone misses
// articles stored under a legacy key.
func docKey(id string) string {
	if checkMarkerToken(id) == nil && !strings.HasPrefix(id, escapedKeyPrefix) {
		return id
	}
	var b strings.Builder
	b.WriteString(escapedKeyPrefix)
	prevHyphen := false
	for _, r := range id {
		switch {
		case r == '%' || isMarkerUnsafe(r) || (r == '-' && prevHyphen):
			for _, c := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
			prevHyphen = false
		default:
			b.WriteRune(r)
			prevHyphen = r == '-'
		}
	}
	return b.String()
}

// resolveKey returns the key under which the document with the given ID is or
// will be stored. Earlier versions wrote some IDs unencoded that docKey now
// encodes (a no-break space or a control character, for example). An article
// stored that way keeps its key, so it is found again instead of duplicated.
func resolveKey(existing map[string]client.Article, id string) string {
	key := docKey(id)
	if key != id {
		if _, legacy := existing[id]; legacy {
			return id
		}
	}
	return key
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
// yet. Volatile blocks are only replaced with refreshVolatile. Blocks that are
// no longer wanted and second copies of a block are removed. Everything
// outside the markers is kept verbatim.
func merge(existing string, wanted []block, refreshVolatile bool) string {
	byID := make(map[string]block, len(wanted))
	for _, w := range wanted {
		byID[w.id] = w
	}
	seen := make(map[string]bool, len(wanted))
	var out strings.Builder
	pos := 0
	for _, sp := range parseBlocks(existing) {
		out.WriteString(existing[pos:sp.start])
		pos = sp.end
		w, wanted := byID[sp.id]
		switch {
		case !sp.closed:
			// Unterminated block: keep the marker as manual text.
			out.WriteString(existing[sp.start:sp.end])
			continue
		case sp.id == staleBlockID:
			// Managed by the stale handling, not a section of the document.
			out.WriteString(existing[sp.start:sp.end])
		case !wanted || seen[sp.id]:
			// Obsolete section or second copy: drop it with the blank line
			// that separated it from what follows.
			pos += len(leadingLineBreak(existing[pos:]))
		case w.hash != sp.hash && (refreshVolatile || !w.volatile):
			out.WriteString(w.render())
		default:
			out.WriteString(existing[sp.start:sp.end])
		}
		seen[sp.id] = seen[sp.id] || wanted
	}
	out.WriteString(existing[pos:])
	merged := out.String()
	for _, w := range wanted {
		if !seen[w.id] {
			merged = strings.TrimRight(merged, "\n") + "\n\n" + w.render()
		}
	}
	return merged
}

// leadingLineBreak returns the line break s starts with, or "".
func leadingLineBreak(s string) string {
	switch {
	case strings.HasPrefix(s, "\r\n"):
		return "\r\n"
	case strings.HasPrefix(s, "\n"):
		return "\n"
	}
	return ""
}
