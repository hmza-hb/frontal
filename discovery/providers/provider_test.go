package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
	platformcircuit "github.com/hmza-hb/lead-intelligence/platform/circuit"
)

// fakeProvider is a scriptable provider for testing the runner's rules.
type fakeProvider struct {
	name     string
	kinds    []Kind
	readyErr error
	mu       sync.Mutex
	queries  []string
	calls    atomic.Int64
	search   func(ctx context.Context, q Query) (Result, error)
	panics   bool
	perCall  time.Duration
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Kinds() []Kind { return f.kinds }

func (f *fakeProvider) Ready() error { return f.readyErr }

func (f *fakeProvider) Search(ctx context.Context, q Query) (Result, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.queries = append(f.queries, q.Text)
	f.mu.Unlock()
	if f.panics {
		panic("provider exploded on a malformed response")
	}
	if f.perCall > 0 {
		select {
		case <-time.After(f.perCall):
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	if f.search != nil {
		return f.search(ctx, q)
	}
	return Result{Candidates: one("Acme", "acme.test", q)}, nil
}

func (f *fakeProvider) callCount() int { return int(f.calls.Load()) }

func (f *fakeProvider) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

// one is a minimal single-candidate result for the default fake behaviour.
func one(name, domain string, q Query) []candidate.Candidate {
	c := candidate.Candidate{Name: name, Domain: domain, URL: "https://" + domain + "/"}
	c.AddEvidence(candidate.Evidence{
		Source:     candidate.SourceSearch,
		Method:     candidate.MethodSearchResult,
		URL:        "https://example.org/" + domain,
		Query:      q.Text,
		ObservedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	})
	return []candidate.Candidate{c}
}

func seedLike(q Query) []candidate.Candidate { return one("Acme", "acme.test", q) }

func newRunner(t *testing.T, cfg RunnerConfig) *Runner {
	t.Helper()
	if cfg.Ledger == nil {
		cfg.Ledger = lineage.NewLedger()
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) }
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRunnerRequiresALedger(t *testing.T) {
	// A run that cannot record what it did cannot be resumed or explained, so
	// the ledger is not optional.
	if _, err := NewRunner(RunnerConfig{}); err == nil {
		t.Fatal("NewRunner accepted a run with no ledger")
	}
}

func TestDuplicateProviderNameIsRejected(t *testing.T) {
	r := newRunner(t, RunnerConfig{})
	if err := r.Register(&fakeProvider{name: "search"}); err != nil {
		t.Fatal(err)
	}
	// A silent replacement would mean one provider's results are attributed to
	// another, and provenance is the whole product.
	if err := r.Register(&fakeProvider{name: "search"}); err == nil {
		t.Fatal("Register accepted a duplicate provider name")
	}
	if got := len(r.ProviderNames()); got != 1 {
		t.Errorf("ProviderNames = %d entries, want 1", got)
	}
}

func TestUnknownProviderNameIsRejected(t *testing.T) {
	for _, bad := range []string{"", " ", "Search", "search provider", "search\n", "séarch", strings.Repeat("a", 65)} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) accepted an invalid name", bad)
		}
	}
	for _, good := range []string{"search", "crt.sh", "rdap", "seed-1", "a_b", "news.v2"} {
		if err := ValidateName(good); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", good, err)
		}
	}
}

func TestPerProviderBudgetStopsCalls(t *testing.T) {
	p := &fakeProvider{name: "search"}
	r := newRunner(t, RunnerConfig{PerProviderCalls: 3, RunBudget: 100})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	calls := make([]Call, 5)
	for i := range calls {
		calls[i] = Call{Provider: "search", Query: Query{Text: fmt.Sprintf("query %d", i), Kind: KindIndustry}}
	}
	results := r.Run(context.Background(), calls)

	// Every call yields a Result so the caller's slice always matches.
	if len(results) != len(calls) {
		t.Fatalf("Run returned %d results for %d calls", len(results), len(calls))
	}
	if p.callCount() != 3 {
		t.Errorf("provider was called %d times, want 3", p.callCount())
	}
	// Which three succeed depends on goroutine scheduling, so the invariant is on
	// the counts rather than on which indices won. A caller that needs a specific
	// call to be answered must run it alone.
	var ok, exhausted int
	for _, res := range results {
		switch {
		case res.Err == nil:
			ok++
		case errors.Is(res.Err, ErrBudgetExhausted):
			exhausted++
		default:
			t.Errorf("unexpected error %v", res.Err)
		}
	}
	if ok != 3 || exhausted != 2 {
		t.Errorf("got %d succeeded and %d budget-exhausted, want 3 and 2", ok, exhausted)
	}
}

func TestRunBudgetStopsCallsAcrossProviders(t *testing.T) {
	// A dozen providers each within their own allowance can still exceed the
	// run ceiling, which is the only bound that actually caps total spend.
	a := &fakeProvider{name: "aaa"}
	b := &fakeProvider{name: "bbb"}
	c := &fakeProvider{name: "ccc"}
	r := newRunner(t, RunnerConfig{PerProviderCalls: 10, RunBudget: 4})
	for _, p := range []*fakeProvider{a, b, c} {
		if err := r.Register(p); err != nil {
			t.Fatal(p)
		}
	}
	var calls []Call
	for i := 0; i < 30; i++ {
		provider := []string{"aaa", "bbb", "ccc"}[i%3]
		calls = append(calls, Call{Provider: provider, Query: Query{Text: fmt.Sprintf("q%d", i), Kind: KindIndustry}})
	}
	r.Run(context.Background(), calls)
	total := a.callCount() + b.callCount() + c.callCount()
	if total > 4 {
		t.Errorf("providers were called %d times, want at most the run budget of 4", total)
	}
	if total == 0 {
		t.Error("the run budget must still allow some work")
	}
}

func TestUnreadyProviderIsSkippedWithoutSpendingBudget(t *testing.T) {
	// A deployment with no search key should still run from its other sources
	// and record that search was never asked, not fail the whole run.
	p := &fakeProvider{name: "search", readyErr: fmt.Errorf("%w: no key", ErrNotConfigured)}
	r := newRunner(t, RunnerConfig{PerProviderCalls: 5})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	results := r.Run(context.Background(), []Call{{Provider: "search", Query: Query{Text: "q"}}})
	if !errors.Is(results[0].Err, ErrNotConfigured) {
		t.Errorf("got %v, want ErrNotConfigured", results[0].Err)
	}
	if p.callCount() != 0 {
		t.Errorf("an unready provider was called %d times", p.callCount())
	}
	// The attempt is recorded as skipped, not failed: nothing went wrong.
	attempts := r.ledger.Attempts()
	if len(attempts) != 1 || attempts[0].Outcome != lineage.OutcomeSkipped {
		t.Errorf("attempt = %+v, want a skipped outcome", attempts)
	}
}

func TestProviderDecliningAKindSpendsNothing(t *testing.T) {
	// The provider answers "no" before any request goes out, so the reservation
	// must be refunded: nothing was spent and the budget should not pretend so.
	p := &fakeProvider{name: "ct", kinds: []Kind{KindName}}
	r := newRunner(t, RunnerConfig{PerProviderCalls: 1})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	results := r.Run(context.Background(), []Call{{Provider: "ct", Query: Query{Text: "acme.com", Kind: KindIndustry}}})
	if !errors.Is(results[0].Err, ErrUnsupportedQuery) {
		t.Errorf("got %v, want ErrUnsupportedQuery", results[0].Err)
	}
	if p.callCount() != 0 {
		t.Errorf("the provider was called %d times, want 0", p.callCount())
	}
	// The budget is still whole, so the kind it does support can run.
	results = r.Run(context.Background(), []Call{{Provider: "ct", Query: Query{Text: "acme.com", Kind: KindName}}})
	if results[0].Err != nil {
		t.Errorf("the supported kind should have run, got %v", results[0].Err)
	}
}

func TestEmptyResultIsNotAFailure(t *testing.T) {
	// "No results" and "could not ask" must never be recorded the same way. A
	// breaker that counts empties as failures opens on a provider that is working
	// perfectly and has simply run out of matches.
	p := &fakeProvider{name: "search", search: func(context.Context, Query) (Result, error) {
		return Result{}, nil
	}}
	r := newRunner(t, RunnerConfig{})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background(), []Call{{Provider: "search", Query: Query{Text: "q", Kind: KindIndustry}}})
	attempts := r.ledger.Attempts()
	if len(attempts) != 1 || attempts[0].Outcome != lineage.OutcomeEmpty {
		t.Fatalf("attempt = %+v, want an empty outcome recorded as coverage", attempts)
	}
	stats := r.Stats()
	if got := stats.ByProvider["search"]; got.Failures != 0 {
		t.Errorf("Failures = %d, want 0 for an empty answer", got.Failures)
	}
	if state := stats.ByProvider["search"].CircuitState; state != "closed" {
		t.Errorf("circuit = %q, want closed: empty answers are not failures", state)
	}
}

func TestNoResultsErrorIsAlsoNotAFailure(t *testing.T) {
	p := &fakeProvider{name: "search", search: func(context.Context, Query) (Result, error) {
		return Result{}, fmt.Errorf("nothing matched: %w", ErrNoResults)
	}}
	r := newRunner(t, RunnerConfig{})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background(), []Call{{Provider: "search", Query: Query{Text: "q", Kind: KindIndustry}}})
	if got := r.Stats().ByProvider["search"]; got.Failures != 0 {
		t.Errorf("Failures = %d, want 0: ErrNoResults is an answer, not a failure", got.Failures)
	}
	if got := r.Stats().ByProvider["search"]; got.NoResults != 1 {
		t.Errorf("NoResults = %d, want 1", got.NoResults)
	}
}

func TestRepeatedFailuresTripTheBreaker(t *testing.T) {
	p := &fakeProvider{name: "search", search: func(context.Context, Query) (Result, error) {
		return Result{}, fmt.Errorf("%w: 503", ErrUpstream)
	}}
	r := newRunner(t, RunnerConfig{Breaker: platformcircuit.Config{
		FailureThreshold: 2, SuccessThreshold: 1, OpenTimeout: time.Minute, HalfOpenMaxCalls: 1,
	}})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	var calls []Call
	for i := 0; i < 6; i++ {
		calls = append(calls, Call{Provider: "search", Query: Query{Text: fmt.Sprintf("q%d", i), Kind: KindIndustry}})
	}
	r.Run(context.Background(), calls)
	// The breaker should stop the calls before the budget is spent on a surface
	// that is visibly broken.
	if p.callCount() >= 6 {
		t.Errorf("provider was called %d times; the breaker should have stopped it", p.callCount())
	}
	if got := r.Stats().ByProvider["search"]; got.CircuitState != "open" {
		t.Errorf("circuit = %q, want open", got.CircuitState)
	}
}

func TestPanickingProviderDoesNotKillTheRun(t *testing.T) {
	// A provider is a thin adapter over a third-party client. One of them
	// panicking on a malformed response must not take down a run that has already
	// spent real money reaching this point.
	bad := &fakeProvider{name: "bad", panics: true}
	good := &fakeProvider{name: "good"}
	r := newRunner(t, RunnerConfig{})
	for _, p := range []*fakeProvider{bad, good} {
		if err := r.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	results := r.Run(context.Background(), []Call{
		{Provider: "bad", Query: Query{Text: "q", Kind: KindIndustry}},
		{Provider: "good", Query: Query{Text: "q", Kind: KindIndustry}},
	})
	if results[0].Err == nil {
		t.Error("the panicking provider should have produced an error")
	}
	if !strings.Contains(results[0].Err.Error(), "bad") {
		t.Errorf("the error should name the provider that panicked, got %v", results[0].Err)
	}
	if results[1].Err != nil {
		t.Errorf("the healthy provider should still have answered, got %v", results[1].Err)
	}
}

func TestAlreadyAnsweredQueryIsNotAskedAgain(t *testing.T) {
	// Re-asking an answered question is how a provider's bill triples for no new
	// information.
	p := &fakeProvider{name: "search"}
	r := newRunner(t, RunnerConfig{})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	call := []Call{{Provider: "search", Query: Query{Text: "acme robots", Kind: KindIndustry}}}
	r.Run(context.Background(), call)
	r.Run(context.Background(), call)
	if p.callCount() != 1 {
		t.Errorf("the provider was called %d times, want 1", p.callCount())
	}
	attempts := r.ledger.Attempts()
	if len(attempts) != 1 {
		t.Errorf("lineage has %d attempts, want 1", len(attempts))
	}
}

func TestTruncatedQueryIsResumedNotRepeated(t *testing.T) {
	page := 0
	p := &fakeProvider{name: "search", search: func(context.Context, Query) (Result, error) {
		page++
		return Result{
			Candidates: nil,
			Truncated:  true,
			Cursor:     fmt.Sprintf("page=%d", page+1),
		}, nil
	}}
	ledger := lineage.NewLedger()
	r := newRunner(t, RunnerConfig{Ledger: ledger})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background(), []Call{{Provider: "search", Query: Query{Text: "acme", Kind: KindIndustry}}})
	if got := ledger.Pending(); len(got) != 1 {
		t.Fatalf("Pending = %v, want the truncated query to be resumable", got)
	}
	run, cursor := ledger.ShouldRun(LineageKey("search", Query{Text: "acme", Kind: KindIndustry}))
	if !run || cursor != "page=2" {
		t.Errorf("ShouldRun = %v, cursor %q; want a resume from page 2", run, cursor)
	}
}

func TestConcurrencyIsRespected(t *testing.T) {
	var inFlight, peak atomic.Int64
	p := &fakeProvider{name: "slow", perCall: 20 * time.Millisecond, search: func(context.Context, Query) (Result, error) {
		cur := inFlight.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inFlight.Add(-1)
		return Result{}, nil
	}}
	r := newRunner(t, RunnerConfig{Concurrency: 3, PerProviderCalls: 50})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	var calls []Call
	for i := 0; i < 20; i++ {
		calls = append(calls, Call{Provider: "slow", Query: Query{Text: fmt.Sprintf("q%d", i), Kind: KindIndustry}})
	}
	r.Run(context.Background(), calls)
	if got := peak.Load(); got > 3 {
		t.Errorf("peak concurrency %d exceeded the limit of 3", got)
	}
}

func TestCancelledContextStillReturnsOneResultPerCall(t *testing.T) {
	// A caller must never have to reconcile a short slice with a set of calls.
	p := &fakeProvider{name: "search"}
	r := newRunner(t, RunnerConfig{})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := []Call{
		{Provider: "search", Query: Query{Text: "a", Kind: KindIndustry}},
		{Provider: "search", Query: Query{Text: "b", Kind: KindIndustry}},
	}
	results := r.Run(ctx, calls)
	if len(results) != len(calls) {
		t.Fatalf("Run returned %d results for %d calls", len(results), len(calls))
	}
	for i, res := range results {
		if !errors.Is(res.Err, context.Canceled) {
			t.Errorf("call %d: got %v, want context.Canceled", i, res.Err)
		}
	}
}

func TestEveryCallStampsTheRunID(t *testing.T) {
	p := &fakeProvider{name: "search", search: func(_ context.Context, q Query) (Result, error) {
		return Result{Candidates: seedLike(q)}, nil
	}}
	r := newRunner(t, RunnerConfig{RunID: "run-42"})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	results := r.Run(context.Background(), []Call{{Provider: "search", Query: Query{Text: "q", Kind: KindIndustry}}})
	if len(results[0].Candidates) != 1 {
		t.Fatalf("got %d candidates", len(results[0].Candidates))
	}
	if results[0].Candidates[0].RunID != "run-42" {
		t.Errorf("RunID = %q, want the runner's run stamped on every candidate", results[0].Candidates[0].RunID)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want FailureClass
	}{
		{nil, ""},
		{ErrNoResults, ""},
		{fmt.Errorf("x: %w", ErrNoResults), ""},
		{ErrNotConfigured, FailureNotConfigured},
		{ErrNotFound, FailurePermanent},
		{ErrUnsupportedQuery, FailurePermanent},
		{ErrBudgetExhausted, FailureTransient},
		{ErrUpstream, FailureTransient},
		{errors.New("something else"), FailureTransient},
	}
	for _, tc := range cases {
		if got := Classify(tc.err); got != tc.want {
			t.Errorf("Classify(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestStatsPartitionTheCalls(t *testing.T) {
	r := newRunner(t, RunnerConfig{PerProviderCalls: 100})
	mk := func(name string, res Result, err error) *fakeProvider {
		return &fakeProvider{name: name, search: func(context.Context, Query) (Result, error) { return res, err }}
	}
	ok := mk("ok", Result{Candidates: seedLike(Query{Text: "q"})}, nil)
	empty := mk("empty", Result{}, nil)
	trunc := mk("trunc", Result{Truncated: true, Cursor: "page=2"}, nil)
	bad := mk("bad", Result{}, ErrUpstream)
	for _, p := range []*fakeProvider{ok, empty, trunc, bad} {
		if err := r.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	r.Run(context.Background(), []Call{
		{Provider: "ok", Query: Query{Text: "a", Kind: KindIndustry}},
		{Provider: "empty", Query: Query{Text: "b", Kind: KindIndustry}},
		{Provider: "trunc", Query: Query{Text: "c", Kind: KindIndustry}},
		{Provider: "bad", Query: Query{Text: "d", Kind: KindIndustry}},
	})
	s := r.Stats()
	if s.Calls != 4 {
		t.Errorf("Calls = %d, want 4", s.Calls)
	}
	if s.Succeeded != 3 || s.Failed != 1 {
		t.Errorf("succeeded=%d failed=%d, want 3 and 1", s.Succeeded, s.Failed)
	}
	if s.Empty != 1 {
		t.Errorf("Empty = %d, want 1", s.Empty)
	}
	if s.Truncated != 1 {
		t.Errorf("Truncated = %d, want 1", s.Truncated)
	}
	if len(s.ByProvider) != 4 {
		t.Errorf("ByProvider has %d entries, want 4", len(s.ByProvider))
	}
	// The per-provider view has to agree with the run total, or an operator
	// cannot tell which surface is truncating its results. A provider that
	// silently returned partial pages forever is exactly the kind of data loss
	// nobody notices until a lead count drops.
	for name, want := range map[string]struct{ calls, empty, truncated int }{
		"ok":    {1, 0, 0},
		"empty": {1, 1, 0},
		"trunc": {1, 0, 1},
		"bad":   {1, 0, 0},
	} {
		ps, ok := s.ByProvider[name]
		if !ok {
			t.Fatalf("ByProvider is missing %s", name)
		}
		if ps.Calls != want.calls {
			t.Errorf("%s: Calls = %d, want %d", name, ps.Calls, want.calls)
		}
		if ps.Empty != want.empty {
			t.Errorf("%s: Empty = %d, want %d", name, ps.Empty, want.empty)
		}
		if ps.Truncated != want.truncated {
			t.Errorf("%s: Truncated = %d, want %d", name, ps.Truncated, want.truncated)
		}
		if ps.Succeeded+ps.Failed != ps.Calls {
			t.Errorf("%s: succeeded %d + failed %d != calls %d", name, ps.Succeeded, ps.Failed, ps.Calls)
		}
	}
}

func TestProviderDeclaringNoKindsIsRegisteredButNeverCalled(t *testing.T) {
	// A provider that says it serves no kind at all is still registered, so its
	// readiness shows up in the stats. But sending it every query would spend
	// budget collecting nothing, which is the whole point of the declaration.
	none := &fakeProvider{name: "certificate", kinds: []Kind{},
		search: func(context.Context, Query) (Result, error) {
			return Result{Candidates: seedLike(Query{Text: "q"})}, nil
		}}
	all := &fakeProvider{name: "search", kinds: []Kind{KindIndustry},
		search: func(context.Context, Query) (Result, error) { return Result{}, ErrNoResults }}
	r := newRunner(t, RunnerConfig{})
	for _, p := range []*fakeProvider{none, all} {
		if err := r.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.Name(), err)
		}
	}
	results := r.Run(context.Background(), []Call{
		{Provider: "certificate", Query: Query{Text: "a", Kind: KindIndustry}},
		{Provider: "search", Query: Query{Text: "b", Kind: KindIndustry}},
	})
	if got := results[0]; got.Err == nil {
		t.Error("a provider that declared it serves no kind answered a query")
	}
	if !errors.Is(results[0].Err, ErrUnsupportedQuery) {
		t.Errorf("err = %v, want ErrUnsupportedQuery so the run records it as not applicable", results[0].Err)
	}
	if n := none.calls.Load(); n != 0 {
		t.Errorf("the undeclared provider was called %d times", n)
	}
	if _, ok := r.Stats().ByProvider["certificate"]; !ok {
		t.Error("the undeclared provider is missing from the stats, so its state is invisible")
	}
}

func TestBudgetReservationSurvivesFailure(t *testing.T) {
	// A call that failed still cost money. Refunding on failure would mean a
	// provider failing every time is free, which is backwards.
	p := &fakeProvider{name: "search", search: func(context.Context, Query) (Result, error) {
		return Result{}, ErrUpstream
	}}
	r := newRunner(t, RunnerConfig{PerProviderCalls: 3})
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	var calls []Call
	for i := 0; i < 5; i++ {
		calls = append(calls, Call{Provider: "search", Query: Query{Text: fmt.Sprintf("q%d", i), Kind: KindIndustry}})
	}
	r.Run(context.Background(), calls)
	if got := r.Stats().ByProvider["search"]; got.Spent != 3 {
		t.Errorf("Spent = %d, want 3: failed calls still cost their budget", got.Spent)
	}
}

func TestBudgetHelpers(t *testing.T) {
	unlimited := NewBudget(0)
	if !unlimited.Unlimited() {
		t.Error("a zero budget should be unlimited")
	}
	for i := 0; i < 100; i++ {
		if !unlimited.Reserve() {
			t.Fatal("an unlimited budget refused a reservation")
		}
	}

	b := NewBudget(2)
	if b.Unlimited() {
		t.Error("a positive budget is not unlimited")
	}
	if !b.Reserve() || !b.Reserve() {
		t.Fatal("a budget of 2 should allow two reservations")
	}
	if b.Reserve() {
		t.Error("a budget of 2 allowed a third reservation")
	}
	if b.Remaining() != 0 {
		t.Errorf("Remaining = %d, want 0", b.Remaining())
	}
	b.Refund()
	if b.Remaining() != 1 {
		t.Errorf("Remaining after refund = %d, want 1", b.Remaining())
	}
	if got := NewBudget(-5).Unlimited(); !got {
		t.Error("a negative budget should normalise to unlimited")
	}
}

func TestBudgetIsConcurrencySafe(t *testing.T) {
	b := NewBudget(50)
	var wg sync.WaitGroup
	var ok atomic.Int64
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if b.Reserve() {
					ok.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	// Oversubscription must not let more calls through than the ceiling, which is
	// the entire reason the check and the increment happen under one lock.
	if got := ok.Load(); got != 50 {
		t.Errorf("%d reservations succeeded, want exactly 50", got)
	}
}
