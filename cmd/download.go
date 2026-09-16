package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// target is a book identifier in either of the two shapes the CLI accepts:
// "id/hash" for Z-Library, or a bare hash/MD5.
type target struct {
	ID   string
	Hash string
}

// parseTarget splits an identifier argument.
//
// The id/hash form is what `search` prints for Z-Library results, so it can be
// pasted back verbatim. A bare value is treated as an MD5, which is what Anna's
// Archive and LibGen use.
func parseTarget(arg string) target {
	arg = strings.TrimSpace(arg)
	if i := strings.Index(arg, "/"); i > 0 && i < len(arg)-1 {
		return target{ID: arg[:i], Hash: arg[i+1:]}
	}
	return target{Hash: arg}
}

// downloadOutput is the JSON envelope a download reports.
type downloadOutput struct {
	Source string `json:"source"`
	Status string `json:"status"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Note   string `json:"note,omitempty"`
}

func cmdDownload(ctx *Context, args []string) error {
	fs := newFlagSet("download", "")
	source := fs.String("source", "", "backend to use: zlib|annas")
	flagID := fs.String("id", "", "book id (Z-Library)")
	flagHash := fs.String("hash", "", "book hash (Z-Library) or MD5 (Anna's Archive)")
	flagMD5 := fs.String("md5", "", "MD5 (Anna's Archive); alias of --hash")
	outDir := fs.String("out", "", "output directory (default from config)")
	fs.StringVar(outDir, "o", "", "output directory (shorthand)")
	name := fs.String("name", "", "output filename, e.g. \"deep_learning.pdf\"")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	// Accept the identifier either positionally or through flags.
	tgt := target{}
	switch {
	case *flagID != "" || *flagHash != "":
		tgt = target{ID: *flagID, Hash: *flagHash}
	case *flagMD5 != "":
		tgt = target{Hash: *flagMD5}
	case len(fs.Args()) == 1:
		tgt = parseTarget(fs.Args()[0])
	case len(fs.Args()) > 1:
		return usagef("expected a single identifier argument, got %d", len(fs.Args()))
	default:
		return usagef("an identifier is required (see `zlib help` for the accepted forms)")
	}
	if tgt.Hash == "" {
		return usagef("no book hash or MD5 was provided")
	}

	picked := *source
	if picked == "" {
		// The identifier shape decides: an id/hash pair is Z-Library's form,
		// while a bare MD5 can only come from the MD5-backed sources.
		if tgt.ID != "" {
			picked = "zlib"
		} else {
			picked = "annas"
		}
	}
	switch picked {
	case "zlib", "annas":
	default:
		return usagef("unknown --source %q (expected zlib or annas)", picked)
	}

	dir := *outDir
	if dir == "" {
		dir = ctx.Cfg.DownloadDir
	}
	if dir == "" {
		return usagef("no output directory: pass --out or set download_dir in the config")
	}
	dir = expandHome(dir)

	dctx, cancel := context.WithTimeout(context.Background(), downloadBudget)
	defer cancel()

	var (
		path    string
		err     error
		source2 = picked
	)
	switch picked {
	case "zlib":
		if tgt.ID == "" {
			return usagef("Z-Library downloads need both the book id and its hash.\n" +
				"  Copy the identifier from a search result: zlib download <id>/<hash>\n" +
				"  Or download by MD5 from Anna's Archive: zlib download <md5> --source annas")
		}
		client, cerr := newZlibClient(dctx, ctx)
		if cerr != nil {
			return cerr
		}
		path, err = client.Download(dctx, tgt.ID, tgt.Hash, dir, *name)
	case "annas":
		client, cerr := newAnnasClient(ctx)
		if cerr != nil {
			return cerr
		}
		path, err = client.Download(dctx, tgt.Hash, dir, *name)
	}
	if err != nil {
		return err
	}

	info, statErr := os.Stat(path)
	var size int64
	if statErr == nil {
		size = info.Size()
	}

	if ctx.JSON {
		return writeJSON(ctx, downloadOutput{
			Source: source2,
			Status: "ok",
			Path:   path,
			Size:   size,
		})
	}
	fmt.Fprintf(ctx.Out, "%s %s\n", ctx.Colors.Green("Downloaded:"), path)
	if size > 0 {
		fmt.Fprintf(ctx.Out, "%s %s\n", ctx.Colors.Dim("Size:"), humanBytes(size))
	}
	fmt.Fprintf(ctx.Out, "%s open %q\n", ctx.Colors.Dim("Open with:"), path)
	return nil
}

// downloadBudget bounds a single transfer including link resolution.
const downloadBudget = 10 * time.Minute

// expandHome resolves a leading ~ so --out ~/Books works when the shell did not
// expand it (for example when the value came from the config file).
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + strings.TrimPrefix(p, "~")
		}
	}
	return p
}

// humanBytes renders a size for humans.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
