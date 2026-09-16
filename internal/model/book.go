// Package model defines the unified book record every source adapter returns.
//
// The three backends disagree about what identifies a book: Z-Library needs a
// numeric id plus an opaque hash, Anna's Archive and LibGen need a bare MD5.
// Book carries all of it and lets each adapter fill in only what its source
// actually publishes, so a caller never has to know which backend answered.
package model

import "strings"

// Source identifies which backend produced a result.
type Source string

const (
	SourceZlib   Source = "zlib"
	SourceAnnas  Source = "annas"
	SourceLibgen Source = "libgen"
)

// Book is the normalized form of one search hit or detail lookup.
type Book struct {
	Source      Source   `json:"source"`
	ID          string   `json:"id,omitempty"`   // Z-Library numeric id
	Hash        string   `json:"hash,omitempty"` // Z-Library hash, or MD5 for annas/libgen
	Title       string   `json:"title"`
	Author      string   `json:"author,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Year        string   `json:"year,omitempty"`
	Language    string   `json:"language,omitempty"`
	Extension   string   `json:"extension,omitempty"`
	Filesize    string   `json:"filesize,omitempty"`
	Cover       string   `json:"cover,omitempty"`
	URL         string   `json:"url,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	Provenance  []string `json:"provenance,omitempty"` // other sources Anna's reports holding the same file

	// Detail-only fields, populated by `info` rather than by search.
	Description string `json:"description,omitempty"`
	ISBN        string `json:"isbn,omitempty"`
	Pages       string `json:"pages,omitempty"`
}

// Identifier renders the value a user can paste back into `download`.
// Z-Library requires both halves; the MD5-backed sources need only the hash.
func (b Book) Identifier() string {
	if b.ID != "" && b.Hash != "" {
		return b.ID + "/" + b.Hash
	}
	return b.Hash
}

// Label returns a human-readable one-line description used in messages.
func (b Book) Label() string {
	title := strings.TrimSpace(b.Title)
	if title == "" {
		title = "(untitled)"
	}
	if b.Author != "" {
		return title + " — " + b.Author
	}
	return title
}
