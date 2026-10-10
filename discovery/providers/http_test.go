package providers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testFetcher points a fetcher at a local httptest server. Loopback is permitted
// and the server's own certificate authority is trusted, so the real TLS and
// transport paths are still exercised rather than bypassed.
func testFetcher(t *testing.T, srv *httptest.Server) *HTTPFetcher {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return NewHTTPFetcher(HTTPConfig{
		AllowLoopback: true,
		Deps: HTTPDeps{
			ResolveHost: resolver(map[string][]string{}),
			TLSConfig:   &tls.Config{RootCAs: pool},
		},
	})
}

// resolver returns fixed addresses for a host, so SSRF checks can be exercised
// without real DNS.
func resolver(mapping map[string][]string) func(context.Context, string) ([]net.IP, error) {
	return func(_ context.Context, host string) ([]net.IP, error) {
		raw, ok := mapping[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		out := make([]net.IP, 0, len(raw))
		for _, s := range raw {
			out = append(out, net.ParseIP(s))
		}
		return out, nil
	}
}

func TestSSRFRefusesInternalEndpoints(t *testing.T) {
	// A provider endpoint is configured data, and a misconfigured or hostile one
	// must not be able to steer discovery at internal infrastructure.
	blocked := []string{
		"https://127.0.0.1/x",
		"https://localhost/x",
		"https://10.1.2.3/x",
		"https://192.168.0.1/x",
		"https://172.16.5.4/x",
		"https://169.254.169.254/latest/meta-data/",
		"https://[::1]/x",
		"https://100.64.0.1/x",
		"https://198.18.0.1/x",
		"https://service.internal/x",
		"https://db.local/x",
		"https://box.home.arpa/x",
		"http://api.example.com/x",
		"https://user:pass@api.example.com/x",
	}
	for _, raw := range blocked {
		f := NewHTTPFetcher(HTTPConfig{
			Deps: HTTPDeps{ResolveHost: resolver(map[string][]string{
				"localhost":       {"127.0.0.1"},
				"api.example.com": {"93.184.216.34"},
			})},
		})
		if _, _, err := f.Get(context.Background(), raw, nil); err == nil {
			t.Errorf("Get(%q) was allowed; it must be refused", raw)
		}
	}
}

func TestSSRFAllowsPublicEndpoints(t *testing.T) {
	// A hostname that resolves to a private address is refused even though the
	// name itself looks harmless, because the resolution is what matters.
	f := NewHTTPFetcher(HTTPConfig{
		Deps: HTTPDeps{ResolveHost: resolver(map[string][]string{
			"rebind.example.com":   {"93.184.216.34"},
			"internal.example.com": {"10.0.0.5"},
			"mixed.example.com":    {"93.184.216.34", "127.0.0.1"},
		})},
	})
	if _, _, err := f.Get(context.Background(), "https://rebind.example.com/x", nil); err == nil {
		t.Error("a public resolution should be allowed")
	}
	if _, _, err := f.Get(context.Background(), "https://internal.example.com/x", nil); err == nil {
		t.Error("a host resolving to a private address must be refused")
	}
	// One bad address in the list is enough: a hostname that resolves to both a
	// public and a loopback address is a rebinding attempt.
	if _, _, err := f.Get(context.Background(), "https://mixed.example.com/x", nil); err == nil {
		t.Error("a mixed public/private resolution must be refused")
	}
}

func TestSecretHeadersAreRefused(t *testing.T) {
	// A provider must not be able to persuade discovery to attach a credential to
	// a host it controls.
	f := NewHTTPFetcher(HTTPConfig{
		Deps: HTTPDeps{ResolveHost: resolver(map[string][]string{"api.example.com": {"93.184.216.34"}})},
	})
	for _, h := range []string{"Authorization", "authorization", "X-API-Key", "Cookie", "Proxy-Authorization"} {
		if _, _, err := f.Get(context.Background(), "https://api.example.com/x", map[string]string{h: "secret"}); err == nil {
			t.Errorf("header %q was forwarded to a third-party endpoint", h)
		}
	}
}

func TestResponseSizeIsCappedWhileReading(t *testing.T) {
	// The cap has to be enforced during the read, not by trusting Content-Length,
	// because a server can omit or lie about the header and "trust it, then read
	// forever" is how a provider takes the run's memory with it.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A genuine oversized body with no declared length, so the only thing that
		// can stop it is the cap in the reader.
		w.Header().Set("Transfer-Encoding", "chunked")
		chunk := strings.Repeat("A", 64*1024)
		for i := 0; i < 400; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	f := testFetcher(t, srv)
	_, _, err := f.Get(context.Background(), srv.URL, nil)
	if err == nil {
		t.Fatal("an oversized response was accepted")
	}
	if !strings.Contains(err.Error(), "bytes") {
		t.Errorf("error = %v, want it to mention the size cap", err)
	}
}

func TestLargeButLegalBodyIsRead(t *testing.T) {
	// The cap must not be so low that it refuses legitimate payloads.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 1<<20))
	}))
	defer srv.Close()
	f := testFetcher(t, srv)
	body, _, err := f.Get(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("a 1 MiB body was refused: %v", err)
	}
	if len(body) != 1<<20 {
		t.Errorf("read %d bytes, want %d", len(body), 1<<20)
	}
}

func TestRedactURLHidesCredentials(t *testing.T) {
	// Provider keys travel in query strings, so an unredacted URL in a log line
	// or the ledger is a leaked key.
	got := redactURL("https://api.example.com/search?q=acme&api_key=sk_live_secret123&page=2")
	if strings.Contains(got, "sk_live_secret123") {
		t.Errorf("redactURL left the key in place: %s", got)
	}
	if strings.Contains(got, "acme") {
		t.Errorf("redactURL should drop the whole query: %s", got)
	}
	if !strings.Contains(got, "api.example.com") {
		t.Errorf("redactURL should keep the host for diagnosis: %s", got)
	}
	if got := redactURL("https://user:pass@api.example.com/x"); strings.Contains(got, "pass") {
		t.Errorf("redactURL left credentials in place: %s", got)
	}
	if got := redactURL("://not a url"); strings.Contains(got, "not a url") {
		t.Errorf("an unparseable URL should be replaced wholesale, got %s", got)
	}
}

func TestNormalizeDomainFromAcceptsEveryProviderForm(t *testing.T) {
	cases := map[string]string{
		"acme.com":                 "acme.com",
		"www.acme.com":             "acme.com",
		"https://acme.com/about":   "acme.com",
		"http://shop.acme.co.uk/x": "acme.co.uk",
		"HTTPS://ACME.COM":         "acme.com",
		"acme.com:8443":            "acme.com",
		"acme.com/?q=1":            "acme.com",
		"münchen-industrie.de":     "xn--mnchen-industrie-jzb.de",
	}
	for in, want := range cases {
		got, err := normalizeDomainFrom(in)
		if err != nil {
			t.Errorf("normalizeDomainFrom(%q) = %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeDomainFrom(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeDomainFromRejectsNonDomains(t *testing.T) {
	// A value that cannot be reduced to a registrable domain is not a company
	// website, and guessing would put a junk key in the candidate set.
	for _, in := range []string{
		"",
		"   ",
		"192.0.2.10",
		"https://192.0.2.10/x",
		"not a domain",
		"localhost",
		"https://",
	} {
		if got, err := normalizeDomainFrom(in); err == nil {
			t.Errorf("normalizeDomainFrom(%q) = %q, want an error", in, got)
		}
	}
}

func TestIsDisallowedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "0.0.0.0", "10.0.0.1", "172.16.0.1", "192.168.1.1",
		"169.254.1.1", "100.64.0.1", "198.18.0.1", "224.0.0.1", "::1",
		"fe80::1", "fc00::1", "ff02::1",
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test address %q does not parse", s)
		}
		if !isDisallowedIP(ip) {
			t.Errorf("isDisallowedIP(%s) = false, want true", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "93.184.216.34", "2001:4860:4860::8888"} {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test address %q does not parse", s)
		}
		if isDisallowedIP(ip) {
			t.Errorf("isDisallowedIP(%s) = true, want false", s)
		}
	}
	if !isDisallowedIP(nil) {
		t.Error("a nil address must be treated as disallowed: failing open is not an option")
	}
}

func TestGetJSONDecodesAndReportsStatus(t *testing.T) {
	// The status is what lets a caller tell 404 (genuinely absent) from 429 or
	// 500 (the surface is unhealthy), which is the difference between a real
	// answer and a broken run.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte(`{"hello":"world"}`))
		case "/gone":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"nope"}`))
		case "/garbage":
			w.Write([]byte(`not json at all`))
		case "/denied":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"forbidden"}`))
		case "/throttled":
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"slow down"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	f := testFetcher(t, srv)

	var ok struct{ Hello string }
	if status, err := f.GetJSON(context.Background(), srv.URL+"/ok", nil, &ok); err != nil {
		t.Fatalf("GetJSON(/ok) = %v", err)
	} else if status != 200 || ok.Hello != "world" {
		t.Errorf("status=%d body=%+v", status, ok)
	}

	var gone struct{}
	status, err := f.GetJSON(context.Background(), srv.URL+"/gone", nil, &gone)
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
	// A 404 still returns an error, because the caller must not mistake an absent
	// subject for a successful empty answer.
	if err == nil {
		t.Error("a 404 returned no error; the caller cannot tell it apart from a hit")
	}

	if _, err := f.GetJSON(context.Background(), srv.URL+"/garbage", nil, &ok); err == nil {
		t.Error("undecodable JSON was accepted")
	}
	if _, err := f.GetJSON(context.Background(), srv.URL+"/boom", nil, &ok); !errors.Is(err, ErrUpstream) {
		t.Errorf("a 500 returned %v, want ErrUpstream", err)
	}
	if _, err := f.GetJSON(context.Background(), srv.URL+"/denied", nil, &ok); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("a 403 returned %v, want ErrNotConfigured", err)
	}
	if _, err := f.GetJSON(context.Background(), srv.URL+"/throttled", nil, &ok); !errors.Is(err, ErrBudgetExhausted) {
		t.Errorf("a 429 returned %v, want ErrBudgetExhausted", err)
	}
}
