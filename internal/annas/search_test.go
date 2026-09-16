package annas

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture reads a captured page from testdata.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// titleAnchors returns the text-bearing /md5/ anchors of a parsed page, which
// is exactly the selection Search performs.
func titleAnchors(t *testing.T, body []byte) []*node {
	t.Helper()
	doc, err := parseHTML(body)
	if err != nil {
		t.Fatalf("parseHTML: %v", err)
	}
	return findAll(doc, func(n *node) bool {
		return n.isElement("a") && strings.HasPrefix(n.attr("href"), "/md5/") && n.text() != ""
	})
}

// TestParseSearchResults uses a page captured from the live site.
//
// The extraction half of Search is testable offline precisely because the site
// is often behind an anti-bot wall: the parser still has to be correct on the
// days the client cannot reach it, and a live test would fail for reasons that
// have nothing to do with the code.
func TestParseSearchResults(t *testing.T) {
	body := loadFixture(t, "search_results.html")
	anchors := titleAnchors(t, body)

	// The fixture holds four records, each rendering TWO /md5/ anchors: a cover
	// with no text and the title. Counting anchors rather than text-bearing ones
	// yields eight "results", four of them titled from the cover image.
	if len(anchors) != 4 {
		t.Fatalf("found %d text-bearing /md5/ anchors, want 4", len(anchors))
	}

	books := make([]string, 0, len(anchors))
	seen := map[string]bool{}
	for _, a := range anchors {
		href := a.attr("href")
		md5 := href[strings.LastIndex(href, "/")+1:]
		if seen[md5] {
			continue
		}
		seen[md5] = true
		b := buildBook(a, md5, a.text(), "https://annas-archive.gl")
		books = append(books, b.Title)

		if b.Hash == "" {
			t.Error("a parsed book has no md5")
		}
		if b.URL == "" {
			t.Error("a parsed book has no URL")
		}
		if b.Source != "annas" {
			t.Errorf("source = %q, want annas", b.Source)
		}
	}

	// Titles are the load-bearing field: the fixture exists because a
	// dedupe-by-first-anchor bug titled every result "Unknown".
	wantTitles := []string{
		"Phenomenology of spirit",
		"Phenomenology of Spirit",
		"Decoherence through interaction with the environment",
		"Phenomenology of spirit",
	}
	for i, want := range wantTitles {
		if i >= len(books) {
			break
		}
		if books[i] != want {
			t.Errorf("title[%d] = %q, want %q", i, books[i], want)
		}
	}
	for i, title := range books {
		if title == "" || strings.EqualFold(title, "Unknown") {
			t.Errorf("title[%d] = %q — the cover anchor was selected instead of the title anchor", i, title)
		}
	}
}

// TestMetadataStripIsNotPositional pins the documented contract: the
// "·"-separated strip has optional segments in no fixed order, and one fixture
// record omits the year. A positional parser shifts year into extension on
// exactly that record.
func TestMetadataStripIsNotPositional(t *testing.T) {
	cases := []struct {
		name   string
		strip  string
		expect map[string]string
	}{
		{
			name:  "complete record",
			strip: "English [en] · AZW3 · 0.6MB · 1977 · 📘 Book (non-fiction) · 🚀/lgli/zlib",
			expect: map[string]string{
				"language": "en", "extension": "azw3", "size": "0.6MB", "year": "1977",
				"provenance": "lgli/zlib",
			},
		},
		{
			name:  "year omitted",
			strip: "English [en] · PDF · 6.7MB · 📗 Book (unknown) · 🚀/upload",
			expect: map[string]string{
				"language": "en", "extension": "pdf", "size": "6.7MB", "provenance": "upload",
			},
		},
		{
			name:  "no size segment",
			strip: "English [en] · EPUB · 2008 · 🚀/zlib",
			expect: map[string]string{
				"language": "en", "extension": "epub", "year": "2008", "provenance": "zlib",
			},
		},
		{
			name:  "pre-1500 imprint",
			strip: "Latin [la] · PDF · 12MB · 1492 · 🚀/lgli",
			expect: map[string]string{
				"language": "la", "extension": "pdf", "size": "12MB", "year": "1492",
			},
		},
		{
			name:  "djvu with several upstream sources",
			strip: "English [en] · DJVU · 3.1MB · 1955 · 🚀/lgli/nexusstc/zlib",
			expect: map[string]string{
				"language": "en", "extension": "djvu", "size": "3.1MB", "year": "1955",
				"provenance": "lgli/nexusstc/zlib",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStrip(tc.strip)
			for k, want := range tc.expect {
				if got[k] != want {
					t.Errorf("segment %q = %q, want %q (all: %v)", k, got[k], want, got)
				}
			}
			if want, ok := tc.expect["year"]; ok && got["extension"] == want {
				t.Errorf("year %q leaked into the extension field", want)
			}
		})
	}
}

// TestMetadataRejectsInlineJavaScript guards against capturing the page's own
// script, which contains the separator inside a template literal:
// icons.filter(Boolean).join(` · `)
func TestMetadataRejectsInlineJavaScript(t *testing.T) {
	body := loadFixture(t, "search_results.html")
	for _, a := range titleAnchors(t, body) {
		for k, v := range findMetadata(a) {
			if len(v) > 80 {
				t.Errorf("segment %q parsed as %d chars, which looks like captured markup: %q", k, len(v), v)
			}
			for _, marker := range []string{"function", "icons", "const ", "{", "}"} {
				if strings.Contains(v, marker) {
					t.Errorf("segment %q captured script content (%q): %q", k, marker, v)
				}
			}
		}
	}
}

// TestBuildBookExtractsAuthorAndPublisher checks the icon-anchored lookups,
// which is what survives the site's generated class-name churn.
func TestBuildBookExtractsAuthorAndPublisher(t *testing.T) {
	body := loadFixture(t, "search_results.html")
	anchors := titleAnchors(t, body)
	if len(anchors) == 0 {
		t.Fatal("no anchors to inspect")
	}

	foundAuthor := false
	for _, a := range anchors {
		href := a.attr("href")
		md5 := href[strings.LastIndex(href, "/")+1:]
		b := buildBook(a, md5, a.text(), "https://annas-archive.gl")
		if b.Author != "" {
			foundAuthor = true
		}
		// A publisher that is purely numeric is the degraded "<year>" form and
		// must be rejected, not reported as a publisher.
		if b.Publisher != "" {
			for _, r := range b.Publisher {
				if r < '0' || r > '9' {
					break
				}
				t.Errorf("numeric publisher %q was accepted for %q", b.Publisher, b.Title)
			}
		}
	}
	if !foundAuthor {
		t.Error("no author was extracted from any fixture record; the icon lookup is broken")
	}
}
