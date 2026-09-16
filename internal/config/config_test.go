package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points the config at a temporary directory so a test never reads or
// writes the developer's real ~/.config/zlib/config.json.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ZLIB_CONFIG_DIR", dir)
	// Clear every variable Load consults, so the developer's own environment
	// cannot change the outcome.
	for _, key := range []string{
		"ZLIBRARY_EMAIL", "ZLIBRARY_PASSWORD", "ZLIBRARY_EAPI_DOMAIN",
		"ANNAS_SECRET_KEY", "ANNAS_BASE_URL", "LIBGEN_MIRROR",
		"ZLIB_DOWNLOAD_DIR", "ZLIB_SOURCE", "ZLIB_PROXY",
	} {
		t.Setenv(key, "")
	}
	return dir
}

func TestLoadAppliesDefaultsWhenNoFileExists(t *testing.T) {
	isolate(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Annas.BaseURL != DefaultAnnasBaseURL {
		t.Errorf("annas base url = %q, want %q", cfg.Annas.BaseURL, DefaultAnnasBaseURL)
	}
	if cfg.Libgen.Mirror != DefaultLibgenMirror {
		t.Errorf("libgen mirror = %q, want %q", cfg.Libgen.Mirror, DefaultLibgenMirror)
	}
	if cfg.DefaultSource != DefaultSource {
		t.Errorf("default source = %q, want %q", cfg.DefaultSource, DefaultSource)
	}
	if cfg.DownloadDir == "" {
		t.Error("download dir was not defaulted to the home directory")
	}
	if cfg.HasZlib() {
		t.Error("a fresh config reported Z-Library as configured")
	}
}

// TestEnvironmentOverridesFile pins the precedence order. It matters because the
// documented way to fix a blocked domain is a one-off environment variable, and
// that has to win over a value already written to the file.
func TestEnvironmentOverridesFile(t *testing.T) {
	dir := isolate(t)

	onDisk := &Config{
		Zlib:          ZlibConfig{Email: "from-file@example.com", Password: "file-pw", Domain: "z-library.sk"},
		Annas:         AnnasConfig{BaseURL: "https://file.example"},
		Libgen:        LibgenConfig{Mirror: "vg"},
		DownloadDir:   "/tmp/from-file",
		DefaultSource: "annas",
	}
	if err := onDisk.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("config was not written: %v", err)
	}

	t.Setenv("ZLIBRARY_EMAIL", "from-env@example.com")
	t.Setenv("ZLIBRARY_EAPI_DOMAIN", "z-library.ec")
	t.Setenv("ANNAS_BASE_URL", "https://env.example")
	t.Setenv("ZLIB_DOWNLOAD_DIR", "/tmp/from-env")
	t.Setenv("ZLIB_SOURCE", "zlib")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Zlib.Email != "from-env@example.com" {
		t.Errorf("email = %q, want the environment value", cfg.Zlib.Email)
	}
	// A field with no environment override keeps the file's value.
	if cfg.Zlib.Password != "file-pw" {
		t.Errorf("password = %q, want the file's value", cfg.Zlib.Password)
	}
	if cfg.Zlib.Domain != "z-library.ec" {
		t.Errorf("domain = %q", cfg.Zlib.Domain)
	}
	if cfg.Annas.BaseURL != "https://env.example" {
		t.Errorf("annas base url = %q", cfg.Annas.BaseURL)
	}
	if cfg.DownloadDir != "/tmp/from-env" {
		t.Errorf("download dir = %q", cfg.DownloadDir)
	}
	if cfg.DefaultSource != "zlib" {
		t.Errorf("source = %q", cfg.DefaultSource)
	}
	if cfg.Libgen.Mirror != "vg" {
		t.Errorf("libgen mirror = %q, want the file's value", cfg.Libgen.Mirror)
	}
}

// TestCorruptConfigIsReportedNotSwallowed keeps a typo from looking like "no
// credentials configured", which would send the user down the wrong path.
func TestCorruptConfigIsReportedNotSwallowed(t *testing.T) {
	dir := isolate(t)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("a malformed config file was silently ignored")
	} else if !strings.Contains(err.Error(), "config.json") {
		t.Errorf("the error does not name the offending file: %v", err)
	}
}

// TestSavedConfigIsOwnerOnly is a security property: the file can hold a
// password and an API key in plain text.
func TestSavedConfigIsOwnerOnly(t *testing.T) {
	dir := isolate(t)
	cfg := &Config{Zlib: ZlibConfig{Password: "secret"}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config mode = %04o, want 0600", perm)
	}
}

// TestRedactedHidesSecrets checks the display path, which is what stops a
// password reaching a terminal, a log, or a screenshot.
func TestRedactedHidesSecrets(t *testing.T) {
	cfg := &Config{
		Zlib:  ZlibConfig{Email: "user@example.com", Password: "hunter2", RemixUserKey: "abcdefghijklmnop"},
		Annas: AnnasConfig{SecretKey: "annas-secret-key-value"},
	}
	out := cfg.Redacted()

	if out.Zlib.Password != "***" {
		t.Errorf("password not masked: %q", out.Zlib.Password)
	}
	if strings.Contains(out.Zlib.RemixUserKey, "klmnop") {
		t.Errorf("remix key leaked: %q", out.Zlib.RemixUserKey)
	}
	if strings.Contains(out.Annas.SecretKey, "key-value") {
		t.Errorf("annas key leaked: %q", out.Annas.SecretKey)
	}
	// The email is not a secret and must stay readable, otherwise the user
	// cannot tell which account is configured.
	if out.Zlib.Email != "user@example.com" {
		t.Errorf("email was masked but should not be: %q", out.Zlib.Email)
	}
	// Redaction must not mutate the live config.
	if cfg.Zlib.Password != "hunter2" {
		t.Error("Redacted modified the original config")
	}

	// And the redacted form must survive JSON encoding, since that is how it is
	// printed with --json.
	if _, err := json.Marshal(out); err != nil {
		t.Errorf("redacted config is not JSON-serializable: %v", err)
	}
}

func TestRedactedMasksShortSecretsEntirely(t *testing.T) {
	cfg := &Config{Annas: AnnasConfig{SecretKey: "short"}}
	if got := cfg.Redacted().Annas.SecretKey; got != "***" {
		t.Errorf("a short secret was partially revealed: %q", got)
	}
}

func TestHasZlibAcceptsEitherCredentialShape(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"email and password", Config{Zlib: ZlibConfig{Email: "a@b.c", Password: "p"}}, true},
		{"cached token pair", Config{Zlib: ZlibConfig{RemixUserID: "1", RemixUserKey: "k"}}, true},
		{"email only", Config{Zlib: ZlibConfig{Email: "a@b.c"}}, false},
		{"password only", Config{Zlib: ZlibConfig{Password: "p"}}, false},
		{"token id only", Config{Zlib: ZlibConfig{RemixUserID: "1"}}, false},
		{"empty", Config{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.HasZlib(); got != tc.want {
				t.Errorf("HasZlib() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCacheAndClearToken covers the round trip that makes `zlib login` worth
// having: without caching, every command would spend one of the ~10 hourly
// login attempts.
func TestCacheAndClearToken(t *testing.T) {
	isolate(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.CacheZlibToken("42", "key-xyz"); err != nil {
		t.Fatalf("CacheZlibToken: %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Zlib.RemixUserID != "42" || reloaded.Zlib.RemixUserKey != "key-xyz" {
		t.Errorf("token did not survive a reload: %q / %q",
			reloaded.Zlib.RemixUserID, reloaded.Zlib.RemixUserKey)
	}
	if !reloaded.HasZlib() {
		t.Error("a cached token pair did not count as configured")
	}

	if err := reloaded.ClearZlibToken(); err != nil {
		t.Fatalf("ClearZlibToken: %v", err)
	}
	cleared, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Zlib.RemixUserID != "" || cleared.Zlib.RemixUserKey != "" {
		t.Error("ClearZlibToken left credentials behind")
	}
}

func TestSaveCreatesDirectoryWithOwnerOnlyMode(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "nested", "zlib")
	t.Setenv("ZLIB_CONFIG_DIR", dir)

	cfg := &Config{}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("config directory was not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %04o, want 0700", perm)
	}
}
