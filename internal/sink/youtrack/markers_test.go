package youtrack

import (
	"strings"
	"testing"
	"time"
)

func mustBlock(t *testing.T, id, markdown string) block {
	t.Helper()
	b, err := newBlock(id, markdown)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Editors and the server may rewrite line endings and spacing. The markers
// must still be recognised, or blocks would be appended a second time.
func TestMarkersAreRecognisedInOtherSpellings(t *testing.T) {
	a := mustBlock(t, "a", "## A\n\ntext\n")
	canonical := buildContent("netbox:host:web-1", []block{a})
	tests := map[string]string{
		"crlf":                strings.ReplaceAll(canonical, "\n", "\r\n"),
		"no trailing newline": strings.TrimRight(canonical, "\n"),
		"extra spaces":        strings.NewReplacer("<!-- docgen:begin a hash=", "<!--  docgen:begin  a   hash=", " -->\n## A", "  -->  \n## A", "<!-- docgen:end a -->", "<!--docgen:end   a-->").Replace(canonical),
		"tabs":                strings.ReplaceAll(canonical, "<!-- docgen:begin a hash=", "<!--\tdocgen:begin\ta\thash="),
	}
	for name, existing := range tests {
		t.Run(name, func(t *testing.T) {
			if existing == canonical && name != "no trailing newline" {
				t.Fatal("test input equals the canonical form")
			}
			if got := mergeContent(existing, []block{a}); got != existing {
				t.Errorf("unchanged block was rewritten:\n%q\n->\n%q", existing, got)
			}
			changed := mustBlock(t, "a", "## A\n\nnew\n")
			got := mergeContent(existing, []block{changed})
			if strings.Count(got, "docgen:begin") != 1 || !strings.Contains(got, "new") || strings.Contains(got, "\ntext") && !strings.Contains(got, "new") {
				t.Errorf("changed block was not replaced in place:\n%q", got)
			}
			if strings.Contains(got, "text\n") || strings.Contains(got, "text\r\n") {
				t.Errorf("old body left behind:\n%q", got)
			}
			if key, ok := documentKey(existing); !ok || key != "netbox:host:web-1" {
				t.Errorf("document key = %q, %v", key, ok)
			}
		})
	}
}

// A block whose hash was removed by hand cannot be compared, so it is written
// again in canonical form, in place.
func TestBlockWithoutHashIsRewrittenInPlace(t *testing.T) {
	a := mustBlock(t, "a", "## A\n\ntext\n")
	existing := "<!-- docgen:doc k -->\n\nmanual above\n\n<!-- docgen:begin a -->\n## A\n\nedited by hand\n<!-- docgen:end a -->\n\nmanual below\n"
	got := mergeContent(existing, []block{a})
	want := "<!-- docgen:doc k -->\n\nmanual above\n\n" + a.render() + "\nmanual below\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// What the YouTrack editor does to an article on save: blank lines around
// markers, re-aligned table separators, CRLF line endings. None of this may
// cause a write or a second copy of a block.
func TestEditorNormalisationCausesNoUpdateAndNoDuplicate(t *testing.T) {
	table := mustBlock(t, "network", "## Network\n\n| IP | VLAN |\n| --- | --- |\n| 192.0.2.10 | 10 |\n")
	summary := mustBlock(t, "summary", "## Summary\n\nweb-1.example.com\n")
	blocks := []block{summary, table}
	saved := buildContent("netbox:host:web-1", blocks)

	edited := strings.NewReplacer(
		"| --- | --- |", "|------------|------|",
		"| IP | VLAN |", "| IP         | VLAN |",
		" -->\n## ", " -->\n\n## ",
		"\n<!-- docgen:end", "\n\n<!-- docgen:end",
	).Replace(saved)
	edited = strings.ReplaceAll(edited, "\n", "\r\n") + "\r\nA manual note.\r\n"
	if edited == saved {
		t.Fatal("the simulated edit changed nothing")
	}

	if got := mergeContent(edited, blocks); got != edited {
		t.Errorf("an article normalised by the editor was rewritten:\n%q", got)
	}
	changed := []block{mustBlock(t, "summary", "## Summary\n\nweb-1.example.com (renamed)\n"), table}
	got := mergeContent(edited, changed)
	if strings.Count(got, "docgen:begin summary") != 1 || strings.Count(got, "docgen:begin network") != 1 {
		t.Errorf("a block was duplicated:\n%s", got)
	}
	if !strings.Contains(got, "(renamed)") || !strings.Contains(got, "A manual note.") || !strings.Contains(got, "|------------|------|") {
		t.Errorf("the changed block, the manual note or the untouched block is wrong:\n%s", got)
	}
}

func TestDocKeyKeepsValidIDsAndEncodesTheRest(t *testing.T) {
	for _, id := range []string{"overview:root", "group:hosts", "netbox:host:web-1.example.com", "kubernetes:workload:default/app"} {
		if got := docKey(id); got != id {
			t.Errorf("docKey(%q) = %q, want it unchanged so published articles keep their key", id, got)
		}
	}
	seen := map[string]string{}
	for _, id := range []string{
		"netbox:host:Core Switch 1", "netbox:host:Core%20Switch%201", "netbox:host:xn--mnchen-3ya.example.com",
		"netbox:host:a\tb", "netbox:host:a b", "netbox:host:a--b", "netbox:host:a---b", "netbox:host:tab\nline",
	} {
		key := docKey(id)
		if err := checkMarkerToken(key); err != nil {
			t.Errorf("docKey(%q) = %q is not marker-safe: %v", id, key, err)
		}
		if key != docKey(id) {
			t.Errorf("docKey(%q) is not stable", id)
		}
		if other, dup := seen[key]; dup {
			t.Errorf("docKey(%q) and docKey(%q) collide on %q", id, other, key)
		}
		seen[key] = id
		if got, ok := documentKey(buildContent(key, nil)); !ok || got != key {
			t.Errorf("key %q does not survive a round trip through the marker: %q, %v", key, got, ok)
		}
	}
}

// Only a marker on a line of its own names the document. A marker quoted in
// manual text must not be taken for the key.
func TestDocumentKeyIgnoresMarkersInsideText(t *testing.T) {
	content := "See `<!-- docgen:doc other:key -->` for details.\n<!-- docgen:doc netbox:host:web-1 -->\n"
	if key, ok := documentKey(content); !ok || key != "netbox:host:web-1" {
		t.Errorf("key = %q, %v, want the marker on its own line", key, ok)
	}
	if _, ok := documentKey("See `<!-- docgen:doc other:key -->` for details.\n"); ok {
		t.Error("a marker inside a sentence was taken for the document key")
	}
}

// A section the renderer no longer produces must not stay in the article
// forever. Manual text and the stale banner are not generated sections of the
// document and stay.
func TestObsoleteAndDuplicateBlocksAreRemoved(t *testing.T) {
	a := mustBlock(t, "a", "## A\n\ntext\n")
	old := mustBlock(t, "old", "## Old\n\ngone\n")
	banner := block{id: staleBlockID, hash: "00", body: staleBanner}
	existing := "<!-- docgen:doc k -->\n\n" + banner.render() +
		"\nmanual above\n\n" + a.render() +
		"\n" + old.render() +
		"\nmanual between\n\n" + a.render() +
		"\nmanual below\n"
	got := mergeContent(existing, []block{a})
	want := "<!-- docgen:doc k -->\n\n" + banner.render() +
		"\nmanual above\n\n" + a.render() +
		"\nmanual between\n\nmanual below\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if again := mergeContent(got, []block{a}); again != got {
		t.Errorf("the cleaned article changes again:\n%s", again)
	}
}

// Removing an obsolete block is a real change even when the only other
// difference is the volatile timestamp, which is then refreshed too.
func TestRemovingAnObsoleteBlockRefreshesVolatileBlocks(t *testing.T) {
	a := mustBlock(t, "a", "text\n")
	old := mustBlock(t, "old", "gone\n")
	stamp := mustBlock(t, "generated", "03:00\n")
	stamp.volatile = true
	existing := buildContent("k", []block{a, old, stamp})
	later := mustBlock(t, "generated", "04:00\n")
	later.volatile = true
	got := mergeContent(existing, []block{a, later})
	if strings.Contains(got, "gone") || !strings.Contains(got, "04:00") || strings.Contains(got, "03:00") {
		t.Errorf("got:\n%s", got)
	}
}

// Article text is written by other people. Many begin markers without an end
// must not make parsing quadratic.
func TestManyUnterminatedBlocksAreParsedInLinearTime(t *testing.T) {
	const n = 20000
	content := "<!-- docgen:doc k -->\n" + strings.Repeat("<!-- docgen:begin x hash=00 -->\nfiller text\n", n)
	a := mustBlock(t, "a", "text\n")
	start := time.Now()
	got := mergeContent(content, []block{a})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("merging %d unterminated blocks took %s", n, elapsed)
	}
	if !strings.HasPrefix(got, content) || !strings.HasSuffix(got, a.render()) {
		t.Error("unterminated blocks must stay as manual text and the wanted block must be appended")
	}
}

// A begin marker inside a closed block belongs to that block's body.
func TestBeginMarkerInsideAClosedBlockIsBody(t *testing.T) {
	a := mustBlock(t, "a", "new\n")
	existing := "<!-- docgen:begin a hash=00 -->\n<!-- docgen:begin b hash=00 -->\ninner\n<!-- docgen:end b -->\n<!-- docgen:end a -->\ntail\n"
	if got, want := mergeContent(existing, []block{a}), a.render()+"tail\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A hash damaged by hand is still a hash attribute: the block is rewritten in
// place and not appended a second time.
func TestBlockWithDamagedHashIsRewrittenInPlace(t *testing.T) {
	a := mustBlock(t, "a", "text\n")
	for _, hash := range []string{"ABC", "xyz!", ""} {
		existing := "<!-- docgen:begin a hash=" + hash + " -->\nold\n<!-- docgen:end a -->\ntail\n"
		if got, want := mergeContent(existing, []block{a}), a.render()+"tail\n"; got != want {
			t.Errorf("hash=%q: got %q, want %q", hash, got, want)
		}
	}
}
