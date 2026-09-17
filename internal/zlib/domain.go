package zlib

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/difyz9/zlib-go/internal/fetch"
)

// DefaultDomains is the candidate list probed, in order, when no domain is
// pinned. z-library.ec serves normal EAPI JSON and goes first; z-library.sk and
// 1lib.sk were fronted by the DiamWall anti-bot wall as of 2026-07-24 but stay
// listed in case the wall is lifted.
var DefaultDomains = []string{"z-library.ec", "z-library.sk", "1lib.sk"}

// walledStatuses are the status codes DiamWall-fronted domains return where
// EAPI JSON was expected: a 307 self-redirect that sets a __diamwall cookie,
// then 513/517 "Access Denied" on retry, plus 403 for generic bot blocking.
var walledStatuses = map[int]bool{307: true, 403: true, 513: true, 517: true}

// ProbeDomain cheaply checks whether a domain serves real EAPI JSON.
//
// It issues an unauthenticated GET /eapi/info/domains — never
// /eapi/user/login, which is rate limited to roughly ten attempts per hour per
// IP and would lock out real credentials. Healthy means HTTP 200 with a
// parseable JSON object carrying a "domains" payload. Redirects are NOT
// followed: DiamWall's 307 self-redirect is itself the walled signal.
func ProbeDomain(ctx context.Context, domain string, timeout time.Duration) bool {
	domain = strings.TrimSpace(strings.TrimSuffix(domain, "/"))
	if domain == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/eapi/info/domains", nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", fetch.UserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")

	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // do not follow; a redirect is the wall
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := fetch.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	// A wall page can arrive with status 200; detect it by content too.
	if isWalledBody(body) {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	_, ok := payload["domains"]
	return ok
}

// ResolveDomain picks the EAPI domain to use.
//
// A pinned domain is returned as-is with no probing: an explicit override means
// no silent switching, ever. Otherwise each candidate is probed in order and the
// first healthy one wins. If every candidate fails, the first is returned so the
// real error surfaces at the call site instead of a confusing "no domain".
func ResolveDomain(ctx context.Context, pinned string, timeout time.Duration, log func(string, ...any)) string {
	if d := strings.TrimSpace(pinned); d != "" {
		if log != nil {
			log("EAPI domain pinned: %s", d)
		}
		return strings.TrimSuffix(d, "/")
	}
	for _, candidate := range DefaultDomains {
		if ProbeDomain(ctx, candidate, timeout) {
			if log != nil {
				log("EAPI domain selected by probe: %s", candidate)
			}
			return candidate
		}
		if log != nil {
			log("EAPI candidate %s failed health probe, trying next", candidate)
		}
	}
	if log != nil {
		log("all EAPI candidates failed the health probe; falling back to %s", DefaultDomains[0])
	}
	return DefaultDomains[0]
}

func isWalledBody(body []byte) bool {
	return strings.Contains(strings.ToLower(string(body)), "diamwall")
}

// domainStatus renders a short description for a walled response.
func domainStatus(code int) string {
	switch code {
	case 307:
		return "HTTP 307 self-redirect (__diamwall cookie)"
	case 403:
		return "HTTP 403 Access Denied"
	case 513:
		return "HTTP 513 Access Denied"
	case 517:
		return "HTTP 517 Access Denied"
	default:
		return fmt.Sprintf("HTTP %d", code)
	}
}
