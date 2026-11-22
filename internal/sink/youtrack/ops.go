package youtrack

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/diff"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

// DefaultMaxStaleRatio is the share of managed articles a single run may mark
// as stale when Config.MaxStaleRatio is not set.
const DefaultMaxStaleRatio = 0.5

// ErrMassStale is returned when a run would mark more of the articles as stale
// than Config.MaxStaleRatio allows. Nothing was written. A collector that
// silently returned nothing looks exactly like this.
var ErrMassStale = errors.New("youtrack sink: refusing to mark most articles as stale")

// ErrDenyCleanup is returned by Publish when an article of a denied object
// cannot be cleaned safely, or when it cannot be told whether the rules deny
// an article. Nothing was written. A dry run reports the same as a warning.
var ErrDenyCleanup = errors.New("youtrack sink: cannot clean denied articles")

const groupPrefix = "group:"

// opKind is what one operation of a run does to one article.
type opKind int

const (
	opCreate opKind = iota
	opUpdate
	opUnchanged
	opMarkStale
	opClearStale
)

// op is one step of a run. Publish executes the list of operations and the
// Planner shows the same list, so a dry run cannot drift from what Publish
// does. Parents are named by document key: the ID of a parent that is created
// in the same run is only known when it has been created.
type op struct {
	kind opKind
	key  string
	// parentKey is the key of the parent document, "" for the root.
	parentKey string

	// article is the article as the run found it; unset for opCreate.
	article client.Article
	// title and content are the values the article gets: the whole article for
	// opCreate, the changed parts (setTitle, setContent) for the other writes.
	title, content string
	setTitle       bool
	setContent     bool
	setParent      bool
	// addTag asks for the stale tag, removeTagID names the tag to remove.
	addTag      bool
	removeTagID string
	// newlyStale is true for an article that was not marked before.
	newlyStale bool
	// forced is true for a cleanup of a denied object. It is intended, so the
	// mass-stale guard does not count it.
	forced bool

	diff string
}

// writes lists the article write calls the operation makes, as
// "<call>:<document key>", in call order.
func (o op) writes() []string {
	var out []string
	switch o.kind {
	case opCreate:
		out = append(out, "create:"+o.key)
	case opUpdate:
		out = append(out, "update:"+o.key)
	case opMarkStale, opClearStale:
		if o.setContent {
			out = append(out, "update:"+o.key)
		}
		if o.addTag {
			out = append(out, "tag-add:"+o.key)
		}
		if o.removeTagID != "" {
			out = append(out, "tag-remove:"+o.key)
		}
	}
	return out
}

// opList is everything a run does, in order.
type opList struct {
	ops []op
	// adopted maps the key of a document to the ID of the marker-less article
	// that was claimed for it; see orphans. Such an article is not written, but
	// it is the parent of its children.
	adopted map[string]string
	// warnings describe articles whose markers are damaged, see strayMarkers.
	warnings []string
	// blocked are articles that must be cleaned or checked but cannot be
	// decided safely. Publish fails on them before the first write.
	blocked []string
}

// buildOps decides what a run does. It reads existing and lost and writes
// nothing. The returned list is complete even when the error is ErrMassStale,
// so a dry run can still show it; for any other error it is nil.
func buildOps(existing map[string]client.Article, lost *orphans, root render.Document, cfg Config) (*opList, error) {
	b := &opBuilder{
		denyCheck:  cfg.DenyCheck,
		incomplete: activeSet(cfg.IncompleteSources),
		existing:   existing,
		keyOfID:    make(map[string]string, len(existing)),
		orphans:    lost,
		adopted:    map[string]client.Article{},
		published:  map[string]bool{},
		after:      map[string]articleState{},
		active:     activeSet(cfg.ActiveSources),
	}
	for key, a := range existing {
		b.keyOfID[a.ID] = key
	}
	b.force = make(map[string]bool, len(cfg.ForceStaleIDs))
	for _, id := range cfg.ForceStaleIDs {
		b.force[resolveKey(existing, id)] = true
	}
	if err := b.documents(root); err != nil {
		return nil, err
	}
	b.reconcileStale()

	list := &opList{ops: b.ops, adopted: map[string]string{}, warnings: b.warnings, blocked: b.blocked}
	for key, a := range b.adopted {
		list.adopted[key] = a.ID
	}
	return list, b.guard(cfg)
}

// articleState is an article as it is after the operations so far.
type articleState struct {
	title, parentKey, content string
}

type opBuilder struct {
	existing map[string]client.Article
	keyOfID  map[string]string // article database ID -> document key
	orphans  *orphans
	// denyCheck decides from a document ID whether the rules deny it; may be nil.
	denyCheck  func(id string) (denied, decided bool)
	incomplete map[string]bool
	adopted    map[string]client.Article
	// published holds the keys of the documents of this run; after holds
	// their articles as the update leaves them.
	published map[string]bool
	after     map[string]articleState
	active    map[string]bool // nil: every source
	ops       []op
	force     map[string]bool
	warnings  []string
	// blocked are articles that must be cleaned or checked but cannot be
	// decided safely. Publish fails on them before the first write.
	blocked []string
}

func activeSet(sources []string) map[string]bool {
	if len(sources) == 0 {
		return nil
	}
	set := make(map[string]bool, len(sources))
	for _, s := range sources {
		set[s] = true
	}
	return set
}

// documents walks the tree below root the way Publish always did: the root,
// then every child below its container article.
func (b *opBuilder) documents(root render.Document) error {
	rootKey := resolveKey(b.existing, root.ID)
	if err := b.document(root, ""); err != nil {
		return err
	}
	groups := map[string]bool{}
	for _, child := range root.Children {
		parent := rootKey
		if key, title := groupFor(child.ID); key != "" {
			if !groups[key] {
				groups[key] = true
				if err := b.document(render.Document{ID: key, Title: title}, rootKey); err != nil {
					return err
				}
			}
			parent = key
		}
		if err := b.tree(child, parent); err != nil {
			return err
		}
	}
	return nil
}

func (b *opBuilder) tree(d render.Document, parentKey string) error {
	if err := b.document(d, parentKey); err != nil {
		return err
	}
	for _, child := range d.Children {
		if err := b.tree(child, resolveKey(b.existing, d.ID)); err != nil {
			return err
		}
	}
	return nil
}

// document adds the operation for the article of d. parentKey is the key of
// the parent document, or "" for the root.
func (b *opBuilder) document(d render.Document, parentKey string) error {
	key := resolveKey(b.existing, d.ID)
	blocks, err := sectionBlocks(d)
	if err != nil {
		return err
	}
	b.published[key] = true

	cur, found := b.existing[key]
	if !found {
		if orphan, ok := b.orphans.claim(key, d.Title); ok {
			// Not ours to write: see orphans. Children stay below it.
			b.adopted[key] = orphan
			b.keyOfID[orphan.ID] = key
			return nil
		}
		content := buildContent(key, blocks)
		b.ops = append(b.ops, op{
			kind: opCreate, key: key, parentKey: parentKey, title: d.Title, content: content,
			diff: diff.Unified("/dev/null", "b/"+key, "", articleText(d.Title, parentKey, content, false), diffContext),
		})
		return nil
	}

	if stray := strayMarkers(cur.Content); len(stray) > 0 {
		b.warnings = append(b.warnings, fmt.Sprintf(
			"article %s %q has damaged docgen markers (%s); they were left as text. Restore the missing marker line, or remove the leftover one, so the section is managed again",
			cur.IDReadable, cur.Summary, strings.Join(stray, ", ")))
	}
	merged := mergeContent(cur.Content, blocks)
	curParent := b.parentKeyOf(cur)
	wantParent := curParent
	setParent := false
	if parentKey != "" {
		parentArticle, known := b.existing[parentKey]
		if !known {
			parentArticle, known = b.adopted[parentKey]
		}
		setParent = !known || cur.ParentArticle == nil || cur.ParentArticle.ID != parentArticle.ID
		wantParent = parentKey
	}
	b.after[key] = articleState{title: d.Title, parentKey: wantParent, content: merged}
	setTitle, setContent := cur.Summary != d.Title, merged != cur.Content
	if !setTitle && !setContent && !setParent {
		b.ops = append(b.ops, op{kind: opUnchanged, key: key})
		return nil
	}
	b.ops = append(b.ops, op{
		kind: opUpdate, key: key, parentKey: parentKey, article: cur,
		title: d.Title, content: merged, setTitle: setTitle, setContent: setContent, setParent: setParent,
		diff: diff.Unified("a/"+key, "b/"+key,
			articleText(cur.Summary, curParent, cur.Content, false), articleText(d.Title, wantParent, merged, false), diffContext),
	})
	return nil
}

// parentKeyOf returns the document key of the parent of a, or its database ID
// when the parent is not a document of this project.
func (b *opBuilder) parentKeyOf(a client.Article) string {
	if a.ParentArticle == nil {
		return ""
	}
	if key := b.keyOfID[a.ParentArticle.ID]; key != "" {
		return key
	}
	return a.ParentArticle.ID
}

// reconcileStale adds the operations that mark articles of vanished objects
// as stale and clear the marking from articles that are published again.
// Articles are never deleted.
func (b *opBuilder) reconcileStale() {
	keys := make([]string, 0, len(b.existing))
	for key := range b.existing {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		a := b.existing[key]
		if b.published[key] {
			b.clearStale(key, a)
		} else if b.mayCheckDeny(key) && !b.checkDeny(key, a) {
			continue
		} else if b.inScope(key) || b.force[key] {
			b.markStale(key, a)
		}
	}
}

// mayCheckDeny reports whether the key is one of an entity article that the
// deny rules can apply to when sources were read incompletely.
func (b *opBuilder) mayCheckDeny(key string) bool {
	return len(b.incomplete) > 0 && b.denyCheck != nil &&
		!strings.HasPrefix(key, overviewPref) && !strings.HasPrefix(key, groupPrefix)
}

// checkDeny asks the rules about an unpublished article of a source that was
// read incompletely, because it may belong to a denied object that was never
// collected. A denied article is marked forced, so it is cleaned like any
// other denied one. It returns false when the article must not be touched in
// this run because the ID cannot decide. A key that is no entity ID is not
// checked and does not block.
func (b *opBuilder) checkDeny(key string, a client.Article) (proceed bool) {
	id, ok := idFromKey(key)
	if !ok {
		if source, _, found := strings.Cut(strings.TrimPrefix(key, escapedKeyPrefix), ":"); found && b.incomplete[source] {
			b.block(a, "its document key cannot be decoded, so it cannot be told whether the article must be cleaned")
			return false
		}
		return true
	}
	source, _, _, err := model.ParseID(model.ID(id))
	if err != nil {
		if s, _, found := strings.Cut(id, ":"); found && b.incomplete[s] {
			b.warnings = append(b.warnings, fmt.Sprintf(
				"article %s %q has the key %q, which is not an entity ID; no deny rule was checked for it", a.IDReadable, a.Summary, key))
		}
		return true
	}
	if !b.incomplete[source] || b.denyCheck == nil || b.force[key] {
		return true
	}
	denied, decided := b.denyCheck(id)
	switch {
	case decided && denied:
		b.force[key] = true
	case decided:
	case staleContent(a.Content) == a.Content && hasStaleTag(a) && !hasUnterminatedBlock(a.Content):
		// already clean: nothing left to decide
	default:
		b.block(a, "its source was read incompletely and the deny rules use labels the document ID does not carry, so it cannot be told whether the article must be cleaned")
		return false
	}
	return true
}

func (b *opBuilder) block(a client.Article, reason string) {
	b.blocked = append(b.blocked, fmt.Sprintf("article %s %q: %s", a.IDReadable, a.Summary, reason))
}

// markStale empties the generated sections of a, so names and addresses of an
// object that is gone (or was denied since) do not stay in the Knowledge Base,
// and adds the banner and the tag. Text outside the markers stays.
func (b *opBuilder) markStale(key string, a client.Article) {
	forced := b.force[key]
	if stray := strayMarkers(a.Content); len(stray) > 0 {
		b.warnings = append(b.warnings, fmt.Sprintf(
			"article %s %q has damaged docgen markers (%s); they were left as text. Restore the missing marker line, or remove the leftover one, so the section is managed again",
			a.IDReadable, a.Summary, strings.Join(stray, ", ")))
		if forced && hasUnterminatedBlock(a.Content) {
			b.block(a, "it is denied but has a generated block without end marker, so its content cannot be emptied safely; fix the markers or remove the article content by hand")
			return
		}
	}
	content := staleContent(a.Content)
	setContent, addTag := content != a.Content, !hasStaleTag(a)
	if !setContent && !addTag {
		return
	}
	parent := b.parentKeyOf(a)
	b.ops = append(b.ops, op{
		kind: opMarkStale, key: key, article: a, content: content, setContent: setContent, addTag: addTag,
		newlyStale: addTag || !hasBlock(a.Content, staleBlockID), forced: forced,
		diff: diff.Unified("a/"+key, "b/"+key,
			articleText(a.Summary, parent, a.Content, hasStaleTag(a)), articleText(a.Summary, parent, content, true), diffContext),
	})
}

// hasUnterminatedBlock reports whether content has a begin marker without end
// marker. What follows such a marker may be generated text, which a cleanup
// cannot tell from manual text.
func hasUnterminatedBlock(content string) bool {
	for _, sp := range parseBlocks(content) {
		if !sp.closed {
			return true
		}
	}
	return false
}

// clearStale removes banner and tag from an article whose object is back. The
// generated sections return through the update of the same run.
func (b *opBuilder) clearStale(key string, a client.Article) {
	state, ok := b.after[key]
	if !ok {
		state = articleState{title: a.Summary, parentKey: b.parentKeyOf(a), content: a.Content}
	}
	hasBanner, tagID := hasBlock(state.content, staleBlockID), staleTagID(a)
	if !hasBanner && tagID == "" {
		return
	}
	content := state.content
	if hasBanner {
		content = removeBlock(content, staleBlockID)
	}
	b.ops = append(b.ops, op{
		kind: opClearStale, key: key, article: a, content: content, setContent: hasBanner, removeTagID: tagID,
		diff: diff.Unified("a/"+key, "b/"+key,
			articleText(state.title, state.parentKey, state.content, tagID != ""), articleText(state.title, state.parentKey, content, false), diffContext),
	})
}

// staleContent is content with its generated sections removed and the stale
// banner in place.
func staleContent(content string) string {
	cleared := merge(content, nil, true)
	if !hasBlock(cleared, staleBlockID) {
		cleared = insertBanner(cleared)
	}
	return cleared
}

// inScope reports whether the run may mark the article with the given key as
// stale. With ActiveSources set, an article of a source that did not run is
// not vanished, it is just not looked at.
func (b *opBuilder) inScope(key string) bool {
	if b.active == nil {
		return true
	}
	switch {
	case strings.HasPrefix(key, overviewPref):
		return true
	case strings.HasPrefix(key, groupPrefix):
		return !b.hasChildOutOfScope(key)
	}
	return b.entityActive(key)
}

// entityActive reports whether key belongs to a source that ran.
func (b *opBuilder) entityActive(key string) bool {
	source, _, found := strings.Cut(strings.TrimPrefix(key, escapedKeyPrefix), ":")
	return found && b.active[source]
}

// hasChildOutOfScope reports whether a container article holds a document of a
// source that did not run: the container still has a purpose then.
func (b *opBuilder) hasChildOutOfScope(groupKey string) bool {
	group := b.existing[groupKey]
	for key, a := range b.existing {
		if a.ParentArticle != nil && a.ParentArticle.ID == group.ID && !strings.HasPrefix(key, groupPrefix) && !b.entityActive(key) {
			return true
		}
	}
	return false
}

// guard returns ErrMassStale when the run would mark more than the allowed
// share of the live articles as stale. Live articles are the in-scope ones
// that do not already carry both the stale tag and the banner, so the limit
// does not loosen as stale articles accumulate. With no live article left, any
// newly stale one aborts the run.
func (b *opBuilder) guard(cfg Config) error {
	if cfg.AllowMassStale {
		return nil
	}
	total := 0
	for key, a := range b.existing {
		if b.inScope(key) && !b.force[key] && (!hasStaleTag(a) || !hasBlock(a.Content, staleBlockID)) {
			total++
		}
	}
	newly := 0
	for _, o := range b.ops {
		if o.kind == opMarkStale && o.newlyStale && !o.forced {
			newly++
		}
	}
	limit := cfg.maxStaleRatio()
	if newly == 0 {
		return nil
	}
	if total == 0 {
		return fmt.Errorf("%w: this run would mark %d articles as stale and no live article would be left; "+
			"check that every collector ran and returned data, or run again with --allow-mass-stale if the objects really are gone",
			ErrMassStale, newly)
	}
	if float64(newly) <= limit*float64(total) {
		return nil
	}
	return fmt.Errorf("%w: this run would mark %d of %d articles as stale (%.0f%% of the live articles, the limit is %.0f%%) and wrote nothing; "+
		"check that every collector ran and returned data, or run again with --allow-mass-stale if the objects really are gone",
		ErrMassStale, newly, total, 100*float64(newly)/float64(total), 100*limit)
}

// articleText is the text form of an article used for diffing: its title,
// parent and stale tag are shown as header lines above the content.
func articleText(title, parentKey, content string, stale bool) string {
	var b strings.Builder
	b.WriteString("Title: " + title + "\n")
	if parentKey != "" {
		b.WriteString("Parent: " + parentKey + "\n")
	}
	if stale {
		b.WriteString("Tags: " + StaleTag + "\n")
	}
	b.WriteString("\n" + content)
	return b.String()
}
