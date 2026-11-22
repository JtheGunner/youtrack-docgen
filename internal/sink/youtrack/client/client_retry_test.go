package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetriesTransientStatusesOfIdempotentRequests(t *testing.T) {
	for _, status := range []int{429, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var hits atomic.Int32
			c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) <= 2 {
					w.WriteHeader(status)
					return
				}
				_, _ = io.WriteString(w, `{"id":"1-1"}`)
			})
			if _, err := c.GetArticle(context.Background(), "1-1"); err != nil {
				t.Fatal(err)
			}
			if hits.Load() != 3 || len(rec.delays) != 2 {
				t.Errorf("hits = %d, sleeps = %d", hits.Load(), len(rec.delays))
			}
		})
	}
}

func TestServerErrorRetriesAreBounded(t *testing.T) {
	var hits atomic.Int32
	c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}, WithMaxRetries(3))
	_, err := c.GetArticle(context.Background(), "1-1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 4 || len(rec.delays) != 3 {
		t.Errorf("hits = %d, sleeps = %d, want 4 and 3", hits.Load(), len(rec.delays))
	}
}

func TestOtherStatusesAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 500, 501} {
		var hits atomic.Int32
		c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.WriteHeader(status)
		})
		if _, err := c.GetArticle(context.Background(), "1-1"); err == nil {
			t.Fatalf("status %d: expected error", status)
		}
		if hits.Load() != 1 {
			t.Errorf("status %d: hits = %d, want 1", status, hits.Load())
		}
	}
}

func TestCreateIsOnlyRetriedWhenRejectedUnprocessed(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantHits int32
	}{
		{"503 may have created the article", 503, 1},
		{"502 may have created the article", 502, 1},
		{"429 was rejected unprocessed", 429, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) == 1 {
					w.WriteHeader(tt.status)
					return
				}
				_, _ = io.WriteString(w, `{"id":"1-1"}`)
			})
			_, _ = c.CreateArticle(context.Background(), NewArticle{ProjectID: "0-1", Summary: "s"})
			if hits.Load() != tt.wantHits {
				t.Errorf("hits = %d, want %d", hits.Load(), tt.wantHits)
			}
		})
	}
}

func TestIdempotentPostsAreRetried(t *testing.T) {
	var hits atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1)%2 == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"id":"1-1"}`)
	})
	if err := c.AddTag(context.Background(), "1-1", "6-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateArticle(context.Background(), "1-1", ArticleUpdate{Summary: ptr("s")}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 4 {
		t.Errorf("hits = %d, want 4", hits.Load())
	}
}

func TestNetworkErrorsAreRetriedOnlyForIdempotentRequests(t *testing.T) {
	var hits atomic.Int32
	dropFirst := func(n int32) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			if hits.Add(1) <= n {
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
				return
			}
			_, _ = io.WriteString(w, `{"id":"1-1"}`)
		}
	}
	c, rec := newTestClient(t, dropFirst(2))
	if _, err := c.GetArticle(context.Background(), "1-1"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 || len(rec.delays) != 2 {
		t.Errorf("hits = %d, sleeps = %d", hits.Load(), len(rec.delays))
	}

	hits.Store(0)
	c, _ = newTestClient(t, dropFirst(1))
	if _, err := c.CreateArticle(context.Background(), NewArticle{ProjectID: "0-1", Summary: "s"}); err == nil {
		t.Fatal("expected error")
	}
	if hits.Load() != 1 {
		t.Errorf("create after network error: hits = %d, want 1", hits.Load())
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"future date", now.Add(7 * time.Second).Format(http.TimeFormat), 7 * time.Second},
		{"past date", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"capped", now.Add(time.Hour).Format(http.TimeFormat), 10 * time.Second},
		{"seconds", "4", 4 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) == 1 {
					w.Header().Set("Retry-After", tt.header)
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = io.WriteString(w, `{"id":"1-1"}`)
			})
			c.now = func() time.Time { return now }
			if _, err := c.GetArticle(context.Background(), "1-1"); err != nil {
				t.Fatal(err)
			}
			if len(rec.delays) != 1 || rec.delays[0] != tt.want {
				t.Errorf("delays = %v, want [%v]", rec.delays, tt.want)
			}
		})
	}
}

func TestResponseBodyIsLimited(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		chunk := strings.Repeat("a", 1<<20)
		_, _ = io.WriteString(w, `{"id":"`)
		for range maxResponseBody/len(chunk) + 1 {
			_, _ = io.WriteString(w, chunk)
		}
		_, _ = io.WriteString(w, `"}`)
	})
	_, err := c.GetArticle(context.Background(), "1-1")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want size limit error", err)
	}
}

func TestFindTagPaginates(t *testing.T) {
	var pages atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		pages.Add(1)
		skip, _ := strconv.Atoi(r.URL.Query().Get("$skip"))
		var batch []TagRef
		switch skip {
		case 0:
			for i := range searchPage {
				batch = append(batch, TagRef{ID: "x-" + strconv.Itoa(i), Name: "other-" + strconv.Itoa(i)})
			}
		case searchPage:
			batch = []TagRef{{ID: "6-9", Name: "docgen-stale"}}
		}
		_ = json.NewEncoder(w).Encode(batch)
	})
	tag, err := c.FindTag(context.Background(), "docgen-stale")
	if err != nil || tag == nil || tag.ID != "6-9" {
		t.Fatalf("FindTag = %+v, %v", tag, err)
	}
	if pages.Load() != 2 {
		t.Errorf("pages = %d, want 2", pages.Load())
	}
}

func TestErrorTextNeverContainsToken(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"plain echo", "proxy saw Authorization: Bearer " + fakeToken},
		{"json error field", `{"error":"bad ` + fakeToken + `"}`},
		{"json description", `{"error_description":"token ` + fakeToken + ` rejected"}`},
		{"json unicode escape", `{"error":"bad perm:fake-token-value"}`},
		{"straddles truncation", strings.Repeat("x", maxErrorBody-5) + fakeToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.GetArticle(context.Background(), "1-1")
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), fakeToken) || strings.Contains(err.Error(), "fake-token-value") {
				t.Errorf("error leaks token: %q", err)
			}
		})
	}
}
