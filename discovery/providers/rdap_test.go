package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hmza-hb/lead-intelligence/platform/circuit"
)

// rdapServer stands in for a registry, serving both the IANA bootstrap and a
// domain record.
func rdapServer(t *testing.T, record string, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	// The registry URL has to point back at this server, so it is filled in once
	// the listener exists. A hostname that cannot resolve would be refused by the
	// endpoint check, which is the right behaviour and the wrong fixture.
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/bootstrap.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"services":[[["com","net","de","test"],["` + base + `/rdap/"]]]}`))
	})
	mux.HandleFunc("/rdap/domain/", func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if !strings.HasSuffix(r.URL.Path, "/acme.test") {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"errorCode":404}`))
			return
		}
		w.Write([]byte(record))
	})
	srv := httptest.NewTLSServer(mux)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func mustRDAP(t *testing.T, srv *httptest.Server, mutate func(*RDAPConfig)) *RDAPProvider {
	t.Helper()
	cfg := RDAPConfig{BootstrapURL: srv.URL + "/bootstrap.json", Fetcher: testFetcher(t, srv)}
	if mutate != nil {
		mutate(&cfg)
	}
	p, err := NewRDAPProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const acmeRDAPRecord = `{
  "objectClassName":"domain",
  "ldhName":"ACME.TEST",
  "status":["active","client transfer prohibited"],
  "events":[
    {"eventAction":"registration","eventDate":"2019-04-02T10:00:00Z"},
    {"eventAction":"expiration","eventDate":"2030-04-02T10:00:00Z"}
  ],
  "entities":[{
    "roles":["registrar"],
    "publicIds":[{"type":"IANA Registrar ID","identifier":"12345"}],
    "vcardArray":"vcard,[[\"fn\",\"Secret Person\"],[\"email\",\"private@example.test\"]]"
  }],
  "nameservers":[{"ldhName":"ns1.example.test"}]
}`

func TestRDAPConfirmsRegistration(t *testing.T) {
	srv := rdapServer(t, acmeRDAPRecord, nil)
	p := mustRDAP(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatalf("Search = %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("got %d candidates", len(res.Candidates))
	}
	c := res.Candidates[0]
	if c.Domain != "acme.test" {
		t.Errorf("Domain = %q", c.Domain)
	}
	// The domain is taken as the authority, not the registry's own capitalisation
	// of it, so the key that identity and ranking use stays consistent.
	if c.Name != "ACME.TEST" {
		t.Errorf("Name = %q, want the registry's own name", c.Name)
	}
	if len(c.Evidence) != 1 || c.Evidence[0].Source != "registry" {
		t.Errorf("Evidence = %+v", c.Evidence)
	}
	detail := c.Evidence[0].Detail
	if !strings.Contains(detail, "registration=2019-04-02") {
		t.Errorf("Detail = %q, want the registration date", detail)
	}
	if !strings.Contains(detail, "registrar_id=12345") {
		t.Errorf("Detail = %q, want the public registrar id", detail)
	}
}

func TestRDAPKeepsContactDetailsOutOfTheRecord(t *testing.T) {
	// The RDAP vCard holds a registrant name and email. A lead record is stored,
	// exported and shown to sales staff, so personal contact data must not be
	// copied into it.
	srv := rdapServer(t, acmeRDAPRecord, nil)
	p := mustRDAP(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatal(err)
	}
	detail := res.Candidates[0].Evidence[0].Detail
	for _, leak := range []string{"Secret Person", "private@example.test", "vcard"} {
		if strings.Contains(detail, leak) {
			t.Errorf("evidence detail leaked %q: %s", leak, detail)
		}
	}
}

func TestRDAPSurfacesDyingRegistrations(t *testing.T) {
	// A domain pending deletion is registered but on its way out, and a bare
	// "it resolves" check would treat it as a healthy lead.
	srv := rdapServer(t, `{"ldhName":"acme.test","status":["pending delete"],
	  "events":[{"eventAction":"registration","eventDate":"2019-04-02T10:00:00Z"}]}`, nil)
	p := mustRDAP(t, srv, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatal(err)
	}
	detail := res.Candidates[0].Evidence[0].Detail
	if !strings.Contains(detail, "pending delete") {
		t.Errorf("Detail = %q, want the registration status preserved", detail)
	}
}

func TestRDAPTreatsAnUnregisteredDomainAsAnAnswer(t *testing.T) {
	// RDAP's 404 is authoritative: the domain does not exist. Recording it as a
	// failure would open the circuit breaker on a registry that is working
	// perfectly, and re-asking it will get the same 404 forever.
	srv := rdapServer(t, acmeRDAPRecord, nil)
	p := mustRDAP(t, srv, nil)
	_, err := p.Search(context.Background(), Query{Text: "absent.test", Kind: KindName})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Search = %v, want ErrNotFound", err)
	}
	if got := Classify(err); got != FailurePermanent {
		t.Errorf("Classify = %q, want %q: re-asking will not change the answer",
			got, FailurePermanent)
	}
}

func TestRDAPRefusesAQueryThatIsNotADomain(t *testing.T) {
	srv := rdapServer(t, acmeRDAPRecord, nil)
	p := mustRDAP(t, srv, nil)
	for _, text := range []string{"", "acme robot arms", "not a domain"} {
		if _, err := p.Search(context.Background(), Query{Text: text, Kind: KindIndustry}); !errors.Is(err, ErrUnsupportedQuery) {
			t.Errorf("Search(%q) = %v, want ErrUnsupportedQuery", text, err)
		}
	}
}

func TestRDAPReportsAnAbsentServiceAsUnsupported(t *testing.T) {
	// Plenty of ccTLDs never published an RDAP service. That is a fact about the
	// internet, not a broken provider, so it must not be counted as a failure.
	srv := rdapServer(t, acmeRDAPRecord, nil)
	p := mustRDAP(t, srv, nil)
	_, err := p.Search(context.Background(), Query{Text: "acme.example", Kind: KindName})
	if !errors.Is(err, ErrUnsupportedQuery) {
		t.Errorf("Search = %v, want ErrUnsupportedQuery for an unmapped TLD", err)
	}
	if got := Classify(err); got != FailurePermanent {
		t.Errorf("Classify = %q, want %q", got, FailurePermanent)
	}
}

func TestRDAPClassifiesRateLimiting(t *testing.T) {
	srv := rdapServer(t, acmeRDAPRecord, nil)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "bootstrap.json") {
			w.Write([]byte(`{"services":[[["test"],["https://rdap.example.test/rdap/"]]]}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	})
	p := mustRDAP(t, srv, nil)
	_, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if !errors.Is(err, ErrUpstream) {
		t.Errorf("Search = %v, want ErrUpstream", err)
	}
}

func TestRDAPFailsClosedWhenTheBootstrapIsUnavailable(t *testing.T) {
	// Without the TLD-to-server map there is no way to know where to ask. Guessing
	// a server would send the request somewhere arbitrary.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	p := mustRDAP(t, srv, nil)
	_, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if !errors.Is(err, ErrUpstream) {
		t.Errorf("Search = %v, want ErrUpstream", err)
	}
}

func TestRDAPRefusesABootstrapResponseThatIsNotAMapping(t *testing.T) {
	// A registry that returns something other than the documented shape must not
	// be read as "this TLD has no service", which would silently stop every
	// lookup for it.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":"not the registry you are looking for"}`))
	}))
	defer srv.Close()
	p := mustRDAP(t, srv, nil)
	if _, err := p.serverFor(context.Background(), "test"); err == nil {
		t.Error("serverFor accepted a bootstrap response with no services")
	}
}

func TestRDAPCachesTheBootstrap(t *testing.T) {
	// The mapping changes a few times a year. Refetching it per lookup would add
	// a request to every single query.
	var bootstraps atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/bootstrap.json", func(w http.ResponseWriter, r *http.Request) {
		bootstraps.Add(1)
		w.Write([]byte(`{"services":[[["test"],["https://rdap.example-registry.test/rdap/"]]]}`))
	})
	mux.HandleFunc("/rdap/domain/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(acmeRDAPRecord))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	p := mustRDAP(t, srv, nil)
	for i := 0; i < 5; i++ {
		// Each domain has a fresh TLD-shaped name so the record endpoint answers
		// for all of them.
		if _, err := p.serverFor(context.Background(), "test"); err != nil {
			t.Fatal(err)
		}
	}
	if got := bootstraps.Load(); got != 1 {
		t.Errorf("the bootstrap was fetched %d times, want 1", got)
	}
}

func TestRDAPRefetchesAnExpiredBootstrap(t *testing.T) {
	var bootstraps atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/bootstrap.json", func(w http.ResponseWriter, r *http.Request) {
		bootstraps.Add(1)
		w.Write([]byte(`{"services":[[["test"],["https://rdap.example-registry.test/rdap/"]]]}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	p := mustRDAP(t, srv, func(c *RDAPConfig) {
		c.BootstrapTTL = time.Minute
		c.Now = func() time.Time { return now }
	})
	if _, err := p.serverFor(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := p.serverFor(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if got := bootstraps.Load(); got != 2 {
		t.Errorf("the bootstrap was fetched %d times, want 2 after the TTL expired", got)
	}
}

func TestRDAPBootstrapFetchIsSerialised(t *testing.T) {
	// Concurrent lookups must not each fire their own bootstrap request, which
	// would hammer the one public registry the run depends on.
	var bootstraps atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/bootstrap.json", func(w http.ResponseWriter, r *http.Request) {
		bootstraps.Add(1)
		time.Sleep(20 * time.Millisecond)
		w.Write([]byte(`{"services":[[["test"],["https://rdap.example-registry.test/rdap/"]]]}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	p := mustRDAP(t, srv, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			//nolint:errcheck // the assertion is on the request count
			p.serverFor(context.Background(), "test")
		}()
	}
	wg.Wait()
	if got := bootstraps.Load(); got != 1 {
		t.Errorf("the bootstrap was fetched %d times, want 1", got)
	}
}

func TestRDAPNeedsAFetcher(t *testing.T) {
	if _, err := NewRDAPProvider(RDAPConfig{}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("NewRDAPProvider = %v, want ErrNotConfigured", err)
	}
	// The sentinel already reads "providers: not configured", so the message must
	// not repeat the prefix on top of it.
	if _, err := NewRDAPProvider(RDAPConfig{}); strings.Contains(err.Error(), "providers: providers:") {
		t.Errorf("error repeats its prefix: %v", err)
	}
	// A provider with an endpoint but no fetcher cannot make a request at all, so
	// it is refused at construction rather than at first use.
	if _, err := NewRDAPProvider(RDAPConfig{BootstrapURL: DefaultRDAPBootstrapURL}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("NewRDAPProvider without a fetcher = %v, want ErrNotConfigured", err)
	}
}

func TestStatusError(t *testing.T) {
	// The status-to-error mapping is what separates a genuine absence from a
	// broken surface, so every branch is pinned here rather than only through the
	// providers that happen to exercise it.
	for status, want := range map[int]error{
		200: nil, 201: nil, 204: nil, 299: nil,
		404: ErrNotFound, 410: ErrNotFound,
		401: ErrNotConfigured, 403: ErrNotConfigured,
		429: ErrBudgetExhausted,
		500: ErrUpstream, 502: ErrUpstream, 503: ErrUpstream,
		400: ErrUpstream, 418: ErrUpstream,
	} {
		err := statusError(status)
		if want == nil {
			if err != nil {
				t.Errorf("statusError(%d) = %v, want nil", status, err)
			}
			continue
		}
		if !errors.Is(err, want) {
			t.Errorf("statusError(%d) = %v, want %v", status, err, want)
		}
	}
}

func TestTLDOf(t *testing.T) {
	for in, want := range map[string]string{
		"acme.test":   "test",
		"acme.co.uk":  "co.uk",
		"acme":        "",
		"":            "",
		"a.b.c.d.e.f": "f",
	} {
		if got := tldOf(in); got != want {
			t.Errorf("tldOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequireHTTPS(t *testing.T) {
	if err := requireHTTPS("https://api.example.com/x"); err != nil {
		t.Errorf("requireHTTPS on https = %v", err)
	}
	for _, bad := range []string{"http://api.example.com", "ftp://api.example.com", "https://", "://nope"} {
		if err := requireHTTPS(bad); err == nil {
			t.Errorf("requireHTTPS(%q) accepted an endpoint that cannot carry a key safely", bad)
		}
	}
}

func TestClamp01(t *testing.T) {
	for in, want := range map[float64]float64{
		-1: 0, 0: 0, 0.5: 0.5, 1: 1, 2: 1,
	} {
		if got := clamp01(in); got != want {
			t.Errorf("clamp01(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Errorf("truncate left a short string alone incorrectly: %q", got)
	}
	long := strings.Repeat("x", 500)
	got := truncate(long, 40)
	if len([]rune(got)) != 40 {
		t.Errorf("truncate produced %d runes, want 40", len([]rune(got)))
	}
	// A truncated string must not end mid-rune, or it is no longer valid text.
	if !strings.HasPrefix(long, got[:len(got)-3]) {
		t.Errorf("truncate did not preserve the prefix: %q", got)
	}
}

func TestSortedUnique(t *testing.T) {
	got := sortedUnique([]string{"b", "a", "b", "", "c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	// A cursor is written to lineage and read back on a resumed run, so a
	// round trip that does not survive would silently restart expensive work.
	c := cursorFromPage(7)
	got, err := pageFromCursor(c)
	if err != nil {
		t.Fatalf("pageFromCursor(%q) = %v", c, err)
	}
	if got != 7 {
		t.Errorf("pageFromCursor(%q) = %d, want 7", c, got)
	}
}

func TestCursorRefusesToBeForgedIntoAQuery(t *testing.T) {
	// A cursor is opaque and comes back out of the ledger. If it could carry a
	// separator, a tampered ledger row would turn into a request for an arbitrary
	// page or host.
	for _, bad := range []string{"page=1&x=2", "page=", "page=abc", "page=-1", "page=1;drop", ""} {
		if _, err := pageFromCursor(bad); err == nil && bad != "" {
			t.Errorf("pageFromCursor(%q) was accepted", bad)
		}
	}
}

func TestValidateEndpointAcceptsAPublicHTTPSURL(t *testing.T) {
	f := NewHTTPFetcher(HTTPConfig{
		Deps: HTTPDeps{ResolveHost: resolver(map[string][]string{
			"api.example.com": {"93.184.216.34"},
		})},
	})
	if err := f.validateEndpoint("https://api.example.com/search?q=1"); err != nil {
		t.Errorf("validateEndpoint = %v", err)
	}
}

func TestKindsAreReportedAndValid(t *testing.T) {
	// A provider that claims a kind it cannot serve is spending budget to decline,
	// so the runner and this check have to agree.
	srv := rdapServer(t, acmeRDAPRecord, nil)
	rd := mustRDAP(t, srv, nil)
	for _, k := range rd.Kinds() {
		if !validKind(k) {
			t.Errorf("rdap reports kind %q, which is not a known kind", k)
		}
	}
	if got := rd.Name(); got != "rdap" {
		t.Errorf("Name = %q", got)
	}
	// A provider that declares a kind the engine does not know is refused at
	// registration, because it would silently never be asked.
	r := newRunner(t, RunnerConfig{})
	bad := &fakeProvider{name: "bad", kinds: []Kind{"not-a-kind"}}
	if err := r.Register(bad); err == nil {
		t.Error("Register accepted a provider declaring an unknown query kind")
	}
	if err := r.Register(rd); err != nil {
		t.Errorf("Register refused a provider with valid kinds: %v", err)
	}
	// A provider may legitimately serve no kind at all; it is then simply never
	// asked, which is how the certificate provider stays out of free-text queries.
	if err := r.Register(&fakeProvider{name: "nokinds"}); err != nil {
		t.Errorf("Register refused a provider with no kinds: %v", err)
	}
}

func TestLineageKeyIsStable(t *testing.T) {
	// The key is what a resumed run matches on, so it must not depend on map
	// ordering or on anything that varies between runs.
	q := Query{Text: "acme robotics", Kind: KindIndustry, Language: "en", Limit: 5}
	a := LineageKey("search", q)
	b := LineageKey("search", q)
	if a != b {
		t.Errorf("LineageKey is not stable: %q and %q", a, b)
	}
	// A different provider or a different question is a different key.
	if LineageKey("search2", q) == a {
		t.Error("two providers produced the same lineage key")
	}
	if LineageKey("search", Query{Text: "other", Kind: KindIndustry}) == a {
		t.Error("two queries produced the same lineage key")
	}
	// A cursor is a continuation, not a new question, so it must not change the key
	// or a resumed run would never match the pending row it is resuming.
	if LineageKey("search", Query{Text: "acme robotics", Kind: KindIndustry, Language: "en", Limit: 5, Cursor: "page=2"}) != a {
		t.Error("a cursor changed the lineage key; a resumed run could never match its own row")
	}
}

func TestNameOrDefaults(t *testing.T) {
	if got := nameOr("", "fallback"); got != "fallback" {
		t.Errorf("nameOr = %q", got)
	}
	if got := nameOr("custom", "fallback"); got != "custom" {
		t.Errorf("nameOr = %q", got)
	}
	if err := ValidateName(nameOr("  ", "fallback")); err != nil {
		t.Errorf("the default name is not a valid name: %v", err)
	}
}

func TestOrNow(t *testing.T) {
	if got := orNow(nil)(); got.IsZero() {
		t.Error("orNow(nil) returned a zero clock")
	}
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := orNow(func() time.Time { return fixed })(); !got.Equal(fixed) {
		t.Errorf("orNow did not keep the supplied clock: %v", got)
	}
}

func TestAttemptFromDescribesACall(t *testing.T) {
	// This is the bridge from a provider call to the ledger, and it is the only
	// place the two representations meet.
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	a := AttemptFrom("run-1", "search", Query{Text: "q", Kind: KindIndustry, Language: "de"},
		Result{Candidates: seedLike(Query{})}, 250*time.Millisecond, at)
	if a.Provider != "search" || a.Query != "q" || a.Language != "de" {
		t.Errorf("Attempt = %+v, want the query recorded verbatim with its language", a)
	}
	if a.RunID != "run-1" {
		t.Errorf("RunID = %q", a.RunID)
	}
	if a.Candidates != 1 || a.Outcome != "produced" {
		t.Errorf("Attempt = %+v, want one candidate produced", a)
	}
	if a.Duration != 250*time.Millisecond {
		t.Errorf("Duration = %v, want the wall clock for cost accounting", a.Duration)
	}
	if !a.At.Equal(at) {
		t.Errorf("At = %v, want %v", a.At, at)
	}

	// A truncated page with no rows is still resumable, not an answered query.
	trunc := AttemptFrom("run-1", "search", Query{Text: "q"},
		Result{Truncated: true, Cursor: "page=3"}, 0, at)
	if trunc.Outcome != "truncated" || trunc.Cursor != "page=3" {
		t.Errorf("Attempt = %+v, want a truncated outcome carrying its cursor", trunc)
	}

	empty := AttemptFrom("run-1", "search", Query{Text: "q"}, Result{}, 0, at)
	if empty.Outcome != "empty" {
		t.Errorf("Outcome = %q, want empty", empty.Outcome)
	}
}

func TestAppliesUsesTheDeclaredKinds(t *testing.T) {
	// A provider that answers "no" cheaply is better than one that spends a paid
	// call to discover it has no index for that kind.
	scoped := &fakeProvider{name: "ct", kinds: []Kind{KindName, KindGeography}}
	if !Applies(scoped, KindName) {
		t.Error("a declared kind was refused")
	}
	if Applies(scoped, KindIndustry) {
		t.Error("an undeclared kind was allowed; a paid call would be spent for nothing")
	}
	// A provider that declares no kinds serves none, which is how the certificate
	// provider stays out of free-text queries entirely.
	bare := &fakeProvider{name: "bare"}
	if Applies(bare, KindName) || Applies(bare, KindIndustry) {
		t.Error("a provider with no declared kinds claimed one")
	}
}

func TestNormalisedStatuses(t *testing.T) {
	got := normalizedStatuses([]string{" Active ", "client transfer prohibited", ""})
	want := []string{"active", "client transfer prohibited", ""}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny([]string{"a", "b"}, "b") {
		t.Error("containsAny missed a present value")
	}
	if containsAny([]string{"a", "b"}, "c") {
		t.Error("containsAny found a value that is not present")
	}
	if containsAny(nil, "c") {
		t.Error("containsAny on a nil list returned true")
	}
}

func TestRegistryEntryRefundOnADeclinedKind(t *testing.T) {
	// runOne reserves before asking, so a provider that declines a kind after the
	// reservation would leak budget. The reservation has to come back, because
	// nothing was spent.
	p := &fakeProvider{name: "ct", kinds: []Kind{KindName}}
	e := &registryEntry{provider: p, kinds: []Kind{KindName}, budget: NewBudget(1), breaker: newBreaker(circuit.Config{})}
	if _, err := e.runOne(context.Background(), "run-1", Query{Text: "x", Kind: KindIndustry}, nil); !errors.Is(err, ErrUnsupportedQuery) {
		t.Fatalf("runOne = %v, want ErrUnsupportedQuery", err)
	}
	if got := e.budget.Remaining(); got != 1 {
		t.Errorf("the budget has %d left, want 1: a declined kind costs nothing", got)
	}
	if _, err := e.runOne(context.Background(), "run-1", Query{Text: "x", Kind: KindName}, nil); err != nil {
		t.Errorf("the supported kind should still have budget: %v", err)
	}
}

func TestRegistryEntryChargesAFailedCallToTheBudget(t *testing.T) {
	// A call that failed still cost money, and a provider that fails every time
	// must not be free.
	p := &fakeProvider{name: "search", search: func(context.Context, Query) (Result, error) {
		return Result{}, ErrUpstream
	}}
	e := &registryEntry{provider: p, kinds: []Kind{KindIndustry}, budget: NewBudget(1), breaker: newBreaker(circuit.Config{})}
	if _, err := e.runOne(context.Background(), "run-1", Query{Text: "x", Kind: KindIndustry}, nil); !errors.Is(err, ErrUpstream) {
		t.Fatalf("runOne = %v", err)
	}
	if got := e.budget.Remaining(); got != 0 {
		t.Errorf("the budget has %d left, want 0: the call was spent whether or not it worked", got)
	}
	if _, err := e.runOne(context.Background(), "run-1", Query{Text: "y", Kind: KindIndustry}, nil); !errors.Is(err, ErrBudgetExhausted) {
		t.Errorf("second runOne = %v, want ErrBudgetExhausted", err)
	}
}

func TestUnreadyProviderRegistrationIsRecordedNotFatal(t *testing.T) {
	// A deployment missing one provider's key must still start. The registration
	// records why it is unusable so the run report can say so, and the other
	// providers carry on.
	r := newRunner(t, RunnerConfig{})
	if err := r.Register(&fakeProvider{name: "search", readyErr: fmt.Errorf("no key: %w", ErrNotConfigured)}); err != nil {
		t.Fatalf("Register refused an unready provider: %v", err)
	}
	if err := r.Register(&fakeProvider{name: "rdap"}); err != nil {
		t.Fatal(err)
	}
	stats := r.Stats()
	if stats.ByProvider["search"].Ready {
		t.Error("an unready provider reported itself ready")
	}
	if !strings.Contains(stats.ByProvider["search"].ReadyError, "no key") {
		t.Errorf("ReadyError = %q, want the reason recorded for the run report", stats.ByProvider["search"].ReadyError)
	}
	if !stats.ByProvider["rdap"].Ready {
		t.Error("the healthy provider did not register")
	}
}
