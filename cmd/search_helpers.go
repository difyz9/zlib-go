package cmd

import (
	"strings"
	"time"

	"github.com/difyz9/zlib-go/internal/zlib"
)

// searchTimeout bounds one search across all backends.
const searchTimeout = 60 * time.Second

// zlibSearchOptions maps the CLI flags onto the EAPI client's option struct.
//
// The language and extension filters reach Z-Library as array form fields, so a
// comma-separated value becomes multiple entries: --lang "english,chinese".
func zlibSearchOptions(opts searchOptions) zlib.SearchOptions {
	return zlib.SearchOptions{
		Query:      opts.query,
		Limit:      opts.limit,
		Page:       opts.page,
		YearFrom:   opts.yearFrom,
		YearTo:     opts.yearTo,
		Languages:  splitList(opts.lang),
		Extensions: splitList(opts.ext),
		Exact:      opts.exact,
		Order:      opts.order,
	}
}

// splitList splits a comma-separated flag value, dropping blank entries so
// --lang "" is simply no filter rather than one empty filter.
func splitList(v string) []string {
	if v == "" {
		return nil
	}
	parts := make([]string, 0, 2)
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}
