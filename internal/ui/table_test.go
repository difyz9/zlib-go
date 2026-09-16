package ui

import (
	"bytes"
	"strings"
	"testing"

	"zlib/internal/model"
)

// TestDisplayWidthCountsCJKAsTwoColumns is the reason this package exists: a
// Chinese title occupies two terminal columns per character, and counting runes
// instead misaligns every row that contains one.
func TestDisplayWidthCountsCJKAsTwoColumns(t *testing.T) {
	cases := map[string]int{
		"abc":                  3,
		"控制":                   4, // two CJK characters
		"a控b":                  4,
		"日本語":                  6,
		"":                     0,
		"Deep 学习":              9, // "Deep " = 5, "学习" = 4
		"\x1b[32mgreen\x1b[0m": 5, // escapes must not count
	}
	for in, want := range cases {
		if got := DisplayWidth(in); got != want {
			t.Errorf("DisplayWidth(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTruncateRespectsDisplayWidth(t *testing.T) {
	// A 3-column budget fits one CJK character (2 columns) plus the ellipsis.
	got := truncate("控制论与控制", 3)
	if DisplayWidth(got) > 3 {
		t.Errorf("truncate produced %d columns, want <= 3: %q", DisplayWidth(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected an ellipsis marker, got %q", got)
	}

	// Text already inside the budget is returned unchanged.
	if got := truncate("short", 20); got != "short" {
		t.Errorf("unchanged text was altered: %q", got)
	}
}

// TestTableColumnsAlignWithCJK verifies the rendered table keeps its columns
// lined up even though the cells have different byte and rune counts.
func TestTableColumnsAlignWithCJK(t *testing.T) {
	books := []model.Book{
		{Source: model.SourceZlib, Title: "Deep Learning", Author: "Ian Goodfellow", Year: "2016", Extension: "pdf", Filesize: "22.5 MB"},
		{Source: model.SourceZlib, Title: "控制论", Author: "诺伯特·维纳", Year: "1948", Extension: "epub", Filesize: "1.2 MB"},
	}

	var buf bytes.Buffer
	table := BookTable(books, NewColors(false), BookColumns{ShowIndex: true})
	table.Render(&buf)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected a header and two rows, got %d lines", len(lines))
	}

	// Every line must have the same display width up to the last column, which
	// is what "aligned" means for a table whose cells contain CJK.
	widths := make([]int, len(lines))
	for i, line := range lines {
		widths[i] = DisplayWidth(strings.TrimRight(line, " "))
	}
	if widths[0] != widths[1] {
		t.Errorf("header and first row disagree: %d vs %d columns\n%q\n%q",
			widths[0], widths[1], lines[0], lines[1])
	}
}

func TestTableTruncatesLongTitles(t *testing.T) {
	long := strings.Repeat("很长的书名", 40)
	var buf bytes.Buffer
	table := BookTable([]model.Book{{Title: long, Source: model.SourceAnnas}}, NewColors(false), BookColumns{TitleWidth: 20})
	table.Render(&buf)

	out := buf.String()
	if !strings.Contains(out, "…") {
		t.Error("a long title was not truncated")
	}
	for _, line := range strings.Split(out, "\n") {
		if w := DisplayWidth(line); w > 200 {
			t.Errorf("row is %d columns wide despite a 20-column title cap: %q", w, line)
		}
	}
}

// TestMixedSourcesDetectsMultipleBackends drives whether the Source column is
// worth rendering at all.
func TestMixedSourcesDetectsMultipleBackends(t *testing.T) {
	same := []model.Book{{Source: model.SourceZlib}, {Source: model.SourceZlib}}
	if MixedSources(same) {
		t.Error("single-source results reported as mixed")
	}
	mixed := []model.Book{{Source: model.SourceZlib}, {Source: model.SourceAnnas}}
	if !MixedSources(mixed) {
		t.Error("mixed results were not detected")
	}
	if MixedSources(nil) {
		t.Error("an empty result set reported as mixed")
	}
}

// TestColorsDisabledEmitsNoEscapes guards the NO_COLOR contract: a pipeline
// consumer must never receive escape sequences.
func TestColorsDisabledEmitsNoEscapes(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	c := NewColors(true) // the caller asked for color...
	if c.Enabled() {
		t.Error("NO_COLOR did not disable color")
	}
	if got := c.Title("hello"); got != "hello" {
		t.Errorf("disabled colorizer altered the string: %q", got)
	}
}

func TestColorsEnabledWrapsInEscapes(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	c := NewColors(true)
	if got := c.Title("hello"); !strings.Contains(got, "\x1b[") {
		t.Errorf("enabled colorizer emitted no escape: %q", got)
	}
}

func TestBookTableOmitsEmptyFields(t *testing.T) {
	// Records from Anna's Archive frequently lack a year; the row must still
	// render with a placeholder rather than shifting columns.
	var buf bytes.Buffer
	table := BookTable([]model.Book{
		{Source: model.SourceAnnas, Title: "No Year Book", Extension: "pdf", Filesize: "6.7MB"},
	}, NewColors(false), BookColumns{})
	table.Render(&buf)

	out := buf.String()
	if !strings.Contains(out, "—") {
		t.Errorf("missing fields were not rendered as a placeholder:\n%s", out)
	}
	if strings.Contains(out, "\t") {
		t.Error("tabs appear in the output, which breaks column alignment")
	}
}
