// Package fetch holds the HTTP plumbing the source adapters share: bounded
// reads, JSON envelope helpers, safe filename handling, and the atomic
// download-to-disk routine.
//
// It deliberately knows nothing about any particular book source, so the
// zlib and annas packages can each layer their own error taxonomy on top.
package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// UserAgent identifies the client as a desktop browser. Several of these
// sources maintain blocklists that reject obviously programmatic agents;
// measured against LibGen, a self-identifying string returned a 641-byte nginx
// stub where a browser string returned the full 265 KB page.
const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// MaxResponseBytes bounds how much of any JSON or HTML response is buffered.
const MaxResponseBytes = 32 << 20

// StatusError carries a non-200 response so callers can classify it in terms of
// their own source (for example, an anti-bot wall versus a plain 404).
type StatusError struct {
	Code int
	Host string
	URL  string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("HTTP %d from %s", e.Code, e.Host)
}

// ReadAll buffers a response body, refusing to exceed MaxResponseBytes so a
// hostile or misconfigured endpoint cannot exhaust memory.
func ReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, MaxResponseBytes))
}

// DecodeJSON unmarshals a response into the map shape every one of these APIs
// returns at the top level.
func DecodeJSON(body []byte) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// JSONString renders a JSON scalar as a string. These APIs mix types freely:
// a book id arrives as a number on one endpoint and a string on another.
func JSONString(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		return strconv.FormatBool(v)
	default:
		return ""
	}
}

// JSONInt reads a numeric field, returning 0 when absent or unparseable.
func JSONInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return 0
}

// NestedString reads payload[outer][inner] as a string.
func NestedString(m map[string]any, outer, inner string) string {
	obj, ok := m[outer].(map[string]any)
	if !ok {
		return ""
	}
	return JSONString(obj, inner)
}

// FirstNonEmpty returns the first non-empty value, which is how these adapters
// express "try each spelling the API has used for this field".
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// HostOf extracts the host (with port when present) from an absolute URL.
func HostOf(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// --- filenames ---

// SanitizeFilename reduces a name to a bare basename safe to join onto a
// directory.
//
// The value can originate from a server-controlled Content-Disposition header
// or from book metadata, so directory components must be stripped before it
// reaches the filesystem: a value like "../../x" would otherwise escape the
// destination entirely. Backslashes are normalised first because a
// Windows-oriented server may use them as separators and path.Base on POSIX
// would not treat them as such.
func SanitizeFilename(name string) string {
	if name == "" {
		return ""
	}
	candidate := path.Base(strings.ReplaceAll(name, "\\", "/"))
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || candidate == "." || candidate == ".." || candidate == "/" {
		return ""
	}
	candidate = strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			return '_'
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, candidate)
	// A trailing dot or space is silently stripped by Windows, producing a
	// mismatch between the reported path and the file actually on disk.
	candidate = strings.TrimRight(candidate, ". ")
	if candidate == "" {
		return ""
	}
	if len(candidate) > 200 {
		ext := filepath.Ext(candidate)
		base := strings.TrimSuffix(candidate, ext)
		for len(base) > 0 && len(base)+len(ext) > 200 {
			_, size := utf8.DecodeLastRuneInString(base)
			base = base[:len(base)-size]
		}
		candidate = base + ext
	}
	return candidate
}

// RFC 6266 permits three spellings of the filename parameter, and Z-Library
// uses all of them depending on the title's character set. The extended form is
// tried first because a server sending filename* also sends an ASCII-mangled
// filename fallback that loses characters.
var (
	cdExtended = regexp.MustCompile(`(?i)filename\*\s*=\s*UTF-8''([^;\s]+)`)
	cdQuoted   = regexp.MustCompile(`(?i)filename\s*=\s*"([^"]*)"`)
	cdBare     = regexp.MustCompile(`(?i)filename\s*=\s*([^;"\s]+)`)
)

// FilenameFromContentDisposition extracts a filename from a
// Content-Disposition header, or returns "" when it carries none.
func FilenameFromContentDisposition(header string) string {
	if header == "" {
		return ""
	}
	if m := cdExtended.FindStringSubmatch(header); m != nil {
		if decoded, err := url.QueryUnescape(m[1]); err == nil {
			return strings.TrimSpace(decoded)
		}
		return strings.TrimSpace(m[1])
	}
	for _, re := range []*regexp.Regexp{cdQuoted, cdBare} {
		if m := re.FindStringSubmatch(header); m != nil {
			if v := strings.Trim(strings.TrimSpace(m[1]), "'"); v != "" {
				return v
			}
		}
	}
	return ""
}

// --- download ---

// StagingPrefix marks partial files an interrupted download leaves behind, so
// they are recognisable as incomplete rather than as real books.
const StagingPrefix = ".zlib-part-"

// ErrEmptyDownload reports a zero-byte transfer. These sources answer
// quota-exhausted requests with HTTP 200 and an empty body, which would
// otherwise be indistinguishable from success.
var ErrEmptyDownload = errors.New("download produced an empty file: the daily quota may be exhausted or the file removed")

// DownloadToFile streams rawURL into outDir/filename atomically.
//
// The body lands in a hidden staging file first and is renamed into place only
// once the transfer completes and the size is known to be non-zero. This keeps
// an interrupted run from leaving a truncated file that looks finished.
//
// A non-200 response returns *StatusError so the caller can decide whether the
// code means "walled", "gone", or "retry".
func DownloadToFile(ctx context.Context, client *http.Client, rawURL, outDir, filename, cookie string, timeout time.Duration, logf func(string, ...any)) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("create download directory: %w", err)
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	dlClient := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := dlClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", &StatusError{Code: resp.StatusCode, Host: HostOf(rawURL), URL: rawURL}
	}

	if filename == "" {
		filename = SanitizeFilename(FilenameFromContentDisposition(resp.Header.Get("Content-Disposition")))
	}
	if filename == "" && resp.Request != nil && resp.Request.URL != nil {
		base := path.Base(strings.Split(resp.Request.URL.Path, "?")[0])
		if base != "" && base != "/" && base != "." {
			filename = SanitizeFilename(base)
		}
	}
	if filename == "" {
		filename = "book.bin"
	}

	staging, err := os.CreateTemp(outDir, StagingPrefix+"*.part")
	if err != nil {
		return "", fmt.Errorf("create staging file: %w", err)
	}
	stagingPath := staging.Name()

	discard := func() {
		staging.Close()
		os.Remove(stagingPath)
	}

	written, err := io.Copy(staging, resp.Body)
	if err != nil {
		discard()
		return "", fmt.Errorf("download interrupted after %d bytes: %w", written, err)
	}
	if err := staging.Sync(); err != nil {
		discard()
		return "", fmt.Errorf("flush download: %w", err)
	}
	if err := staging.Close(); err != nil {
		os.Remove(stagingPath)
		return "", fmt.Errorf("close download: %w", err)
	}
	if written == 0 {
		os.Remove(stagingPath)
		return "", ErrEmptyDownload
	}

	finalPath := filepath.Join(outDir, filename)
	// A rename over an existing file succeeds on POSIX, which is the desired
	// overwrite behaviour; remove first so the same code path works everywhere.
	if _, err := os.Stat(finalPath); err == nil {
		if err := os.Remove(finalPath); err != nil {
			os.Remove(stagingPath)
			return "", fmt.Errorf("replace existing file: %w", err)
		}
	}
	if err := os.Rename(stagingPath, finalPath); err != nil {
		os.Remove(stagingPath)
		return "", fmt.Errorf("finalize download: %w", err)
	}
	if logf != nil {
		logf("downloaded %d bytes to %s", written, finalPath)
	}
	return finalPath, nil
}

// FileSize renders a byte count in the compact form the book sites use.
func FileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
