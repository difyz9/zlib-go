package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/difyz9/zlib-go/internal/config"
)

// stdout and stderr are variables so tests can substitute buffers.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// SetWriters overrides the output streams, for tests.
func SetWriters(out, err io.Writer) {
	stdout, stderr = out, err
}

func printHelp(ctx *Context) {
	w := ctx.Out
	b := ctx.Colors.Bold

	fmt.Fprint(w, `zlib — search and download books from Z-Library and Anna's Archive

`)
	fmt.Fprintf(w, "%s\n", b("USAGE"))
	fmt.Fprint(w, `  zlib [global options] <command> [arguments]

`)
	fmt.Fprintf(w, "%s\n", b("COMMANDS"))
	fmt.Fprint(w, `  search <query>        Search for books
  info <id>/<hash>      Show full metadata for one book (Z-Library)
  download <target>     Download a book
  login                 Sign in to Z-Library and cache the session token
  quota                 Show the account's remaining daily downloads
  config show|set|reset Inspect or change configuration
  doctor                Check credentials, dependencies, and connectivity
  version               Print the version
  help                  Print this message

`)
	fmt.Fprintf(w, "%s\n", b("GLOBAL OPTIONS"))
	fmt.Fprint(w, `  --json                Emit JSON instead of a table
  --verbose             Print progress details to stderr
  --no-color            Disable colored output (also honours NO_COLOR)

`)
	fmt.Fprintf(w, "%s\n", b("EXAMPLES"))
	fmt.Fprint(w, `  # Search Z-Library, fall back to Anna's Archive automatically
  zlib search "deep learning" --limit 5

  # Chinese books as PDFs on Z-Library only
  zlib search "莱姆 索利斯" --source zlib --lang chinese --ext pdf

  # Pipe results into jq
  zlib --json search "reinforcement learning" | jq '.books[].title'

  # Download by the identifier shown in a search result
  zlib download 12345/abc123def456 -o ~/Books

  # Anna's Archive download by MD5
  zlib download a1b2c3d4e5f6 --source annas --name "some_book.pdf"

  # Sign in once; the token is cached so later runs skip the rate-limited API
  zlib login

`)
	fmt.Fprintf(w, "%s\n", b("CONFIGURATION"))
	fmt.Fprintf(w, "  File: %s\n", config.Path())
	fmt.Fprint(w, `  Environment variables override the file:
    ZLIBRARY_EMAIL, ZLIBRARY_PASSWORD   Z-Library credentials
    ZLIBRARY_EAPI_DOMAIN                Pin a specific EAPI domain (skips probing)
    ANNAS_SECRET_KEY, ANNAS_BASE_URL    Anna's Archive API access
    ZLIB_DOWNLOAD_DIR                   Default download directory
    ZLIB_SOURCE                         Default source: auto|zlib|annas

`)
	fmt.Fprintf(w, "%s\n", b("NOTES"))
	fmt.Fprint(w, `  Z-Library has no documented API; this tool speaks its undocumented EAPI
  endpoints. Its domains rotate and are sometimes behind an anti-bot wall, so
  a working domain is probed at runtime. Pin one with ZLIBRARY_EAPI_DOMAIN if
  automatic selection ever picks a blocked host.

  Z-Library rate-limits logins to roughly ten per hour per IP. Run `+"`zlib login`"+`
  once; the cached token is reused until you run `+"`config reset`"+`.

  Free Z-Library accounts can download about ten books per day. Check the
  remaining quota with `+"`zlib quota`"+` before spending one.
`)
}
