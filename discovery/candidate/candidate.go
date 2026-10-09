// Package candidate defines what a discovery provider emits.
//
// The type in this file is the engine's central contract. A provider's only job
// is to turn some external surface into a stream of Candidate values, each of
// which must be able to explain itself: which provider saw it, in response to
// which query, at which URL, with what evidence.
//
// Nothing here is judged yet. A Candidate carries a Confidence number, but that
// number records how strongly the *source* supports the discovery — a curated
// seed list scores high, a name match inside a directory listing scores lower.
// Deciding which candidates are worth a crawler's budget happens in ranking,
// where a candidate is seen alongside its competitors. Keeping the two apart
// means a provider cannot inflate itself by self-assigning a high score.
package candidate

import (
	"strings"
	"time"
)

// Source identifies the external surface a candidate came from. It is a string
// rather than an enum so that a new provider can be added without editing this
// package, which is what keeps the provider set open.
type Source string

const (
	// SourceSeed is a curated list the operator supplied. These are treated as
	// the operator's own statement about who to look for.
	SourceSeed Source = "seed"
	// SourceCertificate is a certificate transparency log, which proves a
	// domain exists but says nothing about the business behind it.
	SourceCertificate Source = "certificate"
	// SourceRegistry is an official corporate or domain registry record.
	SourceRegistry Source = "registry"
	// SourceDirectory is a business directory: Crunchbase, a chamber of
	// commerce, a government supplier list.
	SourceDirectory Source = "directory"
	// SourceNews is a news, blog, or press index.
	SourceNews Source = "news"
	// SourceDeveloper is a code-hosting or package registry page.
	SourceDeveloper Source = "developer"
	// SourceSearch is a web search API.
	SourceSearch Source = "search"
	// SourceSitemap is a sitemap.xml discovered on a known site.
	SourceSitemap Source = "sitemap"
	// SourceExpansion is a candidate derived from an existing candidate rather
	// than observed directly, such as a sibling domain found on a contact page.
	SourceExpansion Source = "expansion"
)

// Method records how the candidate was obtained, which is a different question
// from Source ("which surface") and Method ("what did we do there").
type Method string

const (
	// MethodSeedImport is a line the operator wrote in a seed file.
	MethodSeedImport Method = "seed_import"
	// MethodListPage is an item read from a paginated listing.
	MethodListPage Method = "list_page"
	// MethodSearchResult is a result in a search API response.
	MethodSearchResult Method = "search_result"
	// MethodAPIRecord is a record returned by a structured API.
	MethodAPIRecord Method = "api_record"
	// MethodSitemapEntry is a <loc> entry in a sitemap.
	MethodSitemapEntry Method = "sitemap_entry"
	// MethodCertificate is a subject name in a CT log.
	MethodCertificate Method = "certificate"
	// MethodLink is a hyperlink on a page already being read.
	MethodLink Method = "link"
	// MethodDerived is inferred by the engine from a known candidate.
	MethodDerived Method = "derived"
)

// Status is the lifecycle of a candidate. Discovery is resumable and long
// running, so a candidate that was seen and then dropped must be distinguishable
// from one that was never seen.
type Status string

const (
	// StatusNew has never been evaluated. This is what a provider emits.
	StatusNew Status = "new"
	// StatusAccepted passed ranking and is queued for downstream work.
	StatusAccepted Status = "accepted"
	// StatusRejected was evaluated and failed. A rejection records a reason so
	// the operator can see why a company they expected is missing.
	StatusRejected Status = "rejected"
	// StatusDuplicate resolved to a candidate already known.
	StatusDuplicate Status = "duplicate"
	// StatusError was a valid candidate that failed processing.
	StatusError Status = "error"
)

// Reason codes for rejection and error. These are strings for the same reason
// Source is: a provider may need to introduce its own, and an unknown reason
// must never be a parse failure.
const (
	ReasonPlatformDomain  = "platform_domain"
	ReasonNotCompany      = "not_a_company"
	ReasonInvalidDomain   = "invalid_domain"
	ReasonBlockedDomain   = "blocked_domain"
	ReasonPrivateNetwork  = "private_network"
	ReasonInvalidURL      = "invalid_url"
	ReasonAlreadyKnown    = "already_known"
	ReasonLowScore        = "low_score"
	ReasonDuplicateDomain = "duplicate_domain"
	ReasonSuperseded      = "superseded"
	ReasonProviderFailed  = "provider_failed"
	ReasonRateLimited     = "rate_limited"
	ReasonBudgetExhausted = "budget_exhausted"
	ReasonMalformed       = "malformed_provider_output"
)

// Evidence is a single observation that supports a candidate. Several providers
// point at the same company through different surfaces, and Evidence is what
// makes the difference between "one directory listed this" and "three
// independent sources agree, including its own website".
//
// Evidence is append-only in spirit: a candidate that accumulates two sources
// is stronger than one seen twice by the same source, and the ranking stage
// depends on being able to tell those apart.
type Evidence struct {
	// Source is the surface.
	Source Source
	// Method is what was done on that surface.
	Method Method
	// URL is where the observation was made. For a domain candidate this is the
	// company's own page; for a name match it is the listing that mentioned the
	// name. It is the field a human uses to check the claim.
	URL string
	// Query is the query that led to the observation, verbatim, so that a
	// surprising candidate can be traced back to the exact search that found it.
	Query string
	// Snippet is the surrounding text, when the source provides one. It is
	// evidence for a human, never input to automated matching, because a
	// provider's snippet is not trustworthy enough to key identity on.
	Snippet string
	// ObservedAt is when the provider saw this. A seed file has a file
	// timestamp; an API has a response time.
	ObservedAt time.Time
	// Detail carries provider-specific context, such as a registry number or a
	// certificate issuer. It must never contain credentials or personal data.
	Detail string
}

// Candidate is a company that discovery has found, with everything needed to
// judge and later reproduce the finding.
//
// A candidate is a claim, not a conclusion. It may point at a domain that turns
// out to be a parked page, or at a name that turns out to be a different company
// with the same name. Downstream stages exist to resolve exactly those cases,
// and they can only do so because this struct records where the claim came from.
type Candidate struct {
	// ID is assigned by the engine on first persistence. Providers leave it
	// zero; a provider that invents IDs would break resumability.
	ID string
	// Name is the company name as the source stated it, unnormalized beyond
	// whitespace. Two candidates with different names can still be the same
	// company, which is the entity-resolution stage's problem, not this one's.
	Name string
	// Domain is the registrable domain, the primary identity key. It is empty
	// when a source named a company without a website; that is a legitimate
	// discovery, and the expansion stage is what tries to find the domain.
	Domain string
	// URL is the best known landing page for the company, which may be more
	// specific than Domain.
	URL string
	// Country is an ISO 3166-1 alpha-2 code, from an explicit source field
	// where one exists. A country implied by a ccTLD belongs in
	// Domain.Country as a hint, not here, where a later stage would read it as
	// a verified location.
	Country string
	// Industry is a free-text or provider taxonomy term. Providers use wildly
	// different vocabularies, so this is recorded as stated and reconciled
	// later rather than forced into a shared enum here.
	Industry string
	// EmployeeHint is a size band as reported by the source, such as "11-50".
	// It is kept as text because a numeric value would imply a precision the
	// sources do not have.
	EmployeeHint string
	// Description is short context from the source.
	Description string
	// Keywords are terms that a later query generator can use to look for this
	// company by name, such as a legal name or a product line.
	Keywords []string
	// Evidence is every observation supporting this candidate, newest last.
	Evidence []Evidence
	// SourceCount is the number of distinct sources that contributed. It is
	// maintained by the engine on merge, not by providers, and it exists
	// because "seen by three independent surfaces" is the single strongest
	// signal in discovery and is expensive to recompute from Evidence on every
	// ranking pass.
	SourceCount int
	// Confidence is the source's own strength, in [0,1]. See the package
	// documentation for why this is the provider's view and not the engine's
	// verdict.
	Confidence float64
	// Status is the lifecycle position, set by the engine.
	Status Status
	// Reason explains a non-new status.
	Reason string
	// FirstSeen and LastSeen bracket the observation window. A candidate that
	// reappears months later is worth revisiting, which is why this is a range
	// and not a single timestamp.
	FirstSeen time.Time
	LastSeen  time.Time
	// RunID ties the candidate to the discovery run that produced it, so a
	// failed run can be rolled back without deleting real history.
	RunID string
}

// AddEvidence records an observation, keeping the first sighting time and
// deduplicating by source so that the same source reporting twice does not
// inflate the corroboration count.
func (c *Candidate) AddEvidence(e Evidence) {
	if e.ObservedAt.IsZero() {
		e.ObservedAt = time.Now().UTC()
	}
	c.Evidence = append(c.Evidence, e)
	if c.FirstSeen.IsZero() || e.ObservedAt.Before(c.FirstSeen) {
		c.FirstSeen = e.ObservedAt
	}
	if e.ObservedAt.After(c.LastSeen) {
		c.LastSeen = e.ObservedAt
	}
	c.SourceCount = c.distinctSources()
}

// Merge folds another candidate for the same company into this one, keeping the
// best value of each field rather than overwriting with whatever arrived last.
//
// The field-by-field rule matters: a seed file names the company accurately and
// carries no domain, while a directory listing carries a domain and an industry
// but a garbled name. A blind overwrite produces a candidate that is worse than
// either input.
func (c *Candidate) Merge(other Candidate) {
	if other.ID != "" && c.ID == "" {
		c.ID = other.ID
	}
	if len(other.Name) > len(c.Name) {
		c.Name = other.Name
	}
	if other.Domain != "" {
		c.Domain = other.Domain
	}
	if other.URL != "" {
		c.URL = other.URL
	}
	if other.Country != "" {
		c.Country = other.Country
	}
	if other.Industry != "" {
		c.Industry = other.Industry
	}
	if other.EmployeeHint != "" {
		c.EmployeeHint = other.EmployeeHint
	}
	if len(other.Description) > len(c.Description) {
		c.Description = other.Description
	}
	for _, k := range other.Keywords {
		if !c.HasKeyword(k) {
			c.Keywords = append(c.Keywords, k)
		}
	}
	// Evidence first: AddEvidence widens the observation window, so reconciling
	// the window beforehand would let the incoming evidence's stamps be
	// overwritten by a stale last-seen value.
	for _, e := range other.Evidence {
		if !c.hasEvidence(e) {
			c.AddEvidence(e)
		}
	}
	// Recomputed unconditionally. A candidate assembled from struct literals
	// rather than AddEvidence carries no count, and a merge in which every
	// incoming row is a duplicate adds nothing — without this line the count
	// would silently stay zero and a corroborated candidate would rank as
	// uncorroborated.
	c.SourceCount = c.distinctSources()

	if other.Confidence > c.Confidence {
		c.Confidence = other.Confidence
	}
	if other.LastSeen.After(c.LastSeen) {
		c.LastSeen = other.LastSeen
	}
	if !other.FirstSeen.IsZero() && (c.FirstSeen.IsZero() || other.FirstSeen.Before(c.FirstSeen)) {
		c.FirstSeen = other.FirstSeen
	}
}

// HasKeyword reports whether the candidate already carries a keyword, compared
// case-insensitively since the sources differ on capitalization.
func (c *Candidate) HasKeyword(k string) bool {
	k = normalize(k)
	if k == "" {
		return false
	}
	for _, existing := range c.Keywords {
		if normalize(existing) == k {
			return true
		}
	}
	return false
}

// hasEvidence reports whether this exact observation is already recorded. The
// method is part of the identity: a directory listing and an API record from the
// same provider at the same URL are two observations, and collapsing them would
// lose the fact that a source was consulted twice in different ways.
func (c *Candidate) hasEvidence(e Evidence) bool {
	for _, existing := range c.Evidence {
		if existing.Source == e.Source && existing.Method == e.Method &&
			existing.URL == e.URL && existing.Query == e.Query {
			return true
		}
	}
	return false
}

func (c *Candidate) distinctSources() int {
	seen := map[Source]struct{}{}
	for _, e := range c.Evidence {
		seen[e.Source] = struct{}{}
	}
	return len(seen)
}

// Validate reports whether the candidate is worth persisting. It checks only
// what makes a record usable at all: some way to identify the company and at
// least one observation explaining why it exists.
//
// A candidate with no evidence is not a weak candidate, it is an unsupported
// assertion, and storing it would let an unauditable company enter the pipeline.
func (c Candidate) Validate() error {
	if strings.TrimSpace(c.Name) == "" && strings.TrimSpace(c.Domain) == "" {
		return &ValidationError{Reason: ReasonNotCompany, Detail: "neither a name nor a domain"}
	}
	if len(c.Evidence) == 0 {
		return &ValidationError{Reason: ReasonMalformed, Detail: "no evidence"}
	}
	for _, e := range c.Evidence {
		if e.Source == "" || e.Method == "" {
			return &ValidationError{Reason: ReasonMalformed, Detail: "evidence missing source or method"}
		}
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		return &ValidationError{Reason: ReasonMalformed, Detail: "confidence out of range"}
	}
	return nil
}

// ValidationError carries a machine-readable reason so that a rejection can be
// counted by reason in metrics instead of by parsing log lines.
type ValidationError struct {
	Reason string
	Detail string
}

func (e *ValidationError) Error() string {
	if e.Detail == "" {
		return "candidate rejected: " + e.Reason
	}
	return "candidate rejected: " + e.Reason + ": " + e.Detail
}

func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
