package ui

import (
	"os"
	"strings"
)

// Colors renders ANSI escapes when enabled. A disabled Colors returns every
// string unchanged, so call sites never need to branch on whether color is on.
type Colors struct{ enabled bool }

// NewColors builds a colorizer. It is forced off by NO_COLOR (any value) and by
// TERM=dumb, matching the convention the rest of the toolchain follows.
func NewColors(enabled bool) *Colors {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		enabled = false
	}
	return &Colors{enabled: enabled}
}

// Enabled reports whether escapes will be emitted.
func (c *Colors) Enabled() bool { return c != nil && c.enabled }

func (c *Colors) wrap(code, s string) string {
	if !c.Enabled() || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Dim styles secondary information such as sizes and dates.
func (c *Colors) Dim(s string) string { return c.wrap("2", s) }

// Bold styles headings.
func (c *Colors) Bold(s string) string { return c.wrap("1", s) }

// Red marks failures and destructive outcomes.
func (c *Colors) Red(s string) string { return c.wrap("31", s) }

// Green marks successful outcomes.
func (c *Colors) Green(s string) string { return c.wrap("32", s) }

// Yellow marks warnings, such as a nearly exhausted quota.
func (c *Colors) Yellow(s string) string { return c.wrap("33", s) }

// Blue styles neutral metadata.
func (c *Colors) Blue(s string) string { return c.wrap("34", s) }

// Magenta styles identifiers.
func (c *Colors) Magenta(s string) string { return c.wrap("35", s) }

// Cyan styles the result index column.
func (c *Colors) Cyan(s string) string { return c.wrap("36", s) }

// Title styles a book title.
func (c *Colors) Title(s string) string { return c.wrap("1", s) }

// Source styles a backend label per source, so a mixed result set stays
// readable at a glance.
func (c *Colors) Source(source string) string {
	switch strings.ToLower(source) {
	case "zlib":
		return c.wrap("32", source)
	case "annas":
		return c.wrap("35", source)
	case "libgen":
		return c.wrap("34", source)
	default:
		return source
	}
}
