// Package zlib implements the Z-Library EAPI client.
//
// Z-Library publishes no documented API. Since the February 2026 EAPI
// migration, access goes through the undocumented JSON endpoints under /eapi/,
// which bypass the Cloudflare browser challenge the HTML site presents. This
// package is a from-scratch Go port of the behaviour the vendored Python
// clients in the sibling projects established:
//
//   - the domain list and health probe (probe /eapi/info/domains, never
//     /eapi/user/login, which is rate limited to ~10/hour/IP);
//   - DiamWall anti-bot detection, so a wall reads as "anti-bot wall" rather
//     than a JSON decode error;
//   - cookie-based auth via the remix_userid/remix_userkey pair;
//   - the download flow, including Content-Disposition filename extraction and
//     path-traversal sanitisation.
package zlib

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotAuthenticated is returned when a call needs credentials and none were
// supplied or accepted.
var ErrNotAuthenticated = errors.New("z-library: not authenticated")

// ErrDomainWalled is returned when the selected domain is behind an anti-bot
// wall. It is a distinct error because the remedy (switch domains) differs from
// a generic transport failure (retry).
var ErrDomainWalled = errors.New("z-library: anti-bot wall")

// WalledError names the domain and the wall that intercepted the request.
type WalledError struct {
	Domain string
	Detail string
}

func (e *WalledError) Error() string {
	return fmt.Sprintf(
		"anti-bot wall detected on %s (%s): the domain blocks programmatic /eapi access. "+
			"Set ZLIBRARY_EAPI_DOMAIN to a working domain (e.g. z-library.ec) or leave it unset "+
			"to let the built-in fallback list pick one",
		e.Domain, e.Detail)
}

func (e *WalledError) Is(target error) bool { return target == ErrDomainWalled }

// APIError is a structured error returned inside an otherwise successful HTTP
// response, e.g. {"success":0,"error":"Book not found"}.
type APIError struct {
	Operation string
	Message   string
}

func (e *APIError) Error() string {
	if e.Operation == "" {
		return "z-library API error: " + e.Message
	}
	return fmt.Sprintf("z-library %s failed: %s", e.Operation, e.Message)
}

// QuotaError reports that the account's daily download limit was reached.
// It is a distinct type because the remedy (switch to another account) differs
// from every other download failure (give up or retry the same account later).
type QuotaError struct {
	Account string // email or "" when unknown
	Message string
}

func (e *QuotaError) Error() string {
	if e.Account == "" {
		return "z-library download quota exhausted: " + e.Message
	}
	return fmt.Sprintf("z-library download quota exhausted for %s: %s", e.Account, e.Message)
}

// IsQuotaError reports whether err is, or wraps, a QuotaError.
func IsQuotaError(err error) bool {
	var q *QuotaError
	return errors.As(err, &q)
}

// quotaMessages are substrings the EAPI uses when the daily download limit is
// the failure reason. The exact wording has varied, so a substring match on a
// lowercased message is used rather than an exact comparison.
var quotaMessages = []string{
	"daily limit",
	"downloads limit",
	"download limit",
	"limit of downloads",
	"limit reached",
	"quota",
	"too many downloads",
}

// classifyQuota wraps an APIError into a QuotaError when its message indicates
// the account's download allowance is spent. Callers use this to decide
// whether rotating to another account could help.
func classifyQuota(op, message string) error {
	lower := strings.ToLower(message)
	for _, m := range quotaMessages {
		if strings.Contains(lower, m) {
			return &QuotaError{Message: message}
		}
	}
	return &APIError{Operation: op, Message: message}
}
