// Package client is a small REST client for YouTrack Knowledge Base articles.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	articleFields = "id,idReadable,summary,content,project(id),parentArticle(id,idReadable),tags(id,name)"
	maxErrorBody  = 512
	searchPage    = 100
)

// Article is a Knowledge Base article.
type Article struct {
	ID            string   `json:"id,omitempty"`
	IDReadable    string   `json:"idReadable,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	Content       string   `json:"content,omitempty"`
	Project       *Ref     `json:"project,omitempty"`
	ParentArticle *Ref     `json:"parentArticle,omitempty"`
	Tags          []TagRef `json:"tags,omitempty"`
}

// Ref references an entity by database ID.
type Ref struct {
	ID         string `json:"id"`
	IDReadable string `json:"idReadable,omitempty"`
}

// TagRef references a tag by ID; Name is filled in responses.
type TagRef struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// NewArticle describes an article to create.
type NewArticle struct {
	ProjectID       string
	Summary         string
	Content         string
	ParentArticleID string
	TagIDs          []string
}

// ArticleUpdate describes changes to an article. Nil fields stay unchanged.
type ArticleUpdate struct {
	Summary         *string
	Content         *string
	ParentArticleID *string
}

// APIError is returned for non-successful HTTP responses. It never contains
// the access token.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("youtrack: %s %s: status %d", e.Method, e.Path, e.StatusCode)
	switch e.StatusCode {
	case http.StatusUnauthorized:
		msg += " (authentication failed: check the YouTrack token)"
	case http.StatusForbidden:
		msg += " (permission denied for the configured token)"
	case http.StatusNotFound:
		msg += " (not found)"
	case http.StatusTooManyRequests:
		msg += " (rate limit still exceeded after retries)"
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP client used for requests.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithMaxRetries sets how often a rate-limited request is retried.
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// WithBackoff sets the base and maximum delay used when the server gives no
// Retry-After hint.
func WithBackoff(base, maxDelay time.Duration) Option {
	return func(c *Client) { c.baseDelay, c.maxDelay = base, maxDelay }
}

// WithSleep replaces the context-aware sleep function, mainly for tests.
func WithSleep(f func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.sleep = f }
}

// Client talks to the YouTrack REST API.
type Client struct {
	baseURL    string
	token      string
	http       *http.Client
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	sleep      func(context.Context, time.Duration) error
}

// New creates a Client. baseURL is the YouTrack base URL, token a permanent token.
func New(baseURL, token string, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("youtrack: base URL must be absolute, e.g. https://youtrack.example.com")
	}
	if token == "" {
		return nil, errors.New("youtrack: token must not be empty")
	}
	c := &Client{
		baseURL:    u.String(),
		token:      token,
		http:       &http.Client{Timeout: 30 * time.Second},
		maxRetries: 5,
		baseDelay:  time.Second,
		maxDelay:   30 * time.Second,
		sleep:      sleepContext,
	}
	for _, o := range opts {
		o(c)
	}
	c.http = withSameOriginRedirects(c.http, u)
	return c, nil
}

// withSameOriginRedirects returns a copy of h that refuses redirects leaving
// the base URL's scheme and host (including port), so the bearer token is
// never sent elsewhere. A CheckRedirect already set on h still applies after
// this check. The caller's client is not modified.
func withSameOriginRedirects(h *http.Client, base *url.URL) *http.Client {
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	clone := *h
	inner := h.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != base.Scheme || req.URL.Host != base.Host {
			return fmt.Errorf("youtrack: refusing redirect to a different origin (%s://%s): the token is only sent to the configured base URL", req.URL.Scheme, req.URL.Host)
		}
		if inner != nil {
			return inner(req, via)
		}
		if len(via) >= 10 {
			return errors.New("youtrack: stopped after 10 redirects")
		}
		return nil
	}
	return &clone
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// SearchArticles returns all articles matching a YouTrack search query.
func (c *Client) SearchArticles(ctx context.Context, query string) ([]Article, error) {
	var out []Article
	for skip := 0; ; skip += searchPage {
		q := url.Values{}
		q.Set("query", query)
		q.Set("fields", articleFields)
		q.Set("$top", strconv.Itoa(searchPage))
		q.Set("$skip", strconv.Itoa(skip))
		var batch []Article
		if err := c.do(ctx, http.MethodGet, "/api/articles?"+q.Encode(), nil, &batch); err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < searchPage {
			return out, nil
		}
	}
}

// GetArticle fetches one article by ID.
func (c *Client) GetArticle(ctx context.Context, id string) (*Article, error) {
	var a Article
	path := "/api/articles/" + url.PathEscape(id) + "?fields=" + url.QueryEscape(articleFields)
	if err := c.do(ctx, http.MethodGet, path, nil, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// CreateArticle creates an article, optionally below a parent and with tags.
func (c *Client) CreateArticle(ctx context.Context, n NewArticle) (*Article, error) {
	if n.ProjectID == "" || n.Summary == "" {
		return nil, errors.New("youtrack: project ID and summary are required")
	}
	body := Article{Summary: n.Summary, Content: n.Content, Project: &Ref{ID: n.ProjectID}}
	if n.ParentArticleID != "" {
		body.ParentArticle = &Ref{ID: n.ParentArticleID}
	}
	for _, id := range n.TagIDs {
		body.Tags = append(body.Tags, TagRef{ID: id})
	}
	var a Article
	path := "/api/articles?fields=" + url.QueryEscape(articleFields)
	if err := c.do(ctx, http.MethodPost, path, body, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateArticle changes summary, content and/or parent of an article.
func (c *Client) UpdateArticle(ctx context.Context, id string, u ArticleUpdate) (*Article, error) {
	if id == "" {
		return nil, errors.New("youtrack: article ID is required")
	}
	body := map[string]any{}
	if u.Summary != nil {
		body["summary"] = *u.Summary
	}
	if u.Content != nil {
		body["content"] = *u.Content
	}
	if u.ParentArticleID != nil {
		body["parentArticle"] = Ref{ID: *u.ParentArticleID}
	}
	var a Article
	path := "/api/articles/" + url.PathEscape(id) + "?fields=" + url.QueryEscape(articleFields)
	if err := c.do(ctx, http.MethodPost, path, body, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// AddTag attaches an existing tag to an article.
func (c *Client) AddTag(ctx context.Context, articleID, tagID string) error {
	path := "/api/articles/" + url.PathEscape(articleID) + "/tags?fields=id"
	return c.do(ctx, http.MethodPost, path, TagRef{ID: tagID}, nil)
}

// RemoveTag detaches a tag from an article.
func (c *Client) RemoveTag(ctx context.Context, articleID, tagID string) error {
	path := "/api/articles/" + url.PathEscape(articleID) + "/tags/" + url.PathEscape(tagID)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// FindTag returns the tag with the exact name, or nil if none exists.
func (c *Client) FindTag(ctx context.Context, name string) (*TagRef, error) {
	q := url.Values{}
	q.Set("query", name)
	q.Set("fields", "id,name")
	var tags []TagRef
	if err := c.do(ctx, http.MethodGet, "/api/tags?"+q.Encode(), nil, &tags); err != nil {
		return nil, err
	}
	for i := range tags {
		if tags[i].Name == name {
			return &tags[i], nil
		}
	}
	return nil, nil
}

// CreateTag creates a tag.
func (c *Client) CreateTag(ctx context.Context, name string) (*TagRef, error) {
	var t TagRef
	if err := c.do(ctx, http.MethodPost, "/api/tags?fields=id,name", TagRef{Name: name}, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return fmt.Errorf("youtrack: encode request: %w", err)
		}
	}
	for attempt := 0; ; attempt++ {
		status, header, body, err := c.send(ctx, method, path, payload)
		if err != nil {
			return err
		}
		if status == http.StatusTooManyRequests && attempt < c.maxRetries {
			if err := c.sleep(ctx, c.delay(header, attempt)); err != nil {
				return fmt.Errorf("youtrack: waiting for rate limit: %w", err)
			}
			continue
		}
		if status < 200 || status > 299 {
			return &APIError{Method: method, Path: stripQuery(path), StatusCode: status, Message: errorMessage(body)}
		}
		if out == nil || len(body) == 0 {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("youtrack: decode response of %s %s: %w", method, stripQuery(path), err)
		}
		return nil
	}
}

func (c *Client) send(ctx context.Context, method, path string, payload []byte) (int, http.Header, []byte, error) {
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("youtrack: build request for %s %s", method, stripQuery(path))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, nil, fmt.Errorf("youtrack: %s %s: %w", method, stripQuery(path), ctxErr)
		}
		// The *url.Error is unwrapped so request URLs never leak into messages.
		cause := errors.Unwrap(err)
		if cause == nil {
			cause = errors.New("request failed")
		}
		return 0, nil, nil, fmt.Errorf("youtrack: %s %s: %w", method, stripQuery(path), cause)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("youtrack: read response of %s %s: %w", method, stripQuery(path), err)
	}
	return resp.StatusCode, resp.Header, body, nil
}

// delay honors Retry-After (seconds) and otherwise backs off exponentially.
func (c *Client) delay(h http.Header, attempt int) time.Duration {
	if s := h.Get("Retry-After"); s != "" {
		if secs, err := strconv.Atoi(s); err == nil && secs >= 0 {
			return min(time.Duration(secs)*time.Second, c.maxDelay)
		}
	}
	d := c.baseDelay << min(attempt, 20)
	if d <= 0 || d > c.maxDelay {
		return c.maxDelay
	}
	return d
}

func stripQuery(path string) string {
	p, _, _ := strings.Cut(path, "?")
	return p
}

func errorMessage(body []byte) string {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(body, &e) == nil && (e.Error != "" || e.Description != "") {
		return strings.TrimSpace(e.Error + " " + e.Description)
	}
	s := strings.TrimSpace(string(body))
	if len(s) > maxErrorBody {
		s = s[:maxErrorBody] + "..."
	}
	return s
}
