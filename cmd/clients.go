package cmd

import (
	"context"
	"fmt"

	"zlib/internal/annas"
	"zlib/internal/config"
	"zlib/internal/zlib"
)

// newZlibClient resolves the EAPI domain and returns a client holding valid
// credentials.
//
// A cached remix token is used when present. Otherwise the configured
// email/password pair is exchanged for one, and the result is cached so
// subsequent runs never touch /eapi/user/login — that endpoint is rate limited
// to roughly ten attempts per hour per IP, so logging in per invocation would
// lock the account out within a handful of commands.
func newZlibClient(ctx context.Context, c *Context) (*zlib.Client, error) {
	if !c.Cfg.HasZlib() {
		return nil, fmt.Errorf(
			"Z-Library credentials are not configured.\n" +
				"  Run: zlib config set --zlib-email <email> --zlib-password <password>\n" +
				"  Or set ZLIBRARY_EMAIL and ZLIBRARY_PASSWORD in the environment.\n" +
				"  Anna's Archive search works without credentials: zlib search \"...\" --source annas")
	}

	domain := zlib.ResolveDomain(ctx, c.Cfg.Zlib.Domain, zlib.ProbeTimeout, c.Logf)
	client := zlib.New(zlib.Options{
		Domain:       domain,
		RemixUserID:  c.Cfg.Zlib.RemixUserID,
		RemixUserKey: c.Cfg.Zlib.RemixUserKey,
		Proxy:        c.Cfg.Proxy,
		Logf:         c.Logf,
		Progress:     c.Progress,
	})

	if client.LoggedIn() {
		return client, nil
	}

	c.Logf("no cached token; logging in to %s", domain)
	userID, userKey, err := client.Login(ctx, c.Cfg.Zlib.Email, c.Cfg.Zlib.Password)
	if err != nil {
		return nil, err
	}
	// Caching is best-effort: a read-only home directory should not fail a
	// search that otherwise succeeded.
	if err := c.Cfg.CacheZlibToken(userID, userKey); err != nil {
		c.Logf("could not cache the session token: %v", err)
	} else {
		c.Logf("session token cached in %s", config.Path())
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
