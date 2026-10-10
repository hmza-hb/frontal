package providers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/config"
)

// SeedProvider serves the operator's own list.
//
// A seed list is the highest-trust source available and the only one that costs
// nothing, which is why it sorts first in every ranking. It also makes a
// deployment testable: a run with only seeds configured is a complete run that
// touches no third party, and it is how the pipeline is exercised end to end in
// tests without depending on anyone's uptime.
//
// It is deliberately not a query-driven provider. Seeds are not answers to
// queries, so a Search call is answered with the entries that match rather than
// by re-reading the file, and the entries are read once and cached.
type SeedProvider struct {
	name  string
	path  string
	seeds []config.SeedEntry
	// maxEntries bounds rows read from Path.
	maxEntries int
	loadErr    error
	loaded     bool
	now        func() time.Time
	mu         sync.Mutex
}

// SeedConfig configures a SeedProvider.
type SeedConfig struct {
	// Name overrides the provider name.
	Name string
	// Path is the seed file.
	Path string
	// Seeds are already-parsed entries. When present, Path is not read, which is
	// how an in-process run is seeded without a file.
	Seeds []config.SeedEntry
	// DefaultConfidence is applied to a seed that does not state one.
	DefaultConfidence float64
	// MaxEntries bounds how many rows a seed file may contribute. Zero means
	// DefaultMaxSeedEntries.
	MaxEntries int
	// Now is the clock.
	Now func() time.Time
}

// DefaultMaxSeedEntries bounds a seed file so a mistyped path to a large export
// cannot pull an unbounded number of companies into one run.
const DefaultMaxSeedEntries = 10000

// NewSeedProvider returns a seed provider.
func NewSeedProvider(cfg SeedConfig) (*SeedProvider, error) {
	p := &SeedProvider{
		name:       nameOr(cfg.Name, "seed"),
		path:       strings.TrimSpace(cfg.Path),
		maxEntries: cfg.MaxEntries,
		seeds:      append([]config.SeedEntry(nil), cfg.Seeds...),
		now:        orNow(cfg.Now),
	}
	// Inline seeds go through load() too, so the usability check cannot be skipped
	// by configuring seeds in process instead of reading a file.
	return p, nil
}

func (p *SeedProvider) Name() string { return p.name }

// Kinds is every kind: seeds are an operator's answer, not a surface's, so they
// answer whatever is asked of them.
func (p *SeedProvider) Kinds() []Kind { return nil }

func (p *SeedProvider) Ready() error {
	if err := p.load(); err != nil {
		return err
	}
	return nil
}

func (p *SeedProvider) load() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loaded {
		return p.loadErr
	}
	if p.path == "" && len(p.seeds) == 0 {
		p.loaded = true
		p.loadErr = fmt.Errorf("%w: no seed file configured", ErrNotConfigured)
		return p.loadErr
	}
	if p.path != "" {
		max := p.maxEntries
		if max <= 0 {
			// config.LoadSeeds refuses a non-positive limit on purpose, so the
			// bound is applied here instead of being left at zero, which would
			// make every seed file fail to load.
			max = DefaultMaxSeedEntries
		}
		entries, err := config.LoadSeeds(p.path, max)
		if err != nil {
			p.loaded = true
			p.loadErr = fmt.Errorf("%w: seed file: %v", ErrNotConfigured, err)
			return p.loadErr
		}
		p.seeds = entries
	}
	usable := make([]config.SeedEntry, 0, len(p.seeds))
	for _, e := range p.seeds {
		if strings.TrimSpace(e.Name) == "" && strings.TrimSpace(e.Domain) == "" {
			continue
		}
		usable = append(usable, e)
	}
	if len(usable) == 0 {
		p.loaded = true
		p.loadErr = fmt.Errorf("%w: no usable seed entries", ErrNotConfigured)
		return p.loadErr
	}
	p.seeds = usable
	p.loaded = true
	p.loadErr = nil
	return nil
}

// Len reports how many seeds are available, loading the file if needed.
func (p *SeedProvider) Len() int {
	if err := p.load(); err != nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.seeds)
}

// Search returns the seeds matching a query.
//
// A query is matched loosely against a seed's name, domain, keywords, and
// industry, because the point of a seed is that the operator already knows the
// company and the query is only steering which ones this step surfaces. When the
// query is empty, every seed is returned: the operator's list is already
// targeted, and filtering it by a generated query would silently discard
// companies the operator specifically asked for.
func (p *SeedProvider) Search(ctx context.Context, q Query) (Result, error) {
	if err := p.load(); err != nil {
		return Result{}, err
	}
	p.mu.Lock()
	seeds := p.seeds
	p.mu.Unlock()

	needle := strings.ToLower(strings.TrimSpace(q.Text))
	now := p.now()
	out := make([]candidate.Candidate, 0, len(seeds))
	for _, s := range seeds {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if needle != "" && !seedMatches(s, needle) {
			continue
		}
		domain := strings.ToLower(strings.TrimSpace(s.Domain))
		domain = strings.TrimPrefix(domain, "https://")
		domain = strings.TrimPrefix(domain, "http://")
		domain = strings.TrimSuffix(domain, "/")
		if i := strings.IndexAny(domain, "/?#"); i >= 0 {
			domain = domain[:i]
		}
		if domain != "" {
			if reg, err := normalizeDomainFrom(domain); err == nil {
				domain = reg
			} else {
				// A seed row with an unusable domain is still worth surfacing: the
				// company is real and the expansion stage can look for its site.
				domain = ""
			}
		}
		name := strings.TrimSpace(s.Name)
		if name == "" && domain != "" {
			name = domain
		}
		confidence := 0.7
		if s.Confidence != nil {
			confidence = *s.Confidence
		}
		industry := strings.TrimSpace(s.Industry)
		c := candidate.Candidate{
			Name:         name,
			Domain:       domain,
			URL:          seedURL(domain),
			Country:      normalizeCountry(s.Country),
			Industry:     industry,
			EmployeeHint: strings.TrimSpace(s.EmployeeHint),
			Description:  strings.TrimSpace(s.Notes),
			Keywords:     seedKeywords(s, industry),
			Confidence:   clamp01(confidence),
		}
		evidenceURL := "urn:seed:" + p.path
		if p.path == "" {
			evidenceURL = "urn:seed:inline"
		}
		c.AddEvidence(candidate.Evidence{
			Source:     candidate.SourceSeed,
			Method:     candidate.MethodSeedImport,
			URL:        evidenceURL,
			Query:      q.Text,
			ObservedAt: now,
			Detail:     "operator supplied",
		})
		out = append(out, c)
	}
	return Result{
		Candidates: out,
		Detail:     fmt.Sprintf("%d of %d seeds", len(out), len(seeds)),
	}, nil
}

// seedKeywords derives query terms from a seed row. The industry is included
// because the query generator uses it, and the notes often carry the product
// line or the legal name the expansion stage will need.
func seedKeywords(s config.SeedEntry, industry string) []string {
	var out []string
	if industry != "" {
		out = append(out, industry)
	}
	for _, f := range strings.FieldsFunc(s.Notes, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9')
	}) {
		if len(f) >= 4 {
			out = append(out, f)
		}
	}
	return out
}

func normalizeCountry(in string) string {
	in = strings.ToUpper(strings.TrimSpace(in))
	if len(in) != 2 {
		return ""
	}
	for i := 0; i < len(in); i++ {
		if in[i] < 'A' || in[i] > 'Z' {
			return ""
		}
	}
	return in
}

func seedMatches(s config.SeedEntry, needle string) bool {
	haystacks := []string{s.Name, s.Domain, s.Industry, s.Notes}
	for _, h := range haystacks {
		h = strings.ToLower(h)
		if h == "" {
			continue
		}
		if strings.Contains(h, needle) {
			return true
		}
		// Match on whole words too, so "acme" does not match "placeable".
		for _, field := range strings.FieldsFunc(h, func(r rune) bool {
			return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
		}) {
			if field == needle {
				return true
			}
		}
	}
	return false
}

func seedURL(domain string) string {
	if domain == "" {
		return ""
	}
	return "https://" + domain + "/"
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
