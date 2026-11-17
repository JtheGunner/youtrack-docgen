package youtrack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/diff"
	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

const diffContext = 3

// Reader is the read-only part of the YouTrack client. A Planner only gets
// this interface, so a dry run cannot write by construction.
type Reader interface {
	SearchArticles(ctx context.Context, query string) ([]client.Article, error)
}

// Change is one article a Publish run would create or update.
type Change struct {
	// Key is the stable document key of the article.
	Key string
	// Create is true when the article does not exist yet.
	Create bool
	// Diff is the unified diff of the article; it is never empty.
	Diff string
}

// Plan is what Publish would do, without having done it.
type Plan struct {
	Changes   []Change
	unchanged int
}

// Created returns the number of articles that would be created.
func (p Plan) Created() int {
	n := 0
	for _, c := range p.Changes {
		if c.Create {
			n++
		}
	}
	return n
}

// Updated returns the number of existing articles that would change.
func (p Plan) Updated() int { return len(p.Changes) - p.Created() }

// Unchanged returns the number of articles that are already up to date.
func (p Plan) Unchanged() int { return p.unchanged }

// Summary is a one-line count of the plan.
func (p Plan) Summary() string {
	return fmt.Sprintf("%d to create, %d to update, %d unchanged", p.Created(), p.Updated(), p.Unchanged())
}

// Diff is the unified diff of every changed article, in publish order.
func (p Plan) Diff() string {
	var b strings.Builder
	for _, c := range p.Changes {
		b.WriteString(c.Diff)
	}
	return b.String()
}

// Planner computes what Publish would change. It only reads from YouTrack.
type Planner struct {
	sink *Sink
}

// NewPlanner creates a Planner for the project described by cfg.
func NewPlanner(api Reader, cfg Config) (*Planner, error) {
	if api == nil {
		return nil, errors.New("youtrack sink: reader must not be nil")
	}
	if cfg.ProjectID == "" || cfg.ProjectQuery == "" {
		return nil, errors.New("youtrack sink: project ID and project query are required")
	}
	return &Planner{sink: &Sink{api: readOnly{api}, cfg: cfg}}, nil
}

// Plan compares the document tree below root with the articles in YouTrack,
// using the same rules as Publish.
func (pl *Planner) Plan(ctx context.Context, root render.Document) (Plan, error) {
	existing, err := pl.sink.index(ctx)
	if err != nil {
		return Plan{}, err
	}
	keyOfID := make(map[string]string, len(existing))
	for key, a := range existing {
		keyOfID[a.ID] = key
	}
	c := &planCollector{existing: existing, keyOfID: keyOfID}
	if err := c.document(root, ""); err != nil {
		return Plan{}, err
	}
	groups := map[string]bool{}
	for _, child := range root.Children {
		parent := root.ID
		if key, title := groupFor(child.ID); key != "" {
			if !groups[key] {
				groups[key] = true
				if err := c.document(render.Document{ID: key, Title: title}, root.ID); err != nil {
					return Plan{}, err
				}
			}
			parent = key
		}
		if err := c.tree(child, parent); err != nil {
			return Plan{}, err
		}
	}
	return c.plan, nil
}

// readOnly adapts a Reader to API. The write methods are never reached: only
// index and the planner use the sink, and neither writes.
type readOnly struct{ Reader }

func (readOnly) CreateArticle(context.Context, client.NewArticle) (*client.Article, error) {
	return nil, errors.New("youtrack sink: dry run must not create articles")
}

func (readOnly) UpdateArticle(context.Context, string, client.ArticleUpdate) (*client.Article, error) {
	return nil, errors.New("youtrack sink: dry run must not update articles")
}

func (readOnly) FindTag(context.Context, string) (*client.TagRef, error) {
	return nil, errors.New("youtrack sink: dry run must not look up tags")
}

func (readOnly) CreateTag(context.Context, string) (*client.TagRef, error) {
	return nil, errors.New("youtrack sink: dry run must not create tags")
}

func (readOnly) AddTag(context.Context, string, string) error {
	return errors.New("youtrack sink: dry run must not tag articles")
}

func (readOnly) RemoveTag(context.Context, string, string) error {
	return errors.New("youtrack sink: dry run must not untag articles")
}

type planCollector struct {
	existing map[string]client.Article
	keyOfID  map[string]string // article database ID -> document key
	plan     Plan
}

func (c *planCollector) tree(d render.Document, parentKey string) error {
	if err := c.document(d, parentKey); err != nil {
		return err
	}
	for _, child := range d.Children {
		if err := c.tree(child, d.ID); err != nil {
			return err
		}
	}
	return nil
}

// document mirrors publisher.publish without any write. parentKey is the key
// of the parent document, or "" for the root.
func (c *planCollector) document(d render.Document, parentKey string) error {
	if err := checkMarkerToken(d.ID); err != nil {
		return err
	}
	blocks, err := sectionBlocks(d)
	if err != nil {
		return err
	}

	cur, found := c.existing[d.ID]
	if !found {
		c.plan.Changes = append(c.plan.Changes, Change{
			Key:    d.ID,
			Create: true,
			Diff:   diff.Unified("/dev/null", "b/"+d.ID, "", articleText(d.Title, parentKey, buildContent(d.ID, blocks)), diffContext),
		})
		return nil
	}

	merged := mergeContent(cur.Content, blocks)
	curParent := ""
	if cur.ParentArticle != nil {
		curParent = c.keyOfID[cur.ParentArticle.ID]
		if curParent == "" {
			curParent = cur.ParentArticle.ID
		}
	}
	wantParent := curParent
	parentMoves := false
	if parentKey != "" {
		parentArticle, parentKnown := c.existing[parentKey]
		parentMoves = !parentKnown || cur.ParentArticle == nil || cur.ParentArticle.ID != parentArticle.ID
		wantParent = parentKey
	}
	if cur.Summary == d.Title && merged == cur.Content && !parentMoves {
		c.plan.unchanged++
		return nil
	}
	text := diff.Unified("a/"+d.ID, "b/"+d.ID,
		articleText(cur.Summary, curParent, cur.Content), articleText(d.Title, wantParent, merged), diffContext)
	c.plan.Changes = append(c.plan.Changes, Change{Key: d.ID, Diff: text})
	return nil
}

// articleText is the text form of an article used for diffing: its title and
// parent are shown as two header lines above the content.
func articleText(title, parentKey, content string) string {
	var b strings.Builder
	b.WriteString("Title: " + title + "\n")
	if parentKey != "" {
		b.WriteString("Parent: " + parentKey + "\n")
	}
	b.WriteString("\n" + content)
	return b.String()
}
