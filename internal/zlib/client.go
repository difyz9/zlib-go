package zlib

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/difyz9/zlib-go/internal/fetch"
	"github.com/difyz9/zlib-go/internal/model"
)

const (
	// apiTimeout bounds a single /eapi call.
	apiTimeout = 30 * time.Second
	// connectTimeout bounds the dial and TLS handshake separately, so a host
	// that accepts a connection and then stalls does not consume the whole
	// request budget.
	connectTimeout = 10 * time.Second
	// ProbeTimeout bounds a domain health probe.
	ProbeTimeout = 8 * time.Second
	// downloadTimeout bounds a book transfer.
	downloadTimeout = 5 * time.Minute
)

// Client talks to one resolved EAPI domain.
type Client struct {
	Domain       string
	RemixUserID  string
	RemixUserKey string

	// endpoint overrides the scheme and host. Requests go to
	// https://<Domain> when it is empty; tests set it to a local server.
	endpoint string

	http     *http.Client
	logf     func(string, ...any)
	progress fetch.ProgressReporter
}

// Options configures a Client.
type Options struct {
	Domain       string
	RemixUserID  string
	RemixUserKey string
	// Proxy is an http(s) or socks5 proxy URL; empty falls back to the standard
	// proxy environment variables.
	Proxy string
	Logf  func(string, ...any)
	// Progress, when set, receives download transfer updates.
	Progress fetch.ProgressReporter
}

// New creates a client bound to the given (already resolved) domain.
func New(opts Options) *Client {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	// A proxy that cannot be parsed is reported through the logger rather than
	// failing construction: a bad proxy URL should not make `zlib help` or
	// `zlib config` unusable.
	transport, err := fetch.NewTransport(opts.Proxy, apiTimeout)
	if err != nil {
		logf("proxy configuration ignored: %v", err)
		transport, _ = fetch.NewTransport("", apiTimeout)
	}
	return &Client{
		Domain:       strings.TrimSuffix(opts.Domain, "/"),
		RemixUserID:  opts.RemixUserID,
		RemixUserKey: opts.RemixUserKey,
		logf:         logf,
		progress:     opts.Progress,
		http: &http.Client{
			Timeout:   apiTimeout,
			Transport: transport,
		},
	}
}

// LoggedIn reports whether the client holds a usable credential pair.
func (c *Client) LoggedIn() bool {
	return c.RemixUserID != "" && c.RemixUserKey != ""
}

func (c *Client) baseURL() string {
	if c.endpoint != "" {
		return strings.TrimSuffix(c.endpoint, "/")
	}
	return "https://" + c.Domain
}

// cookies builds the cookie set every EAPI request carries. siteLanguageV2
// pins the response language to English, which keeps field names and the
// filesizeString formatting stable across requests.
func (c *Client) cookies() string {
	parts := []string{"siteLanguageV2=en"}
	if c.RemixUserID != "" {
		parts = append(parts, "remix_userid="+c.RemixUserID)
	}
	if c.RemixUserKey != "" {
		parts = append(parts, "remix_userkey="+c.RemixUserKey)
	}
	return strings.Join(parts, "; ")
}

// do performs a request and decodes the EAPI JSON envelope, classifying
// anti-bot walls explicitly so callers can react to them by domain.
func (c *Client) do(ctx context.Context, method, path string, form, params url.Values) (map[string]any, error) {
	var body *strings.Reader
	endpoint := c.baseURL() + path
	if method == http.MethodPost && form != nil {
		body = strings.NewReader(form.Encode())
	}
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	var reader *strings.Reader
	if body != nil {
		reader = body
	} else {
		reader = strings.NewReader("")
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fetch.UserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if ck := c.cookies(); ck != "" {
		req.Header.Set("Cookie", ck)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("z-library request %s: %w", path, err)
	}
	defer resp.Body.Close()

	raw, err := fetch.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("z-library read %s: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		if isWalledBody(raw) || walledStatuses[resp.StatusCode] {
			return nil, &WalledError{Domain: c.Domain, Detail: domainStatus(resp.StatusCode)}
		}
		return nil, fmt.Errorf("z-library %s: HTTP %d", path, resp.StatusCode)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		if isWalledBody(raw) {
			return nil, &WalledError{Domain: c.Domain, Detail: "DiamWall block page served with HTTP 200"}
		}
		return nil, fmt.Errorf("z-library %s: response was not JSON (possible block page or upstream change)", path)
	}
	return payload, nil
}

func (c *Client) post(ctx context.Context, path string, form url.Values) (map[string]any, error) {
	return c.do(ctx, http.MethodPost, path, form, nil)
}

func (c *Client) get(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, path, nil, params)
}

// --- Auth ---

// Login exchanges email and password for the remix token pair.
//
// /eapi/user/login is rate limited to roughly ten attempts per hour per IP, so
// callers should cache the returned tokens rather than logging in per run.
func (c *Client) Login(ctx context.Context, email, password string) (userID, userKey string, err error) {
	form := url.Values{"email": {email}, "password": {password}}
	payload, err := c.post(ctx, "/eapi/user/login", form)
	if err != nil {
		return "", "", err
	}
	if !isSuccess(payload) {
		return "", "", &APIError{Operation: "login", Message: errorMessage(payload, "invalid credentials or login rejected")}
	}
	user, ok := payload["user"].(map[string]any)
	if !ok {
		return "", "", &APIError{Operation: "login", Message: "response contained no user object"}
	}
	userID = fetch.JSONString(user, "id")
	userKey = fetch.JSONString(user, "remix_userkey")
	if userID == "" || userKey == "" {
		return "", "", &APIError{Operation: "login", Message: "response contained no remix token pair"}
	}
	c.RemixUserID, c.RemixUserKey = userID, userKey
	return userID, userKey, nil
}

// Profile describes the signed-in account's state, including today's quota.
type Profile struct {
	Email          string `json:"email,omitempty"`
	Name           string `json:"name,omitempty"`
	KindleEmail    string `json:"kindle_email,omitempty"`
	DownloadsLimit int    `json:"downloads_limit"`
	DownloadsToday int    `json:"downloads_today"`
	DownloadsLeft  int    `json:"downloads_left"`
	IsPremium      bool   `json:"is_premium"`
}

// GetProfile fetches account details and the remaining daily download quota.
func (c *Client) GetProfile(ctx context.Context) (*Profile, error) {
	if !c.LoggedIn() {
		return nil, ErrNotAuthenticated
	}
	payload, err := c.get(ctx, "/eapi/user/profile", nil)
	if err != nil {
		return nil, err
	}
	if !isSuccess(payload) {
		return nil, &APIError{Operation: "profile", Message: errorMessage(payload, "profile unavailable")}
	}
	user, ok := payload["user"].(map[string]any)
	if !ok {
		return nil, &APIError{Operation: "profile", Message: "response contained no user object"}
	}

	p := &Profile{
		Email:       fetch.JSONString(user, "email"),
		Name:        fetch.JSONString(user, "name"),
		KindleEmail: fetch.JSONString(user, "kindle_email"),
	}
	// The API omits the limits for some account tiers; 10/day is the free
	// account default and is the value the reference clients assume.
	p.DownloadsLimit = fetch.JSONInt(user, "downloads_limit")
	if p.DownloadsLimit == 0 {
		p.DownloadsLimit = 10
	}
	p.DownloadsToday = fetch.JSONInt(user, "downloads_today")
	p.DownloadsLeft = p.DownloadsLimit - p.DownloadsToday
	if p.DownloadsLeft < 0 {
		p.DownloadsLeft = 0
	}
	if v, ok := user["is_premium"].(bool); ok {
		p.IsPremium = v
	}
	return p, nil
}

// --- Search ---

// SearchOptions are the filters /eapi/book/search accepts.
type SearchOptions struct {
	Query      string
	Limit      int
	Page       int
	YearFrom   int
	YearTo     int
	Languages  []string
	Extensions []string
	Exact      bool
	Order      string
}

// Search runs a keyword search and returns normalized results.
func (c *Client) Search(ctx context.Context, opts SearchOptions) ([]model.Book, error) {
	if !c.LoggedIn() {
		return nil, ErrNotAuthenticated
	}
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("search query must not be empty")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	page := opts.Page
	if page <= 0 {
		page = 1
	}

	// The API reads these as form fields. Array parameters repeat the key with
	// a [] suffix, which url.Values.Add reproduces exactly.
	form := url.Values{
		"message": {opts.Query},
		"limit":   {strconv.Itoa(limit)},
		"page":    {strconv.Itoa(page)},
	}
	if opts.YearFrom > 0 {
		form.Set("yearFrom", strconv.Itoa(opts.YearFrom))
	}
	if opts.YearTo > 0 {
		form.Set("yearTo", strconv.Itoa(opts.YearTo))
	}
	for _, lang := range opts.Languages {
		if lang != "" {
			form.Add("languages[]", lang)
		}
	}
	for _, ext := range opts.Extensions {
		if ext != "" {
			form.Add("extensions[]", ext)
		}
	}
	if opts.Exact {
		form.Set("e", "1")
	}
	if opts.Order != "" {
		form.Set("order", opts.Order)
	}

	payload, err := c.post(ctx, "/eapi/book/search", form)
	if err != nil {
		return nil, err
	}
	if !isSuccess(payload) {
		return nil, &APIError{Operation: "search", Message: errorMessage(payload, "search unavailable")}
	}

	rawBooks, ok := payload["books"].([]any)
	if !ok {
		return nil, nil // a valid empty result set
	}
	books := make([]model.Book, 0, len(rawBooks))
	for _, item := range rawBooks {
		rec, ok := item.(map[string]any)
		if !ok {
			continue
		}
		books = append(books, model.Book{
			Source:    model.SourceZlib,
			ID:        fetch.JSONString(rec, "id"),
			Hash:      fetch.JSONString(rec, "hash"),
			Title:     fetch.JSONString(rec, "title"),
			Author:    fetch.JSONString(rec, "author"),
			Publisher: fetch.JSONString(rec, "publisher"),
			Year:      fetch.JSONString(rec, "year"),
			Language:  fetch.JSONString(rec, "language"),
			Extension: fetch.JSONString(rec, "extension"),
			Filesize:  fetch.JSONString(rec, "filesizeString"),
			Cover:     fetch.JSONString(rec, "cover"),
		})
	}
	return books, nil
}

// --- Detail ---

// GetBookInfo fetches full metadata for one book.
//
// The raw payload is returned alongside the normalized record so the caller can
// surface fields the unified model does not model, such as terms, ratings, and
// the table of contents.
func (c *Client) GetBookInfo(ctx context.Context, id, hash string) (model.Book, map[string]any, error) {
	if !c.LoggedIn() {
		return model.Book{}, nil, ErrNotAuthenticated
	}
	if id == "" || hash == "" {
		return model.Book{}, nil, fmt.Errorf("book id and hash are both required")
	}
	payload, err := c.get(ctx, fmt.Sprintf("/eapi/book/%s/%s", url.PathEscape(id), url.PathEscape(hash)), nil)
	if err != nil {
		return model.Book{}, nil, err
	}
	if !isSuccess(payload) {
		return model.Book{}, nil, &APIError{Operation: "book info", Message: errorMessage(payload, "book unavailable")}
	}

	book := model.Book{
		Source:      model.SourceZlib,
		ID:          id,
		Hash:        hash,
		Title:       fetch.FirstNonEmpty(fetch.JSONString(payload, "title"), fetch.NestedString(payload, "book", "title")),
		Author:      fetch.FirstNonEmpty(fetch.JSONString(payload, "author"), fetch.NestedString(payload, "book", "author")),
		Publisher:   fetch.FirstNonEmpty(fetch.JSONString(payload, "publisher"), fetch.NestedString(payload, "book", "publisher")),
		Year:        fetch.FirstNonEmpty(fetch.JSONString(payload, "year"), fetch.NestedString(payload, "book", "year")),
		Language:    fetch.FirstNonEmpty(fetch.JSONString(payload, "language"), fetch.NestedString(payload, "book", "language")),
		Extension:   fetch.FirstNonEmpty(fetch.JSONString(payload, "extension"), fetch.NestedString(payload, "book", "extension")),
		Filesize:    fetch.JSONString(payload, "filesizeString"),
		Cover:       fetch.JSONString(payload, "cover"),
		Description: fetch.FirstNonEmpty(fetch.JSONString(payload, "description"), fetch.NestedString(payload, "book", "description")),
		ISBN:        fetch.FirstNonEmpty(fetch.JSONString(payload, "isbn"), fetch.NestedString(payload, "book", "isbn")),
		Pages:       fetch.FirstNonEmpty(fetch.JSONString(payload, "pages"), fetch.NestedString(payload, "book", "pages")),
	}
	return book, payload, nil
}

// --- Download ---

// DownloadLink is what /eapi/book/{id}/{hash}/file returns.
type DownloadLink struct {
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	Extension string `json:"extension"`
	Filesize  string `json:"filesize,omitempty"`
}

// GetDownloadLink resolves the direct file URL for a book.
func (c *Client) GetDownloadLink(ctx context.Context, id, hash string) (*DownloadLink, error) {
	if !c.LoggedIn() {
		return nil, ErrNotAuthenticated
	}
	if id == "" || hash == "" {
		return nil, fmt.Errorf("book id and hash are both required")
	}
	payload, err := c.get(ctx, fmt.Sprintf("/eapi/book/%s/%s/file", url.PathEscape(id), url.PathEscape(hash)), nil)
	if err != nil {
		return nil, err
	}
	if !isSuccess(payload) {
		return nil, &APIError{
			Operation: "download link",
			Message:   errorMessage(payload, "download unavailable (quota exhausted or file removed)"),
		}
	}

	// The link arrives nested under "file" on current deployments and at the
	// top level on some older ones; accept either.
	dl := &DownloadLink{
		URL: fetch.FirstNonEmpty(
			fetch.NestedString(payload, "file", "downloadLink"),
			fetch.JSONString(payload, "downloadLink"),
			fetch.JSONString(payload, "url"),
			fetch.JSONString(payload, "link"),
		),
		Extension: fetch.FirstNonEmpty(fetch.NestedString(payload, "file", "extension"), fetch.JSONString(payload, "extension")),
		Filesize:  fetch.FirstNonEmpty(fetch.NestedString(payload, "file", "filesizeString"), fetch.JSONString(payload, "filesizeString")),
	}
	if dl.URL == "" {
		return nil, &APIError{Operation: "download link", Message: "response contained no download link"}
	}
	if strings.HasPrefix(dl.URL, "/") {
		dl.URL = c.baseURL() + dl.URL
	}

	// Build the filename the way the reference clients do: "<title> (<author>).<ext>".
	description := fetch.FirstNonEmpty(fetch.NestedString(payload, "file", "description"), fetch.JSONString(payload, "description"))
	author := fetch.FirstNonEmpty(fetch.NestedString(payload, "file", "author"), fetch.JSONString(payload, "author"))
	if description == "" {
		description = "book_" + id
	}
	name := description
	if author != "" && !strings.Contains(name, author) {
		name += " (" + author + ")"
	}
	if dl.Extension != "" {
		name += "." + strings.TrimPrefix(dl.Extension, ".")
	}
	dl.Filename = fetch.SanitizeFilename(name)
	return dl, nil
}

// Download retrieves a book file into outDir and returns the written path.
func (c *Client) Download(ctx context.Context, id, hash, outDir, filename string) (string, error) {
	link, err := c.GetDownloadLink(ctx, id, hash)
	if err != nil {
		return "", err
	}
	if filename == "" {
		filename = link.Filename
	}
	filename = fetch.SanitizeFilename(filename)
	if filename == "" {
		filename = "book_" + id + ".bin"
	}
	return downloadBook(ctx, c.http, link.URL, outDir, filename, c.cookies(), c.logf, c.progress)
}

// --- envelope helpers ---

// isSuccess accepts every shape the API has used for its success flag: 1,
// true, "1", and an object with no flag at all (treated as success).
func isSuccess(payload map[string]any) bool {
	v, ok := payload["success"]
	if !ok {
		return true
	}
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	default:
		return true
	}
}

func errorMessage(payload map[string]any, fallback string) string {
	for _, key := range []string{"error", "message", "msg"} {
		if s := fetch.JSONString(payload, key); s != "" {
			return s
		}
	}
	if nested := fetch.NestedString(payload, "error", "message"); nested != "" {
		return nested
	}
	return fallback
}
