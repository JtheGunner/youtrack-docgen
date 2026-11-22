// Package youtrack publishes rendered documents to the YouTrack Knowledge
// Base. Generated sections are wrapped in docgen:begin/end markers; only those
// blocks are replaced, everything else in an article stays untouched.
package youtrack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
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
	// that project, for example "project: {KB}". Documents are looked up among
	// these articles by their stable key; articles of any other project are
	// ignored, whatever the query returns.
	ProjectQuery string
	// ActiveSources names the sources (the first part of an entity ID) that
	// took part in the run. Only articles of these sources can be marked as
	// stale: a collector that was switched off did not see its objects vanish.
	// Empty means every source.
	ActiveSources []string
	// ForceStaleIDs are IDs of documents whose object was removed on purpose
	// (denied by a rule). Their articles are cleaned and marked as stale even
	// when their source is not in ActiveSources, so denied content does not
	// stay in the Knowledge Base because a collection was incomplete.
	ForceStaleIDs []string
	// MaxStaleRatio is the largest share of the managed articles one run may
	// mark as stale, between 0 and 1. A run above it fails before it writes.
	// Zero means DefaultMaxStaleRatio.
	MaxStaleRatio float64
	// AllowMassStale lifts the MaxStaleRatio limit for the run.
	AllowMassStale bool
}

func (c Config) maxStaleRatio() float64 {
	if c.MaxStaleRatio == 0 {
		return DefaultMaxStaleRatio
	}
	return c.MaxStaleRatio
}

func (c Config) validate() error {
	if c.ProjectID == "" || c.ProjectQuery == "" {
		return errors.New("youtrack sink: project ID and project query are required")
	}
	if c.MaxStaleRatio < 0 || c.MaxStaleRatio > 1 {
		return fmt.Errorf("youtrack sink: max stale ratio %v must be between 0 and 1", c.MaxStaleRatio)
	}
	return nil
}

// Result counts what Publish did.
type Result struct {
	Created   int
	Updated   int
	Unchanged int
	// Stale counts articles marked as stale; Restored counts articles
	// whose stale marking was removed because the object reappeared.
	Stale    int
	Restored int
	// Warnings describe articles the run left alone on purpose, such as an
	// article that lost its document marker. See orphans.
	Warnings []string
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
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Sink{api: api, cfg: cfg}, nil
}

// Publish writes the document tree below root. The root becomes a top-level
// article; overviews sit directly below it, host documents below a "Hosts"
// article and Kubernetes documents below a "Kubernetes" article. A document is
// recognized by its stable ID, not its title, so renames update the existing
// article. Nothing is deleted; articles of vanished objects lose their
// generated sections and get the stale tag and a banner, both removed again
// when the object reappears. A run on unchanged data makes no write call (it
// only reads the project's articles once).
//
// What a run does is decided first, as a list of operations that the Planner
// shows as well. A run that would mark more than Config.MaxStaleRatio of the
// articles as stale returns ErrMassStale before the first write.
func (s *Sink) Publish(ctx context.Context, root render.Document) (Result, error) {
	existing, lost, err := s.index(ctx)
	if err != nil {
		return Result{}, err
	}
	list, err := buildOps(existing, lost, root, s.cfg)
	res := Result{Warnings: lost.all()}
	if list != nil {
		res.Warnings = append(res.Warnings, list.warnings...)
	}
	if err != nil {
		return res, err
	}
	x := &executor{api: s.api, cfg: s.cfg, ids: map[string]string{}}
	for key, a := range existing {
		x.ids[key] = a.ID
	}
	for key, id := range list.adopted {
		x.ids[key] = id
	}
	for _, o := range list.ops {
		if err := x.run(ctx, o, &res); err != nil {
			res.Warnings = append(lost.claimedWarnings(), list.warnings...)
			return res, err
		}
	}
	return res, nil
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
func (s *Sink) index(ctx context.Context) (map[string]client.Article, *orphans, error) {
	return loadIndex(ctx, s.api, s.cfg)
}

// loadIndex reads the articles of the project and maps document keys to them.
func loadIndex(ctx context.Context, api Reader, cfg Config) (map[string]client.Article, *orphans, error) {
	articles, err := api.SearchArticles(ctx, cfg.ProjectQuery)
	if err != nil {
		return nil, nil, fmt.Errorf("youtrack sink: list articles: %w", err)
	}
	out := map[string]client.Article{}
	lost := &orphans{byTitle: map[string]client.Article{}}
	for _, a := range articles {
		// The search query is only a hint. An article is ours only if the
		// server says it belongs to the configured project.
		if a.Project == nil || a.Project.ID != cfg.ProjectID {
			continue
		}
		key, ok := documentKey(a.Content)
		if !ok {
			lost.add(a)
			continue
		}
		if prev, dup := out[key]; dup {
			return nil, nil, fmt.Errorf("youtrack sink: document key %q is used by articles %s and %s", key, prev.IDReadable, a.IDReadable)
		}
		out[key] = a
	}
	return out, lost, nil
}

// executor carries out the operations of a run.
type executor struct {
	api API
	cfg Config
	// ids maps document keys to article database IDs.
	ids map[string]string
	tag *client.TagRef // resolved lazily, only when an article needs tagging
}

func (x *executor) run(ctx context.Context, o op, res *Result) error {
	switch o.kind {
	case opCreate:
		created, err := x.api.CreateArticle(ctx, client.NewArticle{
			ProjectID: x.cfg.ProjectID, Summary: o.title, Content: o.content, ParentArticleID: x.ids[o.parentKey],
		})
		if err != nil {
			return fmt.Errorf("youtrack sink: create %q: %w", o.key, err)
		}
		x.ids[o.key] = created.ID
		res.Created++
	case opUpdate:
		var upd client.ArticleUpdate
		if o.setTitle {
			upd.Summary = &o.title
		}
		if o.setContent {
			upd.Content = &o.content
		}
		if o.setParent {
			parent := x.ids[o.parentKey]
			upd.ParentArticleID = &parent
		}
		if _, err := x.api.UpdateArticle(ctx, o.article.ID, upd); err != nil {
			return fmt.Errorf("youtrack sink: update %q: %w", o.key, err)
		}
		res.Updated++
	case opUnchanged:
		res.Unchanged++
	case opMarkStale:
		if o.setContent {
			if _, err := x.api.UpdateArticle(ctx, o.article.ID, client.ArticleUpdate{Content: &o.content}); err != nil {
				return fmt.Errorf("youtrack sink: mark %q stale: %w", o.key, err)
			}
		}
		if o.addTag {
			if x.tag == nil {
				t, err := x.staleTag(ctx)
				if err != nil {
					return err
				}
				x.tag = t
			}
			if err := x.api.AddTag(ctx, o.article.ID, x.tag.ID); err != nil {
				return fmt.Errorf("youtrack sink: tag %q stale: %w", o.key, err)
			}
		}
		res.Stale++
	case opClearStale:
		if o.setContent {
			if _, err := x.api.UpdateArticle(ctx, o.article.ID, client.ArticleUpdate{Content: &o.content}); err != nil {
				return fmt.Errorf("youtrack sink: remove stale banner from %q: %w", o.key, err)
			}
		}
		if o.removeTagID != "" {
			if err := x.api.RemoveTag(ctx, o.article.ID, o.removeTagID); err != nil {
				return fmt.Errorf("youtrack sink: remove stale tag from %q: %w", o.key, err)
			}
		}
		res.Restored++
	}
	return nil
}
