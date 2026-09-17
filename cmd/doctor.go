package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/difyz9/zlib-go/internal/config"
	"github.com/difyz9/zlib-go/internal/fetch"
	"github.com/difyz9/zlib-go/internal/zlib"
)

// check is one doctor result line.
type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	// Fatal marks a failure that makes the tool unusable rather than merely
	// degraded, which is what decides the overall ready flag.
	Fatal bool `json:"-"`
}

// cmdDoctor verifies the environment and reports what will and will not work.
//
// Local facts are checked before network ones, and only sources that are
// actually configured are probed, so a user who only wants Anna's Archive is
// not told that Z-Library is broken.
func cmdDoctor(ctx *Context, args []string) error {
	if err := parseFlags(newFlagSet("doctor", ""), args); err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	checks := []check{
		checkConfigDir(ctx),
		checkDownloadDir(ctx),
		checkProxy(ctx),
	}
	checks = append(checks, checkZlib(ctx, dctx)...)
	checks = append(checks, checkAnnas(ctx, dctx)...)

	if ctx.JSON {
		ready := true
		for _, c := range checks {
			if c.Fatal {
				ready = false
			}
		}
		return writeJSON(ctx, map[string]any{
			"ready":  ready,
			"checks": checks,
			"config": ctx.Cfg.Redacted(),
		})
	}

	b := ctx.Colors.Bold
	fmt.Fprintf(ctx.Out, "%s\n\n", b("zlib doctor"))
	for _, c := range checks {
		mark := ctx.Colors.Green("✓")
		if !c.OK {
			if c.Fatal {
				mark = ctx.Colors.Red("✗")
			} else {
				mark = ctx.Colors.Yellow("!")
			}
		}
		fmt.Fprintf(ctx.Out, "  %s %-26s %s\n", mark, c.Name, ctx.Colors.Dim(c.Detail))
	}

	if next := nextStep(checks); next != "" {
		fmt.Fprintf(ctx.Out, "\n%s\n%s\n", ctx.Colors.Bold("Next step"), next)
	} else {
		fmt.Fprintf(ctx.Out, "\n%s\n", ctx.Colors.Green("Ready. Try: zlib search \"deep learning\" --limit 5"))
	}
	return nil
}

func checkConfigDir(ctx *Context) check {
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return check{Name: "config directory", Fatal: true,
			Detail: fmt.Sprintf("cannot create %s: %v", dir, err)}
	}
	info, err := os.Stat(config.Path())
	if err != nil {
		return check{Name: "config file", OK: true,
			Detail: config.Path() + " not created yet (defaults and environment only)"}
	}
	// A config holding a password should not be group- or world-readable.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return check{Name: "config file",
			Detail: fmt.Sprintf("%s is mode %04o — run `chmod 600 %s` to protect stored credentials",
				config.Path(), perm, config.Path())}
	}
	return check{Name: "config file", OK: true, Detail: config.Path()}
}

func checkDownloadDir(ctx *Context) check {
	dir := expandHome(ctx.Cfg.DownloadDir)
	if dir == "" {
		return check{Name: "download directory", Fatal: true, Detail: "not configured"}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return check{Name: "download directory", Fatal: true,
			Detail: fmt.Sprintf("cannot create %s: %v", dir, err)}
	}
	// Writable-ness has to be probed by writing: a mode bit can lie when the
	// directory belongs to someone else or sits on a read-only mount.
	probe := filepath.Join(dir, ".zlib-doctor-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return check{Name: "download directory", Fatal: true,
			Detail: fmt.Sprintf("%s is not writable: %v", dir, err)}
	}
	os.Remove(probe)
	return check{Name: "download directory", OK: true, Detail: dir + " (writable)"}
}

func checkProxy(ctx *Context) check {
	proxy := fetch.EffectiveProxy(ctx.Cfg.Proxy)
	if proxy == "" {
		return check{Name: "proxy", OK: true,
			Detail: "none — connecting directly (set ZLIB_PROXY or HTTPS_PROXY if these sources are blocked)"}
	}
	return check{Name: "proxy", OK: true, Detail: "via " + proxy}
}

func checkZlib(ctx *Context, dctx context.Context) []check {
	if !ctx.Cfg.HasZlib() {
		return []check{{
			Name:   "Z-Library credentials",
			Detail: "not configured — `zlib config set --zlib-email <email> --zlib-password <password>`",
		}}
	}

	results := make([]string, 0, len(zlib.DefaultDomains))
	usable := ""
	for _, domain := range zlib.DefaultDomains {
		ok, detail := probeEAPIDomain(dctx, domain, ctx.Cfg.Proxy)
		if ok {
			results = append(results, domain+" "+detail)
			if usable == "" {
				usable = domain
			}
		} else {
			results = append(results, domain+" "+detail)
		}
	}

	cred := check{Name: "Z-Library credentials", OK: true}
	if ctx.Cfg.Zlib.RemixUserKey != "" {
		cred.Detail = "cached session token present"
	} else {
		cred.Detail = "email and password set; no cached token yet"
	}

	probe := check{
		Name:   "Z-Library EAPI domains",
		OK:     usable != "",
		Detail: strings.Join(results, "; "),
	}
	return []check{cred, probe}
}

// probeEAPIDomain checks one candidate domain and describes the outcome.
//
// The description is the point: "unusable" is not actionable, whereas
// "connect timed out — the traffic is being dropped" tells the user they need a
// proxy rather than different credentials.
func probeEAPIDomain(ctx context.Context, domain, proxy string) (bool, string) {
	if strings.TrimSpace(proxy) != "" {
		// Through a proxy, DNS and TCP diagnostics would describe the hop to the
		// proxy rather than to the target, so only the end-to-end answer means
		// anything.
		transport, err := fetch.NewTransport(proxy, 15*time.Second)
		if err != nil {
			return false, "invalid proxy: " + err.Error()
		}
		hc := &http.Client{Timeout: 15 * time.Second, Transport: transport}
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/eapi/info/domains", nil)
		if rerr != nil {
			return false, rerr.Error()
		}
		req.Header.Set("User-Agent", fetch.UserAgent)
		resp, derr := hc.Do(req)
		if derr != nil {
			return false, "via proxy: " + derr.Error()
		}
		defer resp.Body.Close()
		body, _ := fetch.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK && !strings.Contains(strings.ToLower(string(body)), "diamwall") {
			return true, "ok via proxy"
		}
		return false, fmt.Sprintf("via proxy: HTTP %d", resp.StatusCode)
	}

	d := fetch.Diagnose(ctx, domain, 443, true, zlib.ProbeTimeout)
	if !d.Healthy() {
		return false, d.Detail
	}
	// A TCP/TLS-clean host can still be behind the DiamWall anti-bot wall, so the
	// application-level probe decides usability.
	if zlib.ProbeDomain(ctx, domain, zlib.ProbeTimeout) {
		return true, "ok"
	}
	return false, fmt.Sprintf("reachable (HTTP %d) but serves an anti-bot wall instead of EAPI JSON", d.Status)
}

func checkAnnas(ctx *Context, dctx context.Context) []check {
	client, err := newAnnasClient(ctx)
	if err != nil {
		return []check{{Name: "Anna's Archive base URL", Fatal: false, Detail: err.Error()}}
	}

	checks := []check{{Name: "Anna's Archive host", OK: true, Detail: client.Host()}}
	reach := check{Name: "Anna's Archive search"}

	req, rerr := http.NewRequestWithContext(dctx, http.MethodGet,
		client.BaseURL+"/search?q=test", nil)
	if rerr == nil {
		req.Header.Set("User-Agent", fetch.UserAgent)
		if resp, herr := client.HTTPClient().Do(req); herr == nil {
			body, _ := fetch.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				reach.OK = true
				reach.Detail = fmt.Sprintf("HTTP 200, %d bytes", len(body))
			} else {
				reach.Detail = fmt.Sprintf("HTTP %d — %s", resp.StatusCode, annasProblemHint(resp.StatusCode, body))
			}
		} else {
			reach.Detail = "unreachable: " + herr.Error()
		}
	}
	checks = append(checks, reach)

	key := check{Name: "Anna's Archive API key", OK: ctx.Cfg.HasAnnas()}
	if ctx.Cfg.HasAnnas() {
		key.Detail = "configured (downloads enabled)"
	} else {
		key.Detail = "not set — search still works, downloads do not"
	}
	return append(checks, key)
}

// annasProblemHint explains a non-200 from Anna's Archive in terms the user can
// act on.
func annasProblemHint(status int, body []byte) string {
	lower := strings.ToLower(string(body))
	if strings.Contains(lower, "ddos-guard") || strings.Contains(lower, "__ddg") ||
		strings.Contains(lower, "checking your browser") {
		return "DDoS-Guard browser challenge, which a plain HTTP client cannot pass; use a proxy or another network"
	}
	if status == http.StatusForbidden || status == http.StatusUnauthorized {
		return "request blocked or key rejected"
	}
	return "unexpected response"
}

// nextStep returns the single most useful action, or "" when nothing is wrong.
func nextStep(checks []check) string {
	var credential, connection bool
	for _, c := range checks {
		if c.OK {
			continue
		}
		switch {
		case strings.Contains(c.Name, "credentials"), strings.Contains(c.Name, "API key"):
			credential = true
		case strings.Contains(c.Name, "domain"), strings.Contains(c.Name, "search"):
			connection = true
		}
	}
	switch {
	case connection && credential:
		return "  No source is reachable from this network, and credentials are incomplete.\n" +
			"    If these sites are blocked where you are, set a proxy:\n" +
			"      zlib config set --proxy socks5://127.0.0.1:1080\n" +
			"    Then configure a source and sign in:\n" +
			"      zlib config set --zlib-email <email> --zlib-password <password> && zlib login"
	case connection:
		return "  The network is blocking these sources.\n" +
			"    Set a proxy: zlib config set --proxy socks5://127.0.0.1:1080\n" +
			"    Or export HTTPS_PROXY before running zlib."
	case credential:
		return "  Configure a source, then run `zlib doctor` again:\n" +
			"    zlib config set --zlib-email <email> --zlib-password <password>\n" +
			"    zlib login"
	default:
		return ""
	}
}
