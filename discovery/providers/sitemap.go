package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/domain"
)

// SitemapSource retrieves a site's declared sitemaps.
//
// The implementation is the crawler's, wired in by the service layer. Discovery
// declares the contract rather than importing crawler's service wiring, so this
// module stays buildable on its own and the fetching, robots handling and
// politeness stay in the component that already owns them.
type SitemapSource interface {
	// Sitemaps returns the sitemap URLs the site at origin declares, in
	// robots.txt or at the conventional locations. It returns an empty slice when
	// the site simply has none, which is an answer rather than a failure.
	Sitemaps(ctx context.Context, origin string) ([]string, error)
	// Fetch retrieves and parses one sitemap. The implementation is responsible
	// for bounding the response and for obeying robots.txt; a caller must not
	// re-implement either, or the two will drift.
	Fetch(ctx context.Context, sitemapURL string) ([]SitemapEntry, error)
}

// SitemapEntry is one URL in a sitemap.
type SitemapEntry struct {
	// URL is the absolute location.
	URL string
	// LastMod is the source's own modification timestamp, when it states one.
	LastMod time.Time
}

// SitemapProvider reads a known company's sitemap and surfaces the other
// companies it points at.
//
// Why this earns its place: a portfolio page, a partners listing, or a press
// page links to real companies, and that link is a claim of relationship made by
// the site itself rather than by a search index guessing at keywords. Those
// cross-domain links are the only thing here that becomes a candidate; the
// site's own URLs are already known and are dropped.
type SitemapProvider struct {
	name    string
	source  SitemapSource
	maxURLs int
	maxDocs int
	now     func() time.Time
}

// SitemapConfig configures a SitemapProvider.
type SitemapConfig struct {
	// Name overrides the provider name.
	Name string
	// Source retrieves sitemaps. Required.
	Source SitemapSource
	// MaxURLs caps how many sitemap URLs one query reads, so a site with a
	// 50,000-URL sitemap cannot consume a run's whole budget on one domain.
	MaxURLs int
	// MaxDocs caps how many sitemap documents one query opens.
	MaxDocs int
	// Now is the clock.
	Now func() time.Time
}

// NewSitemapProvider returns a sitemap provider.
func NewSitemapProvider(cfg SitemapConfig) (*SitemapProvider, error) {
	if cfg.Source == nil {
		return nil, fmt.Errorf("%w: sitemap provider needs a sitemap source", ErrNotConfigured)
	}
	if cfg.MaxURLs <= 0 {
		cfg.MaxURLs = 500
	}
	if cfg.MaxDocs <= 0 {
		cfg.MaxDocs = 5
	}
	return &SitemapProvider{
		name:    nameOr(cfg.Name, "sitemap"),
		source:  cfg.Source,
		maxURLs: cfg.MaxURLs,
		maxDocs: cfg.MaxDocs,
		now:     orNow(cfg.Now),
	}, nil
}

func (p *SitemapProvider) Name() string { return p.name }

// Kinds is nil: a sitemap lookup is only meaningful for a domain the engine
// already has. Answering "no" to every other kind is what keeps it out of
// industry and geography queries, which it has no opinion about.
func (p *SitemapProvider) Kinds() []Kind { return nil }

func (p *SitemapProvider) Ready() error {
	if p.source == nil {
		return fmt.Errorf("%w: sitemap provider has no sitemap source", ErrNotConfigured)
	}
	return nil
}

// Search reads the queried company's sitemaps and returns the companies it links
// to.
func (p *SitemapProvider) Search(ctx context.Context, q Query) (Result, error) {
	origin, err := domainForQuery(q)
	if err != nil {
		return Result{}, err
	}
	docs, err := p.source.Sitemaps(ctx, "https://"+origin+"/")
	if err != nil {
		return Result{}, fmt.Errorf("%w: sitemaps for %s: %v", ErrUpstream, origin, err)
	}
	if len(docs) == 0 {
		// Most sites have no sitemap. That is an answer, and recording it as a
		// failure would let a run of ordinary sites open the circuit breaker.
		return Result{Detail: "no sitemaps declared for " + origin}, nil
	}

	limit := q.Limit
	if limit <= 0 || limit > p.maxURLs {
		limit = p.maxURLs
	}
	// More documents than the cap is remaining work for a resumed run. It is kept
	// separate from the "enough candidates" flag below, because conflating them
	// stops the loop after the first document.
	moreDocs := len(docs) >= p.maxDocs
	if len(docs) > p.maxDocs {
		docs = docs[:p.maxDocs]
	}

	now := p.now()
	seen := map[string]bool{}
	out := make([]candidate.Candidate, 0, limit)
	read := 0
	truncated := false

	for _, doc := range docs {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		entries, err := p.source.Fetch(ctx, doc)
		if err != nil {
			// One unreadable sitemap does not invalidate the others, and a site
			// with a stale index should still contribute what it has.
			continue
		}
		for _, e := range entries {
			read++
			if read > p.maxURLs {
				truncated = true
				break
			}
			reg, ok := linkedCompany(e.URL, origin)
			if !ok || seen[reg] {
				continue
			}
			seen[reg] = true
			c := candidate.Candidate{
				Domain: reg,
				URL:    "https://" + reg + "/",
				// A link from one company's site to another is a weak signal. It
				// says the two are related, not that the second is a prospect, and
				// the confidence reflects that.
				Confidence: 0.3,
			}
			observed := now
			if !e.LastMod.IsZero() {
				observed = e.LastMod.UTC()
			}
			c.AddEvidence(candidate.Evidence{
				Source:     candidate.SourceSitemap,
				Method:     candidate.MethodLink,
				URL:        doc,
				Query:      q.Text,
				ObservedAt: observed,
				Detail:     "linked from " + origin,
			})
			out = append(out, c)
			if len(out) >= limit {
				truncated = true
				break
			}
		}
		if len(out) >= limit || read > p.maxURLs {
			break
		}
	}
	// A cap that stopped the walk leaves work behind, and a resumed run has to be
	// able to see that there is more rather than treating the query as answered.
	truncated = truncated || moreDocs

	return Result{
		Candidates: out,
		Truncated:  truncated,
		Detail:     fmt.Sprintf("%d sitemaps, %d linked companies for %s", len(docs), len(out), origin),
	}, nil
}

// linkedCompany reports the registrable domain a sitemap URL points at, when it
// is a different company from the one being read.
//
// The site's own URLs are dropped because the engine already has that domain and
// re-reporting it would inflate its source count with a link to itself.
func linkedCompany(rawURL, origin string) (string, bool) {
	reg, err := normalizeDomainFrom(rawURL)
	if err != nil {
		return "", false
	}
	if reg == origin || sameOrSubdomain(reg, origin) {
		return "", false
	}
	if d, derr := domain.FromHost(reg); derr == nil && d.IsPlatform {
		// A link to a directory, a social network, or a CDN is a link to the
		// platform, not to a company.
		return "", false
	}
	return reg, true
}
