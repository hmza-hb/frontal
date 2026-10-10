package providers

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/domain"
)

// DefaultCertificateEndpoint is crt.sh's JSON API. It is public, needs no
// credentials, and rate-limits, which is why the runner's per-provider budget is
// the thing that keeps a run polite.
const DefaultCertificateEndpoint = "https://crt.sh/"

// CertificateProvider finds company domains from certificate transparency logs.
//
// Why this provider exists: a certificate transparency log is a public,
// append-only record of every domain anyone has obtained a certificate for. That
// makes it one of the few sources that can produce a company's *own* domain
// without anyone having listed it in a directory first, and it cannot be
// censored by the company being found.
//
// What it cannot do: it says nothing about which company a domain belongs to. A
// certificate for acme.io might be an infrastructure provider issuing on behalf
// of thousands of unrelated customers, so every domain found here is a
// hypothesis with evidence attached, never a conclusion. Candidate ranking
// exists partly to keep that honest.
type CertificateProvider struct {
	name        string
	endpoint    string
	fetcher     *HTTPFetcher
	maxPages    int
	maxPerQuery int
	now         func() time.Time
}

// CertificateConfig configures a CertificateProvider.
type CertificateConfig struct {
	// Endpoint is the crt.sh JSON API base. Empty means the public default.
	Endpoint string
	// Fetcher performs requests.
	Fetcher *HTTPFetcher
	// MaxPages caps pagination.
	MaxPages int
	// MaxPerQuery caps how many domains one query may contribute, so one broad
	// query cannot flood the candidate set with a hosting provider's customers.
	MaxPerQuery int
	// Now is the clock.
	Now func() time.Time
}

// NewCertificateProvider returns a certificate transparency provider.
func NewCertificateProvider(cfg CertificateConfig) (*CertificateProvider, error) {
	if cfg.Fetcher == nil {
		return nil, fmt.Errorf("providers: %w: certificate provider needs an HTTP fetcher", ErrNotConfigured)
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultCertificateEndpoint
	}
	if _, err := url.Parse(cfg.Endpoint); err != nil {
		return nil, fmt.Errorf("providers: %w: bad certificate endpoint: %v", ErrNotConfigured, err)
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = 1
	}
	if cfg.MaxPerQuery <= 0 {
		cfg.MaxPerQuery = 50
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &CertificateProvider{
		name:        "certificate",
		endpoint:    strings.TrimRight(cfg.Endpoint, "/"),
		fetcher:     cfg.Fetcher,
		maxPages:    cfg.MaxPages,
		maxPerQuery: cfg.MaxPerQuery,
		now:         cfg.Now,
	}, nil
}

func (p *CertificateProvider) Name() string { return p.name }

func (p *CertificateProvider) Kinds() []Kind { return nil }

func (p *CertificateProvider) Ready() error {
	if p.fetcher == nil {
		return fmt.Errorf("%w: certificate provider has no HTTP fetcher", ErrNotConfigured)
	}
	if p.endpoint == "" {
		return fmt.Errorf("%w: certificate provider has no endpoint", ErrNotConfigured)
	}
	return nil
}

// crtEntry is one row of crt.sh's JSON response.
type crtEntry struct {
	IssuerName     string `json:"issuer_name"`
	CommonName     string `json:"common_name"`
	NameValue      string `json:"name_value"`
	ID             int64  `json:"id"`
	EntryTimestamp string `json:"entry_timestamp"`
	NotBefore      string `json:"not_before"`
	NotAfter       string `json:"not_after"`
}

// Search queries the certificate log for domains matching the query.
//
// The query text is treated as a domain fragment, not as free text. crt.sh has
// no full-text index, so passing a phrase like "warehouse automation companies"
// would return nothing and be recorded as coverage — a silent gap. Detecting
// that here and reporting it as unsupported keeps the coverage record honest.
func (p *CertificateProvider) Search(ctx context.Context, q Query) (Result, error) {
	fragment, err := domainFragment(q.Text)
	if err != nil {
		return Result{}, err
	}
	limit := q.Limit
	if limit <= 0 || limit > p.maxPerQuery {
		limit = p.maxPerQuery
	}
	page := 1
	if q.Cursor != "" {
		var err error
		page, err = pageFromCursor(q.Cursor)
		if err != nil {
			return Result{}, err
		}
	}

	values := make([]crtEntry, 0, limit)
	truncated := false
	for ; page <= p.maxPages; page++ {
		var batch []crtEntry
		status, err := p.fetcher.GetJSON(ctx, p.searchURL(fragment, page, limit), nil, &batch)
		switch {
		case err == nil:
		case status == 429:
			// A rate limit is the expected failure mode here, not an anomaly.
			return Result{}, fmt.Errorf("%w: certificate log rate limited the request", ErrUpstream)
		case status == 404:
			// The log has never heard of this fragment. A real answer.
			return Result{Detail: "no certificate matches"}, nil
		default:
			return Result{}, fmt.Errorf("%w: certificate log: %v", ErrUpstream, err)
		}
		if len(batch) == 0 {
			break
		}
		values = append(values, batch...)
		if len(batch) < limit {
			break
		}
		truncated = true
	}
	if len(values) == 0 {
		return Result{Detail: "no certificate matches"}, nil
	}

	seen := map[string]bool{}
	out := make([]candidate.Candidate, 0, len(values))
	now := p.now()
	for _, row := range values {
		// A single certificate carries a newline-separated list of every name it
		// covers, including wildcard parents and SANs that are not companies.
		for _, raw := range strings.Split(row.NameValue, "\n") {
			raw = strings.TrimSpace(raw)
			if raw == "" || strings.HasPrefix(raw, "*.") {
				// A wildcard proves the parent exists but names no company, and
				// "*.acme.io" is not a website.
				continue
			}
			reg, err := normalizeDomainFrom(raw)
			if err != nil {
				continue
			}
			// The log's fragment match is fuzzy and returns certificates for
			// many other companies. Only the domain that was asked about may
			// become a candidate, or one broad lookup fills the run with leads
			// that have nothing to do with the query.
			if !sameOrSubdomain(reg, fragment) {
				continue
			}
			if seen[reg] {
				continue
			}
			seen[reg] = true
			if d, err := domain.FromHost(reg); err == nil && d.IsPlatform {
				// A certificate for github.com is GitHub's, not a lead.
				continue
			}
			c := candidate.Candidate{
				Domain: reg,
				URL:    "https://" + reg + "/",
				// A certificate proves a domain, not a headquarters. Country is
				// deliberately left empty and lives as a suffix hint in Domain.
				Confidence: 0.4,
			}
			c.AddEvidence(candidate.Evidence{
				Source:     candidate.SourceCertificate,
				Method:     candidate.MethodCertificate,
				URL:        p.searchURL(fragment, 1, limit),
				Query:      q.Text,
				ObservedAt: now,
				Detail:     certDetail(row),
			})
			out = append(out, c)
			if len(out) >= limit {
				truncated = true
				break
			}
		}
		if len(out) >= limit {
			break
		}
	}

	res := Result{
		Candidates: out,
		Truncated:  truncated,
		Detail:     fmt.Sprintf("%d certificate rows, %d distinct domains", len(values), len(out)),
	}
	if truncated {
		res.Cursor = cursorFromPage(page + 1)
	}
	return res, nil
}

func (p *CertificateProvider) searchURL(fragment string, page, limit int) string {
	// The domain is a path segment, not a query parameter: the log indexes by
	// path, and a parameter leaves it matching everything. It is escaped, so a
	// query can never walk out of the path.
	u, err := url.Parse(p.endpoint + "/" + url.PathEscape(fragment))
	if err != nil {
		return p.endpoint
	}
	v := url.Values{}
	v.Set("output", "json")
	if page > 1 {
		v.Set("page", fmt.Sprint(page))
	}
	u.RawQuery = v.Encode()
	return u.String()
}

func certDetail(row crtEntry) string {
	var b strings.Builder
	if row.IssuerName != "" {
		b.WriteString("issuer=")
		b.WriteString(truncate(row.IssuerName, 80))
	}
	if row.NotAfter != "" {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString("not_after=")
		b.WriteString(row.NotAfter)
	}
	return b.String()
}

// domainFragment reduces a query to something a certificate log can search.
//
// A log indexes domain names, not prose. "warehouse automation companies
// germany" has no interpretation there, and quietly returning nothing for it
// would be recorded as coverage, so a query that cannot be reduced is reported
// as unsupported instead.
func domainFragment(text string) (string, error) {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return "", fmt.Errorf("%w: empty query", ErrUnsupportedQuery)
	}
	// Strip certificate-transparency search operators and quoting.
	for _, op := range []string{"%", "*.", "exact=", "exists=", "-"} {
		text = strings.ReplaceAll(text, op, " ")
	}
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') &&
			r != '.' && r != '-' && r != '_'
	})
	// Take the longest field that looks like a domain fragment: a log will match
	// the most specific part, and "acme" finds more than "acme logistics".
	// A fragment must look like a domain. A bare word is refused: the log would
	// match thousands of unrelated certificates, and a broad query that floods
	// the candidate set is worse than no query at all.
	var best string
	for _, f := range fields {
		f = strings.Trim(f, ".-_")
		if !strings.Contains(f, ".") || len(f) < 5 {
			continue
		}
		if len(f) > len(best) {
			best = f
		}
	}
	if best == "" {
		return "", fmt.Errorf("%w: %q names no domain to look up in a certificate log",
			ErrUnsupportedQuery, truncate(text, 40))
	}
	return best, nil
}

// pageFromCursor reads back a page number.
//
// The cursor is opaque state that comes back out of the ledger on a resumed run,
// so it is parsed strictly: exactly one "page" key holding a positive integer.
// Sscanf would stop at the first non-digit and silently accept "page=2&x=3" or
// "page=9;drop", which turns a tampered or corrupted ledger row into a request
// for an arbitrary page.
func pageFromCursor(cursor string) (int, error) {
	bad := fmt.Errorf("providers: cannot read page cursor %q", truncate(cursor, 20))
	if cursor == "" {
		return 0, bad
	}
	v, err := url.ParseQuery(cursor)
	if err != nil {
		return 0, bad
	}
	if len(v) != 1 || len(v["page"]) != 1 {
		return 0, bad
	}
	page, err := strconv.Atoi(v["page"][0])
	if err != nil || page < 1 {
		return 0, bad
	}
	return page, nil
}

func cursorFromPage(page int) string {
	if page < 2 {
		return ""
	}
	return fmt.Sprintf("page=%d", page)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// sortedUnique returns a sorted, de-duplicated copy, used where provider output
// order would otherwise make a run non-deterministic.
func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// sameOrSubdomain reports whether a registrable domain is the one that was
// searched for, allowing for a query that named a parent under a multi-label
// public suffix such as "acme.co.uk".
func sameOrSubdomain(reg, fragment string) bool {
	reg = strings.ToLower(strings.TrimSpace(reg))
	fragment = strings.ToLower(strings.TrimSpace(fragment))
	return reg == fragment || strings.HasSuffix(reg, "."+fragment)
}
