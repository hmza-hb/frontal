package providers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	platformcircuit "github.com/hmza-hb/lead-intelligence/platform/circuit"
)

// Budget is a provider's call allowance.
//
// The reservation happens before the call and is not given back, because the
// whole point of a budget is to bound what a provider can cost, and a call that
// failed still cost money. Refunding on failure would mean a provider failing
// every time is free, which is exactly backwards.
type Budget struct {
	mu    sync.Mutex
	total int
	spent int
}

// NewBudget returns a budget of n calls. A non-positive n means unlimited, which
// is only appropriate for a provider that costs nothing: seed files, and
// certificate transparency, which is free but rate-limited.
func NewBudget(n int) *Budget {
	if n < 0 {
		n = 0
	}
	return &Budget{total: n}
}

// Unlimited reports whether the budget places no ceiling.
func (b *Budget) Unlimited() bool { return b != nil && b.total == 0 }

// Reserve claims one call, returning false when the allowance is gone.
func (b *Budget) Reserve() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total > 0 && b.spent >= b.total {
		return false
	}
	b.spent++
	return true
}

// Spent reports how many calls have been reserved.
func (b *Budget) Spent() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent
}

// Remaining reports how many calls are left, or a negative number when the budget
// is unlimited.
func (b *Budget) Remaining() int {
	if b == nil || b.total == 0 {
		return -1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spent >= b.total {
		return 0
	}
	return b.total - b.spent
}

// Refund returns a reservation. It is only correct when a call was never made,
// which the runner uses to avoid spending budget on a query a provider declined
// to answer before any request went out.
func (b *Budget) Refund() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total > 0 && b.spent > 0 {
		b.spent--
	}
}

// breaker wraps the platform circuit breaker with the classification discovery
// needs: a provider that returns ErrNoResults is not failing, however many
// consecutive empty answers it gives.
type breaker struct {
	b *platformcircuit.Breaker
}

func newBreaker(cfg platformcircuit.Config) *breaker {
	return &breaker{b: platformcircuit.New(cfg)}
}

func (br *breaker) Allow() (func(bool), error) {
	if br == nil || br.b == nil {
		return func(bool) {}, nil
	}
	return br.b.Allow()
}

func (br *breaker) State() string {
	if br == nil || br.b == nil {
		return "closed"
	}
	return br.b.State().String()
}

func (br *breaker) isFailure(err error) bool {
	return Classify(err) == FailureTransient
}

// registry is the set of providers a run may call, with per-provider budget,
// rate limiting, and health.
type registry struct {
	mu      sync.RWMutex
	entries map[string]*registryEntry
	order   []string
}

// register adds a provider. It fails on a duplicate name because a silent
// replacement would mean one provider's results are attributed to another, and
// provenance is the whole product.
func (r *registry) register(p Provider, budget *Budget, cfg platformcircuit.Config) error {
	if p == nil {
		return fmt.Errorf("providers: %w: nil provider", ErrNotConfigured)
	}
	name := p.Name()
	if err := ValidateName(name); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]*registryEntry{}
	}
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("providers: duplicate provider name %q", name)
	}
	e := &registryEntry{
		provider: p,
		budget:   budget,
		breaker:  newBreaker(cfg),
		stats:    Stats{Name: name, Budget: budgetCallBudget(budget)},
	}
	if ks, ok := p.(kindScoped); ok {
		for _, k := range ks.Kinds() {
			if !validKind(k) {
				return fmt.Errorf("providers: %s declares unknown query kind %q", name, k)
			}
		}
		// Copied explicitly so a provider that returns an empty non-nil slice is
		// still distinguishable from one that does not implement Kinds at all.
		e.kinds = append([]Kind(nil), ks.Kinds()...)
		if ks.Kinds() != nil && len(e.kinds) == 0 {
			e.kinds = []Kind{}
		}
	}
	if err := p.Ready(); err != nil {
		e.stats.Ready = false
		e.stats.ReadyError = err.Error()
	} else {
		e.stats.Ready = true
	}
	r.entries[name] = e
	r.order = append(r.order, name)
	return nil
}

func budgetCallBudget(b *Budget) int {
	if b == nil {
		return 0
	}
	return b.total
}

// lookup returns an entry, checking readiness and budget before the caller pays
// for anything.
func (r *registry) lookup(name string) (*registryEntry, error) {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("providers: %w: unknown provider %q", ErrNotConfigured, name)
	}
	return e, nil
}

// reserve takes one call from a provider's budget.
func (e *registryEntry) reserve() error {
	if !e.budget.Reserve() {
		return fmt.Errorf("%w: %s has spent its allowance of %d calls", ErrBudgetExhausted, e.provider.Name(), e.budget.total)
	}
	return nil
}

// lastDuration reports the most recent call's duration.
func (e *registryEntry) lastDuration() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastTook
}

// record updates the health counters after a call.
func (e *registryEntry) record(err error, took time.Duration, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastTook = took
	e.stats.Calls++
	e.stats.Duration += took
	e.stats.LastCall = at
	switch Classify(err) {
	case "":
		// Not a failure at all.
		if errors.Is(err, ErrNoResults) {
			e.stats.NoResults++
		}
	case FailureNotConfigured:
		e.stats.Ready = false
		e.stats.ReadyError = err.Error()
	default:
		e.stats.Failures++
		e.stats.ConsecutiveFailures++
		e.stats.LastError = err.Error()
		e.stats.CircuitState = e.breaker.State()
	}
}

// limiterWait blocks until a provider's rate limiter allows a call.
type limiterWait func(ctx context.Context) error

// runOne executes a single provider call with budget, breaker, rate limiting,
// and lineage recording. Everything a provider does goes through here, so there
// is exactly one place where the rules are enforced.
func (e *registryEntry) runOne(ctx context.Context, runID string, q Query, wait limiterWait) (Result, error) {
	name := e.provider.Name()
	if err := e.provider.Ready(); err != nil {
		return Result{}, err
	}
	if !e.applies(q.Kind) {
		// Declined before any request went out, so the reservation is returned:
		// nothing was spent and the budget should not pretend otherwise.
		return Result{}, fmt.Errorf("%w: %s cannot answer %s queries", ErrUnsupportedQuery, name, q.Kind)
	}
	if err := e.reserve(); err != nil {
		return Result{}, err
	}

	release, err := e.breaker.Allow()
	if err != nil {
		return Result{}, err
	}
	if wait != nil {
		if err := wait(ctx); err != nil {
			release(false)
			return Result{}, err
		}
	}

	start := time.Now()
	res, searchErr := e.callSearch(ctx, q)
	took := time.Since(start)
	e.record(searchErr, took, time.Now())
	release(searchErr == nil || !e.breaker.isFailure(searchErr))
	return res, searchErr
}

// callSearch invokes the provider, converting a panic into an error. A provider
// is a thin adapter over a third-party client, and one of them panicking on a
// malformed response must not take down a run that has already spent real money
// getting here.
func (e *registryEntry) callSearch(ctx context.Context, q Query) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = Result{}, recoverPanic(e.provider.Name(), r)
		}
	}()
	return e.provider.Search(ctx, q)
}

func (e *registryEntry) applies(k Kind) bool {
	// e.kinds is nil for a provider that does not declare kinds at all, and empty
	// for one that declares none. The first is given the benefit of the doubt; the
	// second is a statement that it serves nothing.
	if e.kinds == nil {
		return true
	}
	if len(e.kinds) == 0 {
		return false
	}
	for _, want := range e.kinds {
		if want == k {
			return true
		}
	}
	return false
}
