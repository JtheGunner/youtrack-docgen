package render

import (
	"strings"
)

// Document is a sink-neutral page. Sinks decide how to persist it; the
// Markdown method gives a plain rendering of its own sections.
type Document struct {
	// ID is stable across runs: the entity ID for host documents, a fixed
	// "overview:<name>" key for overviews and the root.
	ID    string
	Title string
	// Sections are the generated parts of the page. Their IDs are stable so a
	// sink can replace exactly these parts and leave manual content alone.
	Sections []Section
	Children []Document
}

// Section is a headed group of blocks with a stable ID.
type Section struct {
	ID      string
	Heading string
	Blocks  []Block
}

// Block is a piece of section content: Paragraph, List or Table.
type Block interface {
	markdown() string
}

// Paragraph is a run of plain text.
type Paragraph string

// List is a bullet list of plain-text items.
type List []string

// Table is a table with a header row. Every row has len(Header) cells.
type Table struct {
	Header []string
	Rows   [][]string
}

// Walk calls fn for d and then for every descendant, depth first.
func (d Document) Walk(fn func(Document)) {
	fn(d)
	for _, c := range d.Children {
		c.Walk(fn)
	}
}

// Markdown renders the title and sections of d, not its children.
func (d Document) Markdown() string {
	var b strings.Builder
	b.WriteString("# " + inline(d.Title) + "\n")
	for _, s := range d.Sections {
		b.WriteString("\n## " + inline(s.Heading) + "\n")
		for _, blk := range s.Blocks {
			b.WriteString("\n" + blk.markdown())
		}
	}
	return b.String()
}

func (p Paragraph) markdown() string { return inline(string(p)) + "\n" }

func (l List) markdown() string {
	var b strings.Builder
	for _, item := range l {
		b.WriteString("- " + inline(item) + "\n")
	}
	return b.String()
}

func (t Table) markdown() string {
	var b strings.Builder
	writeRow := func(cells []string) {
		b.WriteString("|")
		for _, c := range cells {
			b.WriteString(" " + strings.ReplaceAll(inline(c), "|", "\\|") + " |")
		}
		b.WriteString("\n")
	}
	writeRow(t.Header)
	sep := make([]string, len(t.Header))
	for i := range sep {
		sep[i] = "---"
	}
	writeRow(sep)
	for _, r := range t.Rows {
		writeRow(r)
	}
	return b.String()
}

// inline collapses line breaks so a value cannot break out of its list item,
// table cell or heading.
func inline(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
