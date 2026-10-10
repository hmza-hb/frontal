package providers

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/domain"
)

// DefaultRDAPBootstrapURL is IANA's RDAP bootstrap registry, the authoritative
// mapping from a TLD to the RDAP server that answers for it.
const DefaultRDAPBootstrapURL = "https://data.iana.org/rdap/dns.json"

// RDAPProvider resolves registration data through RDAP, the successor to WHOIS.
//
// Why it matters for discovery: RDAP is the only widely-available source that can
// confirm a domain is genuinely registered and tell you when it was created. A
// domain that appeared in a certificate log six months ago and now has no
// registration record was a test domain, a throwaway, or a mistake, and
// dropping it before it reaches a crawler saves real money downstream.
type RDAPProvider struct {
	name         string
	fetcher      *HTTPFetcher
	bootstrap    string
	bootstrapTTL time.Duration
	now          func() time.Time

	mu        sync.Mutex
	servers   map[string]string
	fetchedAt time.Time
	// refreshing is closed when the in-flight bootstrap fetch finishes. Without it
	// every concurrent lookup sees an empty cache at the same instant and fires its
	// own request at the one public registry the whole run depends on.
	refreshMu  sync.Mutex
	refreshing chan struct{}
}

// RDAPConfig configures an RDAPProvider.
type RDAPConfig struct {
	// BootstrapURL is IANA's registry. Empty means the public default.
	BootstrapURL string
	// Fetcher performs requests.
	Fetcher *HTTPFetcher
	// BootstrapTTL is how long the TLD-to-server map is cached. The mapping
	// changes a handful of times a year, so an hour is generous and saves a
	// request per run.
	BootstrapTTL time.Duration
	// Now is the clock.
	Now func() time.Time
}

// NewRDAPProvider returns an RDAP provider.
func NewRDAPProvider(cfg RDAPConfig) (*RDAPProvider, error) {
	if cfg.Fetcher == nil {
		return nil, fmt.Errorf("%w: RDAP provider needs an HTTP fetcher", ErrNotConfigured)
	}
	if cfg.BootstrapURL == "" {
		cfg.BootstrapURL = DefaultRDAPBootstrapURL
	}
	if cfg.BootstrapTTL <= 0 {
		cfg.BootstrapTTL = time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	p := &RDAPProvider{
		name:         "rdap",
		fetcher:      cfg.Fetcher,
		bootstrap:    cfg.BootstrapURL,
		bootstrapTTL: cfg.BootstrapTTL,
		now:          cfg.Now,
		servers:      map[string]string{},
	}
	return p, nil
}

func (p *RDAPProvider) Name() string { return p.name }

func (p *RDAPProvider) Kinds() []Kind { return []Kind{KindName, KindIndustry, KindGeography} }

func (p *RDAPProvider) Ready() error {
	if p.fetcher == nil {
		return fmt.Errorf("%w: RDAP provider has no HTTP fetcher", ErrNotConfigured)
	}
	return nil
}

type rdapBootstrap struct {
	Services [][][]string `json:"services"`
}

type rdapResponse struct {
	ObjectClassName string   `json:"objectClassName"`
	Handle          string   `json:"handle"`
	LdhName         string   `json:"ldhName"`
	UnicodeName     string   `json:"unicodeName"`
	Status          []string `json:"status"`
	Events          []struct {
		ActionDate string `json:"eventAction"`
		EventDate  string `json:"eventDate"`
	} `json:"events"`
	Entities []struct {
		Roles     []string `json:"roles"`
		Handle    string   `json:"handle"`
		VCard     string   `json:"vcardArray"`
		PublicIDs []struct {
			Type string `json:"type"`
			ID   string `json:"identifier"`
		} `json:"publicIds"`
	} `json:"entities"`
	Nameservers []struct {
		LDHName string `json:"ldhName"`
	} `json:"nameservers"`
}

// Search confirms registration for the domain in the query.
func (p *RDAPProvider) Search(ctx context.Context, q Query) (Result, error) {
	reg, err := domainForQuery(q)
	if err != nil {
		return Result{}, err
	}
	tld := tldOf(reg)
	if tld == "" {
		return Result{}, fmt.Errorf("%w: %q has no TLD", ErrUnsupportedQuery, truncate(reg, 40))
	}
	base, err := p.serverFor(ctx, tld)
	if err != nil {
		return Result{}, err
	}
	endpoint := strings.TrimRight(base, "/") + "/domain/" + url.PathEscape(reg)
	var resp rdapResponse
	status, err := p.fetcher.GetJSON(ctx, endpoint, nil, &resp)
	switch {
	case err == nil:
	case status == 404:
		// RDAP's 404 is authoritative: the domain is not registered. This is a
		// real answer and must not be retried or counted as a failure.
		return Result{}, fmt.Errorf("%w: %s is not registered", ErrNotFound, reg)
	case status == 429:
		return Result{}, fmt.Errorf("%w: RDAP rate limited the request for %s", ErrUpstream, reg)
	default:
		return Result{}, fmt.Errorf("%w: RDAP lookup for %s: %v", ErrUpstream, reg, err)
	}

	now := p.now()
	c := candidate.Candidate{
		Name:   resp.LdhName,
		Domain: reg,
		URL:    "https://" + reg + "/",
		// Registration confirms a domain exists. It says nothing about whether
		// the company behind it is worth anything, so confidence stays moderate
		// and Candidate.Status is left alone: deciding a candidate's lifecycle is
		// the engine's job after ranking, not a provider's.
		Confidence: 0.5,
	}
	// A domain whose RDAP status is pending deletion or redacted is a real
	// registration that is on its way out, which downstream stages need to know
	// and which a bare "it resolved" check would miss.
	statuses := normalizedStatuses(resp.Status)
	c.AddEvidence(candidate.Evidence{
		Source:     candidate.SourceRegistry,
		Method:     candidate.MethodAPIRecord,
		URL:        endpoint,
		Query:      q.Text,
		ObservedAt: now,
		Detail:     rdapDetail(resp, statuses, tld),
	})
	return Result{
		Candidates: []candidate.Candidate{c},
		Detail:     "registration confirmed for " + reg,
	}, nil
}

func rdapDetail(resp rdapResponse, statuses []string, tld string) string {
	parts := []string{"tld=" + tld}
	if len(statuses) > 0 {
		parts = append(parts, "status="+strings.Join(statuses, "|"))
	}
	for _, e := range resp.Events {
		if e.ActionDate == "" || e.EventDate == "" {
			continue
		}
		parts = append(parts, e.ActionDate+"="+e.EventDate)
	}
	// The registrar identifier is useful and public. Anything resembling contact
	// details is not, and is deliberately not extracted: a vCard holds personal
	// data that has no business in a lead record.
	if len(resp.Entities) > 0 {
		roles := append([]string(nil), resp.Entities[0].Roles...)
		if len(roles) > 0 {
			parts = append(parts, "registrar_role="+strings.Join(sortedUnique(roles), "|"))
		}
		for _, id := range resp.Entities[0].PublicIDs {
			if id.Type == "IANA Registrar ID" && id.ID != "" {
				parts = append(parts, "registrar_id="+truncate(id.ID, 24))
				break
			}
		}
	}
	return strings.Join(parts, " ")
}

func normalizedStatuses(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.ToLower(strings.TrimSpace(s)))
	}
	return out
}

func containsAny(haystack []string, needles ...string) bool {
	for _, h := range haystack {
		for _, n := range needles {
			if h == n {
				return true
			}
		}
	}
	return false
}

// serverFor returns the RDAP base URL for a TLD, caching the bootstrap registry.
//
// Exactly one goroutine fetches the registry at a time. The rest wait for that
// result rather than adding their own request: IANA's registry is a single small
// public file, and a run with a dozen concurrent lookups would otherwise send a
// dozen requests to it every time the cache expired.
func (p *RDAPProvider) serverFor(ctx context.Context, tld string) (string, error) {
	for {
		if base, loaded := p.cachedServer(tld); loaded {
			if base == "" {
				// The registry was read and still lists no service for this TLD.
				// That is a fact about the internet, not a gap, and reporting it as
				// unsupported keeps it out of the failure count and the breaker.
				return "", fmt.Errorf("%w: no RDAP service for .%s", ErrUnsupportedQuery, tld)
			}
			return base, nil
		}

		wait, owner := p.claimRefresh()
		if owner {
			err := p.refreshBootstrap(ctx)
			p.finishRefresh()
			// Closed on both paths: a failure that stored nothing must still release
			// the waiters, or they block until the process exits.
			close(wait)
			if err != nil {
				return "", err
			}
			continue
		}
		// Stay cancellable while waiting, so a shutting-down run is not held up by a
		// public website that has stopped answering.
		select {
		case <-wait:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// cachedServer reports the TLD's server when the registry has been read and is
// still fresh. loaded is false when the registry needs fetching, and the second
// return is empty when the registry is fresh but has no entry for the TLD.
func (p *RDAPProvider) cachedServer(tld string) (base string, loaded bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fetchedAt.IsZero() || p.now().Sub(p.fetchedAt) > p.bootstrapTTL {
		return "", false
	}
	return p.servers[tld], true
}

// claimRefresh takes ownership of the bootstrap fetch, or returns the channel the
// current owner will close when it is done.
func (p *RDAPProvider) claimRefresh() (wait chan struct{}, owner bool) {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if p.refreshing != nil {
		return p.refreshing, false
	}
	wait = make(chan struct{})
	p.refreshing = wait
	return wait, true
}

func (p *RDAPProvider) finishRefresh() {
	p.refreshMu.Lock()
	p.refreshing = nil
	p.refreshMu.Unlock()
}

func (p *RDAPProvider) refreshBootstrap(ctx context.Context) error {
	var boot rdapBootstrap
	if _, err := p.fetcher.GetJSON(ctx, p.bootstrap, nil, &boot); err != nil {
		return fmt.Errorf("%w: RDAP bootstrap registry: %v", ErrUpstream, err)
	}
	servers := map[string]string{}
	for _, group := range boot.Services {
		// IANA format: a list of TLD lists followed by the server URLs.
		if len(group) < 2 {
			continue
		}
		urls := group[len(group)-1]
		for _, u := range urls {
			if u != "" {
				for i := 0; i < len(group)-1; i++ {
					for _, tld := range group[i] {
						if tld != "" {
							servers[strings.ToLower(tld)] = u
						}
					}
				}
			}
		}
	}
	if len(servers) == 0 {
		return fmt.Errorf("%w: RDAP bootstrap registry listed no services", ErrUpstream)
	}
	p.mu.Lock()
	p.servers = servers
	p.fetchedAt = p.now()
	p.mu.Unlock()
	return nil
}

// domainForQuery extracts the domain an RDAP lookup should target.
func domainForQuery(q Query) (string, error) {
	candidateValue := strings.TrimSpace(q.Text)
	if candidateValue == "" {
		return "", fmt.Errorf("%w: empty query", ErrUnsupportedQuery)
	}
	reg, err := normalizeDomainFrom(candidateValue)
	if err != nil {
		// Classified as unsupported rather than left generic. A query that is not
		// a domain is not a question that went wrong; re-asking it will fail
		// identically, and counting it as a transient failure would let a run of
		// them open the circuit breaker on a provider that is working fine.
		return "", fmt.Errorf("%w: %v", ErrUnsupportedQuery, err)
	}
	return reg, nil
}

func tldOf(registrable string) string {
	// A single-label host has no TLD to ask about. Callers reject it earlier, but
	// returning the host itself as its own suffix would build a lookup for a
	// registry that does not exist.
	if !strings.Contains(registrable, ".") {
		return ""
	}
	d, err := domain.FromHost(registrable)
	if err != nil || d.PublicSuffix == "" {
		if i := strings.LastIndex(registrable, "."); i > 0 && i < len(registrable)-1 {
			return strings.ToLower(registrable[i+1:])
		}
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(d.PublicSuffix, "."))
}
