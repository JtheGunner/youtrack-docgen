package client

import (
	"context"
	"net/http"
	"net/url"
)

// GetArticle fetches one article by ID. Production code reads articles through
// listing calls; the tests use this single-resource GET as a request vehicle.
func (c *Client) GetArticle(ctx context.Context, id string) (*Article, error) {
	var a Article
	path := "/api/articles/" + url.PathEscape(id) + "?fields=" + url.QueryEscape(articleFields)
	if err := c.do(ctx, http.MethodGet, path, nil, &a); err != nil {
		return nil, err
	}
	return &a, nil
}
