package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/difyz9/zlib-go/internal/annas"
	"github.com/difyz9/zlib-go/internal/config"
	"github.com/difyz9/zlib-go/internal/zlib"
)

// errNoZlibCredentials is the actionable message every zlib-backed command
// shares when no account is configured.
var errNoZlibCredentials = fmt.Errorf(
	"Z-Library credentials are not configured.\n" +
		"  Run: zlib account add --email <email> --password <password>\n" +
		"  Or set ZLIBRARY_EMAIL and ZLIBRARY_PASSWORD in the environment.\n" +
		"  Anna's Archive search works without credentials: zlib search \"...\" --source annas")

// newZlibClient resolves the EAPI domain and returns a client holding valid
// credentials for the first account that authenticates.
//
// A cached remix token is used when present. Otherwise the configured
// email/password pair is exchanged for one, and the result is cached on the
// account so subsequent runs never touch /eapi/user/login — that endpoint is
// rate limited to roughly ten attempts per hour per IP, so logging in per
// invocation would lock the account out within a handful of commands.
func newZlibClient(ctx context.Context, c *Context) (*zlib.Client, error) {
	if !c.Cfg.HasZlib() {
		return nil, errNoZlibCredentials
	}

	domain := zlib.ResolveDomain(ctx, c.Cfg.Zlib.Domain, zlib.ProbeTimeout, c.Logf)
	accounts := c.Cfg.AccountList()

	var lastErr error
	for _, acc := range accounts {
		if !acc.Usable() {
			continue
		}
		client, err := newZlibAccountClient(ctx, c, domain, acc)
		if err == nil {
			return client, nil
		}
		c.Logf("account %s unusable: %v", acc.Email, err)
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no usable Z-Library account")
	}
	return nil, lastErr
}

// newZlibAccountClient builds a client for one stored account, logging in with
// the account's password when no cached token is present. The token, when
// freshly issued, is cached back onto the account.
func newZlibAccountClient(ctx context.Context, c *Context, domain string, acc config.ZlibAccount) (*zlib.Client, error) {
	client := zlib.New(zlib.Options{
		Domain:       domain,
		RemixUserID:  acc.RemixUserID,
		RemixUserKey: acc.RemixUserKey,
		Proxy:        c.Cfg.Proxy,
		Logf:         c.Logf,
		Progress:     c.Progress,
	})
	if client.LoggedIn() {
		return client, nil
	}
	if acc.Email == "" || acc.Password == "" {
		return nil, zlib.ErrNotAuthenticated
	}

	c.Logf("no cached token for %s; logging in to %s", acc.Email, domain)
	userID, userKey, err := client.Login(ctx, acc.Email, acc.Password)
	if err != nil {
		return nil, err
	}
	// Caching is best-effort: a read-only home directory should not fail a
	// command that otherwise succeeded.
	if err := c.Cfg.UpdateZlibAccountToken(acc.Email, userID, userKey); err != nil {
		c.Logf("could not cache the session token for %s: %v", acc.Email, err)
	}
	return client, nil
}

// newAnnasClient builds an Anna's Archive client.
//
// A search needs no API key — only the fast-download API does — so a client
// without a key is still useful for browsing.
func newAnnasClient(c *Context) (*annas.Client, error) {
	return annas.New(annas.Options{
		BaseURL:   c.Cfg.Annas.BaseURL,
		SecretKey: c.Cfg.Annas.SecretKey,
		Proxy:     c.Cfg.Proxy,
		Logf:      c.Logf,
		Progress:  c.Progress,
	})
}

// zlibResolveProbe resolves the EAPI domain without building a client, which
// `login` needs so it can report which domain accepted the credentials.
func zlibResolveProbe(c *Context) string {
	return zlib.ResolveDomain(context.Background(), c.Cfg.Zlib.Domain, zlib.ProbeTimeout, c.Logf)
}

// newZlibClientForDomain builds a credential-less client bound to a domain.
// The caller supplies the tokens, which is what `login` does.
func newZlibClientForDomain(c *Context, domain string) *zlib.Client {
	return zlib.New(zlib.Options{
		Domain:       domain,
		RemixUserID:  c.Cfg.Zlib.RemixUserID,
		RemixUserKey: c.Cfg.Zlib.RemixUserKey,
		Proxy:        c.Cfg.Proxy,
		Logf:         c.Logf,
	})
}
