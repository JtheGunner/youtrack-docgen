package youtrack

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/JtheGunner/youtrackt-docgen/internal/render"
	"github.com/JtheGunner/youtrackt-docgen/internal/sink/youtrack/client"
)

const (
	// StaleTag is the tag that marks articles of vanished objects.
	StaleTag = "docgen-stale"

	staleBlockID = "stale-banner"
	staleBanner  = "> **Stale:** this object no longer appears in the documented sources. " +
		"The article is kept so manual content is not lost. " +
		"The notice and the " + StaleTag + " tag are removed automatically if the object reappears.\n"
)

// TagAPI is the part of the YouTrack client needed to tag stale articles.
type TagAPI interface {
	FindTag(ctx context.Context, name string) (*client.TagRef, error)
	CreateTag(ctx context.Context, name string) (*client.TagRef, error)
	AddTag(ctx context.Context, articleID, tagID string) error
	RemoveTag(ctx context.Context, articleID, tagID string) error
}

// publishedKeys returns the keys of every article Publish writes for root,
// including the container articles.
func publishedKeys(root render.Document) map[string]bool {
	keys := map[string]bool{}
	var walk func(d render.Document)
	walk = func(d render.Document) {
		keys[d.ID] = true
		for _, c := range d.Children {
			walk(c)
		}
	}
	walk(root)
	for _, child := range root.Children {
		if key, _ := groupFor(child.ID); key != "" {
			keys[key] = true
		}
	}
	return keys
}

// reconcileStale marks articles whose key was not published as stale and
// clears the marking from articles that are published again. Articles are
// never deleted. It writes nothing when every article is already in the
// right state.
func (p *publisher) reconcileStale(ctx context.Context, published map[string]bool) error {
	keys := make([]string, 0, len(p.existing))
	for key := range p.existing {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var tag *client.TagRef // resolved lazily, only when an article needs tagging
	for _, key := range keys {
		a := p.existing[key]
		if published[key] {
			if err := p.clearStale(ctx, key, a); err != nil {
				return err
			}
			continue
		}
		if tag == nil && !hasStaleTag(a) {
			t, err := p.staleTag(ctx)
			if err != nil {
				return err
			}
			tag = t
		}
		if err := p.markStale(ctx, key, a, tag); err != nil {
			return err
		}
	}
	return nil
}

func (p *publisher) markStale(ctx context.Context, key string, a client.Article, tag *client.TagRef) error {
	needsBanner, needsTag := !hasBlock(a.Content, staleBlockID), !hasStaleTag(a)
	if needsBanner {
		content := insertBanner(a.Content)
		if _, err := p.api.UpdateArticle(ctx, a.ID, client.ArticleUpdate{Content: &content}); err != nil {
			return fmt.Errorf("youtrack sink: add stale banner to %q: %w", key, err)
		}
	}
	if needsTag {
		if err := p.api.AddTag(ctx, a.ID, tag.ID); err != nil {
			return fmt.Errorf("youtrack sink: tag %q stale: %w", key, err)
		}
	}
	if needsBanner || needsTag {
		p.res.Stale++
	}
	return nil
}

func (p *publisher) clearStale(ctx context.Context, key string, a client.Article) error {
	hasBanner, tagID := hasBlock(a.Content, staleBlockID), staleTagID(a)
	if !hasBanner && tagID == "" {
		return nil
	}
	if hasBanner {
		content := removeBlock(a.Content, staleBlockID)
		if _, err := p.api.UpdateArticle(ctx, a.ID, client.ArticleUpdate{Content: &content}); err != nil {
			return fmt.Errorf("youtrack sink: remove stale banner from %q: %w", key, err)
		}
	}
	if tagID != "" {
		if err := p.api.RemoveTag(ctx, a.ID, tagID); err != nil {
			return fmt.Errorf("youtrack sink: remove stale tag from %q: %w", key, err)
		}
	}
	p.res.Restored++
	return nil
}

// staleTag returns the stale tag, creating it when it does not exist.
func (p *publisher) staleTag(ctx context.Context) (*client.TagRef, error) {
	t, err := p.api.FindTag(ctx, StaleTag)
	if err != nil {
		return nil, fmt.Errorf("youtrack sink: find tag %q: %w", StaleTag, err)
	}
	if t != nil {
		return t, nil
	}
	if t, err = p.api.CreateTag(ctx, StaleTag); err != nil {
		return nil, fmt.Errorf("youtrack sink: create tag %q: %w", StaleTag, err)
	}
	return t, nil
}

func staleTagID(a client.Article) string {
	for _, t := range a.Tags {
		if t.Name == StaleTag {
			return t.ID
		}
	}
	return ""
}

func hasStaleTag(a client.Article) bool { return staleTagID(a) != "" }

func hasBlock(content, id string) bool {
	return strings.Contains(content, "<!-- docgen:begin "+id+" ")
}

// insertBanner places the stale banner block directly below the document
// marker, or at the top when the article carries none.
func insertBanner(content string) string {
	rendered := block{id: staleBlockID, hash: "00", body: staleBanner}.render()
	loc := docMarkerRe.FindStringIndex(content)
	if loc == nil {
		return rendered + "\n" + content
	}
	at := loc[1]
	if strings.HasPrefix(content[at:], "\n") {
		at++
	}
	return content[:at] + "\n" + rendered + content[at:]
}

// removeBlock deletes the marked block with the given ID together with the
// blank line that insertBanner put in front of it.
func removeBlock(content, id string) string {
	for _, loc := range beginRe.FindAllStringSubmatchIndex(content, -1) {
		if content[loc[2]:loc[3]] != id {
			continue
		}
		end := strings.Index(content[loc[1]:], endMarker(id))
		if end < 0 {
			return content
		}
		start, stop := loc[0], loc[1]+end+len(endMarker(id))
		if strings.HasSuffix(content[:start], "\n\n") {
			start--
		} else if start == 0 && strings.HasPrefix(content[stop:], "\n") {
			stop++
		}
		return content[:start] + content[stop:]
	}
	return content
}
