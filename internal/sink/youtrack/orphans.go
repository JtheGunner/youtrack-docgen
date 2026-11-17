package youtrack

import (
	"fmt"
	"sort"

	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

// orphans are articles of the project without a document marker, typically
// because someone removed the marker line. Without the marker an article
// cannot be matched to a document by key.
//
// The sink never writes to such an article and never creates a second article
// for a document whose title it carries: it claims the article for that
// document, leaves it untouched and warns. Restoring the marker line makes the
// article a normal one again. This also covers container articles, whose only
// generated content is the marker line, and it means a manual article with
// the title of a document is never shadowed by a generated twin.
//
// The title is the only link left. If the title was changed as well, the
// article matches no document: a new article is created for the document. The
// old one is reported if it still holds generated blocks, so a person can
// merge the two.
type orphans struct {
	list     []client.Article
	byTitle  map[string]client.Article
	claimed  map[string]bool // by article ID
	warnings []string        // for claimed orphans, in document order
}

// add records an article that has no document marker.
func (o *orphans) add(a client.Article) {
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
		"article %s %q has the title of a generated document but no document marker; it was left untouched and no second article was created. To let docgen manage it, add this line to the article: %s",
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
		// Without generated blocks it is a manual article: not ours to report.
		if !o.claimed[a.ID] && beginRe.MatchString(a.Content) {
			rest = append(rest, a)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].ID < rest[j].ID })
	for _, a := range rest {
		out = append(out, fmt.Sprintf(
			"article %s %q has generated blocks but no document marker, and no document has its title; it was left untouched. If it is the article of a renamed document, a new article was created for that document: add the line %s with the key of that document to this article instead and remove the new one",
			a.IDReadable, a.Summary, docMarker("<key>")))
	}
	return out
}
