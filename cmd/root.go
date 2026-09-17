// Package cmd implements the zlib CLI: argument parsing, dispatch, and the
// handlers for every subcommand.
package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"zlib/internal/config"
	"zlib/internal/fetch"
	"zlib/internal/ui"
)

// Version is the tool version, reported by `zlib version`.
const Version = "0.1.0"

// Context carries per-invocation state to subcommand handlers.
type Context struct {
	Cfg    *config.Config
	Colors *ui.Colors
	Out    io.Writer
	Err    io.Writer

	JSON    bool
	Verbose bool

	// Progress, when set, receives download transfer updates. It is populated
	// by `download` for interactive runs only; search and info never need it.
	Progress fetch.ProgressReporter
}

// Logf writes a progress line to stderr, and only in verbose mode. Progress
// must never go to stdout: stdout carries the machine-readable result that a
// pipeline consumes.
func (c *Context) Logf(format string, args ...any) {
	if c.Verbose {
		fmt.Fprintf(c.Err, "· "+format+"\n", args...)
	}
}

// isTerminal reports whether w is a character device such as a terminal.
// Progress bars rewrite one line with \r, which is unreadable when stderr is
// captured to a file, so they are shown only when this returns true.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// UsageError marks a problem with how the tool was invoked, which exits 2 to
// distinguish it from a runtime failure (exit 1).
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(format string, args ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, args...)}
}

// Run parses global options, dispatches to a subcommand, and returns a process
// exit code.
func Run(args []string) int {
	global, rest, err := parseGlobals(args)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		fmt.Fprintln(stderr, "Run `zlib help` for usage.")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	cfg.NoColor = cfg.NoColor || global.noColor
	// A proxy on the command line wins over the config file and the
	// environment, which is what makes `--proxy` useful for a one-off attempt.
	if global.proxy != "" {
		cfg.Proxy = global.proxy
	}

	ctx := &Context{
		Cfg:     cfg,
		Colors:  ui.NewColors(!cfg.NoColor),
		Out:     stdout,
		Err:     stderr,
		JSON:    global.json,
		Verbose: global.verbose,
	}

	if len(rest) == 0 {
		printHelp(ctx)
		return 0
	}

	cmd, cmdArgs := rest[0], rest[1:]
	var runErr error
	switch cmd {
	case "search", "find":
		runErr = cmdSearch(ctx, cmdArgs)
	case "info", "show":
		runErr = cmdInfo(ctx, cmdArgs)
	case "download", "dl", "get":
		runErr = cmdDownload(ctx, cmdArgs)
	case "login":
		runErr = cmdLogin(ctx, cmdArgs)
	case "quota", "profile":
		runErr = cmdQuota(ctx, cmdArgs)
	case "config":
		runErr = cmdConfig(ctx, cmdArgs)
	case "doctor", "check":
		runErr = cmdDoctor(ctx, cmdArgs)
	case "version", "--version", "-v":
		fmt.Fprintf(ctx.Out, "zlib %s\n", Version)
		return 0
	case "help", "--help", "-h":
		printHelp(ctx)
		return 0
	default:
		fmt.Fprintf(stderr, "ERROR: unknown command %q\n\n", cmd)
		printHelp(ctx)
		return 2
	}

	if runErr != nil {
		return reportError(ctx, runErr)
	}
	return 0
}

// reportError renders a failure and picks the exit code.
func reportError(ctx *Context, err error) int {
	var usage *UsageError
	if errors.As(err, &usage) {
		// A usage error implies an interactive reader, so print the command
		// help they most likely needed.
		fmt.Fprintf(ctx.Err, "ERROR: %s\n", usage.Msg)
		if hint := usageHint(usage.Msg); hint != "" {
			fmt.Fprintf(ctx.Err, "HINT: %s\n", hint)
		}
		return 2
	}

	if ctx.JSON {
		// Machine consumers get a structured failure on stderr so a `jq`
		// pipeline can distinguish it from an empty result set on stdout.
		fmt.Fprintf(ctx.Err, "{\"error\": %q}\n", err.Error())
	} else {
		fmt.Fprintf(ctx.Err, "ERROR: %s\n", err)
	}
	return 1
}

func usageHint(msg string) string {
	switch {
	case strings.Contains(msg, "query"):
		return "Example: zlib search \"deep learning\" --limit 5"
	case strings.Contains(msg, "id and hash"):
		return "Get both from a search result, then run: zlib download <id>/<hash>"
	case strings.Contains(msg, "not defined"):
		// The one shape the leading-option parser cannot accept is a global
		// option written after the command, which is also the natural way to
		// ask for JSON.
		return "Global options (--json, --verbose, --no-color, --proxy) go before the command name:\n" +
			"    zlib --json doctor"
	default:
		return ""
	}
}

// globalOptions are the flags accepted before the subcommand name.
type globalOptions struct {
	json    bool
	verbose bool
	noColor bool
	proxy   string
}

// parseGlobals splits leading global flags from the subcommand arguments.
//
// Only flags appearing before the subcommand name are consumed, so a
// subcommand can define its own --json or -v without colliding.
func parseGlobals(args []string) (globalOptions, []string, error) {
	var opts globalOptions
	i := 0
	for ; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.json = true
		case "--verbose":
			opts.verbose = true
		case "--no-color":
			opts.noColor = true
		case "--proxy":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--proxy needs a value, e.g. --proxy socks5://127.0.0.1:1080")
			}
			opts.proxy = args[i+1]
			i++
		case "--":
			i++
			return opts, args[i:], nil
		default:
			return opts, args[i:], nil
		}
	}
	return opts, args[i:], nil
}

// newFlagSet builds a flag set that never writes its own errors or calls
// os.Exit, so handlers stay testable and the failure is reported once, in the
// format reportError uses.
func newFlagSet(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	// The flag package would otherwise print "flag provided but not defined: -x"
	// before parseFlags turns the same problem into a UsageError, which
	// reportError then prints again with a hint attached.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	_ = usage
	return fs
}

// parseFlags parses args and converts flag errors into UsageError values.
//
// The arguments are reordered first so a flag may appear anywhere on the
// command line. Go's flag package stops parsing at the first non-flag argument,
// which would make the natural `zlib extract book.epub --stdout` treat
// `--stdout` as a filename — a silent, confusing failure rather than a usage
// error.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return &UsageError{Msg: "help requested"}
		}
		return &UsageError{Msg: err.Error()}
	}
	return nil
}

// reorderArgs moves flag arguments ahead of positional ones, preserving the
// relative order within each group.
//
// A flag that takes a value keeps its value attached, which is why the set is
// consulted rather than a simple prefix test: `--out /tmp/x` must move as a
// pair, or `/tmp/x` would be read as a filename.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			// Everything after -- is positional by definition. The marker itself
			// must be carried through: dropping it would re-expose those
			// arguments to flag parsing, so `--limit 3 -- --weird` would fail on
			// an unknown flag instead of treating --weird as the query.
			flags = append(flags, arg)
			positional = append(positional, args[i+1:]...)
			break
		}

		// A lone "-" is a conventional stdin placeholder, not a flag.
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}

		flags = append(flags, arg)

		// "--name=value" already carries its value.
		if strings.Contains(arg, "=") {
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if isBoolFlag(fs.Lookup(name)) {
			continue
		}
		// An unknown flag is left alone rather than given the next argument:
		// consuming it could swallow a real filename, and flag.Parse will
		// report the unknown flag itself.
		if fs.Lookup(name) == nil {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	return append(flags, positional...)
}

// isBoolFlag reports whether a flag takes no value.
func isBoolFlag(f *flag.Flag) bool {
	if f == nil {
		return false
	}
	if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok {
		return bf.IsBoolFlag()
	}
	return false
}
