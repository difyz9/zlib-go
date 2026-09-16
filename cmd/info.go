package cmd

import (
	"context"
	"fmt"
	"strings"

	"zlib/internal/model"
	"zlib/internal/ui"
)

// infoOutput is the JSON envelope `--json info` prints.
type infoOutput struct {
	Book  model.Book        `json:"book"`
	Raw   map[string]any    `json:"raw,omitempty"` // provider payload, for fields the model does not carry
	Extra map[string]string `json:"extra,omitempty"`
}

func cmdInfo(ctx *Context, args []string) error {
	fs := newFlagSet("info", "")
	flagID := fs.String("id", "", "book id")
	flagHash := fs.String("hash", "", "book hash")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	tgt := target{}
	switch {
	case *flagID != "" || *flagHash != "":
		tgt = target{ID: *flagID, Hash: *flagHash}
	case len(fs.Args()) == 1:
		tgt = parseTarget(fs.Args()[0])
	case len(fs.Args()) > 1:
		return usagef("expected a single identifier argument, got %d", len(fs.Args()))
	default:
		return usagef("a book identifier is required (id/hash or --id with --hash)")
	}
	if tgt.ID == "" || tgt.Hash == "" {
		return usagef("book info needs both the id and the hash.\n" +
			"  Copy the identifier from a search result: zlib info <id>/<hash>")
	}

	ictx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	client, err := newZlibClient(ictx, ctx)
	if err != nil {
		return err
	}
	book, raw, err := client.GetBookInfo(ictx, tgt.ID, tgt.Hash)
	if err != nil {
		return err
	}

	if ctx.JSON {
		return writeJSON(ctx, infoOutput{Book: book, Raw: raw})
	}

	// A two-column key/value table keeps long descriptions readable without
	// wrapping logic, since only the value column is capped.
	rows := [][2]string{
		{"Title", book.Title},
		{"Author", book.Author},
		{"Publisher", book.Publisher},
		{"Year", book.Year},
		{"Language", book.Language},
		{"Format", book.Extension},
		{"Size", book.Filesize},
		{"ISBN", book.ISBN},
		{"Pages", book.Pages},
		{"ID", book.ID},
		{"Hash", book.Hash},
	}

	table := &ui.Table{
		Headers:  []string{"", ""},
		Aligns:   []ui.Alignment{ui.AlignLeft, ui.AlignLeft},
		Stylers:  []func(string) string{nil, nil},
		MaxWidth: []int{12, 96},
		NoHeader: true,
	}
	for _, r := range rows {
		if strings.TrimSpace(r[1]) == "" {
			continue // a source that omits a field shows no row rather than an empty one
		}
		table.Rows = append(table.Rows, []string{r[0], r[1]})
	}
	table.Render(ctx.Out)

	if book.Description != "" {
		fmt.Fprintf(ctx.Out, "\n%s\n%s\n", ctx.Colors.Bold("Description"),
			wrapText(book.Description, 96))
	}
	fmt.Fprintf(ctx.Out, "\n%s zlib download %s/%s\n",
		ctx.Colors.Dim("Download with:"), book.ID, book.Hash)
	return nil
}

// wrapText wraps s at width display columns, preserving existing line breaks.
//
// Wrapping is done on words, and the width test uses display width so a
// description containing Chinese text does not run to double the intended line
// length.
func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out strings.Builder
	for _, paragraph := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(paragraph) {
			switch {
			case line == "":
				line = word
			case ui.DisplayWidth(line)+1+ui.DisplayWidth(word) <= width:
				line += " " + word
			default:
				out.WriteString(line)
				out.WriteByte('\n')
				line = word
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return strings.TrimRight(out.String(), "\n")
}
