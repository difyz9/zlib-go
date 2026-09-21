package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/difyz9/zlib-go/internal/ui"
	"github.com/difyz9/zlib-go/internal/zlib"
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

	// Progress bars rewrite one line with \r, which only makes sense on an
	// interactive stderr; JSON mode and captured output get plain results.
	if !ctx.JSON && isTerminal(ctx.Err) {
		ctx.Progress = ui.NewProgress(ctx.Err, "Downloading")
	}

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
		path, err = downloadZlibWithRotation(dctx, ctx, tgt.ID, tgt.Hash, dir, *name)
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

// downloadZlibWithRotation downloads one book, rotating through every stored
// Z-Library account when an account's daily download limit blocks the transfer.
//
// A free account gets ten downloads per day, so the intended workflow is to
// store several accounts (`zlib account add`) and let this loop pick one with
// quota left, starting from the active account. Two signals trigger a switch:
// a profile that reports zero downloads left (checked before spending an
// attempt), and a quota error from the download endpoint itself.
func downloadZlibWithRotation(dctx context.Context, ctx *Context, id, hash, dir, name string) (string, error) {
	if !ctx.Cfg.HasZlib() {
		return "", errNoZlibCredentials
	}

	domain := zlib.ResolveDomain(dctx, ctx.Cfg.Zlib.Domain, zlib.ProbeTimeout, ctx.Logf)
	accounts := ctx.Cfg.AccountList()

	var (
		exhausted []string
		lastErr   error
	)
	for _, acc := range accounts {
		if !acc.Usable() {
			continue
		}
		client, err := newZlibAccountClient(dctx, ctx, domain, acc)
		if err != nil {
			lastErr = err
			ctx.Logf("account %s unusable: %v", acc.Email, err)
			continue
		}

		// Skip accounts the profile reports as spent, so a rotation does not
		// burn a failed attempt (and a confusing error message) per account.
		if profile, perr := client.GetProfile(dctx); perr == nil {
			if profile.DownloadsLeft <= 0 {
				exhausted = append(exhausted, fmt.Sprintf("%s: %d/%d used today",
					acc.Email, profile.DownloadsToday, profile.DownloadsLimit))
				continue
			}
			ctx.Logf("account %s has %d/%d downloads left", acc.Email,
				profile.DownloadsLeft, profile.DownloadsLimit)
		}

		path, derr := client.Download(dctx, id, hash, dir, name)
		if derr == nil {
			return path, nil
		}
		if zlib.IsQuotaError(derr) {
			exhausted = append(exhausted, fmt.Sprintf("%s: %v", acc.Email, derr))
			fmt.Fprintf(ctx.Err, "%s\n", ctx.Colors.Yellow(fmt.Sprintf(
				"account %s hit its download limit; switching to the next account...", acc.Email)))
			continue
		}
		// Every other failure (book removed, wall, network) does not depend on
		// the account, so rotating would only repeat the same error.
		return "", derr
	}

	if len(exhausted) > 0 {
		return "", fmt.Errorf(
			"the daily download limit is exhausted on every stored account:\n  %s\n"+
				"  The quota resets on a rolling 24-hour basis, or add another account:\n"+
				"  zlib account add --email <email> --password <password>",
			strings.Join(exhausted, "\n  "))
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no usable Z-Library account is configured")
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
