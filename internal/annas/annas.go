// Package annas implements Anna's Archive search and fast download natively.
//
// The sibling zlib-download-skill project shells out to an external
// annas-mcp binary and parses its human-readable text output, which is both an
// extra install step and a brittle contract. This package speaks to the site
// directly:
//
//   - search scrapes /search?q=..., because no search API exists;
//   - downloads use the documented fast-download JSON API.
//
// The API key travels as a URL query parameter, so it is only ever attached to
// hosts on the trusted list. Anna's Archive domains lapse and get re-registered
// by squatters — annas-archive.li was a parking page as of 2026-03 — and
// sending the key to whoever now owns such a domain would disclose it.
package annas

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"zlib/internal/fetch"
	"zlib/internal/model"
)

// TrustedHosts are the only hosts the API key may be sent to.
var TrustedHosts = map[string]bool{
	"annas-archive.gl": true,
	"annas-archive.pk": true,
	"annas-archive.gd": true,
}

// ErrUntrustedHost is returned when the configured base URL is not a known
// Anna's Archive domain.
var ErrUntrustedHost = errors.New("annas: refusing to send the API key to an unverified host")

// ErrNoSecretKey is returned when a download is requested without a key.
var ErrNoSecretKey = errors.New("annas: ANNAS_SECRET_KEY is not configured")

const (
	apiTimeout = 40 * time.Second
	// downloadTimeout covers the transfer itself; Anna's Archive files can be
	// tens of megabytes and the mirror throttles.
	downloadTimeout = 5 * time.Minute
)

// Client talks to one Anna's Archive host.
type Client struct {
	BaseURL   string
	SecretKey string

	http *http.Client
	logf func(string, ...any)
}

// Options configures a Client.
type Options struct {
	BaseURL   string
	SecretKey string
	// Proxy is an http(s) or socks5 proxy URL; empty falls back to the standard
	// proxy environment variables.
	Proxy string
	Logf  func(string, ...any)
}

// New creates a client.
//
// The base URL is validated against the trusted list up front when a key is
// present, so a misconfiguration fails immediately and loudly rather than
// silently disclosing the key on the first download.
func New(opts Options) (*Client, error) {
	base := strings.TrimSuffix(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://annas-archive.gl"
	}
	host := fetch.HostOf(base)
	if opts.SecretKey != "" && !TrustedHosts[host] {
		return nil, fmt.Errorf(
			"%w: %q. The fast-download API passes the key as a URL parameter and lapsed "+
				"Anna's Archive domains get re-registered by squatters. Set ANNAS_BASE_URL to a "+
				"known domain (%s) or add your verified domain to annas.TrustedHosts",
			ErrUntrustedHost, host, trustedList())
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	transport, err := fetch.NewTransport(opts.Proxy, apiTimeout)
	if err != nil {
		logf("proxy configuration ignored: %v", err)
		transport, _ = fetch.NewTransport("", apiTimeout)
	}
	return &Client{
		BaseURL:   base,
		SecretKey: opts.SecretKey,
		logf:      logf,
		http: &http.Client{
			Timeout:   apiTimeout,
			Transport: transport,
		},
	}, nil
}

// Host returns the host this client talks to.
func (c *Client) Host() string { return fetch.HostOf(c.BaseURL) }

// HTTPClient exposes the underlying client so the doctor command can probe the
// host with the same transport, timeout, and proxy the real requests use.
func (c *Client) HTTPClient() *http.Client { return c.http }

// Search scrapes the search results page and returns normalized hits.
//
// Each record renders two /md5/ anchors: a cover image with empty text, then
// the title. Only text-bearing anchors are kept, otherwise every result comes
// back titled "Unknown" from the cover link.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]model.Book, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("search query must not be empty")
	}
	body, err := c.fetch(ctx, c.BaseURL+"/search?q="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	doc, err := parseHTML(body)
	if err != nil {
		return nil, fmt.Errorf("annas: parse search page: %w", err)
	}

	books := make([]model.Book, 0, max(limit, 0))
	seen := make(map[string]bool)

	for _, link := range findAll(doc, func(n *node) bool {
		return n.isElement("a") && strings.HasPrefix(n.attr("href"), "/md5/")
	}) {
		title := link.text()
		if title == "" {
			continue
		}
		href := link.attr("href")
		md5 := href[strings.LastIndex(href, "/")+1:]
		if md5 == "" || seen[md5] {
			continue
		}
		seen[md5] = true

		books = append(books, buildBook(link, md5, title, c.BaseURL))
		if limit > 0 && len(books) >= limit {
			break
		}
	}
	return books, nil
}

// buildBook assembles one result from its title anchor.
func buildBook(link *node, md5, title, baseURL string) model.Book {
	book := model.Book{
		Source: model.SourceAnnas,
		Hash:   md5,
		Title:  title,
		URL:    baseURL + "/md5/" + md5,
	}

	if card := link.parent(); card != nil {
		// Author and publisher carry semantic icon markers, which survive the
		// generated Tailwind class churn that would break a layout selector.
		if author := iconText(card, "mdi--user-edit"); author != "" {
			book.Author = author
		}
		if publisher := iconText(card, "mdi--company"); publisher != "" {
			// The company-marked line is usually "<publisher>, <year>", but on
			// records with no publisher it degrades to a bare number — commonly
			// a year, sometimes a sentinel like 0. No publisher is purely
			// numeric, so the whole class is rejected rather than only the
			// years the year parser happens to cover.
			if !isAllDigits(publisher) {
				book.Publisher = publisher
			}
		}
	}

	meta := findMetadata(link)
	book.Language = meta["language"]
	book.Extension = meta["extension"]
	book.Filesize = meta["size"]
	book.Year = meta["year"]
	book.ContentType = meta["content_type"]
	if prov := meta["provenance"]; prov != "" {
		// Which OTHER sources hold this same file, as claimed by Anna's.
		// Surfaced only: choosing a source from it is a routing decision that
		// belongs to the caller, not to a single source's adapter.
		for _, part := range strings.Split(prov, "/") {
			if part = strings.TrimSpace(part); part != "" {
				book.Provenance = append(book.Provenance, part)
			}
		}
	}
	return book
}

// DownloadResult carries the resolved URL and the account's remaining quota.
type DownloadResult struct {
	URL           string `json:"url"`
	DownloadsLeft int    `json:"downloads_left,omitempty"`
	DownloadsDay  int    `json:"downloads_per_day,omitempty"`
	DownloadsDone int    `json:"downloads_done_today,omitempty"`
	HasQuota      bool   `json:"has_quota"`
}

// GetDownloadURL resolves the fast-download URL for an MD5.
//
// domain_index is pinned to 1: index 0 answers with SSL errors, which is the
// behaviour the reference implementation documents after hitting it in practice.
func (c *Client) GetDownloadURL(ctx context.Context, md5 string) (*DownloadResult, error) {
	if c.SecretKey == "" {
		return nil, ErrNoSecretKey
	}
	if !TrustedHosts[c.Host()] {
		return nil, fmt.Errorf("%w: %s", ErrUntrustedHost, c.Host())
	}
	if strings.TrimSpace(md5) == "" {
		return nil, fmt.Errorf("md5 must not be empty")
	}

	params := url.Values{
		"md5":          {md5},
		"key":          {c.SecretKey},
		"domain_index": {"1"},
	}
	// The key is in the query string; never log this URL.
	body, err := c.fetch(ctx, c.BaseURL+"/dyn/api/fast_download.json?"+params.Encode())
	if err != nil {
		return nil, err
	}
	payload, err := fetch.DecodeJSON(body)
	if err != nil {
		return nil, fmt.Errorf("annas: fast-download response was not JSON (HTTP error page or block page?)")
	}
	if msg := fetch.JSONString(payload, "error"); msg != "" {
		return nil, fmt.Errorf("annas fast-download API error: %s", msg)
	}
	downloadURL := fetch.JSONString(payload, "download_url")
	if downloadURL == "" {
		return nil, fmt.Errorf("annas: fast-download response contained no download_url")
	}

	res := &DownloadResult{URL: downloadURL}
	if acct, ok := payload["account_fast_download_info"].(map[string]any); ok {
		res.HasQuota = true
		res.DownloadsLeft = fetch.JSONInt(acct, "downloads_left")
		res.DownloadsDay = fetch.JSONInt(acct, "downloads_per_day")
		res.DownloadsDone = fetch.JSONInt(acct, "downloads_done_today")
	}
	return res, nil
}

// Download fetches an MD5's file into outDir using the fast-download API.
//
// filename is used as given when non-empty; the fast-download endpoint serves
// with a Content-Disposition header only sometimes, so callers that know the
// book's real name should pass one.
func (c *Client) Download(ctx context.Context, md5, outDir, filename string) (string, error) {
	res, err := c.GetDownloadURL(ctx, md5)
	if err != nil {
		return "", err
	}
	if filename == "" {
		filename = md5 + ".pdf"
	}
	c.logf("annas fast-download URL resolved for %s", md5)
	path, err := fetch.DownloadToFile(ctx, c.http, res.URL, outDir, filename, "", downloadTimeout, c.logf)
	if err != nil {
		return "", err
	}
	if res.HasQuota {
		c.logf("anna's archive quota remaining: %d/%d today", res.DownloadsLeft, res.DownloadsDay)
	}
	return path, nil
}

// fetch performs a GET and returns the body, mapping transport and status
// failures onto errors that name the host.
func (c *Client) fetch(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fetch.UserAgent)
	req.Header.Set("Accept", "text/html,application/json,*/*")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("annas request to %s failed: %w", c.Host(), err)
	}
	defer resp.Body.Close()

	body, err := fetch.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		// Anna's Archive sits behind DDoS-Guard, which answers a plain HTTP
		// client with a 302 to ?check=1 and then a 403 challenge page whose
		// little JavaScript has to run before the real page is served. Naming
		// that explicitly matters: without it the user sees "403" and
		// reasonably concludes their key is wrong.
		if isBrowserChallenge(body) {
			return nil, fmt.Errorf(
				"annas: %s is behind a DDoS-Guard browser challenge, which a command-line "+
					"HTTP client cannot pass.\n"+
					"  Workarounds: route this host through a proxy (zlib config set --proxy ...), "+
					"use a different network, or search Z-Library instead (--source zlib)",
				c.Host())
		}
		return nil, fmt.Errorf("annas: HTTP %d from %s — the API key was rejected or the request was blocked",
			resp.StatusCode, c.Host())
	default:
		return nil, &fetch.StatusError{Code: resp.StatusCode, Host: c.Host(), URL: endpoint}
	}
}

// isBrowserChallenge reports whether a response body is an anti-bot interstitial
// rather than the requested page.
func isBrowserChallenge(body []byte) bool {
	lower := strings.ToLower(string(body))
	for _, marker := range []string{"ddos-guard", "__ddg", "checking your browser", "just a moment", "cf-challenge"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	// Interstitials are tiny; a real search page is tens of kilobytes.
	return len(body) < 4096 && strings.Contains(lower, "browser")
}

func trustedList() string {
	out := make([]string, 0, len(TrustedHosts))
	for h := range TrustedHosts {
		out = append(out, h)
	}
	// Insertion sort keeps the message stable across runs without pulling in
	// the sort package for three elements.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return strings.Join(out, ", ")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
