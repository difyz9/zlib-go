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

// ZlibConfig holds Z-Library credentials. Either the email/password pair or the
// cached remix token pair is enough; the token pair is preferred because
// /eapi/user/login is rate limited to roughly ten attempts per hour per IP.
type ZlibConfig struct {
	Email        string `json:"email,omitempty"`
	Password     string `json:"password,omitempty"`
	RemixUserID  string `json:"remix_userid,omitempty"`
	RemixUserKey string `json:"remix_userkey,omitempty"`
	Domain       string `json:"domain,omitempty"` // pins the EAPI domain; empty means probe
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

// HasZlib reports whether any usable Z-Library credential is present.
func (c *Config) HasZlib() bool {
	return (c.Zlib.Email != "" && c.Zlib.Password != "") ||
		(c.Zlib.RemixUserID != "" && c.Zlib.RemixUserKey != "")
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
// later invocations skip /eapi/user/login entirely.
func (c *Config) CacheZlibToken(userID, userKey string) error {
	c.Zlib.RemixUserID = userID
	c.Zlib.RemixUserKey = userKey
	return c.Save()
}

// ClearZlibToken drops the cached token pair, forcing the next run to log in
// with email and password again.
func (c *Config) ClearZlibToken() error {
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
	return &out
}

func mask(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:8] + "..."
}
