package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/difyz9/zlib-go/internal/model"
	"github.com/difyz9/zlib-go/internal/ui"
)

// searchOutput is the JSON envelope `--json search` prints.
type searchOutput struct {
	Source string       `json:"source"`
	Query  string       `json:"query"`
	Count  int          `json:"count"`
	Books  []model.Book `json:"books"`
	// Errors records per-backend failures in auto mode, so a caller can tell
	// "Anna's Archive returned nothing" from "Z-Library was unreachable".
	Errors []string `json:"errors,omitempty"`
}

func cmdSearch(ctx *Context, args []string) error {
	fs := newFlagSet("search", "")
	source := fs.String("source", "", "backend to use: auto|zlib|annas (default from config)")
	limit := fs.Int("limit", 10, "maximum number of results")
	page := fs.Int("page", 1, "result page")
	lang := fs.String("lang", "", "language filter, e.g. english or chinese")
	ext := fs.String("ext", "", "file extension filter, e.g. pdf or epub")
	yearFrom := fs.Int("year-from", 0, "publication year lower bound")
	yearTo := fs.Int("year-to", 0, "publication year upper bound")
	exact := fs.Bool("exact", false, "require an exact title match")
	order := fs.String("order", "", "result ordering, e.g. popularity or date")
	pager := fs.Bool("no-pager", false, "disable the interactive result pager (n/p/number/q)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return usagef("a search query is required")
	}
	if *limit <= 0 {
		return usagef("--limit must be greater than zero")
	}

	picked := *source
	if picked == "" {
		picked = ctx.Cfg.DefaultSource
	}
	switch picked {
	case "auto", "zlib", "annas":
	default:
		return usagef("unknown --source %q (expected auto, zlib, or annas)", picked)
	}

	opts := searchOptions{
		query:    query,
		limit:    *limit,
		page:     *page,
		lang:     *lang,
		ext:      *ext,
		yearFrom: *yearFrom,
		yearTo:   *yearTo,
		exact:    *exact,
		order:    *order,
	}

	books, usedSource, errs, err := runSearch(ctx, picked, opts)
	if err != nil {
		return err
	}

	if ctx.JSON {
		return writeJSON(ctx, searchOutput{
			Source: usedSource,
			Query:  query,
			Count:  len(books),
			Books:  books,
			Errors: errs,
		})
	}

	if len(books) == 0 {
		fmt.Fprintf(ctx.Out, "No results for %q", query)
		if len(errs) > 0 {
			fmt.Fprintf(ctx.Out, " (%s)", strings.Join(errs, "; "))
		}
		fmt.Fprintln(ctx.Out)
		return nil
	}

	fmt.Fprintf(ctx.Out, "%s %s\n\n",
		ctx.Colors.Dim(fmt.Sprintf("%d result(s) from", len(books))),
		ctx.Colors.Source(usedSource))

	table := ui.BookTable(books, ctx.Colors, ui.BookColumns{
		ShowIndex:      true,
		ShowSource:     ui.MixedSources(books),
		ShowIdentifier: books[0].Identifier() != "",
	})
	table.Render(ctx.Out)

	// The Download ID column carries the per-row identifier; the hint below
	// only shows the command shape using the first row as a concrete example.
	if ident := books[0].Identifier(); ident != "" {
		fmt.Fprintf(ctx.Out, "\n%s zlib download %s -o ~/Downloads\n",
			ctx.Colors.Dim("Download with (use the Download ID column):"), ident)
	}
	if len(errs) > 0 {
		fmt.Fprintf(ctx.Out, "%s\n", ctx.Colors.Yellow("Note: "+strings.Join(errs, "; ")))
	}

	// Interactive paging: on a terminal, the user can page through further
	// results without retyping the search. Non-interactive runs (pipes, JSON
	// output, --no-pager) behave exactly as before.
	if !*pager && canPage(ctx) {
		return runPager(ctx, picked, opts, *page)
	}
	return nil
}

// canPage reports whether interactive result paging makes sense: stdout is a
// terminal (so the table was human-facing) and stdin is readable.
func canPage(ctx *Context) bool {
	if ctx.JSON || ctx.In == nil {
		return false
	}
	return isTerminal(ctx.Out)
}

// runPager drives the n/p/q/number loop over result pages.
//
// Each page triggers a fresh backend request, which is how the underlying
// APIs paginate; nothing is buffered client-side.
func runPager(ctx *Context, source string, opts searchOptions, page int) error {
	reader := bufio.NewReader(ctx.In)
	for {
		fmt.Fprintf(ctx.Out, "\n%s ", ctx.Colors.Dim(
			fmt.Sprintf("[page %d] (n)ext  (p)rev  (1-999) jump  (q)uit:", page)))
		line, err := reader.ReadString('\n')
		if err != nil {
			// EOF (Ctrl-D) or a closed stdin ends the pager cleanly.
			fmt.Fprintln(ctx.Out)
			return nil
		}
		cmd := strings.ToLower(strings.TrimSpace(line))
		var next int
		switch {
		case cmd == "" || cmd == "n" || cmd == "next":
			next = page + 1
		case cmd == "p" || cmd == "prev":
			next = page - 1
			if next < 1 {
				next = 1
			}
		case cmd == "q" || cmd == "quit" || cmd == "exit":
			return nil
		default:
			n, cerr := strconv.Atoi(cmd)
			if cerr != nil || n < 1 {
				fmt.Fprintf(ctx.Out, "%s\n", ctx.Colors.Dim("(unknown command; n/p/number/q)"))
				continue
			}
			next = n
		}

		if next == page {
			fmt.Fprintf(ctx.Out, "%s\n", ctx.Colors.Dim("(already on page 1)"))
			continue
		}

		popts := opts
		popts.page = next
		books, usedSource, _, serr := runSearch(ctx, source, popts)
		if serr != nil {
			fmt.Fprintf(ctx.Out, "%s %v\n", ctx.Colors.Yellow("Search failed:"), serr)
			continue
		}
		if len(books) == 0 {
			fmt.Fprintf(ctx.Out, "%s\n", ctx.Colors.Dim("(no results on this page)"))
			continue
		}
		// A page shorter than the requested limit means the end of the result
		// set on most backends.
		if len(books) < opts.limit {
			fmt.Fprintf(ctx.Out, "%s\n", ctx.Colors.Dim("(end of results)"))
			continue
		}
		page = next

		fmt.Fprintf(ctx.Out, "\n%s %s\n\n",
			ctx.Colors.Dim(fmt.Sprintf("%d result(s) from", len(books))),
			ctx.Colors.Source(usedSource))
		table := ui.BookTable(books, ctx.Colors, ui.BookColumns{
			ShowIndex:      true,
			ShowSource:     ui.MixedSources(books),
			ShowIdentifier: books[0].Identifier() != "",
		})
		table.Render(ctx.Out)
		if ident := books[0].Identifier(); ident != "" {
			fmt.Fprintf(ctx.Out, "\n%s zlib download %s -o ~/Downloads\n",
				ctx.Colors.Dim("Download with (use the Download ID column):"), ident)
		}
	}
}

// searchOptions holds the parsed search filters.
type searchOptions struct {
	query    string
	limit    int
	page     int
	lang     string
	ext      string
	yearFrom int
	yearTo   int
	exact    bool
	order    string
}

// runSearch dispatches to the requested backend.
//
// In auto mode Z-Library is tried first and Anna's Archive second. A backend
// that fails is recorded in errors rather than aborting the search, so one dead
// source still yields usable results from the other.
func runSearch(ctx *Context, source string, opts searchOptions) ([]model.Book, string, []string, error) {
	ctxTimeout, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	switch source {
	case "zlib":
		books, err := searchZlib(ctx, ctxTimeout, opts)
		return books, "zlib", nil, err
	case "annas":
		books, err := searchAnnas(ctx, ctxTimeout, opts)
		return books, "annas", nil, err
	}

	var errs []string
	if ctx.Cfg.HasZlib() {
		if books, err := searchZlib(ctx, ctxTimeout, opts); err == nil {
			return books, "zlib", nil, nil
		} else {
			ctx.Logf("zlib search failed: %v", err)
			errs = append(errs, "zlib: "+err.Error())
		}
	} else {
		errs = append(errs, "zlib: no credentials configured")
	}

	// Anna's Archive search needs no API key, so it is always worth trying.
	if books, err := searchAnnas(ctx, ctxTimeout, opts); err == nil {
		return books, "annas", errs, nil
	} else {
		ctx.Logf("annas search failed: %v", err)
		errs = append(errs, "annas: "+err.Error())
	}

	return nil, "", errs, fmt.Errorf(
		"no backend could complete the search.\n  %s\n"+
			"  Configure Z-Library with `zlib config set --zlib-email <email> --zlib-password <password>`,\n"+
			"  or retry later if the network is the problem",
		strings.Join(errs, "\n  "))
}

func searchZlib(ctx *Context, gctx context.Context, opts searchOptions) ([]model.Book, error) {
	client, err := newZlibClient(gctx, ctx)
	if err != nil {
		return nil, err
	}
	books, err := client.Search(gctx, zlibSearchOptions(opts))
	if err != nil {
		return nil, err
	}
	if len(books) == 0 {
		return nil, fmt.Errorf("Z-Library returned no matches")
	}
	return books, nil
}

func searchAnnas(ctx *Context, gctx context.Context, opts searchOptions) ([]model.Book, error) {
	client, err := newAnnasClient(ctx)
	if err != nil {
		return nil, err
	}
	books, err := client.Search(gctx, opts.query, opts.limit, opts.page)
	if err != nil {
		return nil, err
	}
	if len(books) == 0 {
		return nil, fmt.Errorf("Anna's Archive returned no matches")
	}
	return books, nil
}

// writeJSON renders any value as indented JSON on stdout.
func writeJSON(ctx *Context, v any) error {
	enc := json.NewEncoder(ctx.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}
