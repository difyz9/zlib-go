// Package config resolves credentials and preferences from the CLI, the
// environment, and the on-disk config file, in that order of precedence.
//
// It deliberately mirrors the environment variable names the existing
// zlibrary-mcp project uses (ZLIBRARY_EMAIL, ANNAS_SECRET_KEY, ...) so a user
// who already configured that tool does not have to configure this one.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	Zlib   ZlibConfig   `json:"zlib"`
	Annas  AnnasConfig  `json:"annas"`
	Libgen LibgenConfig `json:"libgen"`

	DownloadDir   string `json:"download_dir"`
	DefaultSource string `json:"default_source"`
	// Proxy is an http(s) or socks5 proxy URL. Empty means the standard
	// HTTPS_PROXY/HTTP_PROXY/ALL_PROXY environment variables are consulted, and
	// failing that, a direct connection.
	Proxy   string `json:"proxy,omitempty"`
	NoColor bool   `json:"-"`
}

// ZlibAccount is one stored Z-Library account. Either the email/password
// pair or the cached remix token pair is enough; the token pair is preferred
// because /eapi/user/login is rate limited to roughly ten attempts per hour
// per IP.
type ZlibAccount struct {
	Email        string `json:"email,omitempty"`
	Password     string `json:"password,omitempty"`
	RemixUserID  string `json:"remix_userid,omitempty"`
	RemixUserKey string `json:"remix_userkey,omitempty"`
}

// Usable reports whether the account can authenticate: it holds either a
// cached token pair or an email/password pair.
func (a ZlibAccount) Usable() bool {
	return (a.Email != "" && a.Password != "") ||
		(a.RemixUserID != "" && a.RemixUserKey != "")
}

// ZlibConfig holds Z-Library credentials. The single-account fields are kept
// as a mirror of Accounts[0] so an older build reading the same config file
// still finds its credentials; Accounts is the canonical store and supports
// any number of accounts for quota rotation.
type ZlibConfig struct {
	Email        string `json:"email,omitempty"`
	Password     string `json:"password,omitempty"`
	RemixUserID  string `json:"remix_userid,omitempty"`
	RemixUserKey string `json:"remix_userkey,omitempty"`
	Domain       string `json:"domain,omitempty"` // pins the EAPI domain; empty means probe

	Accounts []ZlibAccount `json:"accounts,omitempty"`
	// Active is the preferred account index; download rotation starts here.
	Active int `json:"active,omitempty"`
}

// AnnasConfig holds Anna's Archive settings.
type AnnasConfig struct {
	SecretKey string `json:"secret_key,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
}

// LibgenConfig holds LibGen settings. LibGen is the only source that needs no
// account and enforces no daily limit.
type LibgenConfig struct {
	Mirror string `json:"mirror,omitempty"`
}

const (
	// DefaultAnnasBaseURL is Anna's Archive's current primary host. The domain
	// rotates, so it is overridable via ANNAS_BASE_URL.
	DefaultAnnasBaseURL = "https://annas-archive.gl"

	// DefaultLibgenMirror is the LibGen mirror suffix (https://libgen.<mirror>/).
	DefaultLibgenMirror = "li"

	// DefaultSource is used when neither --source nor the config file picks one.
	DefaultSource = "auto"
)

// Dir returns the configuration directory (~/.config/zlib).
func Dir() string {
	if v := os.Getenv("ZLIB_CONFIG_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".zlib"
	}
	return filepath.Join(home, ".config", "zlib")
}

// Path returns the config file location.
func Path() string { return filepath.Join(Dir(), "config.json") }

// Load builds the effective configuration: config file first, then environment
// overrides, then the field-specific defaults.
func Load() (*Config, error) {
	cfg := &Config{
		Annas:         AnnasConfig{BaseURL: DefaultAnnasBaseURL},
		Libgen:        LibgenConfig{Mirror: DefaultLibgenMirror},
		DefaultSource: DefaultSource,
	}

	if err := cfg.mergeFile(); err != nil {
		return nil, err
	}
	cfg.mergeEnv()

	if cfg.DownloadDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cfg.DownloadDir = filepath.Join(home, "Downloads")
		} else {
			cfg.DownloadDir = "."
		}
	}
	if cfg.DefaultSource == "" {
		cfg.DefaultSource = DefaultSource
	}
	cfg.Zlib.normalize()
	if cfg.Annas.BaseURL == "" {
		cfg.Annas.BaseURL = DefaultAnnasBaseURL
	}
	if cfg.Libgen.Mirror == "" {
		cfg.Libgen.Mirror = DefaultLibgenMirror
	}
	return cfg, nil
}

func (c *Config) mergeFile() error {
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no config yet is a normal first-run state
		}
		return fmt.Errorf("read %s: %w", Path(), err)
	}
	// A JSON decode error here is worth surfacing verbatim: silently falling
	// back to defaults would make a typo look like "no credentials configured".
	if err := json.Unmarshal(data, c); err != nil {
		return fmt.Errorf("parse %s: %w", Path(), err)
	}
	return nil
}

func (c *Config) mergeEnv() {
	setStr(&c.Zlib.Email, "ZLIBRARY_EMAIL")
	setStr(&c.Zlib.Password, "ZLIBRARY_PASSWORD")
	setStr(&c.Zlib.Domain, "ZLIBRARY_EAPI_DOMAIN")
	setStr(&c.Annas.SecretKey, "ANNAS_SECRET_KEY")
	setStr(&c.Annas.BaseURL, "ANNAS_BASE_URL")
	setStr(&c.Libgen.Mirror, "LIBGEN_MIRROR")
	setStr(&c.DownloadDir, "ZLIB_DOWNLOAD_DIR")
	setStr(&c.DefaultSource, "ZLIB_SOURCE")
	setStr(&c.Proxy, "ZLIB_PROXY")
}

func setStr(dst *string, key string) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		*dst = v
	}
}

// normalize folds the legacy single-account fields, the account list, and the
// environment overrides into one canonical account list.
//
//   - a config file written by an older build carries only the legacy fields,
//     which become Accounts[0];
//   - ZLIBRARY_EMAIL/ZLIBRARY_PASSWORD override the account with the matching
//     email (or create it first) so env-based setups keep working;
//   - after the merge the legacy fields mirror Accounts[0] again, so saving the
//     config keeps the file readable by older builds.
func (z *ZlibConfig) normalize() {
	if len(z.Accounts) == 0 {
		if z.Email != "" || z.Password != "" || z.RemixUserID != "" || z.RemixUserKey != "" {
			z.Accounts = []ZlibAccount{{
				Email:        z.Email,
				Password:     z.Password,
				RemixUserID:  z.RemixUserID,
				RemixUserKey: z.RemixUserKey,
			}}
		}
	} else if z.Email != "" || z.Password != "" {
		// Legacy fields set alongside an account list only happens when the
		// environment or an old hand-edit filled them in; fold them into the
		// matching account rather than keeping two sources of truth.
		merged := false
		for i := range z.Accounts {
			if z.Accounts[i].Email == z.Email {
				if z.Password != "" {
					z.Accounts[i].Password = z.Password
					z.Accounts[i].RemixUserID, z.Accounts[i].RemixUserKey = "", ""
				}
				merged = true
				break
			}
		}
		if !merged && z.Email != "" {
			z.Accounts = append([]ZlibAccount{{Email: z.Email, Password: z.Password}}, z.Accounts...)
			z.Active = 0
		}
	}

	if z.Active < 0 || z.Active >= len(z.Accounts) {
		z.Active = 0
	}

	// Mirror Accounts[0] into the legacy fields so HasZlib, Redacted, Save, and
	// older builds all agree on the primary account.
	z.Email, z.Password, z.RemixUserID, z.RemixUserKey = "", "", "", ""
	if len(z.Accounts) > 0 {
		z.Email = z.Accounts[0].Email
		z.Password = z.Accounts[0].Password
		z.RemixUserID = z.Accounts[0].RemixUserID
		z.RemixUserKey = z.Accounts[0].RemixUserKey
	}
}

// AccountList returns the stored accounts in preference order: the active
// account first, then the rest. Login state is not considered; the caller
// rotates on failures.
func (c *Config) AccountList() []ZlibAccount {
	accs := c.Zlib.Accounts
	idx := c.Zlib.Active
	if len(accs) == 0 || idx == 0 || idx >= len(accs) {
		return accs
	}
	ordered := make([]ZlibAccount, 0, len(accs))
	ordered = append(ordered, accs[idx])
	for i := range accs {
		if i != idx {
			ordered = append(ordered, accs[i])
		}
	}
	return ordered
}

// AccountCount reports how many Z-Library accounts are stored.
func (c *Config) AccountCount() int { return len(c.Zlib.Accounts) }

// AddZlibAccount stores an account (upsert by email) and makes it the active
// one when it is new. It returns whether the account was newly added.
func (c *Config) AddZlibAccount(email, password string) (bool, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return false, fmt.Errorf("account email and password are both required")
	}
	for i := range c.Zlib.Accounts {
		if c.Zlib.Accounts[i].Email == email {
			c.Zlib.Accounts[i].Password = password
			// A changed password invalidates the cached session.
			c.Zlib.Accounts[i].RemixUserID, c.Zlib.Accounts[i].RemixUserKey = "", ""
			return false, c.Save()
		}
	}
	c.Zlib.Accounts = append(c.Zlib.Accounts, ZlibAccount{Email: email, Password: password})
	c.Zlib.Active = len(c.Zlib.Accounts) - 1
	return true, c.Save()
}

// RemoveZlibAccount drops the account with the given email.
func (c *Config) RemoveZlibAccount(email string) (bool, error) {
	removed := false
	kept := c.Zlib.Accounts[:0]
	for _, a := range c.Zlib.Accounts {
		if a.Email == email {
			removed = true
			continue
		}
		kept = append(kept, a)
	}
	c.Zlib.Accounts = kept
	if c.Zlib.Active >= len(kept) {
		c.Zlib.Active = 0
	}
	if !removed {
		return false, nil
	}
	return true, c.Save()
}

// SetActiveZlibAccount makes the account with the given email the preferred
// one for future runs.
func (c *Config) SetActiveZlibAccount(email string) error {
	for i, a := range c.Zlib.Accounts {
		if a.Email == email {
			c.Zlib.Active = i
			return c.Save()
		}
	}
	return fmt.Errorf("no stored account with email %q", email)
}

// UpdateZlibAccountToken caches a remix token pair on the account with the
// given email so later runs skip /eapi/user/login entirely.
func (c *Config) UpdateZlibAccountToken(email, userID, userKey string) error {
	for i := range c.Zlib.Accounts {
		if c.Zlib.Accounts[i].Email == email {
			c.Zlib.Accounts[i].RemixUserID = userID
			c.Zlib.Accounts[i].RemixUserKey = userKey
			return c.Save()
		}
	}
	return fmt.Errorf("no stored account with email %q", email)
}

// HasZlib reports whether any usable Z-Library credential is present.
func (c *Config) HasZlib() bool {
	if c.Zlib.Email != "" && c.Zlib.Password != "" {
		return true
	}
	if c.Zlib.RemixUserID != "" && c.Zlib.RemixUserKey != "" {
		return true
	}
	for _, a := range c.Zlib.Accounts {
		if a.Usable() {
			return true
		}
	}
	return false
}

// HasAnnas reports whether an Anna's Archive API key is configured.
func (c *Config) HasAnnas() bool { return c.Annas.SecretKey != "" }

// Save writes the configuration to disk with owner-only permissions, since it
// may hold a password and an API key in plain text.
func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", Dir(), err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(Path(), data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", Path(), err)
	}
	// os.WriteFile does not tighten permissions on a file that already exists
	// with a wider mode, so enforce 0600 explicitly.
	return os.Chmod(Path(), 0o600)
}

// CacheZlibToken stores the remix token pair returned by a successful login so
// later invocations skip /eapi/user/login entirely. The pair is cached on
// Accounts[0] (which the legacy fields mirror) and on the legacy fields for
// older builds.
func (c *Config) CacheZlibToken(userID, userKey string) error {
	if len(c.Zlib.Accounts) > 0 {
		c.Zlib.Accounts[0].RemixUserID = userID
		c.Zlib.Accounts[0].RemixUserKey = userKey
	}
	c.Zlib.RemixUserID = userID
	c.Zlib.RemixUserKey = userKey
	return c.Save()
}

// ClearZlibToken drops every cached token pair, forcing the next run to log in
// with email and password again.
func (c *Config) ClearZlibToken() error {
	for i := range c.Zlib.Accounts {
		c.Zlib.Accounts[i].RemixUserID = ""
		c.Zlib.Accounts[i].RemixUserKey = ""
	}
	c.Zlib.RemixUserID = ""
	c.Zlib.RemixUserKey = ""
	return c.Save()
}

// Redacted returns a copy safe to print: secrets replaced by a short prefix.
func (c *Config) Redacted() *Config {
	out := *c
	if out.Zlib.Password != "" {
		out.Zlib.Password = "***"
	}
	if out.Zlib.RemixUserKey != "" {
		out.Zlib.RemixUserKey = mask(out.Zlib.RemixUserKey)
	}
	if out.Annas.SecretKey != "" {
		out.Annas.SecretKey = mask(out.Annas.SecretKey)
	}
	redacted := make([]ZlibAccount, len(out.Zlib.Accounts))
	for i, a := range out.Zlib.Accounts {
		if a.Password != "" {
			a.Password = "***"
		}
		if a.RemixUserKey != "" {
			a.RemixUserKey = mask(a.RemixUserKey)
		}
		redacted[i] = a
	}
	out.Zlib.Accounts = redacted
	return &out
}

func mask(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:8] + "..."
}
