package cmd

import (
	"bytes"
	"flag"
	"io"
	"testing"
)

// newTestFlagSet builds a flag set whose parse errors are discarded, so a test
// that deliberately triggers one does not print to the test log.
func newTestFlagSet(name string) *flag.FlagSet {
	fs := newFlagSet(name, "")
	fs.SetOutput(io.Discard)
	return fs
}

// TestFlagsMayFollowPositionalArguments is the regression test for a bug that
// made the documented usage fail silently.
//
// Go's flag package stops parsing at the first non-flag argument, so
// `zlib search "deep learning" --limit 5` left --limit in fs.Args() and the
// limit kept its default. Nothing errored: the user asked for 5 results and got
// 10. Every example in the help text and README has this shape, so the fix is
// load-bearing rather than cosmetic.
func TestFlagsMayFollowPositionalArguments(t *testing.T) {
	fs := newTestFlagSet("search")
	limit := fs.Int("limit", 10, "")
	lang := fs.String("lang", "", "")
	ext := fs.String("ext", "", "")
	stdout := fs.Bool("stdout", false, "")

	err := parseFlags(fs, []string{"deep learning", "--limit", "5", "--lang", "english", "--ext", "pdf", "--stdout"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	if *limit != 5 {
		t.Errorf("--limit = %d, want 5 — the flag was left in the positional arguments", *limit)
	}
	if *lang != "english" {
		t.Errorf("--lang = %q, want english", *lang)
	}
	if *ext != "pdf" {
		t.Errorf("--ext = %q, want pdf", *ext)
	}
	if !*stdout {
		t.Error("--stdout was not applied")
	}

	args := fs.Args()
	if len(args) != 1 || args[0] != "deep learning" {
		t.Errorf("positional arguments = %v, want exactly [deep learning] — a flag value leaked in", args)
	}
}

// TestFlagValueStaysAttachedToItsFlag covers the subtle half of the reordering:
// a flag that takes a value must move as a pair. Moving `--out` without its
// value would turn the directory path into a filename argument.
func TestFlagValueStaysAttachedToItsFlag(t *testing.T) {
	fs := newTestFlagSet("download")
	out := fs.String("out", "", "")
	fs.StringVar(out, "o", "", "")
	name := fs.String("name", "", "")

	if err := parseFlags(fs, []string{"12345/abc", "--out", "/tmp/books", "--name", "book.pdf"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if *out != "/tmp/books" {
		t.Errorf("--out = %q, want /tmp/books", *out)
	}
	if *name != "book.pdf" {
		t.Errorf("--name = %q, want book.pdf", *name)
	}
	if args := fs.Args(); len(args) != 1 || args[0] != "12345/abc" {
		t.Errorf("positional arguments = %v, want [12345/abc]", args)
	}
}

func TestFlagEqualsFormIsNotSplitAcrossArguments(t *testing.T) {
	fs := newTestFlagSet("test")
	out := fs.String("out", "", "")

	if err := parseFlags(fs, []string{"file.pdf", "--out=/tmp/x"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if *out != "/tmp/x" {
		t.Errorf("--out = %q, want /tmp/x", *out)
	}
	if args := fs.Args(); len(args) != 1 || args[0] != "file.pdf" {
		t.Errorf("positional arguments = %v", args)
	}
}

// TestDoubleDashEndsFlagParsing keeps a query that begins with a dash from being
// mistaken for a flag, which is the documented escape hatch.
func TestDoubleDashEndsFlagParsing(t *testing.T) {
	fs := newTestFlagSet("search")
	limit := fs.Int("limit", 10, "")

	if err := parseFlags(fs, []string{"--limit", "3", "--", "--not-a-flag"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if *limit != 3 {
		t.Errorf("--limit = %d, want 3", *limit)
	}
	args := fs.Args()
	if len(args) != 1 || args[0] != "--not-a-flag" {
		t.Errorf("positional arguments = %v, want [--not-a-flag]", args)
	}
}

// TestUnknownFlagDoesNotSwallowTheNextArgument guards the ordering heuristic
// against eating a real input.
//
// Only reorderArgs is exercised through to Parse's own error, not the resulting
// Args(): an unknown flag makes flag.Parse stop and fail immediately, so what
// Args() holds afterwards proves nothing. The property that matters is that the
// unknown flag was not handed the next argument, since consuming it would turn
// "book.epub" into a flag value and lose the file.
func TestUnknownFlagDoesNotSwallowTheNextArgument(t *testing.T) {
	fs := newTestFlagSet("test")
	_ = fs.String("out", "", "")

	args := reorderArgs(fs, []string{"book.epub", "--bogus"})
	if len(args) != 2 || args[0] != "--bogus" || args[1] != "book.epub" {
		t.Errorf("reorderArgs = %v, want [--bogus book.epub] — the filename must stay positional", args)
	}

	if err := fs.Parse(args); err == nil {
		t.Error("an unknown flag was accepted by the parser")
	}
}

// TestReorderArgsHandlesLoneDash checks that "-" is treated as a conventional
// stdin placeholder rather than as a flag with a missing name.
func TestReorderArgsHandlesLoneDash(t *testing.T) {
	fs := newTestFlagSet("test")
	_ = fs.Bool("verbose", false, "")

	args := reorderArgs(fs, []string{"-", "--verbose"})
	if len(args) != 2 || args[0] != "--verbose" || args[1] != "-" {
		t.Errorf("reorderArgs = %v, want [--verbose -]", args)
	}
}

// TestBoolFlagDoesNotConsumeTheNextArgument is the counterpart to the
// value-flag case: if a boolean flag were treated as value-taking, the filename
// after it would disappear from the positional arguments.
func TestBoolFlagDoesNotConsumeTheNextArgument(t *testing.T) {
	fs := newTestFlagSet("test")
	dry := fs.Bool("dry-run", false, "")

	if err := parseFlags(fs, []string{"--dry-run", "book.epub"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !*dry {
		t.Error("--dry-run was not applied")
	}
	if args := fs.Args(); len(args) != 1 || args[0] != "book.epub" {
		t.Errorf("positional arguments = %v, want [book.epub]", args)
	}
}

// TestUsageHintExplainsGlobalFlagPlacement covers the error a user gets from
// `zlib doctor --json`, which is the natural way to ask for JSON and the one
// thing the leading-flag parser cannot accept.
func TestUsageHintExplainsGlobalFlagPlacement(t *testing.T) {
	var out, errBuf bytes.Buffer
	ctx := &Context{Colors: newTestColors(), Out: &out, Err: &errBuf}

	code := reportError(ctx, usagef(`flag provided but not defined: -json`))
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !bytes.Contains(errBuf.Bytes(), []byte("before the command name")) {
		t.Errorf("the hint does not explain where a global option belongs:\n%s", errBuf.String())
	}
}

// TestFlagSetDoesNotExitOnError keeps handlers testable: a flag set that called
// os.Exit would take the test process down with it.
func TestFlagSetDoesNotExitOnError(t *testing.T) {
	fs := newTestFlagSet("test")
	if fs.ErrorHandling() != flag.ContinueOnError {
		t.Error("the flag set is not in ContinueOnError mode")
	}
}
