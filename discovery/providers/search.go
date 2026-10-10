package providers

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/domain"
)

// SearchProvider talks to an operator-supplied web search API.
//
// This adapter is deliberately generic. Every commercial search API has its own
// request shape, its own response schema, and its own pagination model, and
// hard-coding one of them would make every other vendor unusable without a code
// change. Instead the deployment configures a request template and a small
// response mapping, so adding a vendor is configuration rather than a release.
//
// # Missing credentials degrade coverage
//
// With no API key the provider reports itself unready and the runner skips it.
// That is the intended behaviour: a deployment that has not paid for search
// should produce candidates from the free sources and record that search was
// never asked, rather than failing the run.
//
// # What it will not do
//
// It will not scrape a search engine's HTML. Doing so breaks the provider's
// terms, gets the deployment blocked, and produces worse results than the API it
// is avoiding. A surface that needs no credentials here means an official API,
// never a scraped results page.
type SearchProvider struct {
	name        string
	endpoint    string
	apiKey      string
	apiKeyEnv   string
	fetcher     *HTTPFetcher
	maxPages    int
	maxPerQuery int
	now         func() time.Time
}

// SearchConfig configures a SearchProvider.
type SearchConfig struct {
	// Name overrides the provider name, so two deployments can register two
	// search vendors without colliding.
	Name string
	// Endpoint is the search API URL.
	Endpoint string
	// APIKey is the credential. It is never written to lineage, logs, or any
	// error message.
	APIKey string
	// Fetcher performs requests.
	Fetcher *HTTPFetcher
	// MaxPages caps pagination.
	MaxPages int
	// MaxPerQuery caps candidates per query.
	MaxPerQuery int
	// Now is the clock.
	Now func() time.Time
}

// NewSearchProvider returns a search provider.
func NewSearchProvider(cfg SearchConfig) (*SearchProvider, error) {
	if cfg.Endpoint == "" {
		// No endpoint is a deployment state, not a programming error, so this
		// returns a provider that reports itself unready.
		return &SearchProvider{name: nameOr(cfg.Name, "search"), now: orNow(cfg.Now)}, nil
	}
	if _, err := url.Parse(cfg.Endpoint); err != nil {
		return nil, fmt.Errorf("providers: %w: bad search endpoint: %v", ErrNotConfigured, err)
	}
	if cfg.Fetcher == nil {
		return nil, fmt.Errorf("providers: %w: search provider needs an HTTP fetcher", ErrNotConfigured)
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = 2
	}
	if cfg.MaxPerQuery <= 0 {
		cfg.MaxPerQuery = 25
	}
	return &SearchProvider{
		name:        nameOr(cfg.Name, "search"),
		endpoint:    cfg.Endpoint,
		apiKey:      cfg.APIKey,
		fetcher:     cfg.Fetcher,
		maxPages:    cfg.MaxPages,
		maxPerQuery: cfg.MaxPerQuery,
		now:         orNow(cfg.Now),
	}, nil
}

func nameOr(name, fallback string) string {
	// A whitespace-only name is not a name. Falling through to the default keeps
	// registration from failing on what looks like an unset field in YAML.
	if strings.TrimSpace(name) == "" {
		return fallback
	}
	return name
}

func orNow(fn func() time.Time) func() time.Time {
	if fn == nil {
		return func() time.Time { return time.Now().UTC() }
	}
	return fn
}

func (p *SearchProvider) Name() string { return p.name }

func (p *SearchProvider) Kinds() []Kind {
	return []Kind{KindIndustry, KindTechnology, KindGeography, KindDirectory, KindName, KindCompetitor}
}

func (p *SearchProvider) Ready() error {
	if p.endpoint == "" {
		return fmt.Errorf("%w: no search endpoint configured", ErrNotConfigured)
	}
	if p.apiKey == "" {
		return fmt.Errorf("%w: %s has no API key", ErrNotConfigured, p.name)
	}
	if p.fetcher == nil {
		return fmt.Errorf("%w: %s has no HTTP fetcher", ErrNotConfigured, p.name)
	}
	// An API key sent over plain HTTP is a published credential. This is checked
	// here rather than at construction so a deployment that supplies the endpoint
	// from a secret store is still refused, and refused loudly, at run start.
	if err := requireHTTPS(p.endpoint); err != nil {
		return fmt.Errorf("%w: %s endpoint: %v", ErrNotConfigured, p.name, err)
	}
	return nil
}

// searchResponse is a tolerant decode of a typical search API response.
//
// Vendors disagree on almost every key, so every field is optional and the
// parser looks at all of them. A response that yields nothing is reported as an
// empty result rather than a decode failure, because "the API changed its
// schema" and "there were no matches" look identical to a strict parser and must
// not both be recorded as coverage.
type searchResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
		Desc    string `json:"description"`
	} `json:"results"`
	Items []struct {
		Title      string `json:"title"`
		URL        string `json:"url"`
		Link       string `json:"link"`
		Snippet    string `json:"snippet"`
		Descr      string `json:"description"`
		Registered string `json:"registered_domain"`
	} `json:"items"`
	Organic []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	} `json:"organic_results"`
	// Some APIs return the whole result set under a versioned key.
	WebPages struct {
		Value []struct {
			Title string `json:"name"`
			URL   string `json:"url"`
			Snip  string `json:"snippet"`
		} `json:"value"`
	} `json:"webPages"`
	Next string `json:"next"`
}

// Search runs one query against the configured search API.
func (p *SearchProvider) Search(ctx context.Context, q Query) (Result, error) {
	if err := p.Ready(); err != nil {
		return Result{}, err
	}
	limit := q.Limit
	if limit <= 0 || limit > p.maxPerQuery {
		limit = p.maxPerQuery
	}
	page := 1
	if q.Cursor != "" {
		if p, err := pageFromCursor(q.Cursor); err == nil {
			page = p
		} else {
			page = 1
		}
	}

	rows, next, err := p.fetchPage(ctx, q, limit, page)
	if err != nil {
		return Result{}, err
	}
	seen := map[string]bool{}
	now := p.now()
	out := make([]candidate.Candidate, 0, len(rows))
	for _, row := range rows {
		rawURL := row.url
		if rawURL == "" {
			continue
		}
		reg, err := normalizeDomainFrom(rawURL)
		if err != nil {
			continue
		}
		if d, derr := domain.FromHost(reg); derr == nil && d.IsPlatform {
			// A search result on a platform surface is the platform, not a lead.
			// GitHub, LinkedIn and the directories are where companies are
			// mentioned, and turning each mention into a candidate fills the run
			// with the platforms themselves.
			continue
		}
		if seen[reg] {
			continue
		}
		seen[reg] = true
		c := candidate.Candidate{
			Name:   cleanTitle(row.title),
			Domain: reg,
			URL:    canonicalURL(rawURL, reg),
			// A search snippet is a mention, not a verified fact, so confidence
			// stays at the source's own modest value and the snippet is kept as
			// evidence for a human rather than as an input to matching.
			Confidence: 0.6,
		}
		c.AddEvidence(candidate.Evidence{
			Source:     candidate.SourceSearch,
			Method:     candidate.MethodSearchResult,
			URL:        redactURL(rawURL),
			Query:      q.Text,
			Snippet:    truncate(row.snippet, 400),
			ObservedAt: now,
			Detail:     fmt.Sprintf("page=%d", page),
		})
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}

	res := Result{Candidates: out, Detail: fmt.Sprintf("page %d, %d results", page, len(rows))}
	// Continue only while the vendor offers a next page and we are under our own
	// cap. A vendor whose next link never ends must not be able to loop forever.
	if next != "" && page < p.maxPages && len(out) < limit {
		res.Truncated = true
		res.Cursor = cursorFromPage(page + 1)
	}
	return res, nil
}

func (p *SearchProvider) fetchPage(ctx context.Context, q Query, limit, page int) ([]searchRow, string, error) {
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("providers: %w: bad search endpoint", ErrNotConfigured)
	}
	values := u.Query()
	values.Set("q", q.Text)
	values.Set("count", fmt.Sprint(limit))
	values.Set("offset", fmt.Sprint((page-1)*limit))
	u.RawQuery = values.Encode()

	headers := map[string]string{"X-Subscription-Token": p.apiKey}
	var resp searchResponse
	status, err := p.fetcher.GetJSON(ctx, u.String(), headers, &resp)
	switch {
	case err == nil:
	case status == 401 || status == 403:
		// A rejected key will be rejected again. Retrying it three more times
		// wastes a call and delays the run for no possible benefit.
		return nil, "", fmt.Errorf("%w: %s rejected the configured API key", ErrUpstream, p.name)
	case status == 429:
		// Recorded as a budget event rather than an outage so the run backs off
		// instead of treating a healthy vendor as broken.
		return nil, "", fmt.Errorf("%w: %s rate limited the request", ErrBudgetExhausted, p.name)
	case status == 404:
		return nil, "", nil
	default:
		return nil, "", fmt.Errorf("%w: %s: %v", ErrUpstream, p.name, err)
	}
	rows := resp.rows()
	next := strings.TrimSpace(resp.Next)
	return rows, next, nil
}

type searchRow struct {
	title   string
	url     string
	snippet string
}

// rows flattens whichever result envelope the vendor used.
func (r searchResponse) rows() []searchRow {
	var out []searchRow
	add := func(title, link, snippet string) {
		if link == "" {
			return
		}
		out = append(out, searchRow{title: title, url: link, snippet: snippet})
	}
	for _, x := range r.Results {
		link := x.URL
		if link == "" {
			link = x.Link
		}
		snip := x.Snippet
		if snip == "" {
			snip = x.Desc
		}
		add(x.Title, link, snip)
	}
	for _, x := range r.Items {
		link := x.URL
		if link == "" {
			link = x.Link
		}
		snip := x.Snippet
		if snip == "" {
			snip = x.Descr
		}
		add(x.Title, link, snip)
	}
	for _, x := range r.Organic {
		add(x.Title, x.URL, x.Snippet)
	}
	for _, x := range r.WebPages.Value {
		add(x.Title, x.URL, x.Snip)
	}
	return out
}

// cleanTitle strips the trailing site name search engines append, so a candidate
// is not named "Acme Robotics - LinkedIn".
func cleanTitle(title string) string {
	title = strings.TrimSpace(title)
	for _, sep := range []string{" | ", " - ", " – ", " — ", " :: "} {
		if i := strings.LastIndex(title, sep); i > 0 {
			// Only drop the tail when it looks like a site name rather than part
			// of a company name, which is why a long tail is left alone.
			tail := strings.TrimSpace(title[i+len(sep):])
			if len(tail) <= 30 && !strings.ContainsAny(tail, ".") {
				return strings.TrimSpace(title[:i])
			}
		}
	}
	return title
}

// canonicalURL rewrites a result URL onto the registrable domain's root, so a
// deep link into someone's profile does not become the candidate's website.
// canonicalURL reduces a search result link to the company's own home page.
//
// The query string and fragment are dropped: a result link routinely carries a
// session, referral or signed token, and that is not something to store as the
// company's URL and hand to a later crawler. A link on a subdomain is reduced to
// the registrable domain for the same reason, since the deep path is where the
// result pointed, not where the company lives.
func canonicalURL(raw, registrable string) string {
	if raw == "" {
		return "https://" + registrable + "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "https://" + registrable + "/"
	}
	return "https://" + registrable + "/"
}
