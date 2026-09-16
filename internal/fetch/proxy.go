package fetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const dialTimeout = 10 * time.Second

// NewTransport builds an HTTP transport that honours a proxy.
//
// Resolution order: an explicit proxy URL, then the standard environment
// variables (HTTPS_PROXY, HTTP_PROXY, ALL_PROXY), then a direct connection.
// Both http(s) and socks5 proxies are supported; socks5 goes through Go's
// native handling of the socks5 scheme in a proxy URL.
func NewTransport(proxyURL string, headerTimeout time.Duration) (*http.Transport, error) {
	if headerTimeout <= 0 {
		headerTimeout = 30 * time.Second
	}
	t := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: headerTimeout,
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}

	if strings.TrimSpace(proxyURL) != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("unsupported proxy scheme %q (expected http, https, or socks5)", u.Scheme)
		}
		t.Proxy = http.ProxyURL(u)
		return t, nil
	}

	t.Proxy = http.ProxyFromEnvironment
	return t, nil
}

// EffectiveProxy reports which proxy a request would use, for diagnostic
// output. It returns "" for a direct connection.
func EffectiveProxy(proxyURL string) string {
	if strings.TrimSpace(proxyURL) != "" {
		return proxyURL
	}
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// Diagnosis is a staged connectivity report for one host.
//
// Each stage records what was learned rather than a boolean, because the
// interesting question is *where* the connection stopped: a resolve that
// returns an unrelated IP means DNS interference, while a resolve that succeeds
// and a connect that times out means the traffic is being dropped.
type Diagnosis struct {
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	Addresses []string `json:"addresses,omitempty"`
	Resolved  bool     `json:"resolved"`
	Connected bool     `json:"connected"`
	TLSOK     bool     `json:"tls_ok"`
	Status    int      `json:"http_status,omitempty"`
	Problem   string   `json:"problem,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

// Healthy reports whether the host answered as expected.
func (d Diagnosis) Healthy() bool { return d.Problem == "" }

// Diagnose walks DNS, TCP, TLS, and HTTP in order and stops at the first
// failure, returning a human-readable explanation of that stage.
//
// The stages are separated deliberately: net/http reports all of them as one
// opaque "request failed" error, which is what makes a blocked domain and a
// dead server indistinguishable in a normal error message.
func Diagnose(ctx context.Context, host string, port int, useTLS bool, timeout time.Duration) Diagnosis {
	d := Diagnosis{Host: host, Port: port}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	resolver := net.Resolver{}
	addrs, err := resolver.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		d.Problem = "dns"
		if err != nil {
			d.Detail = fmt.Sprintf("DNS lookup failed: %v", err)
		} else {
			d.Detail = "DNS lookup returned no addresses"
		}
		return d
	}
	d.Resolved = true
	d.Addresses = addrs

	addr := net.JoinHostPort(host, fmt.Sprint(port))
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		d.Problem = "connect"
		if isTimeout(err) {
			d.Detail = fmt.Sprintf("TCP connect to %s timed out after %s — the traffic is being dropped "+
				"(DNS resolved to %s, so this is a network-level block rather than a missing host)",
				addr, timeout, strings.Join(addrs, ", "))
		} else {
			d.Detail = fmt.Sprintf("TCP connect to %s failed: %v", addr, err)
		}
		return d
	}
	defer conn.Close()
	d.Connected = true

	if !useTLS {
		d.Detail = fmt.Sprintf("connected to %s", addr)
		return d
	}

	tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
	if err := tlsConn.SetDeadline(time.Now().Add(timeout)); err == nil {
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			d.Problem = "tls"
			// A certificate that names a different host is the signature of DNS
			// interference: the connection reached *something*, just not this
			// site.
			if strings.Contains(err.Error(), "certificate") || strings.Contains(err.Error(), "not valid for") {
				d.Detail = fmt.Sprintf("TLS handshake failed with a certificate error (%v) — the host reached "+
					"presents a certificate for a different domain, which indicates DNS interference", err)
			} else {
				d.Detail = fmt.Sprintf("TLS handshake failed: %v", err)
			}
			return d
		}
	}
	d.TLSOK = true
	d.Detail = fmt.Sprintf("TLS established with %s", addr)

	// A HEAD on the root path proves an application-level response without
	// pulling a full page.
	scheme := "https"
	if !useTLS {
		scheme = "http"
	}
	hc := &http.Client{Timeout: timeout}
	req, rerr := http.NewRequestWithContext(ctx, http.MethodHead, scheme+"://"+host+"/", nil)
	if rerr == nil {
		req.Header.Set("User-Agent", UserAgent)
		if resp, herr := hc.Do(req); herr == nil {
			resp.Body.Close()
			d.Status = resp.StatusCode
			d.Detail = fmt.Sprintf("HTTP %d from %s", resp.StatusCode, host)
		}
	}
	return d
}

func isTimeout(err error) bool {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out")
}
