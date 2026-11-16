package youtrack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
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

// mergeContent replaces the marked blocks of existing whose hash differs from
// the wanted block with the same ID and appends wanted blocks that are not
// present yet. Everything outside the markers is kept verbatim. Blocks in
// existing that are no longer wanted are left alone.
func mergeContent(existing string, wanted []block) string {
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
		if wanted && !seen[id] && w.hash != hash {
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
