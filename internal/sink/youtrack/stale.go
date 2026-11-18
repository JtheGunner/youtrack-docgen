package youtrack

import (
	"context"
	"fmt"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
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

// staleTag returns the stale tag, creating it when it does not exist.
func (x *executor) staleTag(ctx context.Context) (*client.TagRef, error) {
	t, err := x.api.FindTag(ctx, StaleTag)
	if err != nil {
		return nil, fmt.Errorf("youtrack sink: find tag %q: %w", StaleTag, err)
	}
	if t != nil {
		return t, nil
	}
	if t, err = x.api.CreateTag(ctx, StaleTag); err != nil {
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
	_, _, ok := findBlock(content, id)
	return ok
}

// findBlock returns the offsets of the closed block with the given ID.
func findBlock(content, id string) (start, end int, ok bool) {
	for _, sp := range parseBlocks(content) {
		if sp.closed && sp.id == id {
			return sp.start, sp.end, true
		}
	}
	return 0, 0, false
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
	start, stop, ok := findBlock(content, id)
	if !ok {
		return content
	}
	if strings.HasSuffix(content[:start], "\n\n") {
		start--
	} else if start == 0 && strings.HasPrefix(content[stop:], "\n") {
		stop++
	}
	return content[:start] + content[stop:]
}
