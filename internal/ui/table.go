// Package ui renders CLI output: aligned tables, JSON, and progress messages.
//
// Table layout is computed on display width rather than byte or rune count,
// because a Chinese or Japanese title occupies two terminal columns per
// character and a naive width calculation misaligns every row that contains one.
package ui

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"zlib/internal/model"
)

// runeWidth returns how many terminal columns a rune occupies.
//
// Zero-width and combining marks count as 0; the East Asian Wide and Fullwidth
// ranges, plus the emoji blocks, count as 2. This mirrors the ranges
// go-runewidth covers without taking the dependency.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32, r >= 0x7F && r < 0xA0:
		return 0 // control characters
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return 0 // combining marks and format characters
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK Radicals, Kangxi
		r >= 0x3041 && r <= 0x33FF, // Hiragana, Katakana, CJK symbols
		r >= 0x3400 && r <= 0x4DBF, // CJK Extension A
		r >= 0x4E00 && r <= 0x9FFF, // CJK Unified Ideographs
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK Compatibility Ideographs
		r >= 0xFE30 && r <= 0xFE6F, // CJK Compatibility Forms
		r >= 0xFF00 && r <= 0xFF60, // Fullwidth Forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD: // CJK Extension B and beyond
		return 2
	default:
		return 1
	}
}

// DisplayWidth returns the terminal column count of s, ignoring ANSI escapes.
func DisplayWidth(s string) int {
	w := 0
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		w += runeWidth(r)
	}
	return w
}

// truncate shortens s to at most width display columns, appending an ellipsis
// when it had to cut. The cut lands on a rune boundary so the result stays
// valid UTF-8.
func truncate(s string, width int) string {
	if width <= 0 || DisplayWidth(s) <= width {
		return s
	}
	const ellipsis = "…"
	budget := width - DisplayWidth(ellipsis)
	if budget <= 0 {
		return ellipsis
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeWidth(r)
		if used+rw > budget {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + ellipsis
}

// pad appends spaces so the visible width of s reaches width.
func pad(s string, width int) string {
	if gap := width - DisplayWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// Alignment selects how a column's cells are padded.
type Alignment int

const (
	AlignLeft Alignment = iota
	AlignRight
)

// Table is a fixed-column text table.
type Table struct {
	Headers []string
	Rows    [][]string
	Aligns  []Alignment
	// MaxWidth caps a column's rendered width; 0 means unlimited.
	MaxWidth []int
	// Stylers colorize a cell after padding decisions are made, so escape
	// sequences never affect alignment.
	Stylers []func(string) string
	// NoHeader suppresses the header row (used for single-record output).
	NoHeader bool
}

// Render writes the table, padding every column to its content width.
func (t *Table) Render(w io.Writer) {
	widths := make([]int, len(t.Headers))
	for i, h := range t.Headers {
		widths[i] = DisplayWidth(h)
	}

	// Widths are derived from the truncated form of each cell, so a cap on a
	// column is honoured rather than being overridden by one long value.
	display := make([][]string, len(t.Rows))
	for r, row := range t.Rows {
		display[r] = make([]string, len(row))
		for c, cell := range row {
			v := cell
			if c < len(t.MaxWidth) && t.MaxWidth[c] > 0 {
				v = truncate(v, t.MaxWidth[c])
			}
			display[r][c] = v
			if c < len(widths) {
				if wd := DisplayWidth(v); wd > widths[c] {
					widths[c] = wd
				}
			}
		}
	}

	line := func(cells []string) {
		var b strings.Builder
		for i, cell := range cells {
			if i >= len(widths) {
				break
			}
			styled := cell
			if i < len(t.Stylers) && t.Stylers[i] != nil {
				styled = t.Stylers[i](cell)
			}
			// Padding is computed from the unstyled width so ANSI escapes never
			// shift a column.
			switch alignAt(t.Aligns, i) {
			case AlignRight:
				// A right-aligned cell is padded on the left, which is what
				// lines up a numeric column's trailing edge. The header is
				// included: without this the heading "Size" sits three columns
				// left of the values beneath it.
				b.WriteString(strings.Repeat(" ", max(0, widths[i]-DisplayWidth(cell))))
				b.WriteString(styled)
			default:
				if i < len(cells)-1 {
					b.WriteString(pad(styled, widths[i]))
				} else {
					// The final left-aligned column needs no trailing padding:
					// it would add invisible whitespace that shows up in diffs
					// and in terminal selections.
					b.WriteString(styled)
				}
			}
			if i < len(cells)-1 {
				b.WriteString("  ")
			}
		}
		fmt.Fprintln(w, b.String())
	}

	if !t.NoHeader && len(t.Headers) > 0 {
		styled := make([]string, len(t.Headers))
		for i, h := range t.Headers {
			styled[i] = h
		}
		line(styled)
	}
	for _, row := range display {
		line(row)
	}
}

func alignAt(aligns []Alignment, i int) Alignment {
	if i < len(aligns) {
		return aligns[i]
	}
	return AlignLeft
}

// BookColumns describes the standard search-result table.
type BookColumns struct {
	ShowSource bool
	ShowIndex  bool
	// ShowIdentifier adds a "Download" column carrying the value to paste
	// back into `zlib download`. Without it a user has no way to know which
	// row of the table maps to which identifier.
	ShowIdentifier bool
	TitleWidth     int
}

// BookTable builds the table used by `search`.
//
// Year, language, format, and size are omitted per row when a source could not
// supply them, which is normal for Anna's Archive records that render without a
// year segment; an em dash keeps the columns aligned.
func BookTable(books []model.Book, colors *Colors, cols BookColumns) *Table {
	if cols.TitleWidth == 0 {
		cols.TitleWidth = 56
	}

	headers := make([]string, 0, 8)
	aligns := make([]Alignment, 0, 8)
	maxw := make([]int, 0, 8)
	stylers := make([]func(string) string, 0, 8)

	if cols.ShowIndex {
		headers = append(headers, "#")
		aligns = append(aligns, AlignRight)
		maxw = append(maxw, 0)
		stylers = append(stylers, colors.Cyan)
	}
	if cols.ShowSource {
		headers = append(headers, "Source")
		aligns = append(aligns, AlignLeft)
		maxw = append(maxw, 8)
		stylers = append(stylers, func(s string) string { return colors.Source(s) })
	}
	headers = append(headers, "Title", "Author", "Year", "Lang", "Fmt", "Size")
	aligns = append(aligns, AlignLeft, AlignLeft, AlignRight, AlignLeft, AlignLeft, AlignRight)
	maxw = append(maxw, cols.TitleWidth, 28, 0, 0, 0, 0)
	stylers = append(stylers,
		colors.Title, nil, nil, nil, nil,
		func(s string) string { return colors.Dim(s) })
	if cols.ShowIdentifier {
		headers = append(headers, "Download ID")
		aligns = append(aligns, AlignLeft)
		maxw = append(maxw, 42)
		stylers = append(stylers, colors.Magenta)
	}

	rows := make([][]string, 0, len(books))
	for i, b := range books {
		row := make([]string, 0, 8)
		if cols.ShowIndex {
			row = append(row, fmt.Sprintf("%d", i+1))
		}
		if cols.ShowSource {
			row = append(row, string(b.Source))
		}
		row = append(row,
			dash(b.Title),
			dash(b.Author),
			dash(b.Year),
			shortLang(b.Language),
			dash(b.Extension),
			dash(b.Filesize),
		)
		if cols.ShowIdentifier {
			row = append(row, b.Identifier())
		}
		rows = append(rows, row)
	}

	return &Table{
		Headers:  headers,
		Rows:     rows,
		Aligns:   aligns,
		MaxWidth: maxw,
		Stylers:  stylers,
	}
}

// MixedSources reports whether the results span more than one backend, which is
// what decides whether the Source column is worth showing.
func MixedSources(books []model.Book) bool {
	seen := ""
	for _, b := range books {
		if seen == "" {
			seen = string(b.Source)
			continue
		}
		if string(b.Source) != seen {
			return true
		}
	}
	return false
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// shortLang trims a language name to its code when the source returned a full
// name, so the column stays narrow across mixed sources.
func shortLang(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return "—"
	}
	switch strings.ToLower(lang) {
	case "english":
		return "en"
	case "chinese":
		return "zh"
	case "german":
		return "de"
	case "french":
		return "fr"
	case "spanish":
		return "es"
	case "russian":
		return "ru"
	case "japanese":
		return "ja"
	}
	if len(lang) > 4 {
		return lang[:4]
	}
	return lang
}
