package cmd

import (
	"bytes"
	"testing"

	"zlib/internal/ui"
)

// newTestColors returns a colorizer that emits no escapes, so assertions compare
// plain text.
func newTestColors() *ui.Colors { return ui.NewColors(false) }

// TestParseTarget covers the two identifier shapes the CLI accepts: Z-Library's
// "id/hash" pair, which is what `search` prints and a user pastes back, and a
// bare MD5 for the MD5-backed sources.
func TestParseTarget(t *testing.T) {
	cases := []struct {
		in       string
		wantID   string
		wantHash string
	}{
		{"12345/abc123def", "12345", "abc123def"},
		{"abc123def", "", "abc123def"},
		{" 12345/abc123def ", "12345", "abc123def"},
		{"818bb275a80ff302848a2a4154b07202", "", "818bb275a80ff302848a2a4154b07202"},
		// A leading slash is not an id, so the whole value is treated as a hash
		// rather than half-parsed into an empty id.
		{"/abc", "", "/abc"},
		// A trailing slash likewise leaves nothing usable as a hash.
		{"12345/", "", "12345/"},
	}
	for _, tc := range cases {
		got := parseTarget(tc.in)
		if got.ID != tc.wantID || got.Hash != tc.wantHash {
			t.Errorf("parseTarget(%q) = {%q, %q}, want {%q, %q}",
				tc.in, got.ID, got.Hash, tc.wantID, tc.wantHash)
		}
	}
}

func TestSplitList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"english", []string{"english"}},
		{"english,chinese", []string{"english", "chinese"}},
		{"english, chinese ", []string{"english", "chinese"}},
		{"english,,chinese", []string{"english", "chinese"}},
		{",,", nil},
	}
	for _, tc := range cases {
		got := splitList(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitList(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitList(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

// TestParseGlobalsOnlyConsumesLeadingFlags matters because subcommands define
// their own --json-like flags; a global parser that scanned the whole argument
// list would swallow them.
func TestParseGlobalsOnlyConsumesLeadingFlags(t *testing.T) {
	opts, rest, err := parseGlobals([]string{"--json", "--verbose", "search", "deep learning", "--limit", "5"})
	if err != nil {
		t.Fatalf("parseGlobals: %v", err)
	}
	if !opts.json || !opts.verbose {
		t.Errorf("global flags were not picked up: %+v", opts)
	}
	if len(rest) != 4 || rest[0] != "search" {
		t.Errorf("rest = %v, want the subcommand and its arguments untouched", rest)
	}

	// A flag after the subcommand belongs to the subcommand.
	_, rest2, err := parseGlobals([]string{"search", "--json"})
	if err != nil {
		t.Fatalf("parseGlobals: %v", err)
	}
	if len(rest2) != 2 || rest2[1] != "--json" {
		t.Errorf("rest2 = %v; a trailing --json was consumed by the global parser", rest2)
	}
}

func TestParseGlobalsProxyTakesAValue(t *testing.T) {
	opts, rest, err := parseGlobals([]string{"--proxy", "socks5://127.0.0.1:1080", "doctor"})
	if err != nil {
		t.Fatalf("parseGlobals: %v", err)
	}
	if opts.proxy != "socks5://127.0.0.1:1080" {
		t.Errorf("proxy = %q", opts.proxy)
	}
	if len(rest) != 1 || rest[0] != "doctor" {
		t.Errorf("rest = %v", rest)
	}

	// A missing value is a usage error, not a panic.
	if _, _, err := parseGlobals([]string{"--proxy"}); err == nil {
		t.Error("--proxy with no value was accepted")
	}
}

func TestParseGlobalsDoubleDashStopsOptionParsing(t *testing.T) {
	opts, rest, err := parseGlobals([]string{"--json", "--", "--weird-query"})
	if err != nil {
		t.Fatalf("parseGlobals: %v", err)
	}
	if !opts.json {
		t.Error("--json before -- was not applied")
	}
	if len(rest) != 1 || rest[0] != "--weird-query" {
		t.Errorf("rest = %v, want the literal query after --", rest)
	}
}

// TestReportErrorExitCodes pins the distinction the exit codes carry: 2 means
// "you invoked it wrong", 1 means "it ran and failed".
func TestReportErrorExitCodes(t *testing.T) {
	var out, errBuf bytes.Buffer
	ctx := &Context{Colors: newTestColors(), Out: &out, Err: &errBuf}

	if code := reportError(ctx, usagef("a query is required")); code != 2 {
		t.Errorf("usage error exit code = %d, want 2", code)
	}
	if code := reportError(ctx, errTest); code != 1 {
		t.Errorf("runtime error exit code = %d, want 1", code)
	}
}

// TestReportErrorJSONEmitsStructuredFailure keeps a machine consumer's stderr
// parseable, so it can distinguish a failure from an empty result set.
func TestReportErrorJSONEmitsStructuredFailure(t *testing.T) {
	var out, errBuf bytes.Buffer
	ctx := &Context{Colors: newTestColors(), Out: &out, Err: &errBuf, JSON: true}

	reportError(ctx, errTest)
	if !bytes.Contains(errBuf.Bytes(), []byte(`"error"`)) {
		t.Errorf("stderr is not structured JSON: %q", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout was written to on failure: %q", out.String())
	}
}

var errTest = testError("something went wrong")

type testError string

func (e testError) Error() string { return string(e) }
