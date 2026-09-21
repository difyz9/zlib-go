package cmd

import (
	"fmt"
	"strings"

	"github.com/difyz9/zlib-go/internal/config"
)

// cmdAccount manages the stored Z-Library accounts used for quota rotation.
//
// A free Z-Library account allows ten downloads per day, so the intended
// workflow is to store several and let `download` rotate to one with quota
// left when the active account is limited.
func cmdAccount(ctx *Context, args []string) error {
	if len(args) == 0 {
		return usagef("account needs a subcommand: add, list, remove, or use")
	}
	switch args[0] {
	case "add", "login":
		return accountAdd(ctx, args[1:])
	case "list", "ls", "show":
		return accountList(ctx)
	case "remove", "rm", "delete":
		return accountRemove(ctx, args[1:])
	case "use", "switch", "active":
		return accountUse(ctx, args[1:])
	default:
		return usagef("unknown account subcommand %q (expected add, list, remove, or use)", args[0])
	}
}

// accountAdd stores (or updates) an account. `zlib account login` additionally
// authenticates immediately, which both validates the credentials and caches a
// session token, so the account never has to log in again.
func accountAdd(ctx *Context, args []string) error {
	fs := newFlagSet("account add", "")
	email := fs.String("email", "", "Z-Library account email")
	password := fs.String("password", "", "Z-Library account password")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *email == "" || *password == "" {
		return usagef("--email and --password are both required:\n" +
			"  zlib account add --email <email> --password <password>")
	}

	added, err := ctx.Cfg.AddZlibAccount(*email, *password)
	if err != nil {
		return err
	}

	if ctx.JSON {
		return writeJSON(ctx, map[string]any{
			"status": "ok",
			"email":  *email,
			"added":  added,
			"file":   config.Path(),
		})
	}

	verb := "Updated"
	if added {
		verb = "Added"
	}
	fmt.Fprintf(ctx.Out, "%s %s (%d account(s) stored in %s)\n",
		ctx.Colors.Green(verb), *email, ctx.Cfg.AccountCount(), config.Path())

	if added && ctx.Cfg.AccountCount() > 1 {
		fmt.Fprintf(ctx.Out, "%s %s\n", ctx.Colors.Dim("Downloads now rotate through"),
			fmt.Sprintf("%d accounts automatically when one hits its daily limit", ctx.Cfg.AccountCount()))
	} else if added {
		fmt.Fprintf(ctx.Out, "%s run `zlib account add` again with another email to enable quota rotation\n",
			ctx.Colors.Dim("Tip:"))
	}
	return nil
}

// accountList prints the stored accounts with their login state.
func accountList(ctx *Context) error {
	if ctx.JSON {
		accounts := make([]map[string]any, 0, len(ctx.Cfg.Zlib.Accounts))
		for _, a := range ctx.Cfg.Zlib.Accounts {
			accounts = append(accounts, map[string]any{
				"email":        a.Email,
				"token_cached": a.RemixUserID != "" && a.RemixUserKey != "",
				"is_active":    a.Email == activeEmail(ctx),
			})
		}
		return writeJSON(ctx, map[string]any{"accounts": accounts})
	}

	if ctx.Cfg.AccountCount() == 0 {
		fmt.Fprintf(ctx.Out, "No Z-Library accounts stored.\n%s %s\n",
			ctx.Colors.Dim("Add one:"), "zlib account add --email <email> --password <password>")
		return nil
	}

	fmt.Fprintf(ctx.Out, "%s %d account(s):\n", ctx.Colors.Bold("Z-Library accounts"), ctx.Cfg.AccountCount())
	active := activeEmail(ctx)
	for _, a := range ctx.Cfg.Zlib.Accounts {
		state := ctx.Colors.Dim("token cached")
		if a.RemixUserID == "" || a.RemixUserKey == "" {
			state = ctx.Colors.Dim("password only")
		}
		marker := "  "
		if a.Email == active {
			marker = ctx.Colors.Green("* ")
		}
		fmt.Fprintf(ctx.Out, "%s%s  %s\n", marker, a.Email, state)
	}
	fmt.Fprintf(ctx.Out, "%s\n", ctx.Colors.Dim("* active account; downloads rotate to the others when the daily limit is hit"))
	return nil
}

func activeEmail(ctx *Context) string {
	i := ctx.Cfg.Zlib.Active
	if i < 0 || i >= len(ctx.Cfg.Zlib.Accounts) {
		return ""
	}
	return ctx.Cfg.Zlib.Accounts[i].Email
}

// accountRemove drops an account by email.
func accountRemove(ctx *Context, args []string) error {
	fs := newFlagSet("account remove", "")
	email := fs.String("email", "", "email of the account to remove")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *email == "" && fs.NArg() > 0 {
		*email = fs.Arg(0)
	}
	if *email == "" {
		return usagef("the account email is required: zlib account remove <email>")
	}

	removed, err := ctx.Cfg.RemoveZlibAccount(strings.TrimSpace(*email))
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no stored account with email %q", *email)
	}

	if ctx.JSON {
		return writeJSON(ctx, map[string]any{"status": "ok", "removed": *email})
	}
	fmt.Fprintf(ctx.Out, "%s %s (%d account(s) left)\n",
		ctx.Colors.Green("Removed"), *email, ctx.Cfg.AccountCount())
	return nil
}

// accountUse makes a stored account the preferred (active) one.
func accountUse(ctx *Context, args []string) error {
	fs := newFlagSet("account use", "")
	email := fs.String("email", "", "email of the account to activate")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *email == "" && fs.NArg() > 0 {
		*email = fs.Arg(0)
	}
	if *email == "" {
		return usagef("the account email is required: zlib account use <email>")
	}

	if err := ctx.Cfg.SetActiveZlibAccount(strings.TrimSpace(*email)); err != nil {
		return err
	}
	if ctx.JSON {
		return writeJSON(ctx, map[string]any{"status": "ok", "active": *email})
	}
	fmt.Fprintf(ctx.Out, "%s %s\n", ctx.Colors.Green("Active account:"), *email)
	return nil
}
