package providers

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
	platformcircuit "github.com/hmza-hb/lead-intelligence/platform/circuit"
	platformratelimit "github.com/hmza-hb/lead-intelligence/platform/ratelimit"
)

// Runner calls providers under a shared run budget.
//
// Two limits apply to every call and they are not the same limit. The per-provider
// budget stops one expensive surface from consuming the run; the run-wide budget
// stops the total from exceeding what the operator agreed to pay. A provider that
// respects only its own budget can still bankrupt a run, because a dozen
// providers each within their allowance can add up to far more than the ceiling.
type Runner struct {
	mu      sync.RWMutex
	entries map[string]*registryEntry
	order   []string
	ledger  *lineage.Ledger
	runID   string

	// runBudget is the ceiling across every provider. Zero means unlimited.
	runBudget *Budget
	// limiters rate-limit outbound calls per provider. A nil entry is unlimited.
	limiters map[string]func(context.Context) error
	// perProviderCalls, rate, burst, and breakerCfg come from RunnerConfig and
	// are read by Register.
	perProviderCalls int
	rate             float64
	burst            int
	breakerCfg       platformcircuit.Config
	concurrency      int
	// now is injectable so tests can assert timings without sleeping.
	now func() time.Time

	// total is the run-wide call count, incremented before each call.
	totalMu sync.Mutex
	total   int

	stats statsAcc
}

// statsAcc aggregates counters for the run.
type statsAcc struct {
	mu         sync.Mutex
	attempts   int
	succeeded  int
	failed     int
	empty      int
	truncated  int
	byProvider map[string]*providerAcc
}

type providerAcc struct {
	calls     int
	failures  int
	noResults int
	empty     int
	truncated int
	duration  time.Duration
}

// RunnerConfig configures a Runner.
type RunnerConfig struct {
	// RunID scopes the lineage records and is stamped on candidates.
	RunID string
	// Ledger records every attempt. Required: a run that cannot record what it
	// did cannot be resumed or explained.
	Ledger *lineage.Ledger
	// RunBudget is the total call ceiling across all providers. Zero is
	// unlimited.
	RunBudget int
	// PerProviderCalls is each provider's own allowance. Zero means unlimited,
	// which is only correct for free providers.
	PerProviderCalls int
	// Concurrency is how many provider calls may be in flight. Zero or negative
	// means one.
	Concurrency int
	// RatePerSecond and RateBurst apply per provider, derived from each provider's
	// documented limits. Zero disables rate limiting.
	RatePerSecond float64
	RateBurst     int
	// Breaker configures the per-provider circuit breaker.
	Breaker platformcircuit.Config
	// Now is the clock, injectable for tests.
	Now func() time.Time
}

// NewRunner returns a Runner.
func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Ledger == nil {
		return nil, errors.New("providers: a ledger is required so the run can be resumed and explained")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Breaker == (platformcircuit.Config{}) {
		cfg.Breaker = platformcircuit.DefaultConfig()
	}
	return &Runner{
		entries:          map[string]*registryEntry{},
		ledger:           cfg.Ledger,
		runID:            cfg.RunID,
		runBudget:        NewBudget(cfg.RunBudget),
		limiters:         map[string]func(context.Context) error{},
		now:              cfg.Now,
		perProviderCalls: cfg.PerProviderCalls,
		rate:             cfg.RatePerSecond,
		burst:            cfg.RateBurst,
		breakerCfg:       cfg.Breaker,
		concurrency:      cfg.Concurrency,
		stats:            statsAcc{byProvider: map[string]*providerAcc{}},
	}, nil
}

// Register adds a provider under the runner's budget, rate limit, and breaker
// configuration. It returns an error for a duplicate name or an invalid name
// rather than silently replacing an existing provider, because attribution of
// results to the wrong surface would corrupt provenance.
func (r *Runner) Register(p Provider) error {
	name := p.Name()
	if err := ValidateName(name); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("providers: duplicate provider name %q", name)
	}
	e := &registryEntry{
		provider: p,
		budget:   NewBudget(r.perProviderCalls),
		breaker:  newBreaker(r.breakerCfg),
		stats:    Stats{Name: name},
	}
	if ks, ok := p.(kindScoped); ok {
		for _, k := range ks.Kinds() {
			if !validKind(k) {
				return fmt.Errorf("providers: %s declares unknown query kind %q", name, k)
			}
		}
		e.kinds = ks.Kinds()
	}
	if err := p.Ready(); err != nil {
		e.stats.Ready = false
		e.stats.ReadyError = err.Error()
	} else {
		e.stats.Ready = true
	}
	if r.rate > 0 {
		lim := platformratelimit.New(r.rate, r.burst)
		r.limiters[name] = lim.Wait
	}
	r.entries[name] = e
	r.order = append(r.order, name)
	return nil
}

// Call is one provider call requested by a Run.
type Call struct {
	// Provider is the provider name.
	Provider string
	// Query is the question.
	Query Query
	// Cursor resumes a paginated query.
	Cursor string
}

// Run executes every call under the shared budget and concurrency limit and
// returns one Result per call, in the order the calls were given. Every call
// produces exactly one Result, so a caller never has to reconcile a short slice
// with a set of calls.
func (r *Runner) Run(ctx context.Context, calls []Call) []Result {
	results := make([]Result, len(calls))
	if len(calls) == 0 {
		return results
	}
	conc := r.concurrency
	if conc < 1 {
		conc = 1
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i, call := range calls {
		// Respect cancellation by not starting new work, but every index is
		// still filled so the caller's slice length always matches the calls.
		if ctx.Err() != nil {
			results[i] = Result{Provider: call.Provider, Query: call.Query, Err: ctx.Err()}
			continue
		}
		wg.Add(1)
		go func(i int, call Call) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = Result{Provider: call.Provider, Query: call.Query, Err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			results[i] = r.runOne(ctx, call)
		}(i, call)
	}
	wg.Wait()
	return results
}

// runOne executes a single call end to end, recording lineage.
func (r *Runner) runOne(ctx context.Context, call Call) Result {
	res := Result{Provider: call.Provider, Query: call.Query}
	if shouldRun, _ := r.ledger.ShouldRun(LineageKey(call.Provider, call.Query)); !shouldRun {
		// The ledger already has a trustworthy answer for this exact query. Do
		// not re-ask; re-asking an answered question is how a provider's bill
		// triples for no new information.
		res.Err = fmt.Errorf("%w: %s already answered this query", ErrNoResults, call.Provider)
		return res
	}
	entry, err := r.entry(call.Provider)
	if err != nil {
		r.recordFailure(call, err, 0)
		res.Err = err
		return res
	}
	if !r.reserveRun() {
		err = fmt.Errorf("%w: run budget of %d calls spent", ErrBudgetExhausted, r.runBudget.total)
		r.recordFailure(call, err, 0)
		res.Err = err
		return res
	}

	query := call.Query
	query.Cursor = call.Cursor
	searchRes, searchErr := entry.runOne(ctx, r.runID, query, r.waiter(call.Provider))
	took := entry.lastDuration()
	searchRes.Provider = call.Provider
	searchRes.Query = call.Query
	r.record(call, searchRes, searchErr, took)
	if searchErr != nil {
		res.Err = searchErr
		return res
	}
	res.Candidates = searchRes.Candidates
	for i := range res.Candidates {
		// Stamp the run and the source so every candidate is attributable even if
		// a provider forgot, and so evidence written by the runner cannot be
		// forged by a provider naming a different run.
		if res.Candidates[i].RunID == "" {
			res.Candidates[i].RunID = r.runID
		}
	}
	return res
}

func (r *Runner) entry(name string) (*registryEntry, error) {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: unknown provider %q", ErrNotConfigured, name)
	}
	return e, nil
}

func (r *Runner) waiter(name string) func(context.Context) error {
	r.mu.RLock()
	w, ok := r.limiters[name]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return w
}

// reserveRun claims one call from the run-wide budget.
func (r *Runner) reserveRun() bool {
	return r.runBudget.Reserve()
}

// record updates lineage and counters after a call.
func (r *Runner) record(call Call, res Result, err error, took time.Duration) {
	now := r.now()
	// Classification is shared with the per-provider counter so the two levels
	// cannot disagree. An ErrNoResults is an answer, and counting it as a failure
	// at run level would make a healthy run look broken.
	class := Classify(err)
	r.stats.mu.Lock()
	r.stats.attempts++
	switch {
	case err == nil:
		r.stats.succeeded++
	case class == FailureTransient || class == FailurePermanent:
		r.stats.failed++
	default:
		// An empty answer or an unconfigured provider is not a failure.
	}
	pa := r.stats.byProvider[call.Provider]
	if pa == nil {
		pa = &providerAcc{}
		r.stats.byProvider[call.Provider] = pa
	}
	pa.calls++
	pa.duration += took
	if err == nil {
		// Truncated and empty are recorded per provider as well as per run. A
		// provider that answers every query with a truncated page is a different
		// problem from one that does it for every other provider, and the run
		// total alone cannot tell them apart.
		switch {
		case res.Truncated:
			r.stats.truncated++
			pa.truncated++
		case len(res.Candidates) == 0:
			r.stats.empty++
			pa.empty++
		}
	}
	// Only a classified failure counts. An empty answer and an unconfigured
	// provider are both non-failures, and the run-level total must agree with the
	// per-provider count.
	if class == FailureTransient || class == FailurePermanent {
		pa.failures++
	}
	if errors.Is(err, ErrNoResults) {
		pa.noResults++
	}
	r.stats.mu.Unlock()

	attempt := lineage.Attempt{
		RunID:    r.runID,
		Provider: call.Provider,
		Query:    call.Query.Text,
		Language: call.Query.Language,
		Duration: took,
		At:       now,
	}
	switch {
	case err != nil:
		attempt.Outcome = classifyOutcome(err)
		if attempt.Outcome != lineage.OutcomeSkipped {
			attempt.Error = err.Error()
		}
	case res.Truncated:
		// Checked before the empty case on purpose: a paginated provider that
		// returns no rows on page two still has more to give, and recording it as
		// empty would mark the query answered so a resumed run skips it.
		attempt.Outcome = lineage.OutcomeTruncated
		attempt.Candidates = len(res.Candidates)
		attempt.Cursor = res.Cursor
	case len(res.Candidates) == 0:
		attempt.Outcome = lineage.OutcomeEmpty
	default:
		attempt.Outcome = lineage.OutcomeProduced
		attempt.Candidates = len(res.Candidates)
	}
	// A redacted or malformed attempt is dropped rather than fatal: losing one
	// lineage row is better than losing a whole run over a provider's error
	// string. The lost coverage is visible in the count, not silent.
	_ = r.ledger.Record(attempt)
}

func (r *Runner) recordFailure(call Call, err error, took time.Duration) {
	r.record(call, Result{}, err, took)
}

func classifyOutcome(err error) lineage.Outcome {
	switch {
	case errors.Is(err, ErrBudgetExhausted):
		return lineage.OutcomeBudgeted
	case errors.Is(err, ErrUnsupportedQuery), errors.Is(err, ErrNotConfigured):
		return lineage.OutcomeSkipped
	default:
		return lineage.OutcomeFailed
	}
}

// Ledger returns the run's lineage ledger, which is the authoritative record of
// what was asked, what came back, and what it cost.
//
// Callers that persist lineage should read it from here rather than
// reconstructing attempts from the results: the ledger holds the classification,
// the duration and the per-call error, and a second set of rules for deriving
// them can only drift from the one the runner actually applied.
func (r *Runner) Ledger() *lineage.Ledger { return r.ledger }

// Stats snapshots the run.
func (r *Runner) Stats() Stats {
	r.stats.mu.Lock()
	s := Stats{
		Calls:      r.stats.attempts,
		Succeeded:  r.stats.succeeded,
		Failed:     r.stats.failed,
		Empty:      r.stats.empty,
		Truncated:  r.stats.truncated,
		ByProvider: map[string]Stats{},
	}
	byProvider := make(map[string]*providerAcc, len(r.stats.byProvider))
	for k, v := range r.stats.byProvider {
		byProvider[k] = v
	}
	r.stats.mu.Unlock()

	r.mu.RLock()
	entries := make(map[string]*registryEntry, len(r.entries))
	for k, v := range r.entries {
		entries[k] = v
	}
	r.mu.RUnlock()
	for name, e := range entries {
		e.mu.Lock()
		ps := e.stats
		e.mu.Unlock()
		if pa := byProvider[name]; pa != nil {
			ps.Calls = pa.calls
			// Both spellings are filled. Stats carries a run-level Failed and a
			// per-provider Failures, and only one of them being set per provider
			// means a consumer that reaches for the other reads zero and concludes
			// a broken provider is healthy.
			ps.Failed = pa.failures
			ps.Failures = pa.failures
			ps.Succeeded = pa.calls - pa.failures
			ps.NoResults = pa.noResults
			ps.Empty = pa.empty
			ps.Truncated = pa.truncated
			ps.Duration = pa.duration
		}
		ps.CircuitState = e.breaker.State()
		ps.Budget = e.budget.total
		ps.Spent = e.budget.Spent()
		s.ByProvider[name] = ps
	}
	return s
}

// ProviderNames lists registered providers in registration order.
func (r *Runner) ProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// recoverPanic converts a provider panic into an error. A provider is third-party
// or a thin adapter over a third-party library, and one of them panicking on a
// malformed response must not take down a run that has spent real money reaching
// this point.
func recoverPanic(provider string, recovered any) error {
	return fmt.Errorf("providers: %s panicked: %v\n%s", provider, recovered, debug.Stack())
}
