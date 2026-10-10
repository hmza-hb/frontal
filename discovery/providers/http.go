package providers

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/domain"
)

// MaxResponseBytes caps how much of any provider response is read.
//
// The cap is enforced while reading rather than by checking Content-Length
// afterwards, because a hostile or broken server can simply omit or lie about
// Content-Length, and "trust the header, then read forever" is how a provider
// takes a run's memory with it.
const MaxResponseBytes = 8 << 20 // 8 MiB

// HTTPDeps are the hooks the HTTP client uses, so tests can drive it without a
// network and without a resolver that reaches the internet.
type HTTPDeps struct {
	// Do issues a request. Nil means a package default client.
	Do func(*http.Request) (*http.Response, error)
	// ResolveHost looks up a hostname. Nil means the system resolver. Tests
	// substitute it to prove private address space is refused without needing
	// real DNS.
	ResolveHost func(ctx context.Context, host string) ([]net.IP, error)
	// TLSConfig supplies the TLS settings, so a test can trust a local httptest
	// certificate authority instead of disabling verification and skipping the
	// handshake it is trying to exercise. Nil means the default.
	TLSConfig *tls.Config
}

// systemResolver is the default. It is deliberately not nil: a fetcher with no
// resolver must still refuse internal addresses, so "no resolver configured"
// cannot degrade into "no check performed".
func systemResolver(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, nil
}

// HTTPFetcher performs bounded, SSRF-checked GETs against provider endpoints.
type HTTPFetcher struct {
	client        *http.Client
	deps          HTTPDeps
	userAgent     string
	maxRedirects  int
	allowHTTP     bool
	allowLoopback bool
}

// HTTPConfig configures an HTTPFetcher.
type HTTPConfig struct {
	// Timeout bounds a single request including body read.
	Timeout time.Duration
	// UserAgent identifies discovery. A provider that blocks default agents is
	// working as intended; an honest agent is still the right thing to send.
	UserAgent string
	// MaxRedirects caps redirect following.
	MaxRedirects int
	// InsecureSkipVerify is available for a corporate TLS-inspecting proxy and
	// is rejected by config validation in production. It is never the default.
	InsecureSkipVerify bool
	// AllowHTTP permits plain HTTP endpoints. Also rejected in production, so a
	// provider cannot silently downgrade a search key onto the wire.
	AllowHTTP bool
	// AllowLoopback permits loopback and private endpoints. It exists so tests can
	// point a fetcher at an httptest server, and config validation refuses it in a
	// real deployment. It is never the default: without it, a provider endpoint
	// can never reach anything on this host's own network.
	AllowLoopback bool
	// Deps are test hooks.
	Deps HTTPDeps
}

// NewHTTPFetcher returns a fetcher.
func NewHTTPFetcher(cfg HTTPConfig) *HTTPFetcher {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "lead-intelligence-discovery/1.0"
	}
	if cfg.MaxRedirects <= 0 {
		cfg.MaxRedirects = 5
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // opt-in, refused by config validation in production
	}
	if cfg.Deps.TLSConfig != nil {
		tlsConfig = cfg.Deps.TLSConfig.Clone()
		tlsConfig.MinVersion = tls.VersionTLS12
	}
	resolveHost := cfg.Deps.ResolveHost
	if resolveHost == nil {
		resolveHost = systemResolver
	}
	if cfg.AllowLoopback {
		// Tests reach an httptest server on 127.0.0.1, so the pinned dial must be
		// able to connect to a literal loopback address. Config validation refuses
		// this flag in a real deployment.
		prevResolve := resolveHost
		resolveHost = func(ctx context.Context, host string) ([]net.IP, error) {
			if ip := net.ParseIP(host); ip != nil {
				return []net.IP{ip}, nil
			}
			return prevResolve(ctx, host)
		}
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// The dialer re-checks the address it is about to connect to. Validating a
		// hostname and then letting the transport resolve it again leaves a window
		// where the second lookup returns a private address, which is DNS
		// rebinding. Connecting to the address that was actually inspected closes
		// that window.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("providers: %q is not a dial address: %w", addr, err)
			}
			ips, err := resolveHost(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("providers: cannot resolve %q: %w", host, err)
			}
			var lastErr error
			for _, ip := range ips {
				if !addrAllowed(ip, cfg.AllowLoopback) {
					lastErr = fmt.Errorf("providers: %q resolves to the reserved address %s", host, ip)
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("providers: no usable address for %q", host)
			}
			return nil, lastErr
		},
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}
	client := &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
	}
	cfg.Deps.ResolveHost = resolveHost
	// The redirect check reuses the same host rules, so it is a method on the
	// fetcher and is installed after construction.
	var redirectCheck func(*url.URL) error
	f := &HTTPFetcher{
		client:        client,
		deps:          cfg.Deps,
		userAgent:     cfg.UserAgent,
		maxRedirects:  cfg.MaxRedirects,
		allowHTTP:     cfg.AllowHTTP,
		allowLoopback: cfg.AllowLoopback,
	}
	redirectCheck = func(u *url.URL) error { return f.validateEndpoint(u.String()) }
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= cfg.MaxRedirects {
			return fmt.Errorf("providers: stopped after %d redirects", cfg.MaxRedirects)
		}
		return redirectCheck(req.URL)
	}
	if f.deps.Do == nil {
		f.deps.Do = client.Do
	}
	return f
}

// GetJSON fetches a URL and decodes JSON into out, refusing a body larger than
// MaxResponseBytes.
//
// It returns the HTTP status so a caller can distinguish 404 (the subject is
// genuinely absent) from 429 or 500 (the surface is unhealthy), which is the
// difference between a real answer and a broken run.
func (f *HTTPFetcher) GetJSON(ctx context.Context, rawURL string, headers map[string]string, out any) (int, error) {
	body, status, err := f.Get(ctx, rawURL, headers)
	if err != nil {
		return status, err
	}
	if out == nil {
		return status, nil
	}
	// A non-2xx is never a success. Without this a 404 body decodes into an empty
	// struct and the caller records "no results", which is how a provider outage
	// gets written into the candidate set as a finding.
	if err := statusError(status); err != nil {
		return status, fmt.Errorf("providers: %s: %w", redactURL(rawURL), err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(out); err != nil {
		return status, fmt.Errorf("providers: %s returned undecodable JSON: %w", redactURL(rawURL), err)
	}
	return status, nil
}

// statusError maps an HTTP status onto the classified provider errors, so the
// runner's breaker and cost accounting can tell an absent subject from a throttled
// or broken endpoint.
func statusError(status int) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusNotFound || status == http.StatusGone:
		return fmt.Errorf("%w: http %d", ErrNotFound, status)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: http %d", ErrNotConfigured, status)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w: http %d", ErrBudgetExhausted, status)
	case status >= 500:
		return fmt.Errorf("%w: http %d", ErrUpstream, status)
	default:
		return fmt.Errorf("%w: http %d", ErrUpstream, status)
	}
}

// Get fetches a URL and returns the size-capped body.
func (f *HTTPFetcher) Get(ctx context.Context, rawURL string, headers map[string]string) ([]byte, int, error) {
	if err := f.validateEndpoint(rawURL); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("providers: build request for %s: %w", redactURL(rawURL), err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		if isSecretHeader(k) {
			// Refuse rather than forward, so a provider cannot persuade discovery
			// to attach a credential to a host it controls.
			return nil, 0, fmt.Errorf("providers: refusing to send header %q to a third-party endpoint", k)
		}
		req.Header.Set(k, v)
	}
	resp, err := f.deps.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("providers: request to %s failed: %w", redactURL(rawURL), err)
	}
	defer func() {
		// Drain a little so the connection can be reused, then close.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	// Trust the byte count from the limited reader, not the header. A server
	// that understates its length must not be able to make discovery allocate
	// unbounded memory.
	limited := io.LimitReader(resp.Body, MaxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("providers: reading %s: %w", redactURL(rawURL), err)
	}
	if len(body) > MaxResponseBytes {
		return nil, resp.StatusCode, fmt.Errorf("providers: %s returned more than %d bytes", redactURL(rawURL), MaxResponseBytes)
	}
	return body, resp.StatusCode, nil
}

// validateEndpoint refuses anything that is not a public HTTPS endpoint.
func (f *HTTPFetcher) validateEndpoint(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("providers: %q is not a URL: %w", redactURL(rawURL), err)
	}
	if u.Scheme != "https" {
		if f.allowHTTP && u.Scheme == "http" {
			// Permitted only when the deployment explicitly allows it.
			return f.validateHost(u.Hostname())
		}
		return fmt.Errorf("providers: %s must use https", redactURL(rawURL))
	}
	if u.User != nil {
		// Credentials in a URL end up in logs and in the ledger.
		return fmt.Errorf("providers: %s must not embed credentials", redactURL(rawURL))
	}
	return f.validateHost(u.Hostname())
}

// validateHost refuses loopback, link-local, private, and otherwise reserved
// addresses, so a provider cannot steer discovery at internal infrastructure.
func (f *HTTPFetcher) validateHost(host string) error {
	if host == "" {
		return errors.New("providers: endpoint has no host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !addrAllowed(ip, f.allowLoopback) {
			return fmt.Errorf("providers: endpoint resolves to the reserved address %s", ip)
		}
		return nil
	}
	lower := strings.ToLower(host)
	// Named internal zones are refused without a lookup, because a lookup that
	// failed open would be worse than no lookup at all.
	for _, suffix := range []string{".local", ".internal", ".localdomain", ".home.arpa"} {
		if strings.HasSuffix(lower, suffix) {
			return fmt.Errorf("providers: endpoint host %q is an internal name", host)
		}
	}
	// Fails closed. A lookup that cannot complete, or that returns nothing, is
	// refused rather than treated as permission: an unresolvable endpoint is
	// never one discovery should contact.
	addrs, err := f.deps.ResolveHost(context.Background(), host)
	if err != nil {
		return fmt.Errorf("providers: cannot resolve endpoint host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("providers: endpoint host %q resolved to no addresses", host)
	}
	for _, ip := range addrs {
		if !addrAllowed(ip, f.allowLoopback) {
			return fmt.Errorf("providers: endpoint host %q resolves to the reserved address %s", host, ip)
		}
	}
	return nil
}

// addrAllowed is the single address rule, shared by the pinned dial and the
// endpoint check so the two can never drift apart.
func addrAllowed(ip net.IP, allowLoopback bool) bool {
	if allowLoopback {
		return true
	}
	return !isDisallowedIP(ip)
}

// isDisallowedIP reports whether an address is one discovery must never contact.
func isDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// Carrier-grade NAT and the IPv4 benchmarking range are not public services.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true // 100.64.0.0/10
		}
		if v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) {
			return true // 198.18.0.0/15
		}
	}
	// IPv6 unique local addresses.
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return true
	}
	return false
}

func isSecretHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "x-api-key", "api-key":
		return true
	default:
		return false
	}
}

// redactURL removes query parameters from a URL before it is written to an error,
// a log line, or the ledger. Provider keys travel in query strings, so an
// unredacted URL is a leaked key.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "a configured endpoint"
	}
	if u.RawQuery != "" {
		u.RawQuery = "redacted"
	}
	u.User = nil
	return u.String()
}

// normalizeDomainFrom reduces any provider-supplied host, URL, or bare domain to
// a registrable domain, returning an error when it cannot. Providers return all
// three forms, and a value that cannot be reduced is not a company website.
func normalizeDomainFrom(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("providers: empty domain")
	}
	host := value
	if strings.Contains(value, "//") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", fmt.Errorf("providers: %q is not a URL: %w", redactURL(value), err)
		}
		host = parsed.Hostname()
	} else if i := strings.IndexAny(value, "/?#"); i >= 0 {
		host = value[:i]
	}
	if host == "" {
		return "", fmt.Errorf("providers: %q has no host", redactURL(value))
	}
	d, err := domain.FromHost(host)
	if err != nil {
		return "", fmt.Errorf("providers: %q is not a usable domain: %w", redactURL(value), err)
	}
	if d.IsIP {
		return "", fmt.Errorf("providers: %q is an address, not a company website", redactURL(value))
	}
	// A single-label host has no public suffix, so it is an internal name rather
	// than a registrable domain, and accepting it would put a junk key in the
	// candidate set.
	if !strings.Contains(d.Registrable, ".") {
		return "", fmt.Errorf("providers: %q is not a registrable domain", redactURL(value))
	}
	return d.Registrable, nil
}

// requireHTTPS refuses an endpoint that is not HTTPS. It is the check that keeps
// an API key from being put on the wire in the clear, applied to any
// credential-bearing endpoint before the first request.
func requireHTTPS(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("not a URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("must use https, got %q", redactURL(rawURL))
	}
	if u.Host == "" {
		return errors.New("has no host")
	}
	return nil
}
