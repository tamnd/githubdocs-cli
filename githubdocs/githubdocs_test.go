package githubdocs_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tamnd/githubdocs-cli/githubdocs"
)

func testClient(baseURL string) *githubdocs.Client {
	cfg := githubdocs.DefaultConfig()
	cfg.BaseURL = baseURL
	cfg.Rate = 0 // no pacing in tests
	cfg.Retries = 3
	return githubdocs.NewClient(cfg)
}

func TestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		if r.URL.Path != "/api/search/v1" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		q := r.URL.Query().Get("query")
		if q == "" {
			t.Error("missing query param")
		}
		resp := map[string]any{
			"meta": map[string]any{
				"found": map[string]any{"value": 2},
				"page":  1,
				"size":  10,
			},
			"hits": []map[string]any{
				{
					"id":          "abc123",
					"url":         "/en/actions/overview",
					"title":       "GitHub <mark>Actions</mark> overview",
					"breadcrumbs": "GitHub Actions",
					"highlights": map[string]any{
						"title":   []string{"GitHub <mark>Actions</mark> overview"},
						"content": []string{"About GitHub <mark>Actions</mark>."},
					},
				},
				{
					"id":          "def456",
					"url":         "/en/actions/concepts",
					"title":       "GitHub <mark>Actions</mark> concepts",
					"breadcrumbs": "GitHub Actions / Concepts",
					"highlights": map[string]any{
						"title":   []string{"GitHub <mark>Actions</mark> concepts"},
						"content": []string{"Core concepts for GitHub <mark>Actions</mark>."},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	articles, err := c.Search(context.Background(), "actions", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(articles) != 2 {
		t.Fatalf("got %d articles, want 2", len(articles))
	}
	if articles[0].Title != "GitHub Actions overview" {
		t.Errorf("title = %q, want mark tags stripped", articles[0].Title)
	}
	if articles[0].Breadcrumb != "GitHub Actions" {
		t.Errorf("breadcrumb = %q", articles[0].Breadcrumb)
	}
	if articles[0].URL != srv.URL+"/en/actions/overview" {
		t.Errorf("url = %q", articles[0].URL)
	}
}

func TestSearchRetriesOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		resp := map[string]any{
			"meta": map[string]any{"found": map[string]any{"value": 0}, "page": 1, "size": 10},
			"hits": []any{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	start := time.Now()
	_, err := c.Search(context.Background(), "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Error("retries did not back off")
	}
}

func TestSearchEmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"meta": map[string]any{"found": map[string]any{"value": 0}, "page": 1, "size": 10},
			"hits": []any{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	articles, err := c.Search(context.Background(), "xyzzy-no-match", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(articles) != 0 {
		t.Errorf("got %d articles, want 0", len(articles))
	}
}
