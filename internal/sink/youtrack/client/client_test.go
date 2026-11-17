package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fakeToken = "perm:fake-token-value"

type sleepRecorder struct{ delays []time.Duration }

func (s *sleepRecorder) sleep(_ context.Context, d time.Duration) error {
	s.delays = append(s.delays, d)
	return nil
}

func newTestClient(t *testing.T, h http.HandlerFunc, opts ...Option) (*Client, *sleepRecorder) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	rec := &sleepRecorder{}
	opts = append([]Option{WithSleep(rec.sleep), WithBackoff(time.Second, 10*time.Second)}, opts...)
	c, err := New(srv.URL, fakeToken, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c, rec
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name, url, token string
	}{
		{"empty token", "https://youtrack.example.com", ""},
		{"relative url", "youtrack.example.com", "t"},
		{"empty url", "", "t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.url, tt.token); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSearchArticlesSendsAuthAndPaginates(t *testing.T) {
	var calls int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer "+fakeToken {
			t.Errorf("auth header = %q", got)
		}
		if r.URL.Path != "/api/articles" || r.URL.Query().Get("query") != "project: KB host" {
			t.Errorf("unexpected request %s", r.URL)
		}
		n := 100
		if r.URL.Query().Get("$skip") == "100" {
			n = 1
		}
		list := make([]Article, n)
		for i := range list {
			list[i] = Article{ID: "1-1", Summary: "host"}
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	got, err := c.SearchArticles(context.Background(), "project: KB host")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 101 || calls != 2 {
		t.Fatalf("got %d articles in %d calls", len(got), calls)
	}
}

func TestCreateArticleBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/articles" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["summary"] != "web01" || in["content"] != "body" {
			t.Errorf("body = %v", in)
		}
		if in["project"].(map[string]any)["id"] != "0-1" || in["parentArticle"].(map[string]any)["id"] != "1-9" {
			t.Errorf("refs = %v", in)
		}
		tags := in["tags"].([]any)
		if len(tags) != 1 || tags[0].(map[string]any)["id"] != "6-1" {
			t.Errorf("tags = %v", tags)
		}
		_ = json.NewEncoder(w).Encode(Article{ID: "1-10", IDReadable: "KB-A-10"})
	})
	a, err := c.CreateArticle(context.Background(), NewArticle{
		ProjectID: "0-1", Summary: "web01", Content: "body", ParentArticleID: "1-9", TagIDs: []string{"6-1"},
	})
	if err != nil || a.ID != "1-10" {
		t.Fatalf("got %+v, %v", a, err)
	}
}

func TestCreateArticleRequiresFields(t *testing.T) {
	c, _ := newTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("no request expected") })
	if _, err := c.CreateArticle(context.Background(), NewArticle{Summary: "x"}); err == nil {
		t.Fatal("expected error for missing project")
	}
	if _, err := c.CreateArticle(context.Background(), NewArticle{ProjectID: "0-1"}); err == nil {
		t.Fatal("expected error for missing summary")
	}
}

func TestUpdateArticleSendsOnlySetFields(t *testing.T) {
	tests := []struct {
		name string
		upd  ArticleUpdate
		want []string
	}{
		{"content only", ArticleUpdate{Content: ptr("c")}, []string{"content"}},
		{"all", ArticleUpdate{Summary: ptr("s"), Content: ptr("c"), ParentArticleID: ptr("1-2")}, []string{"summary", "content", "parentArticle"}},
		{"empty content is sent", ArticleUpdate{Content: ptr("")}, []string{"content"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/articles/1-5" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				var in map[string]any
				_ = json.NewDecoder(r.Body).Decode(&in)
				if len(in) != len(tt.want) {
					t.Errorf("body keys = %v, want %v", in, tt.want)
				}
				for _, k := range tt.want {
					if _, ok := in[k]; !ok {
						t.Errorf("missing key %q", k)
					}
				}
				_ = json.NewEncoder(w).Encode(Article{ID: "1-5"})
			})
			if _, err := c.UpdateArticle(context.Background(), "1-5", tt.upd); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTags(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tags":
			_ = json.NewEncoder(w).Encode([]TagRef{{ID: "6-2", Name: "docgen-stale-old"}, {ID: "6-1", Name: "docgen-stale"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/tags":
			_ = json.NewEncoder(w).Encode(TagRef{ID: "6-3", Name: "new"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/articles/1-5/tags":
			var in TagRef
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.ID != "6-1" {
				t.Errorf("tag id = %q", in.ID)
			}
			_, _ = io.WriteString(w, `{"id":"6-1"}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/articles/1-5/tags/6-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	})
	ctx := context.Background()
	tag, err := c.FindTag(ctx, "docgen-stale")
	if err != nil || tag == nil || tag.ID != "6-1" {
		t.Fatalf("FindTag = %+v, %v", tag, err)
	}
	if tag, err := c.FindTag(ctx, "missing"); err != nil || tag != nil {
		t.Fatalf("FindTag(missing) = %+v, %v", tag, err)
	}
	if tag, err := c.CreateTag(ctx, "new"); err != nil || tag.ID != "6-3" {
		t.Fatalf("CreateTag = %+v, %v", tag, err)
	}
	if err := c.AddTag(ctx, "1-5", "6-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveTag(ctx, "1-5", "6-1"); err != nil {
		t.Fatal(err)
	}
}

func TestRateLimitBackoff(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		want       []time.Duration
	}{
		{"exponential", "", []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}},
		{"retry-after", "3", []time.Duration{3 * time.Second, 3 * time.Second, 3 * time.Second}},
		{"retry-after capped", "999", []time.Duration{10 * time.Second, 10 * time.Second, 10 * time.Second}},
		{"invalid retry-after", "soon", []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) <= 3 {
					if tt.retryAfter != "" {
						w.Header().Set("Retry-After", tt.retryAfter)
					}
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				_, _ = io.WriteString(w, `{"id":"1-1"}`)
			})
			if _, err := c.GetArticle(context.Background(), "1-1"); err != nil {
				t.Fatal(err)
			}
			if len(rec.delays) != len(tt.want) {
				t.Fatalf("delays = %v, want %v", rec.delays, tt.want)
			}
			for i := range tt.want {
				if rec.delays[i] != tt.want[i] {
					t.Errorf("delay[%d] = %v, want %v", i, rec.delays[i], tt.want[i])
				}
			}
		})
	}
}

func TestRateLimitRetriesResendBody(t *testing.T) {
	var hits atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"summary":"s"`) {
			t.Errorf("attempt %d lost body: %q", hits.Load()+1, b)
		}
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"id":"1-1"}`)
	})
	if _, err := c.UpdateArticle(context.Background(), "1-1", ArticleUpdate{Summary: ptr("s")}); err != nil {
		t.Fatal(err)
	}
}

func TestRateLimitExhausted(t *testing.T) {
	var hits atomic.Int32
	c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}, WithMaxRetries(2))
	_, err := c.GetArticle(context.Background(), "1-1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 3 || len(rec.delays) != 2 {
		t.Fatalf("hits = %d, sleeps = %d", hits.Load(), len(rec.delays))
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("message not clear: %v", err)
	}
}

func TestSleepCancelled(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}, WithSleep(func(context.Context, time.Duration) error { return context.Canceled }))
	_, err := c.GetArticle(context.Background(), "1-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorMessagesAreClearAndLeakNoToken(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"unauthorized", 401, `{"error":"Unauthorized","error_description":"Invalid token"}`, "check the YouTrack token"},
		{"forbidden", 403, `{"error":"Forbidden"}`, "permission denied"},
		{"not found", 404, `{"error":"Not Found"}`, "not found"},
		{"server error plain body", 500, "boom " + fakeToken, "status 500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.GetArticle(context.Background(), "1-1")
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q lacks %q", err, tt.want)
			}
			if len(rec.delays) != 0 {
				t.Errorf("non-429 must not be retried")
			}
			if tt.status != 500 && strings.Contains(err.Error(), "?") {
				t.Errorf("query leaked into error: %v", err)
			}
		})
	}
}

func TestTransportErrorLeaksNoToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, err := New(url, fakeToken)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SearchArticles(context.Background(), "secret query")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), fakeToken) || strings.Contains(err.Error(), "secret query") {
		t.Errorf("error leaks data: %v", err)
	}
}

func TestRedirectToOtherOriginNeverReceivesToken(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{"default client", nil},
		{"supplied client", []Option{WithHTTPClient(&http.Client{})}},
		{"supplied client that allows all redirects", []Option{WithHTTPClient(&http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return nil },
		})}},
	}
	for _, status := range []int{http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, tt := range tests {
			t.Run(tt.name+"/"+http.StatusText(status), func(t *testing.T) {
				var targetHits atomic.Int32
				var leaked atomic.Bool
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					targetHits.Add(1)
					if strings.Contains(r.Header.Get("Authorization"), fakeToken) {
						leaked.Store(true)
					}
					_, _ = io.WriteString(w, `{"id":"1-1"}`)
				}))
				t.Cleanup(target.Close)

				c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, target.URL+"/api/articles/1-1", status)
				}, tt.opts...)
				_, err := c.GetArticle(context.Background(), "1-1")
				if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
					t.Fatalf("err = %v", err)
				}
				if strings.Contains(err.Error(), fakeToken) {
					t.Errorf("error leaks token: %v", err)
				}
				if targetHits.Load() != 0 || leaked.Load() {
					t.Errorf("redirect target was contacted (hits=%d, leaked=%v)", targetHits.Load(), leaked.Load())
				}
			})
		}
	}
}

func TestRedirectToOtherSchemeIsRefused(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+"/api/articles/1-1", http.StatusFound)
	})
	if _, err := c.GetArticle(context.Background(), "1-1"); err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("err = %v", err)
	}
}

func TestSameOriginRedirectIsFollowed(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			if r.Header.Get("Authorization") != "Bearer "+fakeToken {
				t.Error("token missing on same-origin redirect")
			}
			_, _ = io.WriteString(w, `{"id":"1-1"}`)
			return
		}
		http.Redirect(w, r, "/moved", http.StatusTemporaryRedirect)
	})
	if _, err := c.GetArticle(context.Background(), "1-1"); err != nil {
		t.Fatal(err)
	}
}

func TestSuppliedHTTPClientIsNotMutated(t *testing.T) {
	h := &http.Client{}
	if _, err := New("https://youtrack.example.com", fakeToken, WithHTTPClient(h)); err != nil {
		t.Fatal(err)
	}
	if h.CheckRedirect != nil {
		t.Error("caller's client was modified")
	}
}

func TestInvalidJSONResponse(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html>") })
	if _, err := c.GetArticle(context.Background(), "1-1"); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v", err)
	}
}

func ptr(s string) *string { return &s }

func TestFindProjectMatchesShortNameAcrossPages(t *testing.T) {
	var skips []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/admin/projects" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+fakeToken {
			t.Errorf("auth header = %q", got)
		}
		skip := r.URL.Query().Get("$skip")
		skips = append(skips, skip)
		var page []Project
		if skip == "0" {
			for i := 0; i < searchPage; i++ {
				page = append(page, Project{ID: "0-" + strconv.Itoa(i), ShortName: "P" + strconv.Itoa(i)})
			}
		} else {
			page = []Project{{ID: "0-777", ShortName: "INFRA", Name: "Infrastructure"}}
		}
		_ = json.NewEncoder(w).Encode(page)
	})

	p, err := c.FindProject(context.Background(), "infra")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.ID != "0-777" {
		t.Fatalf("project = %+v, want ID 0-777", p)
	}
	if strings.Join(skips, ",") != "0,100" {
		t.Errorf("skips = %v, want two pages", skips)
	}
}

func TestFindProjectReturnsNilWhenUnknown(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]Project{{ID: "0-1", ShortName: "OTHER", Name: "INFRA"}})
	})
	p, err := c.FindProject(context.Background(), "INFRA")
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Errorf("project = %+v, want nil: only the short name identifies a project", p)
	}
}
