package netbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const pageSize = "100"

// maxResponseBytes bounds a single API response body.
const maxResponseBytes = 64 << 20

type client struct {
	base  *url.URL
	token string
	http  *http.Client
}

type page[T any] struct {
	Next    string `json:"next"`
	Results []T    `json:"results"`
}

// fetchAll reads every page of a list endpoint. Only the first request is
// built from the endpoint path; later ones follow the "next" link, which must
// stay on the configured host so the token is never sent elsewhere.
func fetchAll[T any](ctx context.Context, c *client, endpoint string) ([]T, error) {
	first := c.base.JoinPath(endpoint)
	q := first.Query()
	q.Set("limit", pageSize)
	first.RawQuery = q.Encode()

	var all []T
	seen := map[string]bool{}
	next := first.String()
	for next != "" {
		if seen[next] {
			return nil, fmt.Errorf("netbox %s: pagination loop at %s", endpoint, next)
		}
		seen[next] = true

		var p page[T]
		if err := c.getJSON(ctx, next, &p); err != nil {
			return nil, fmt.Errorf("netbox %s: %w", endpoint, err)
		}
		all = append(all, p.Results...)

		if p.Next == "" {
			break
		}
		resolved, err := c.sameHost(p.Next)
		if err != nil {
			return nil, fmt.Errorf("netbox %s: %w", endpoint, err)
		}
		next = resolved
	}
	return all, nil
}

func (c *client) sameHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid next link: %w", err)
	}
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host {
		return "", errors.New("next link points to a different host")
	}
	return u.String(), nil
}

func (c *client) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %s", redact(err.Error(), c.token))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func redact(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "***")
}
