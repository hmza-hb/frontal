package providers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// fakeSitemap stands in for the crawler's sitemap retrieval. Discovery declares
// the contract and never imports the implementation, so the provider is tested
// against a substitute.
type fakeSitemap struct {
	docs    map[string][]SitemapEntry
	listed  []string
	listErr error
	fetched []string
	failOn  map[string]error
}

func (f *fakeSitemap) Sitemaps(_ context.Context, origin string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listed, nil
}

func (f *fakeSitemap) Fetch(_ context.Context, u string) ([]SitemapEntry, error) {
	f.fetched = append(f.fetched, u)
	if err, ok := f.failOn[u]; ok {
		return nil, err
	}
	return f.docs[u], nil
}

func mustSitemap(t *testing.T, src SitemapSource, mutate func(*SitemapConfig)) *SitemapProvider {
	t.Helper()
	cfg := SitemapConfig{Source: src}
	if mutate != nil {
		mutate(&cfg)
	}
	p, err := NewSitemapProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSitemapProviderSurfacesLinkedCompanies(t *testing.T) {
	src := &fakeSitemap{
		listed: []string{"https://acme.test/sitemap.xml"},
		docs: map[string][]SitemapEntry{
			"https://acme.test/sitemap.xml": {
				{URL: "https://acme.test/about"},
				{URL: "https://partner.test/"},
				{URL: "https://www.partner.test/team"},
				{URL: "https://vendor.test/pricing"},
			},
		},
	}
	p := mustSitemap(t, src, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatalf("Search = %v", err)
	}
	got := map[string]bool{}
	for _, c := range res.Candidates {
		got[c.Domain] = true
	}
	if len(got) != 2 || !got["partner.test"] || !got["vendor.test"] {
		t.Errorf("got %v, want the two linked companies", got)
	}
	// The site's own URLs are dropped: the engine already has that domain, and
	// counting a link to itself as a source would inflate its corroboration.
	if got["acme.test"] {
		t.Error("a link back to the site itself became a candidate")
	}
	for _, c := range res.Candidates {
		if len(c.Evidence) != 1 || c.Evidence[0].Source != "sitemap" {
			t.Errorf("Evidence = %+v", c.Evidence)
		}
		if c.Confidence > 0.5 {
			t.Errorf("Confidence = %v: a link from another site is a weak signal", c.Confidence)
		}
	}
}

func TestSitemapProviderDropsPlatformAndAddressLinks(t *testing.T) {
	src := &fakeSitemap{
		listed: []string{"https://acme.test/sitemap.xml"},
		docs: map[string][]SitemapEntry{"https://acme.test/sitemap.xml": {
			{URL: "https://github.com/someone"},
			{URL: "https://linkedin.com/company/x"},
			{URL: "https://192.0.2.9/x"},
			{URL: "not a url"},
			{URL: "mailto:hi@partner.test"},
			{URL: "https://real.test/"},
		}},
	}
	p := mustSitemap(t, src, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Domain != "real.test" {
		var domains []string
		for _, c := range res.Candidates {
			domains = append(domains, c.Domain)
		}
		t.Errorf("got %v, want only the one real company", domains)
	}
}

func TestSitemapProviderTreatsNoSitemapAsAnAnswer(t *testing.T) {
	// Most sites have no sitemap at all. Counting that as a failure would let a
	// run of ordinary sites open the circuit breaker on a crawler that is working.
	src := &fakeSitemap{}
	p := mustSitemap(t, src, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatalf("Search = %v, want a clean answer", err)
	}
	if len(res.Candidates) != 0 || res.Truncated {
		t.Errorf("res = %+v, want empty and not truncated", res)
	}
	if got := Classify(err); got != "" {
		t.Errorf("Classify = %q, want no failure", got)
	}
}

func TestSitemapProviderSurvivesOneUnreadableSitemap(t *testing.T) {
	// A site with a stale sitemap index is common. Losing the whole query because
	// one document 404s throws away the links in the documents that did load.
	src := &fakeSitemap{
		listed: []string{"https://acme.test/broken.xml", "https://acme.test/good.xml"},
		docs: map[string][]SitemapEntry{
			"https://acme.test/good.xml": {{URL: "https://partner.test/"}},
		},
		failOn: map[string]error{"https://acme.test/broken.xml": errors.New("404")},
	}
	p := mustSitemap(t, src, nil)
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatalf("Search = %v, want the readable sitemap to still count", err)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Domain != "partner.test" {
		t.Errorf("got %+v, want the link from the sitemap that loaded", res.Candidates)
	}
}

func TestSitemapProviderReportsAListingFailure(t *testing.T) {
	src := &fakeSitemap{listErr: errors.New("connection refused")}
	p := mustSitemap(t, src, nil)
	if _, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName}); !errors.Is(err, ErrUpstream) {
		t.Errorf("Search = %v, want ErrUpstream", err)
	}
}

func TestSitemapProviderBoundsOneQuery(t *testing.T) {
	// A site with a 50,000-URL sitemap must not be able to consume a run's whole
	// budget on one domain.
	var many []SitemapEntry
	for i := 0; i < 5000; i++ {
		many = append(many, SitemapEntry{URL: fmt.Sprintf("https://company%d.test/", i)})
	}
	src := &fakeSitemap{
		listed: []string{"https://acme.test/sitemap.xml"},
		docs:   map[string][]SitemapEntry{"https://acme.test/sitemap.xml": many},
	}
	p := mustSitemap(t, src, func(c *SitemapConfig) { c.MaxURLs = 50 })
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) > 50 {
		t.Errorf("got %d candidates, want at most 50", len(res.Candidates))
	}
	if !res.Truncated {
		t.Error("a bounded query should be reported as truncated so a resumed run continues it")
	}
}

func TestSitemapProviderBoundsDocumentCount(t *testing.T) {
	docs := map[string][]SitemapEntry{}
	var listed []string
	for i := 0; i < 10; i++ {
		u := fmt.Sprintf("https://acme.test/sitemap-%d.xml", i)
		listed = append(listed, u)
		docs[u] = []SitemapEntry{{URL: fmt.Sprintf("https://co%d.test/", i)}}
	}
	src := &fakeSitemap{listed: listed, docs: docs}
	p := mustSitemap(t, src, func(c *SitemapConfig) { c.MaxDocs = 3 })
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatal(err)
	}
	if len(src.fetched) > 3 {
		t.Errorf("opened %d sitemaps, want at most 3", len(src.fetched))
	}
	if len(res.Candidates) != 3 {
		t.Errorf("got %d candidates, want 3", len(res.Candidates))
	}
	if !res.Truncated {
		t.Error("Truncated = false, want the unread documents reported as remaining work")
	}
}

func TestSitemapProviderUsesTheSourceLastMod(t *testing.T) {
	// The source's own timestamp is better evidence than the clock, because it
	// says when the site itself last touched the page.
	mod := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	src := &fakeSitemap{
		listed: []string{"https://acme.test/sitemap.xml"},
		docs: map[string][]SitemapEntry{
			"https://acme.test/sitemap.xml": {
				{URL: "https://old.test/", LastMod: mod},
				{URL: "https://fresh.test/"},
			},
		},
	}
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	p := mustSitemap(t, src, func(c *SitemapConfig) { c.Now = func() time.Time { return now } })
	res, err := p.Search(context.Background(), Query{Text: "acme.test", Kind: KindName})
	if err != nil {
		t.Fatal(err)
	}
	byDomain := map[string]time.Time{}
	for _, c := range res.Candidates {
		byDomain[c.Domain] = c.Evidence[0].ObservedAt
	}
	if !byDomain["old.test"].Equal(mod) {
		t.Errorf("lastmod domain observed at %v, want the source's own %v", byDomain["old.test"], mod)
	}
	if !byDomain["fresh.test"].Equal(now) {
		t.Errorf("a domain with no lastmod observed at %v, want the clock %v", byDomain["fresh.test"], now)
	}
}

func TestSitemapProviderRefusesANonDomainQuery(t *testing.T) {
	src := &fakeSitemap{}
	p := mustSitemap(t, src, nil)
	for _, text := range []string{"", "acme robot arms", "some company"} {
		if _, err := p.Search(context.Background(), Query{Text: text, Kind: KindIndustry}); !errors.Is(err, ErrUnsupportedQuery) {
			t.Errorf("Search(%q) = %v, want ErrUnsupportedQuery", text, err)
		}
	}
	if len(src.listed) != 0 && src.fetched != nil {
		t.Error("a refused query still reached the source")
	}
}

func TestSitemapProviderDeclinesEveryQueryKind(t *testing.T) {
	// A sitemap lookup is only meaningful for a domain the engine already has, so
	// the provider must not be handed an industry or geography query at all.
	p := mustSitemap(t, &fakeSitemap{}, nil)
	for _, k := range []Kind{KindIndustry, KindTechnology, KindGeography, KindDirectory, KindCompetitor} {
		if Applies(p, k) {
			t.Errorf("Applies(%q) = true, want false", k)
		}
	}
	// And a run must not reach the source for one of those.
	r := newRunner(t, RunnerConfig{})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	results := r.Run(context.Background(), []Call{{Provider: "sitemap", Query: Query{Text: "warehouse automation", Kind: KindIndustry}}})
	if !errors.Is(results[0].Err, ErrUnsupportedQuery) {
		t.Errorf("Run = %v, want ErrUnsupportedQuery", results[0].Err)
	}
}

func TestSitemapProviderNeedsASource(t *testing.T) {
	if _, err := NewSitemapProvider(SitemapConfig{}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("NewSitemapProvider = %v, want ErrNotConfigured", err)
	}
	p := mustSitemap(t, &fakeSitemap{}, nil)
	if err := p.Ready(); err != nil {
		t.Errorf("Ready = %v", err)
	}
	if got := p.Name(); got != "sitemap" {
		t.Errorf("Name = %q", got)
	}
}

func TestSitemapProviderHonoursCancellation(t *testing.T) {
	src := &fakeSitemap{
		listed: []string{"https://acme.test/sitemap.xml"},
		docs: map[string][]SitemapEntry{
			"https://acme.test/sitemap.xml": {{URL: "https://partner.test/"}},
		},
	}
	p := mustSitemap(t, src, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Search(ctx, Query{Text: "acme.test", Kind: KindName}); !errors.Is(err, context.Canceled) {
		t.Errorf("Search = %v, want context.Canceled", err)
	}
}

func TestSitemapSourceContractIsSatisfiedByTheCrawlerAdapter(t *testing.T) {
	// The interface is the seam between this module and the crawler. If its shape
	// drifts, the wiring in the service layer stops compiling, and this is the
	// cheapest place to notice.
	var _ SitemapSource = (*fakeSitemap)(nil)
	var _ Provider = (*SitemapProvider)(nil)
}
