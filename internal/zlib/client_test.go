package zlib

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testClient points a Client at a local server, which is the only way to
// exercise the EAPI conversation without the live site (which is frequently
// unreachable or behind an anti-bot wall).
func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		Domain:       "test.invalid",
		RemixUserID:  "42",
		RemixUserKey: "key-abc",
		endpoint:     srv.URL,
		http:         &http.Client{Timeout: 5 * time.Second},
		logf:         func(string, ...any) {},
	}
}

func writeJSONBody(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

// TestLoginStoresTokenPair checks the field names the API actually returns:
// the token is "remix_userkey" under a "user" object, and an id that arrives as
// a number has to survive as a string.
func TestLoginStoresTokenPair(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eapi/user/login" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Errorf("login must be form-encoded, got Content-Type %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.PostForm.Get("email") != "user@example.com" {
			t.Errorf("email not sent: %q", r.PostForm.Get("email"))
		}
		writeJSONBody(t, w, map[string]any{
			"success": 1,
			"user":    map[string]any{"id": 42, "remix_userkey": "key-abc", "email": "user@example.com"},
		})
	})
	client.RemixUserID, client.RemixUserKey = "", ""

	id, key, err := client.Login(context.Background(), "user@example.com", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if id != "42" {
		t.Errorf("user id = %q, want \"42\" — a numeric id must render without a decimal point", id)
	}
	if key != "key-abc" {
		t.Errorf("user key = %q", key)
	}
	if !client.LoggedIn() {
		t.Error("client did not record the credentials it just obtained")
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONBody(t, w, map[string]any{"success": 0, "error": "Incorrect email or password"})
	})
	_, _, err := client.Login(context.Background(), "a@b.c", "wrong")
	if err == nil {
		t.Fatal("expected an error for rejected credentials")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if !strings.Contains(apiErr.Message, "Incorrect") {
		t.Errorf("the API's own message was dropped: %q", apiErr.Message)
	}
}

// TestSearchEncodesArrayFilters pins the form encoding: the API reads languages
// and extensions as repeated "name[]" keys, and url.Values.Add reproduces that.
func TestSearchEncodesArrayFilters(t *testing.T) {
	var captured url.Values
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		captured = r.PostForm
		writeJSONBody(t, w, map[string]any{"success": 1, "books": []any{}})
	})

	_, err := client.Search(context.Background(), SearchOptions{
		Query:      "machine learning",
		Limit:      5,
		Languages:  []string{"english", "chinese"},
		Extensions: []string{"pdf"},
		YearFrom:   1990,
		YearTo:     2020,
		Exact:      true,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if got := captured["message"]; len(got) != 1 || got[0] != "machine learning" {
		t.Errorf("message = %v", got)
	}
	if got := captured["limit"]; len(got) != 1 || got[0] != "5" {
		t.Errorf("limit = %v", got)
	}
	langs := captured["languages[]"]
	if len(langs) != 2 || langs[0] != "english" || langs[1] != "chinese" {
		t.Errorf("languages[] = %v, want [english chinese]", langs)
	}
	if exts := captured["extensions[]"]; len(exts) != 1 || exts[0] != "pdf" {
		t.Errorf("extensions[] = %v", exts)
	}
	if captured.Get("yearFrom") != "1990" || captured.Get("yearTo") != "2020" {
		t.Errorf("year bounds = %q..%q", captured.Get("yearFrom"), captured.Get("yearTo"))
	}
	if captured.Get("e") != "1" {
		t.Error("the exact-match flag was not sent")
	}
}

func TestSearchMapsResults(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONBody(t, w, map[string]any{
			"success": 1,
			"books": []any{map[string]any{
				"id":             12345,
				"hash":           "abc123def",
				"title":          "Deep Learning",
				"author":         "Ian Goodfellow",
				"publisher":      "MIT Press",
				"year":           "2016",
				"language":       "english",
				"extension":      "pdf",
				"filesizeString": "22.5 MB",
				"cover":          "https://example.com/c.jpg",
			}},
		})
	})

	books, err := client.Search(context.Background(), SearchOptions{Query: "deep learning"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("got %d books, want 1", len(books))
	}
	b := books[0]
	if b.ID != "12345" || b.Hash != "abc123def" {
		t.Errorf("identifier fields wrong: id=%q hash=%q", b.ID, b.Hash)
	}
	if b.Identifier() != "12345/abc123def" {
		t.Errorf("Identifier() = %q, want the id/hash form a user can paste back", b.Identifier())
	}
	if b.Title != "Deep Learning" || b.Author != "Ian Goodfellow" {
		t.Errorf("title/author wrong: %q / %q", b.Title, b.Author)
	}
	if b.Filesize != "22.5 MB" {
		t.Errorf("filesize = %q", b.Filesize)
	}
	if b.Source != "zlib" {
		t.Errorf("source = %q", b.Source)
	}
}

// TestGetDownloadLinkBuildsFilename checks the "<title> (<author>).<ext>" rule
// the reference clients use, and that a hostile title cannot escape the
// destination directory.
func TestGetDownloadLinkBuildsFilename(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/file") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSONBody(t, w, map[string]any{
			"success": 1,
			"file": map[string]any{
				"downloadLink": "https://cdn.example.com/get/abc",
				"description":  "Deep Learning",
				"author":       "Ian Goodfellow",
				"extension":    "pdf",
			},
		})
	})

	link, err := client.GetDownloadLink(context.Background(), "12345", "abc123")
	if err != nil {
		t.Fatalf("GetDownloadLink: %v", err)
	}
	if link.URL != "https://cdn.example.com/get/abc" {
		t.Errorf("url = %q", link.URL)
	}
	if link.Filename != "Deep Learning (Ian Goodfellow).pdf" {
		t.Errorf("filename = %q, want \"Deep Learning (Ian Goodfellow).pdf\"", link.Filename)
	}
}

func TestGetDownloadLinkRejectsHostileFilename(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONBody(t, w, map[string]any{
			"success": 1,
			"file": map[string]any{
				"downloadLink": "https://cdn.example.com/get/abc",
				"description":  "../../../../tmp/pwned",
				"author":       "",
				"extension":    "pdf",
			},
		})
	})

	link, err := client.GetDownloadLink(context.Background(), "1", "h")
	if err != nil {
		t.Fatalf("GetDownloadLink: %v", err)
	}
	if strings.ContainsAny(link.Filename, `/\`) {
		t.Errorf("filename %q still contains a path separator", link.Filename)
	}
	if strings.HasPrefix(link.Filename, "..") {
		t.Errorf("filename %q is a traversal payload", link.Filename)
	}
}

// TestRelativeDownloadLinkIsMadeAbsolute covers the API returning a rooted path
// rather than a full URL.
func TestRelativeDownloadLinkIsMadeAbsolute(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONBody(t, w, map[string]any{
			"success": 1,
			"file":    map[string]any{"downloadLink": "/dl/abc", "description": "B", "extension": "epub"},
		})
	})
	link, err := client.GetDownloadLink(context.Background(), "1", "h")
	if err != nil {
		t.Fatalf("GetDownloadLink: %v", err)
	}
	if !strings.HasSuffix(link.URL, "/dl/abc") || !strings.HasPrefix(link.URL, "http") {
		t.Errorf("relative link was not made absolute: %q", link.URL)
	}
}

// TestQuotaArithmetic checks the remaining-downloads calculation, including the
// clamp that keeps a stale server count from rendering a negative number.
func TestQuotaArithmetic(t *testing.T) {
	cases := []struct {
		name         string
		limit, today int
		wantLeft     int
	}{
		{"fresh account", 10, 0, 10},
		{"three used", 10, 3, 7},
		{"exhausted", 10, 10, 0},
		{"stale count exceeds limit", 10, 15, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSONBody(t, w, map[string]any{
					"success": 1,
					"user": map[string]any{
						"email":           "user@example.com",
						"downloads_limit": tc.limit,
						"downloads_today": tc.today,
					},
				})
			})
			p, err := client.GetProfile(context.Background())
			if err != nil {
				t.Fatalf("GetProfile: %v", err)
			}
			if p.DownloadsLeft != tc.wantLeft {
				t.Errorf("downloads left = %d, want %d", p.DownloadsLeft, tc.wantLeft)
			}
		})
	}
}

// TestAntiBotWallIsDetected covers every signal DiamWall-fronted domains emit.
// Classifying these distinctly matters: the remedy is to change domains, not to
// retry, and a bare JSON decode error would hide that.
func TestAntiBotWallIsDetected(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"307 self-redirect", http.StatusTemporaryRedirect, ""},
		{"403 block", http.StatusForbidden, "<html>Access Denied</html>"},
		{"513 block", 513, ""},
		{"517 block", 517, ""},
		{"diamwall page with 200", http.StatusOK, "<html><body>__diamwall challenge</body></html>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
				}
			})
			_, err := client.Search(context.Background(), SearchOptions{Query: "x"})
			if err == nil {
				t.Fatal("expected the wall to be reported as an error")
			}
			if !errors.Is(err, ErrDomainWalled) {
				t.Fatalf("expected ErrDomainWalled, got %T: %v", err, err)
			}
			var walled *WalledError
			if !errors.As(err, &walled) {
				t.Fatalf("expected *WalledError, got %T", err)
			}
			if walled.Domain != "test.invalid" {
				t.Errorf("wall error names %q, want the probed domain", walled.Domain)
			}
		})
	}
}

// TestUnstableSuccessFlagShapes covers every form the API has used: 1, true,
// "1", and no flag at all.
func TestUnstableSuccessFlagShapes(t *testing.T) {
	cases := []struct {
		payload map[string]any
		want    bool
	}{
		{map[string]any{"success": float64(1)}, true},
		{map[string]any{"success": true}, true},
		{map[string]any{"success": "1"}, true},
		{map[string]any{"success": "true"}, true},
		{map[string]any{"books": []any{}}, true}, // absent flag means success
		{map[string]any{"success": float64(0)}, false},
		{map[string]any{"success": false}, false},
		{map[string]any{"success": "0"}, false},
	}
	for _, tc := range cases {
		if got := isSuccess(tc.payload); got != tc.want {
			t.Errorf("isSuccess(%v) = %v, want %v", tc.payload, got, tc.want)
		}
	}
}

// TestUnauthenticatedCallsFailEarly keeps a request from being sent with no
// credentials, which would waste a rate-limited attempt.
func TestUnauthenticatedCallsFailEarly(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("a request was sent without credentials")
	})
	client.RemixUserID, client.RemixUserKey = "", ""

	if _, err := client.Search(context.Background(), SearchOptions{Query: "x"}); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("Search: got %v, want ErrNotAuthenticated", err)
	}
	if _, err := client.GetProfile(context.Background()); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("GetProfile: got %v, want ErrNotAuthenticated", err)
	}
	if _, err := client.GetDownloadLink(context.Background(), "1", "h"); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("GetDownloadLink: got %v, want ErrNotAuthenticated", err)
	}
}

// TestDownloadWritesFileAndRejectsEmptyBody covers the atomic write path and the
// zero-byte case, which the site answers with HTTP 200 when the quota is spent.
func TestDownloadWritesFileAndRejectsEmptyBody(t *testing.T) {
	t.Run("writes the file", func(t *testing.T) {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/file"):
				writeJSONBody(t, w, map[string]any{
					"success": 1,
					"file": map[string]any{
						"downloadLink": "http://" + r.Host + "/cdn/book.pdf",
						"description":  "Book",
						"extension":    "pdf",
					},
				})
			case r.URL.Path == "/cdn/book.pdf":
				_, _ = w.Write([]byte("%PDF-1.4 fake payload"))
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
			}
		})

		dir := t.TempDir()
		path, err := client.Download(context.Background(), "1", "h", dir, "")
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read downloaded file: %v", err)
		}
		if string(data) != "%PDF-1.4 fake payload" {
			t.Errorf("content = %q", data)
		}
		if filepath.Dir(path) != dir {
			t.Errorf("file landed outside the requested directory: %s", path)
		}
		// No staging file may survive a successful transfer.
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".zlib-part-") {
				t.Errorf("staging file %s was left behind", e.Name())
			}
		}
	})

	t.Run("rejects an empty body", func(t *testing.T) {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/file") {
				writeJSONBody(t, w, map[string]any{
					"success": 1,
					"file":    map[string]any{"downloadLink": "http://" + r.Host + "/cdn/empty", "description": "E"},
				})
				return
			}
			// HTTP 200 with no bytes is how a spent quota presents itself.
			w.WriteHeader(http.StatusOK)
		})

		dir := t.TempDir()
		if _, err := client.Download(context.Background(), "1", "h", dir, "x.bin"); err == nil {
			t.Fatal("an empty download was reported as success")
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Errorf("a failed download left %d file(s) behind", len(entries))
		}
	})
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be sent for an empty query")
	})
	if _, err := client.Search(context.Background(), SearchOptions{Query: "   "}); err == nil {
		t.Error("expected an error for a blank query")
	}
}
