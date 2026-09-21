package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/difyz9/zlib-go/internal/config"
)

func cmdConfig(ctx *Context, args []string) error {
	if len(args) == 0 {
		return usagef("config needs a subcommand: show, set, or reset")
	}
	switch args[0] {
	case "show", "list":
		return configShow(ctx)
	case "set":
		return configSet(ctx, args[1:])
	case "reset":
		return configReset(ctx)
	default:
		return usagef("unknown config subcommand %q (expected show, set, or reset)", args[0])
	}
}

func configShow(ctx *Context) error {
	redacted := ctx.Cfg.Redacted()
	if ctx.JSON {
		return writeJSON(ctx, redacted)
	}

	b := ctx.Colors.Bold
	fmt.Fprintf(ctx.Out, "%s %s\n", b("Config file:"), config.Path())
	// Say plainly whether the file exists: an empty-looking config is otherwise
	// ambiguous between "not created" and "every field is blank".
	if _, err := os.Stat(config.Path()); err != nil {
		fmt.Fprintf(ctx.Out, "  %s\n", ctx.Colors.Dim("(not created yet; using defaults and environment only)"))
	}

	rows := [][2]string{
		{"download_dir", ctx.Cfg.DownloadDir},
		{"default_source", ctx.Cfg.DefaultSource},
		{"zlib.email", orDash(redacted.Zlib.Email)},
		{"zlib.password", orDash(redacted.Zlib.Password)},
		{"zlib.remix_userid", orDash(redacted.Zlib.RemixUserID)},
		{"zlib.remix_userkey", orDash(redacted.Zlib.RemixUserKey)},
		{"zlib.domain", orDash(redacted.Zlib.Domain)},
		{"annas.base_url", orDash(redacted.Annas.BaseURL)},
		{"annas.secret_key", orDash(redacted.Annas.SecretKey)},
		{"libgen.mirror", orDash(redacted.Libgen.Mirror)},
	}
	for _, r := range rows {
		fmt.Fprintf(ctx.Out, "  %-20s %s\n", r[0], r[1])
	}
	return nil
}

func configSet(ctx *Context, args []string) error {
	fs := newFlagSet("config set", "")
	email := fs.String("zlib-email", "", "Z-Library account email")
	password := fs.String("zlib-password", "", "Z-Library account password")
	domain := fs.String("zlib-domain", "", "pin a specific EAPI domain")
	annasKey := fs.String("annas-key", "", "Anna's Archive API key")
	annasURL := fs.String("annas-url", "", "Anna's Archive base URL")
	libgenMirror := fs.String("libgen-mirror", "", "LibGen mirror suffix, e.g. li")
	downloadDir := fs.String("download-dir", "", "default download directory")
	source := fs.String("source", "", "default source: auto|zlib|annas")
	proxy := fs.String("proxy", "", "proxy URL for blocked sources, e.g. socks5://127.0.0.1:1080")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	changed := []string{}
	if *email != "" || *password != "" {
		// Route through the account store so the multi-account list stays the
		// single source of truth; the legacy fields are mirrored on save.
		mail, pass := *email, *password
		if mail == "" {
			mail = ctx.Cfg.Zlib.Email
		}
		if pass == "" && *email == "" {
			pass = ctx.Cfg.Zlib.Password
		}
		if mail != "" && pass != "" {
			if _, err := ctx.Cfg.AddZlibAccount(mail, pass); err != nil {
				return err
			}
		}
		if *email != "" {
			ctx.Cfg.Zlib.Email = *email
			changed = append(changed, "zlib.email", "zlib.remix token (cleared)")
		}
		if *password != "" {
			ctx.Cfg.Zlib.Password = *password
			changed = append(changed, "zlib.password", "zlib.remix token (cleared)")
		}
	}
	if *domain != "" {
		ctx.Cfg.Zlib.Domain = *domain
		changed = append(changed, "zlib.domain")
	}
	if *annasKey != "" {
		ctx.Cfg.Annas.SecretKey = *annasKey
		changed = append(changed, "annas.secret_key")
	}
	if *annasURL != "" {
		ctx.Cfg.Annas.BaseURL = *annasURL
		changed = append(changed, "annas.base_url")
	}
	if *libgenMirror != "" {
		ctx.Cfg.Libgen.Mirror = *libgenMirror
		changed = append(changed, "libgen.mirror")
	}
	if *downloadDir != "" {
		ctx.Cfg.DownloadDir = *downloadDir
		changed = append(changed, "download_dir")
	}
	if *source != "" {
		switch *source {
		case "auto", "zlib", "annas":
		default:
			return usagef("unknown --source %q (expected auto, zlib, or annas)", *source)
		}
		ctx.Cfg.DefaultSource = *source
		changed = append(changed, "default_source")
	}
	if *proxy != "" {
		ctx.Cfg.Proxy = *proxy
		changed = append(changed, "proxy")
	}

	if len(changed) == 0 {
		return usagef("nothing to set. Pass at least one option, e.g. " +
			"--zlib-email <email> --zlib-password <password>")
	}
	if err := ctx.Cfg.Save(); err != nil {
		return err
	}

	if ctx.JSON {
		return writeJSON(ctx, map[string]any{
			"status":  "ok",
			"file":    config.Path(),
			"changed": changed,
		})
	}
	fmt.Fprintf(ctx.Out, "%s %s\n", ctx.Colors.Green("Updated"), config.Path())
	fmt.Fprintf(ctx.Out, "  changed: %s\n", strings.Join(changed, ", "))
	if ctx.Cfg.HasZlib() && ctx.Cfg.Zlib.RemixUserKey == "" {
		fmt.Fprintf(ctx.Out, "\n%s %s\n", ctx.Colors.Dim("Next:"),
			"run `zlib login` to authenticate and cache a session token (logins are rate limited)")
	}
	return nil
}

func configReset(ctx *Context) error {
	cfg := &config.Config{
		Annas:         config.AnnasConfig{BaseURL: config.DefaultAnnasBaseURL},
		Libgen:        config.LibgenConfig{Mirror: config.DefaultLibgenMirror},
		DefaultSource: config.DefaultSource,
	}
	if ctx.Cfg.DownloadDir != "" {
		cfg.DownloadDir = ctx.Cfg.DownloadDir
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	if ctx.JSON {
		return writeJSON(ctx, map[string]any{"status": "ok", "file": config.Path()})
	}
	fmt.Fprintf(ctx.Out, "%s credentials cleared in %s\n", ctx.Colors.Green("Reset"), config.Path())
	fmt.Fprintf(ctx.Out, "%s\n", ctx.Colors.Dim("  Environment variables still apply."))
	return nil
}

// --- login ---

func cmdLogin(ctx *Context, args []string) error {
	fs := newFlagSet("login", "")
	email := fs.String("email", "", "Z-Library email (defaults to the configured value)")
	password := fs.String("password", "", "Z-Library password (defaults to the configured value)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	if *email == "" {
		*email = ctx.Cfg.Zlib.Email
	}
	if *password == "" && *email == ctx.Cfg.Zlib.Email {
		*password = ctx.Cfg.Zlib.Password
	}
	if *email == "" || *password == "" {
		return usagef("email and password are required.\n" +
			"  Pass them here: zlib login --email <email> --password <password>\n" +
			"  Or store them first: zlib account add --email <email> --password <password>")
	}

	lctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	// Store (or update) the account first so the token can be cached on it.
	if _, err := ctx.Cfg.AddZlibAccount(*email, *password); err != nil {
		return err
	}

	domain := zlibResolveProbe(ctx)
	client := newZlibClientForDomain(ctx, domain)

	userID, userKey, err := client.Login(lctx, *email, *password)
	if err != nil {
		return err
	}
	if err := ctx.Cfg.UpdateZlibAccountToken(*email, userID, userKey); err != nil {
		return err
	}

	if ctx.JSON {
		return writeJSON(ctx, map[string]any{
			"status":       "ok",
			"domain":       domain,
			"email":        *email,
			"remix_userid": userID,
			"file":         config.Path(),
		})
	}
	fmt.Fprintf(ctx.Out, "%s to Z-Library via %s\n", ctx.Colors.Green("Signed in"), domain)
	fmt.Fprintf(ctx.Out, "  %s %s (%d account(s) stored)\n", ctx.Colors.Dim("Token cached in"), config.Path(), ctx.Cfg.AccountCount())

	// Report the remaining quota now, because that is the next thing the user
	// wants to know and it costs one request.
	if profile, perr := client.GetProfile(lctx); perr == nil {
		fmt.Fprintf(ctx.Out, "  %s %d/%d downloads left today\n",
			ctx.Colors.Dim("Quota:"), profile.DownloadsLeft, profile.DownloadsLimit)
	}
	return nil
}

// --- quota ---

func cmdQuota(ctx *Context, args []string) error {
	fs := newFlagSet("quota", "")
	all := fs.Bool("all", false, "report the quota of every stored account")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	qctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	if *all {
		return quotaAll(ctx, qctx)
	}

	client, err := newZlibClient(qctx, ctx)
	if err != nil {
		return err
	}
	profile, err := client.GetProfile(qctx)
	if err != nil {
		return err
	}

	if ctx.JSON {
		return writeJSON(ctx, profile)
	}
	fmt.Fprintf(ctx.Out, "%s %s\n", ctx.Colors.Bold("Account:"), profile.Email)
	if profile.Name != "" {
		fmt.Fprintf(ctx.Out, "%s %s\n", ctx.Colors.Bold("Name:"), profile.Name)
	}
	fmt.Fprintf(ctx.Out, "%s %d/%d (used %d today)\n", ctx.Colors.Bold("Downloads left:"),
		profile.DownloadsLeft, profile.DownloadsLimit, profile.DownloadsToday)
	if profile.DownloadsLeft == 0 {
		fmt.Fprintf(ctx.Out, "%s\n", ctx.Colors.Yellow("Quota exhausted; it resets on a rolling 24-hour basis."))
	}
	return nil
}

// quotaAll reports the remaining downloads of every stored account, which is
// the overview that matters when rotation is in play.
func quotaAll(ctx *Context, gctx context.Context) error {
	if ctx.Cfg.AccountCount() == 0 {
		return fmt.Errorf("no Z-Library accounts stored; add one with `zlib account add`")
	}
	domain := zlibResolveProbe(ctx)
	type row struct {
		Email  string `json:"email"`
		Limit  int    `json:"downloads_limit"`
		Today  int    `json:"downloads_today"`
		Left   int    `json:"downloads_left"`
		Active bool   `json:"is_active"`
		Error  string `json:"error,omitempty"`
	}
	rows := make([]row, 0, ctx.Cfg.AccountCount())
	active := activeEmail(ctx)
	for _, acc := range ctx.Cfg.AccountList() {
		r := row{Email: acc.Email, Active: acc.Email == active}
		client, err := newZlibAccountClient(gctx, ctx, domain, acc)
		if err != nil {
			r.Error = err.Error()
			r.Limit = 10
			rows = append(rows, r)
			continue
		}
		profile, err := client.GetProfile(gctx)
		if err != nil {
			r.Error = err.Error()
			r.Limit = 10
			rows = append(rows, r)
			continue
		}
		r.Limit = profile.DownloadsLimit
		r.Today = profile.DownloadsToday
		r.Left = profile.DownloadsLeft
		rows = append(rows, r)
	}
	if ctx.JSON {
		return writeJSON(ctx, map[string]any{"accounts": rows})
	}
	fmt.Fprintf(ctx.Out, "%s\n", ctx.Colors.Bold("Z-Library quotas:"))
	for _, r := range rows {
		marker := "  "
		if r.Active {
			marker = ctx.Colors.Green("* ")
		}
		if r.Error != "" {
			fmt.Fprintf(ctx.Out, "%s%-32s %s\n", marker, r.Email, ctx.Colors.Yellow(r.Error))
			continue
		}
		fmt.Fprintf(ctx.Out, "%s%-32s %d/%d left\n", marker, r.Email, r.Left, r.Limit)
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
