// Package youtrack publishes rendered documents to the YouTrack Knowledge
// Base. Generated sections are wrapped in docgen:begin/end markers; only those
// blocks are replaced, everything else in an article stays untouched.
package youtrack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JtheGunner/youtrackt-docgen/internal/model"
	"github.com/JtheGunner/youtrackt-docgen/internal/render"
	"github.com/JtheGunner/youtrackt-docgen/internal/sink/youtrack/client"
)

// Keys of the container articles between the root and the pages.
const (
	hostsKey      = "group:hosts"
	kubernetesKey = "group:kubernetes"
	overviewPref  = "overview:"
)

// API is the part of the YouTrack client the sink needs.
type API interface {
	TagAPI
	SearchArticles(ctx context.Context, query string) ([]client.Article, error)
	CreateArticle(ctx context.Context, n client.NewArticle) (*client.Article, error)
	UpdateArticle(ctx context.Context, id string, u client.ArticleUpdate) (*client.Article, error)
}

// Config configures a Sink.
type Config struct {
	// ProjectID is the database ID of the project that owns the articles.
	ProjectID string
	// ProjectQuery is the YouTrack search query that lists every article of
	// that project, for example "project: KB". Documents are looked up among
	// these articles by their stable key.
	ProjectQuery string
}

// Result counts what Publish did.
type Result struct {
	Created   int
	Updated   int
	Unchanged int
	// Stale counts articles newly marked as stale; Restored counts articles
	// whose stale marking was removed because the object reappeared.
	Stale    int
	Restored int
}

// Sink writes documents to YouTrack.
type Sink struct {
	api API
	cfg Config
}

// New creates a Sink.
func New(api API, cfg Config) (*Sink, error) {
	if api == nil {
		return nil, errors.New("youtrack sink: API must not be nil")
	}
	if cfg.ProjectID == "" || cfg.ProjectQuery == "" {
		return nil, errors.New("youtrack sink: project ID and project query are required")
	}
	return &Sink{api: api, cfg: cfg}, nil
}

// Publish writes the document tree below root. The root becomes a top-level
// article; overviews sit directly below it, host documents below a "Hosts"
// article and Kubernetes documents below a "Kubernetes" article. A document is
// recognized by its stable ID, not its title, so renames update the existing
// article. Nothing is deleted; articles of vanished objects get the stale tag
// and a banner, both removed again when the object reappears. A run on unchanged data makes no write call
// (it only reads the project's articles once).
func (s *Sink) Publish(ctx context.Context, root render.Document) (Result, error) {
	existing, err := s.index(ctx)
	if err != nil {
		return Result{}, err
	}
	p := &publisher{Sink: s, existing: existing}
	rootArticle, err := p.publish(ctx, root, "")
	if err != nil {
		return p.res, err
	}
	groups := map[string]string{} // group key -> article ID
	for _, child := range root.Children {
		parent := rootArticle.ID
		if key, title := groupFor(child.ID); key != "" {
			if _, ok := groups[key]; !ok {
				g, err := p.publish(ctx, render.Document{ID: key, Title: title}, rootArticle.ID)
				if err != nil {
					return p.res, err
				}
				groups[key] = g.ID
			}
			parent = groups[key]
		}
		if err := p.publishTree(ctx, child, parent); err != nil {
			return p.res, err
		}
	}
	return p.res, p.reconcileStale(ctx, publishedKeys(root))
}

// groupFor returns the container a document belongs to, or "" for documents
// that sit directly below the root.
func groupFor(id string) (key, title string) {
	if strings.HasPrefix(id, overviewPref) {
		return "", ""
	}
	if _, kind, _, err := model.ParseID(model.ID(id)); err == nil {
		switch kind {
		case model.KindCluster, model.KindNamespace, model.KindWorkload, model.KindIngress:
			return kubernetesKey, "Kubernetes"
		}
	}
	return hostsKey, "Hosts"
}

// index maps document keys to the articles that carry them.
func (s *Sink) index(ctx context.Context) (map[string]client.Article, error) {
	articles, err := s.api.SearchArticles(ctx, s.cfg.ProjectQuery)
	if err != nil {
		return nil, fmt.Errorf("youtrack sink: list articles: %w", err)
	}
	out := map[string]client.Article{}
	for _, a := range articles {
		key, ok := documentKey(a.Content)
		if !ok {
			continue
		}
		if prev, dup := out[key]; dup {
			return nil, fmt.Errorf("youtrack sink: document key %q is used by articles %s and %s", key, prev.IDReadable, a.IDReadable)
		}
		out[key] = a
	}
	return out, nil
}

type publisher struct {
	*Sink
	existing map[string]client.Article
	res      Result
}

func (p *publisher) publishTree(ctx context.Context, d render.Document, parentID string) error {
	a, err := p.publish(ctx, d, parentID)
	if err != nil {
		return err
	}
	for _, c := range d.Children {
		if err := p.publishTree(ctx, c, a.ID); err != nil {
			return err
		}
	}
	return nil
}

// publish creates or updates the article of d and returns it.
func (p *publisher) publish(ctx context.Context, d render.Document, parentID string) (client.Article, error) {
	if err := checkMarkerToken(d.ID); err != nil {
		return client.Article{}, err
	}
	blocks := make([]block, 0, len(d.Sections))
	for _, sec := range d.Sections {
		b, err := newBlock(sec.ID, sec.Markdown())
		if err != nil {
			return client.Article{}, err
		}
		blocks = append(blocks, b)
	}

	cur, found := p.existing[d.ID]
	if !found {
		created, err := p.api.CreateArticle(ctx, client.NewArticle{
			ProjectID:       p.cfg.ProjectID,
			Summary:         d.Title,
			Content:         buildContent(d.ID, blocks),
			ParentArticleID: parentID,
		})
		if err != nil {
			return client.Article{}, fmt.Errorf("youtrack sink: create %q: %w", d.ID, err)
		}
		p.res.Created++
		p.existing[d.ID] = *created
		return *created, nil
	}

	var upd client.ArticleUpdate
	changed := false
	if cur.Summary != d.Title {
		upd.Summary, changed = &d.Title, true
	}
	if content := mergeContent(cur.Content, blocks); content != cur.Content {
		upd.Content, changed = &content, true
	}
	if parentID != "" && (cur.ParentArticle == nil || cur.ParentArticle.ID != parentID) {
		upd.ParentArticleID, changed = &parentID, true
	}
	if !changed {
		p.res.Unchanged++
		return cur, nil
	}
	updated, err := p.api.UpdateArticle(ctx, cur.ID, upd)
	if err != nil {
		return client.Article{}, fmt.Errorf("youtrack sink: update %q: %w", d.ID, err)
	}
	p.res.Updated++
	p.existing[d.ID] = *updated
	return *updated, nil
}
