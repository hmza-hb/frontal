package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/config"
)

func mustSeed(t *testing.T, cfg SeedConfig) *SeedProvider {
	t.Helper()
	p, err := NewSeedProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSeedProviderReturnsConfiguredSeeds(t *testing.T) {
	conf := 0.9
	p := mustSeed(t, SeedConfig{Seeds: []config.SeedEntry{
		{Name: "Acme Robotics", Domain: "acme.test", Industry: "robotics", Country: "de",
			EmployeeHint: "50-200", Notes: "vision controllers for the automotive sector", Confidence: &conf},
		{Name: "Beta Systems"},
		{Domain: "gamma.test"},
	}})
	if err := p.Ready(); err != nil {
		t.Fatalf("Ready = %v", err)
	}
	// An empty query text means "all seeds", which is how the expansion stage asks
	// for the operator's whole starting set.
	res, err := p.Search(context.Background(), Query{})
	if err != nil {
		t.Fatalf("Search = %v", err)
	}
	if len(res.Candidates) != 3 {
		t.Fatalf("got %d candidates, want 3", len(res.Candidates))
	}
	acme := res.Candidates[0]
	if acme.Domain != "acme.test" || acme.URL != "https://acme.test/" {
		t.Errorf("acme = %+v", acme)
	}
	if acme.Industry != "robotics" || acme.Country != "DE" {
		t.Errorf("acme geography/industry = %q/%q", acme.Country, acme.Industry)
	}
	if acme.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want the seed's 0.9", acme.Confidence)
	}
	// The seed is the operator's own claim, not something a provider confirmed, so
	// the method has to say so or ranking cannot weight it.
	if len(acme.Evidence) != 1 {
		t.Fatalf("Evidence = %+v, want one record", acme.Evidence)
	}
	if acme.Evidence[0].Source != candidate.SourceSeed || acme.Evidence[0].Method != candidate.MethodSeedImport {
		t.Errorf("Evidence = %+v, want a seed/seed_import record", acme.Evidence[0])
	}
	if acme.Evidence[0].ObservedAt.IsZero() {
		t.Error("Evidence has no timestamp; freshness cannot be scored without one")
	}
	if acme.SourceCount != 1 {
		t.Errorf("SourceCount = %d, want 1", acme.SourceCount)
	}
	// A name-only seed is legitimate: finding its domain is the whole job.
	if res.Candidates[2].Name != "gamma.test" || res.Candidates[2].Domain != "gamma.test" {
		t.Errorf("a domain-only seed should be named after its domain, got %+v", res.Candidates[2])
	}
}

func TestSeedProviderNormalisesTheDomain(t *testing.T) {
	// Seeds are typed by hand, so a www prefix, a scheme, or a path all show up.
	p := mustSeed(t, SeedConfig{Seeds: []config.SeedEntry{
		{Name: "A", Domain: "https://www.acme.test/about?x=1"},
	}})
	res, err := p.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates[0].Domain != "acme.test" {
		t.Errorf("Domain = %q, want the registrable form", res.Candidates[0].Domain)
	}
}

func TestSeedProviderKeepsAnUnusableDomainAsANameOnlyLead(t *testing.T) {
	// A company with a bad domain string is still a real company. Dropping the row
	// would lose the lead; the expansion stage is what tries to find the site.
	p := mustSeed(t, SeedConfig{Seeds: []config.SeedEntry{{Name: "Local Co", Domain: "localhost"}}})
	res, err := p.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(res.Candidates))
	}
	if res.Candidates[0].Name != "Local Co" {
		t.Errorf("Name = %q, the operator's name must survive", res.Candidates[0].Name)
	}
	if res.Candidates[0].Domain != "" {
		t.Errorf("Domain = %q, want it dropped as unusable", res.Candidates[0].Domain)
	}
}

func TestSeedProviderFiltersByQueryText(t *testing.T) {
	p := mustSeed(t, SeedConfig{Seeds: []config.SeedEntry{
		{Name: "Acme Robotics", Domain: "acme.test"},
		{Name: "Beta Systems", Domain: "beta.test"},
	}})
	res, err := p.Search(context.Background(), Query{Text: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Domain != "beta.test" {
		t.Errorf("got %+v, want only the matching seed", res.Candidates)
	}
}

func TestSeedProviderRefusesEmptyConfig(t *testing.T) {
	// With no usable seed the provider has nothing to contribute, and reporting it
	// unready keeps the run's coverage report honest.
	p := mustSeed(t, SeedConfig{Seeds: []config.SeedEntry{{Name: "   "}}})
	if err := p.Ready(); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Ready = %v, want ErrNotConfigured", err)
	}
	if _, err := p.Search(context.Background(), Query{Text: "q"}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Search = %v, want ErrNotConfigured", err)
	}
}

func TestSeedProviderRejectsAnUnusableSeedFile(t *testing.T) {
	// A missing or malformed seed file is a configuration error. Silently
	// returning nothing lets a typo quietly produce an empty lead list.
	dir := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"truncated.json", `[{"name":"Acme"`},
		{"wrongtype.json", `["just a string"]`},
		{"blank.json", `[{"name":"   "},{"name":""}]`},
		{"unsupported.log", "Acme\nBeta\n"},
	} {
		path := filepath.Join(dir, tc.name)
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		p := mustSeed(t, SeedConfig{Path: path})
		if err := p.Ready(); err == nil {
			t.Errorf("Ready accepted %s; a typo must not read as an empty lead list", tc.name)
		}
		if _, err := p.Search(context.Background(), Query{}); err == nil {
			t.Errorf("Search accepted %s", tc.name)
		}
	}
	p := mustSeed(t, SeedConfig{Path: filepath.Join(dir, "absent.json")})
	if err := p.Ready(); err == nil {
		t.Error("Ready accepted a missing seed file")
	}
}

func TestSeedProviderLoadsAFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seeds.json")
	if err := os.WriteFile(path, []byte(`[
	  {"name":"Acme","domain":"acme.test","industry":"robotics"},
	  {"name":"Beta","domain":"beta.test","country":"us"}
	]`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := mustSeed(t, SeedConfig{Path: path})
	if err := p.Ready(); err != nil {
		t.Fatalf("Ready = %v", err)
	}
	res, err := p.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 || res.Candidates[1].Country != "US" {
		t.Errorf("got %+v", res.Candidates)
	}
}

func TestSeedProviderIsDeterministic(t *testing.T) {
	// Two identical runs must produce identical candidates, or a re-run cannot be
	// diffed against the first.
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	mk := func() candidate.Candidate {
		p := mustSeed(t, SeedConfig{
			Seeds: []config.SeedEntry{{Name: "Acme", Domain: "acme.test"}},
			Now:   func() time.Time { return now },
		})
		res, err := p.Search(context.Background(), Query{})
		if err != nil {
			t.Fatal(err)
		}
		return res.Candidates[0]
	}
	a, b := mk(), mk()
	if !a.Evidence[0].ObservedAt.Equal(b.Evidence[0].ObservedAt) {
		t.Errorf("ObservedAt differs between runs: %v and %v", a.Evidence[0].ObservedAt, b.Evidence[0].ObservedAt)
	}
	if a.Evidence[0].URL != b.Evidence[0].URL {
		t.Errorf("evidence URL differs between runs: %q and %q", a.Evidence[0].URL, b.Evidence[0].URL)
	}
}

func TestSeedProviderNeverClaimsRemoteProvenance(t *testing.T) {
	// A seed is local data, so it must be usable with no network at all, and must
	// never claim a remote source.
	p := mustSeed(t, SeedConfig{Seeds: []config.SeedEntry{{Name: "Acme", Domain: "acme.test"}}})
	res, err := p.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Candidates {
		for _, ev := range c.Evidence {
			if ev.Source == candidate.SourceSearch || ev.Source == candidate.SourceCertificate {
				t.Errorf("a seed candidate carries remote evidence %q", ev.Source)
			}
			if !strings.HasPrefix(ev.URL, "urn:seed:") {
				t.Errorf("seed evidence URL = %q, want a urn:seed: reference", ev.URL)
			}
		}
	}
}

func TestSeedProviderHonoursCancellation(t *testing.T) {
	p := mustSeed(t, SeedConfig{Seeds: []config.SeedEntry{{Name: "Acme", Domain: "acme.test"}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Search(ctx, Query{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Search = %v, want context.Canceled", err)
	}
}

func mustCert(t *testing.T, srv *httptest.Server, mutate func(*CertificateConfig)) *CertificateProvider {
	t.Helper()
	cfg := CertificateConfig{Endpoint: srv.URL, Fetcher: testFetcher(t, srv)}
	if mutate != nil {
		mutate(&cfg)
	}
	p, err := NewCertificateProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCertificateProviderParsesCrtSh(t *testing.T) {
	var gotPath string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`[
		  {"name_value":"acme.test\nwww.acme.test","not_before":"2026-01-02T00:00:00Z"},
		  {"name_value":"acme.test","not_before":"2025-11-01T00:00:00Z"}
		]`))
	}))
	defer srv.Close()
	p := mustCert(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatalf("Search = %v", err)
	}
	if !strings.HasPrefix(gotPath, "/acme.test") {
		t.Errorf("path = %q, want the escaped domain so a query is never a path traversal", gotPath)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1 after deduplication", len(res.Candidates))
	}
	c := res.Candidates[0]
	if c.Domain != "acme.test" {
		t.Errorf("Domain = %q", c.Domain)
	}
	if len(c.Evidence) != 1 || c.Evidence[0].Method != candidate.MethodCertificate {
		t.Errorf("Evidence = %+v, want one certificate record", c.Evidence)
	}
	// A certificate proves a domain, not a headquarters.
	if c.Country != "" {
		t.Errorf("Country = %q, want empty: a CT log says nothing about location", c.Country)
	}
}

func TestCertificateProviderIgnoresUnrelatedAndWildcardNames(t *testing.T) {
	// A CT log is shared, so a query for one name can surface certificates for
	// entirely different companies. Only the registrable domain asked about may
	// become a candidate, and a wildcard names no company at all.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name_value":"acme.test\n*.acme.test\nwww.acme.test"},
		                 {"name_value":"other-corp.test"},
		                 {"name_value":"github.com"}]`))
	}))
	defer srv.Close()
	p := mustCert(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(res.Candidates))
	}
	if res.Candidates[0].Domain != "acme.test" {
		t.Errorf("Domain = %q, want the queried domain", res.Candidates[0].Domain)
	}
}

func TestCertificateProviderRefusesANonDomainQueryWithoutSpendingARequest(t *testing.T) {
	// A certificate lookup is only meaningful for a domain. Asking for "acme robot
	// arms" would be a wasted paid call, so it is refused before any request.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the provider made a request for a non-domain query")
	}))
	defer srv.Close()
	p := mustCert(t, srv, nil)
	if _, err := p.Search(context.Background(), Query{Text: "acme robot arms", Kind: KindIndustry}); !errors.Is(err, ErrUnsupportedQuery) {
		t.Errorf("Search = %v, want ErrUnsupportedQuery", err)
	}
}

func TestCertificateProviderTruncatesOnAFullPage(t *testing.T) {
	// A full page means there is more to fetch, and the run has to be resumable
	// rather than marking the query answered.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name_value":"a1.test"},{"name_value":"a2.test"}]`))
	}))
	defer srv.Close()
	p := mustCert(t, srv, func(c *CertificateConfig) {
		c.MaxPerQuery = 2
		c.MaxPages = 3
	})
	res, err := p.Search(context.Background(), Query{Text: "a.test", Kind: KindName, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Cursor == "" {
		t.Errorf("Truncated=%v Cursor=%q, want a resumable page", res.Truncated, res.Cursor)
	}
	if _, err := pageFromCursor(res.Cursor); err != nil {
		t.Errorf("the cursor %q is not resumable: %v", res.Cursor, err)
	}
}

func TestCertificateProviderTreatsAnEmptyLogAsAnAnswer(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	p := mustCert(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "nothing.test", Kind: KindName})
	if err != nil {
		t.Fatalf("an empty certificate result is an answer, not a failure: %v", err)
	}
	if len(res.Candidates) != 0 || res.Truncated {
		t.Errorf("res = %+v, want empty and not truncated", res)
	}
}

func TestCertificateProviderClassifiesUpstreamFailures(t *testing.T) {
	// A 404 is the log saying it has never heard of the domain, which is a real
	// answer. A 403 or 429 means the log is refusing us, which is a failure, and
	// the two must not be recorded the same way.
	for _, tc := range []struct {
		status   int
		wantErr  error
		wantRows int
	}{
		{404, nil, 0},
		{429, ErrUpstream, 0},
		{500, ErrUpstream, 0},
		{403, ErrUpstream, 0},
	} {
		status := tc.status
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(`[]`))
		}))
		p := mustCert(t, srv, nil)
		res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
		srv.Close()
		if tc.wantErr == nil {
			if err != nil {
				t.Errorf("%d: Search = %v, want a clean empty answer", status, err)
			}
			if len(res.Candidates) != 0 {
				t.Errorf("%d: got %d candidates", status, len(res.Candidates))
			}
			continue
		}
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%d: Search = %v, want %v", status, err, tc.wantErr)
		}
	}
}

func TestCertificateProviderNeedsAFetcher(t *testing.T) {
	if _, err := NewCertificateProvider(CertificateConfig{}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("NewCertificateProvider = %v, want ErrNotConfigured", err)
	}
}

func mustSearch(t *testing.T, srv *httptest.Server, mutate func(*SearchConfig)) *SearchProvider {
	t.Helper()
	cfg := SearchConfig{Endpoint: srv.URL, APIKey: "test-key", Fetcher: testFetcher(t, srv)}
	if mutate != nil {
		mutate(&cfg)
	}
	p, err := NewSearchProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSearchProviderIsUnreadyWithoutAnEndpointOrKey(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if p := mustSearch(t, srv, func(c *SearchConfig) { c.APIKey = "" }); !errors.Is(p.Ready(), ErrNotConfigured) {
		t.Errorf("Ready without a key = %v, want ErrNotConfigured", p.Ready())
	}
	noEndpoint, err := NewSearchProvider(SearchConfig{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(noEndpoint.Ready(), ErrNotConfigured) {
		t.Errorf("Ready without an endpoint = %v, want ErrNotConfigured", noEndpoint.Ready())
	}
}

func TestSearchProviderRefusesANonHTTPSEndpoint(t *testing.T) {
	// A key sent over plain HTTP is a published credential, so this is refused at
	// run start rather than discovered on the wire.
	p, err := NewSearchProvider(SearchConfig{
		Endpoint: "http://api.example.com/search", APIKey: "k", Fetcher: NewHTTPFetcher(HTTPConfig{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ready(); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Ready = %v, want ErrNotConfigured for a plain-HTTP endpoint", err)
	}
	if _, err := p.Search(context.Background(), Query{Text: "q"}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Search = %v, want it refused before any request", err)
	}
}

func TestSearchProviderSendsTheKeyToItsOwnEndpoint(t *testing.T) {
	var seen *http.Request
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(context.Background())
		w.Write([]byte(`{"results":[{"title":"Acme","url":"https://acme.test/"}]}`))
	}))
	defer srv.Close()
	p := mustSearch(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme robotics", Kind: KindIndustry})
	if err != nil {
		t.Fatalf("Search = %v", err)
	}
	if seen == nil {
		t.Fatal("no request was made")
	}
	if seen.Header.Get("X-Subscription-Token") != "test-key" {
		t.Error("the key was not sent")
	}
	if !strings.Contains(seen.URL.RawQuery, "acme+robotics") {
		t.Errorf("query = %q, want the search text", seen.URL.RawQuery)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Domain != "acme.test" {
		t.Errorf("got %+v", res.Candidates)
	}
}

func TestSearchProviderDropsResultsWithoutAUsableDomain(t *testing.T) {
	// A result that reduces to no company website cannot become a candidate.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[
		  {"title":"No link","url":""},
		  {"title":"Address","url":"https://192.0.2.1/x"},
		  {"title":"Junk","url":"not a url at all"},
		  {"title":"Platform","url":"https://github.com/acme"},
		  {"title":"Good","url":"https://acme.test/about"}
		]}`))
	}))
	defer srv.Close()
	p := mustSearch(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "q", Kind: KindIndustry})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Domain != "acme.test" {
		t.Errorf("got %+v, want only the one usable company domain", res.Candidates)
	}
}

func TestSearchProviderRedactsTheResultURLItKeepsAsEvidence(t *testing.T) {
	// The evidence URL is written to lineage and to the database, so a signed or
	// keyed query string must not survive into it.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"title":"Acme","url":"https://acme.test/p?token=supersecret","snippet":"hi"}]}`))
	}))
	defer srv.Close()
	p := mustSearch(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "q", Kind: KindIndustry})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("got %d candidates", len(res.Candidates))
	}
	if strings.Contains(res.Candidates[0].Evidence[0].URL, "supersecret") {
		t.Errorf("evidence URL leaked a token: %q", res.Candidates[0].Evidence[0].URL)
	}
	// The canonical candidate URL is what later stages follow, and it must be the
	// bare site rather than the tracked path.
	if res.Candidates[0].URL != "https://acme.test/" {
		t.Errorf("URL = %q, want the canonical site root", res.Candidates[0].URL)
	}
}

func TestSearchProviderUsesTheResultTitleAsTheName(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"title":"Acme Robotics GmbH","url":"https://acme.test/"}]}`))
	}))
	defer srv.Close()
	p := mustSearch(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "q", Kind: KindIndustry})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Candidates[0].Name; got != "Acme Robotics GmbH" {
		t.Errorf("Name = %q, want the provider's own title", got)
	}
}

func TestSearchProviderBoundsPagination(t *testing.T) {
	// A vendor whose next-page link never ends must not be able to loop forever,
	// so the provider stops at its own page cap and reports the rest as resumable.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"title":"A","url":"https://a.test/"}],
		                 "next":"https://api.example.com/page/2"}`))
	}))
	defer srv.Close()
	p := mustSearch(t, srv, func(c *SearchConfig) { c.MaxPages = 3 })
	// The per-query limit is left above the page size so the vendor's own next
	// link, not our cap, is what decides there is more to fetch.
	res, err := p.Search(context.Background(), Query{Text: "q", Kind: KindIndustry, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Cursor == "" {
		t.Errorf("Truncated=%v Cursor=%q, want the remainder handed back as resumable", res.Truncated, res.Cursor)
	}
}

func TestSearchProviderReportsThrottling(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p := mustSearch(t, srv, nil)
	if _, err := p.Search(context.Background(), Query{Text: "q", Kind: KindIndustry}); !errors.Is(err, ErrBudgetExhausted) {
		t.Errorf("Search = %v, want ErrBudgetExhausted so throttling does not trip the breaker", err)
	}
}

func TestPageFromCursor(t *testing.T) {
	if got, err := pageFromCursor("page=3"); err != nil || got != 3 {
		t.Errorf("pageFromCursor(page=3) = %d, %v", got, err)
	}
	if _, err := pageFromCursor("garbage"); err == nil {
		t.Error("pageFromCursor accepted a cursor it cannot read; a bad cursor must not silently restart at page one")
	}
}
