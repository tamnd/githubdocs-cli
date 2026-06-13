// Package githubdocs is the library behind the ghdocs command line:
// the HTTP client, request shaping, and the typed data models for GitHub Docs.
//
// The Client here is the spine every command shares. It sets a real
// User-Agent, paces requests so a busy session stays polite, and retries the
// transient failures (429 and 5xx) that any public site throws under load.
package githubdocs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultUserAgent identifies the client to GitHub Docs. A real, honest
// User-Agent is both polite and the thing most likely to keep you unblocked.
const DefaultUserAgent = "ghdocs/dev (+https://github.com/tamnd/githubdocs-cli)"

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = fmt.Errorf("not found")

// Config holds all tuneable parameters for the Client. Callers fill a Config
// (or start from DefaultConfig) and pass it to NewClient.
type Config struct {
	// BaseURL is the root of the GitHub Docs site, without a trailing slash.
	BaseURL   string
	Rate      time.Duration
	Retries   int
	UserAgent string
	Timeout   time.Duration
}

// DefaultConfig returns a Config with production-ready defaults.
func DefaultConfig() Config {
	return Config{
		BaseURL:   "https://docs.github.com",
		Rate:      200 * time.Millisecond,
		Retries:   5,
		UserAgent: DefaultUserAgent,
		Timeout:   30 * time.Second,
	}
}

// Client talks to GitHub Docs over HTTP.
type Client struct {
	cfg  Config
	http *http.Client
	last time.Time
}

// NewClient returns a Client configured by cfg.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// Article is a single search result from GitHub Docs.
type Article struct {
	Title      string `json:"title"`
	Breadcrumb string `json:"breadcrumb"`
	URL        string `json:"url"`
	Excerpt    string `json:"excerpt"`
}

// markRE strips <mark>…</mark> HTML tags left by the search highlight API.
var markRE = regexp.MustCompile(`</?mark>`)

// Search queries the GitHub Docs search endpoint and returns up to limit results.
// If limit <= 0, the server default (10) is used.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Article, error) {
	// Use /api/search/v1 with client_name=docs to get a direct JSON response
	// without the redirect that /search performs to the HTML page.
	u := c.cfg.BaseURL + "/api/search/v1"
	params := url.Values{
		"query":       {query},
		"version":     {"free-pro-team"},
		"language":    {"en"},
		"client_name": {"docs"},
	}
	if limit > 0 {
		params.Set("size", fmt.Sprintf("%d", limit))
	}
	endpoint := u + "?" + params.Encode()

	body, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	articles := make([]Article, 0, len(resp.Hits))
	for _, h := range resp.Hits {
		a := Article{
			Title:      markRE.ReplaceAllString(h.Title, ""),
			Breadcrumb: h.Breadcrumbs,
			URL:        c.cfg.BaseURL + h.URL,
		}
		// pick the best snippet from highlights
		if len(h.Highlights.Title) > 0 {
			a.Excerpt = markRE.ReplaceAllString(
				strings.Join(h.Highlights.Title, " "), "")
		} else if len(h.Highlights.Content) > 0 {
			raw := h.Highlights.Content[0]
			a.Excerpt = markRE.ReplaceAllString(raw, "")
			if len([]rune(a.Excerpt)) > 120 {
				rs := []rune(a.Excerpt)
				a.Excerpt = string(rs[:119]) + "..."
			}
		}
		articles = append(articles, a)
	}
	return articles, nil
}

// searchResponse mirrors the JSON returned by https://docs.github.com/search.
type searchResponse struct {
	Meta struct {
		Found struct {
			Value int `json:"value"`
		} `json:"found"`
		Page int `json:"page"`
		Size int `json:"size"`
	} `json:"meta"`
	Hits []searchHit `json:"hits"`
}

type searchHit struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Breadcrumbs string `json:"breadcrumbs"`
	Highlights  struct {
		Title          []string `json:"title"`
		Content        []string `json:"content"`
		ContentExplict []string `json:"content_explicit"`
	} `json:"highlights"`
}

// get fetches url and returns the response body. It paces and retries
// according to the client's settings.
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) (body []byte, retry bool, err error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// pace blocks until at least Rate has elapsed since the previous request.
func (c *Client) pace() {
	if c.cfg.Rate <= 0 {
		return
	}
	if wait := c.cfg.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}
