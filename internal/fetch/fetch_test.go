package fetch

import (
	"strings"
	"testing"
)

// TestSanitizeFilenameBlocksTraversal is a security test, not a formatting one.
//
// These values originate from a server-controlled Content-Disposition header or
// from book metadata, so a missing strip lets "../../x" escape the download
// directory. Both separator conventions are covered because a Windows-oriented
// server may send backslashes, which path.Base on POSIX does not treat as a
// separator.
func TestSanitizeFilenameBlocksTraversal(t *testing.T) {
	// Only the final path component survives: reducing "../../etc/passwd" to
	// "passwd" is the point, not a loss. Both separator conventions are covered
	// because a Windows-oriented server may send backslashes, which path.Base on
	// POSIX would not treat as a separator at all.
	cases := map[string]string{
		"../../etc/passwd":               "passwd",
		`..\..\windows\system32`:         "system32",
		`C:\Windows\notepad.exe`:         "notepad.exe",
		"/etc/shadow":                    "shadow",
		"book:name?.pdf":                 "book_name_.pdf",
		"..":                             "",
		".":                              "",
		"":                               "",
		"   ":                            "",
		"trailing.  ":                    "trailing",
		"normal_book-v1.2.pdf":           "normal_book-v1.2.pdf",
		"Deep Learning (Goodfellow).pdf": "Deep Learning (Goodfellow).pdf",
		"失控 (莱姆).epub":                   "失控 (莱姆).epub",
	}

	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSanitizeFilenameNeverKeepsDirectoryComponents states the security
// property directly, so the test still holds if the exact outputs above change.
func TestSanitizeFilenameNeverKeepsDirectoryComponents(t *testing.T) {
	hostile := []string{
		"../../../../tmp/pwned",
		`..\..\..\Windows\System32\evil.dll`,
		"/absolute/path/file.pdf",
		"nested/dir/book.epub",
	}
	for _, in := range hostile {
		got := SanitizeFilename(in)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("SanitizeFilename(%q) = %q still contains a path separator", in, got)
		}
		if strings.HasPrefix(got, ".") && got != "" {
			t.Errorf("SanitizeFilename(%q) = %q is a hidden/relative name", in, got)
		}
	}
}

// TestSanitizeFilenameNeutralizesControlChars covers a filename carrying a
// newline, which would otherwise forge an extra line in logged output.
func TestSanitizeFilenameNeutralizesControlChars(t *testing.T) {
	got := SanitizeFilename("book\nname\ttab.pdf")
	if got != "booknametab.pdf" {
		t.Errorf("control characters survived: %q", got)
	}
}

// TestLongFilenameIsTruncated keeps the result inside filesystem name limits
// while preserving the extension, which is what tells a reader what the file is.
func TestLongFilenameIsTruncated(t *testing.T) {
	long := make([]byte, 0, 400)
	for i := 0; i < 400; i++ {
		long = append(long, 'a')
	}
	name := string(long) + ".pdf"

	got := SanitizeFilename(name)
	if len(got) > 200 {
		t.Errorf("sanitized length = %d, want <= 200", len(got))
	}
	if len(got) < 4 || got[len(got)-4:] != ".pdf" {
		t.Errorf("extension lost: %q", got[len(got)-10:])
	}
}

// TestUnicodeTruncationStaysValidUTF8 ensures the byte-budget cut lands on a
// rune boundary; a mid-rune cut produces a filename the filesystem rejects.
func TestUnicodeTruncationStaysValidUTF8(t *testing.T) {
	cjk := ""
	for i := 0; i < 200; i++ {
		cjk += "书"
	}
	got := SanitizeFilename(cjk + ".epub")
	if len(got) > 200 {
		t.Errorf("length = %d, want <= 200", len(got))
	}
	for i, r := range got {
		if r == '\uFFFD' {
			t.Fatalf("invalid UTF-8 introduced at byte %d", i)
		}
	}
}

func TestFilenameFromContentDisposition(t *testing.T) {
	cases := map[string]string{
		// The extended form wins when present: a server sending filename* also
		// sends an ASCII-mangled filename fallback that loses characters.
		`attachment; filename*=UTF-8''%E6%8E%A7%E5%88%B6.epub; filename="??.epub"`: "控制.epub",
		`attachment; filename="Deep Learning.pdf"`:                                 "Deep Learning.pdf",
		`attachment; filename=simple.pdf`:                                          "simple.pdf",
		`attachment; filename='single-quoted.pdf'`:                                 "single-quoted.pdf",
		"":                        "",
		"attachment":              "",
		`attachment; filename=""`: "",
	}

	for in, want := range cases {
		if got := FilenameFromContentDisposition(in); got != want {
			t.Errorf("FilenameFromContentDisposition(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestJSONStringHandlesMixedScalarTypes covers the API's habit of returning an
// id as a number on one endpoint and a string on another. Rendering a float64
// id as "12345" rather than "12345.000000" is what keeps identifiers usable.
func TestJSONStringHandlesMixedScalarTypes(t *testing.T) {
	payload := map[string]any{
		"str":    "  trimmed  ",
		"int":    float64(12345),
		"float":  1.5,
		"bool":   true,
		"null":   nil,
		"absent": nil,
	}
	cases := map[string]string{
		"str": "trimmed", "int": "12345", "float": "1.5", "bool": "true", "null": "", "missing": "",
	}
	for key, want := range cases {
		if got := JSONString(payload, key); got != want {
			t.Errorf("JSONString(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestJSONIntAndNestedString(t *testing.T) {
	payload := map[string]any{
		"n":     float64(7),
		"s":     "9",
		"inner": map[string]any{"downloadLink": "https://example.com/f"},
	}
	if got := JSONInt(payload, "n"); got != 7 {
		t.Errorf("JSONInt(n) = %d, want 7", got)
	}
	if got := JSONInt(payload, "s"); got != 9 {
		t.Errorf("JSONInt(s) = %d, want 9", got)
	}
	if got := JSONInt(payload, "absent"); got != 0 {
		t.Errorf("JSONInt(absent) = %d, want 0", got)
	}
	if got := NestedString(payload, "inner", "downloadLink"); got != "https://example.com/f" {
		t.Errorf("NestedString = %q", got)
	}
	if got := NestedString(payload, "nope", "x"); got != "" {
		t.Errorf("NestedString on a missing branch = %q, want empty", got)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://annas-archive.gl/search?q=x": "annas-archive.gl",
		"https://example.com:8443/path":       "example.com:8443",
		"http://Z-Library.EC/eapi/info":       "z-library.ec",
		"":                                    "",
	}
	for in, want := range cases {
		if got := HostOf(in); got != want {
			t.Errorf("HostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewTransportRejectsUnknownScheme(t *testing.T) {
	if _, err := NewTransport("ftp://proxy.local:21", 0); err == nil {
		t.Error("expected an error for an unsupported proxy scheme")
	}
	for _, ok := range []string{"http://127.0.0.1:8080", "socks5://127.0.0.1:1080", ""} {
		if _, err := NewTransport(ok, 0); err != nil {
			t.Errorf("NewTransport(%q) returned an unexpected error: %v", ok, err)
		}
	}
}

func TestEffectiveProxyPrefersExplicitValue(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://from-env:3128")
	if got := EffectiveProxy("socks5://explicit:1080"); got != "socks5://explicit:1080" {
		t.Errorf("explicit proxy was not preferred: %q", got)
	}
	if got := EffectiveProxy(""); got != "http://from-env:3128" {
		t.Errorf("environment proxy was not used: %q", got)
	}
}
