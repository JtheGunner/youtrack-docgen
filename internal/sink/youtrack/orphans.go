package youtrack

import (
	"fmt"
	"sort"

	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

// orphans are articles of the project that hold generated blocks but no
// document marker, typically because someone removed the marker line. Without
// the marker an article cannot be matched to a document by key.
//
// The sink never writes to an orphan and never creates a second article for
// a document whose title an orphan carries: it claims the orphan for that
// document, leaves it untouched and warns. Restoring the marker line makes the
// article a normal one again.
//
// The title is the only link left. If the title was changed as well, the
// orphan matches no document: a new article is created for the document and
// the orphan is reported, so a person can merge the two.
type orphans struct {
	list     []client.Article
	byTitle  map[string]client.Article
	claimed  map[string]bool // by article ID
	warnings []string        // for claimed orphans, in document order
}

// add records a as an orphan if it holds a generated block.
func (o *orphans) add(a client.Article) {
	if !beginRe.MatchString(a.Content) {
		return
	}
	o.list = append(o.list, a)
	// Two orphans with one title: the lower ID can be claimed, so runs agree.
	if prev, dup := o.byTitle[a.Summary]; dup && prev.ID < a.ID {
		return
	}
	o.byTitle[a.Summary] = a
}

// claim returns the orphan that carries the title of the document with the
// given key, if there is one that no other document claimed.
func (o *orphans) claim(key, title string) (client.Article, bool) {
	a, ok := o.byTitle[title]
	if !ok || o.claimed[a.ID] {
		return client.Article{}, false
	}
	if o.claimed == nil {
		o.claimed = map[string]bool{}
	}
	o.claimed[a.ID] = true
	o.warnings = append(o.warnings, fmt.Sprintf(
		"article %s %q has generated blocks but no document marker; it was left untouched and no second article was created. To let docgen manage it again, add this line to the article: %s",
		a.IDReadable, a.Summary, docMarker(key)))
	return a, true
}

// claimedWarnings returns the messages for the orphans claimed so far.
func (o *orphans) claimedWarnings() []string { return append([]string(nil), o.warnings...) }

// all returns one message per orphan: first the claimed ones, then the
// rest sorted by article ID.
func (o *orphans) all() []string {
	out := o.claimedWarnings()
	var rest []client.Article
	for _, a := range o.list {
		if !o.claimed[a.ID] {
			rest = append(rest, a)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].ID < rest[j].ID })
	for _, a := range rest {
		out = append(out, fmt.Sprintf(
			"article %s %q has generated blocks but no document marker and matches no document; it was left untouched",
			a.IDReadable, a.Summary))
	}
	return out
}
