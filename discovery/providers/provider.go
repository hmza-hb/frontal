// Package providers turns queries into candidate companies by talking to real
// surfaces, inside hard budgets.
//
// # The contract
//
// A Provider answers one question — "what does this surface have for this
// query?" — and returns companies with the evidence attached. It never decides
// whether those companies are good leads, and it never returns an unreproducible
// result: every candidate carries the query, the URL, and the method that
// produced it.
//
// # Budgets are the point
//
// Every outbound call in a discovery run is a call someone pays for. crt.sh
// rate-limits aggressively, a commercial search API bills per query, and an
// unbounded fan-out over a generated query set can cost thousands of dollars
// before anyone notices. So the limits are structural rather than advisory: a
// provider holds a budget, spends from it, and when the budget is gone the
// registry stops calling it. A provider cannot exceed its allowance because
// asking nicely in a comment does not stop a goroutine.
//
// # Health is per provider
//
// A provider that has started failing should stop being called, and one that has
// recovered should start again without a restart. Each provider therefore gets
// its own circuit breaker and rate limiter rather than sharing the runner's, so
// one bad surface cannot throttle the others or trip a breaker that belongs to
// a different provider.
//
// # Untrusted input
//
// Everything a provider returns is hostile until proven otherwise: oversized,
// malformed, wrong, or actively misleading. Responses are size-capped while
// being read, not after, every URL is canonicalised and checked against private
// address space, and a provider that would rather fail than guess returns an
// error instead of a partial answer.
package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
)

// Query is one unit of work handed to a provider.
type Query struct {
	// Text is the query as it will be sent, verbatim. It is recorded on every
	// candidate so a surprising result can be traced to the exact query.
	Text string
	// Language is the BCP-47 tag the query is written in, or empty when the
	// query is language-neutral. It is never inferred from the text.
	Language string
	// Kind classifies the query so a provider can decide whether it is
	// applicable at all: a certificate-transparency log has no opinion about a
	// "companies hiring in Berlin" query, and saying so is cheaper than
	// returning nothing.
	Kind Kind
	// Country is the market the query targets, when it has one.
	Country string
	// Cursor resumes a paginated query. It is opaque to everything except the
	// provider that produced it.
	Cursor string
	// Limit is the maximum number of candidates wanted, zero meaning the
	// provider's own default.
	Limit int
}

// Kind classifies a query's intent.
type Kind string

const (
	// KindIndustry is a vertical query such as "warehouse automation companies".
	KindIndustry Kind = "industry"
	// KindTechnology is a stack query such as "companies using Kubernetes".
	KindTechnology Kind = "technology"
	// KindGeography is a market query such as "logistics software Berlin".
	KindGeography Kind = "geography"
	// KindDirectory is a listing query that names a directory explicitly.
	KindDirectory Kind = "directory"
	// KindName is a query about one company by name.
	KindName Kind = "name"
	// KindCompetitor names a company as a way to find its peers.
	KindCompetitor Kind = "competitor"
)

// Applies reports whether a provider can do anything useful with a query kind.
// A provider that answers "no" cheaply is better than one that spends a paid call
// discovering it has no index for that kind.
func Applies(p Provider, k Kind) bool {
	ks, ok := p.(kindScoped)
	if !ok {
		// The provider cannot say what it serves, so it is given the benefit of
		// the doubt and decides for itself when it is asked.
		return true
	}
	if len(ks.Kinds()) == 0 {
		// It declared that it serves no kind at all. Treating that as "serves
		// everything" would send every query to a provider that has said it can
		// answer none of them, spending budget to collect nothing.
		return false
	}
	for _, want := range ks.Kinds() {
		if want == k {
			return true
		}
	}
	return false
}

// Result is what a provider returns for one query.
type Result struct {
	// Provider names who answered, and Query is what they were asked. The runner
	// fills both, so a caller handling several providers at once never has to
	// remember which answer came from where.
	Provider string
	Query    Query
	// Candidates are the companies found.
	Candidates []candidate.Candidate
	// Cursor is where to resume, when the provider has more.
	Cursor string
	// Truncated reports that the provider had more than it returned, either
	// because it hit its own limit or because the query asked for fewer. It is
	// what lets a resumed run pick up where this one stopped instead of
	// treating a partial answer as a complete one.
	Truncated bool
	// Detail is provider context for the lineage record.
	Detail string
	// Err is the failure, if any. A call that legitimately found nothing carries
	// ErrNoResults, which the run records as coverage rather than as failure.
	Err error
}

// Errors a provider may return. Callers distinguish these, because "no results"
// and "could not ask" must never be recorded the same way: the first is
// coverage, the second is a broken run pretending to be a finished one.
var (
	// ErrNoResults means the surface was asked and genuinely had nothing.
	// It is not a failure and must not open a circuit breaker.
	ErrNoResults = errors.New("providers: no results")
	// ErrBudgetExhausted means this provider's allowance is gone. It is
	// expected, not exceptional, and is how a run stays inside its ceiling.
	ErrBudgetExhausted = errors.New("providers: budget exhausted")
	// ErrNotConfigured means the provider has no credentials or configuration
	// and is therefore skipped. This is a deployment state, not a failure.
	ErrNotConfigured = errors.New("providers: not configured")
	// ErrUnsupportedQuery means the provider cannot answer this kind of query.
	ErrUnsupportedQuery = errors.New("providers: unsupported query kind")
	// ErrNotFound means the subject does not exist. It is a real answer, and
	// the subject is definitely absent rather than merely unfound.
	ErrNotFound = errors.New("providers: not found")
	// ErrUpstream reports a failure at the provider or the network below it.
	ErrUpstream = errors.New("providers: upstream failure")
)

// FailureClass says how a provider error should be treated.
type FailureClass string

const (
	// FailureTransient is worth retrying: a timeout, a 429, a 503.
	FailureTransient FailureClass = "transient"
	// FailurePermanent is not worth retrying: a 404, a malformed request, a
	// rejected key. Retrying these is how a run burns its budget doing nothing.
	FailurePermanent FailureClass = "permanent"
	// FailureNotConfigured means the provider is simply not set up, and should
	// be skipped without counting as a failure.
	FailureNotConfigured FailureClass = "not_configured"
)

// Classify decides how an error should be treated, defaulting to transient so an
// unrecognised failure is retried rather than silently dropped. ErrNoResults is
// excluded because it is not a failure at all, and a breaker that counts it as
// one will open on a provider that is working perfectly and has simply
// exhausted this particular query.
func Classify(err error) FailureClass {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrNoResults):
		return ""
	case errors.Is(err, ErrNotConfigured):
		return FailureNotConfigured
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrUnsupportedQuery):
		return FailurePermanent
	case errors.Is(err, ErrBudgetExhausted):
		return FailureTransient
	default:
		return FailureTransient
	}
}

// Provider is one surface discovery can ask.
type Provider interface {
	// Name is the stable identifier used in configuration, lineage, and logs.
	Name() string
	// Ready reports whether the provider is usable right now, and if not, why.
	// A provider that is not ready is skipped rather than called, so a missing
	// API key degrades coverage instead of failing the run.
	Ready() error
	// Search answers one query. It must respect ctx cancellation and must
	// never return a candidate without evidence tying it to the query.
	Search(ctx context.Context, q Query) (Result, error)
}

// Kinds is the optional method a Provider implements to declare which query
// kinds it can answer.
type kindScoped interface {
	Kinds() []Kind
}

// Stats is health and spend for one provider, or for a run when Name is empty.
// Keeping one type means an operator sees the same fields at both levels.
type Stats struct {
	// Name is the provider, empty for a run-level snapshot.
	Name string
	// Calls is total provider calls attempted, or made for a single provider.
	Calls int
	// Succeeded, Failed, Empty, and Truncated partition the calls. Only the
	// per-provider view fills Failed, Empty, and Truncated; a run-level snapshot
	// fills Succeeded and Failed.
	Succeeded int
	Failed    int
	Empty     int
	Truncated int
	// ByProvider is the per-provider detail, populated only on a run snapshot.
	ByProvider map[string]Stats
	// Ready is whether the provider is currently usable.
	Ready bool
	// ReadyError is why it is not, when it is not.
	ReadyError string
	// Failures is how many returned a classified failure.
	Failures int
	// NoResults is how many were genuine empty answers. Tracked separately
	// because a provider that answers every query with nothing is broken, while
	// a provider that answers most of them is merely out of data.
	NoResults int
	// Budget is the call allowance.
	Budget int
	// Spent is how much of the allowance is gone.
	Spent int
	// ConsecutiveFailures feeds the circuit breaker.
	ConsecutiveFailures int
	// LastError is the most recent failure text.
	LastError string
	// LastCall is when the provider was last called.
	LastCall time.Time
	// Duration is the total provider time spent.
	Duration time.Duration
	// CircuitState is the breaker's state, which is the fastest way to see
	// whether a run is degrading.
	CircuitState string
}

// BudgetRemaining reports how many calls the provider may still make.
func (s Stats) BudgetRemaining() int {
	if s.Budget <= 0 {
		return 0
	}
	if s.Spent >= s.Budget {
		return 0
	}
	return s.Budget - s.Spent
}

// Healthy reports whether a provider is worth calling. A provider that is out of
// budget is not unhealthy, it is finished, and conflating the two would make a
// completed run look like a broken one.
func (s Stats) Healthy() bool {
	if !s.Ready {
		return false
	}
	if s.BudgetRemaining() <= 0 {
		return false
	}
	return s.CircuitState != "open"
}

// registryEntry is the mutable per-provider state the runner keeps.
type registryEntry struct {
	provider Provider
	budget   *Budget
	breaker  *breaker
	lastTook time.Duration
	kinds    []Kind
	mu       sync.Mutex
	stats    Stats
}

// Validate checks a provider name and kind for the problems that would otherwise
// surface as confusing runtime failures.
func ValidateName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("providers: name must not be empty")
	case len(name) > 64:
		return fmt.Errorf("providers: name %q is longer than 64 characters", name)
	case strings.ContainsAny(name, " \t\n\x00"):
		return fmt.Errorf("providers: name %q must not contain whitespace", name)
	}
	for _, r := range name {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLower && !isDigit && r != '-' && r != '_' && r != '.' {
			return fmt.Errorf("providers: name %q must be lowercase letters, digits, '-', '_' or '.'", name)
		}
	}
	return nil
}

func validKind(k Kind) bool {
	switch k {
	case KindIndustry, KindTechnology, KindGeography, KindDirectory, KindName, KindCompetitor:
		return true
	default:
		return false
	}
}

// LineageKey builds the ledger key for a provider and query.
func LineageKey(p string, q Query) lineage.Key {
	return lineage.Key{Provider: p, Query: q.Text}
}

// AttemptFrom builds a lineage attempt for a successful result.
func AttemptFrom(runID, provider string, q Query, r Result, took time.Duration, at time.Time) lineage.Attempt {
	outcome := lineage.OutcomeProduced
	switch {
	case r.Truncated:
		// Truncated is checked first for the same reason it is in the runner: a
		// paginated provider returning no rows on a later page still has more to
		// give, and calling that empty marks the query answered.
		outcome = lineage.OutcomeTruncated
	case len(r.Candidates) == 0:
		outcome = lineage.OutcomeEmpty
	}
	return lineage.Attempt{
		RunID:      runID,
		Provider:   provider,
		Query:      q.Text,
		Language:   q.Language,
		Outcome:    outcome,
		Candidates: len(r.Candidates),
		Cursor:     r.Cursor,
		Duration:   took,
		At:         at,
		Detail:     r.Detail,
	}
}
