// Package service orchestrates a discovery run.
//
// The service is the only place that knows the order of operations: take seeds,
// ask providers bounded questions, merge what comes back, expand, rank, and
// persist. Everything it does is driven by a budget and by the lineage ledger,
// because a discovery run is long-running, costs money, and has to be resumable
// and explainable rather than a one-shot query.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	platformcircuit "github.com/hmza-hb/lead-intelligence/platform/circuit"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/config"
	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
	"github.com/hmza-hb/lead-intelligence/discovery/persistence"
	"github.com/hmza-hb/lead-intelligence/discovery/providers"
	"github.com/hmza-hb/lead-intelligence/discovery/query"
	"github.com/hmza-hb/lead-intelligence/discovery/ranking"
)

// Errors a caller can act on.
var (
	// ErrNoSeeds is returned when a run has nothing to start from and no profile
	// to ask questions with. Refusing here is better than a run that spends
	// budget proving it has nothing to look for.
	ErrNoSeeds = errors.New("service: no seeds and no profile to search from")
	// ErrUnknownProvider is returned when the enabled-provider list names a
	// provider that was not registered. Failing loudly is the point: a typo in
	// the enabled list that silently disables a surface costs the operator a week
	// of missing data before anyone notices.
	ErrUnknownProvider = errors.New("service: unknown provider")
)

// Service runs discovery.
type Service struct {
	cfg    config.Config
	store  persistence.Store
	runner *providers.Runner
	gen    *query.Generator
	// baseRanker is the per-run ranker's starting configuration. The targeting
	// terms are filled in per run from the profile.
	baseRanker ranking.Config
	now        func() time.Time
	log        *slog.Logger
	// provs are the surfaces available to a run. The runner is per-run because
	// its lineage is, but the provider set is per-service.
	provs []providers.Provider
}

// Options configures a Service.
type Options struct {
	// Config is the validated configuration. Required.
	Config config.Config
	// Store is where runs, candidates, evidence, and lineage are written.
	// Required: a run that cannot record what it did cannot be explained or
	// resumed.
	Store persistence.Store
	// Providers are the surfaces to register with the runner. Every one of them
	// is registered, including those with no credentials, so an unconfigured
	// provider reports itself unready in the run's stats rather than vanishing.
	Providers []providers.Provider
	// Now is the clock, injectable so tests do not depend on wall-clock time.
	Now func() time.Time
	// Log receives run-level progress. Nil uses the default logger.
	Log *slog.Logger
}

// New builds a Service.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("service: a store is required so the run can be recorded and resumed")
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}

	rankerCfg := ranking.Config{
		AcceptThreshold:        opts.Config.Rank.AcceptThreshold,
		ReviewBand:             opts.Config.Rank.ReviewBand,
		MaxCandidatesPerDomain: opts.Config.Rank.MaxCandidatesPerDomain,
		Freshness:              FreshnessHalfLife,
		Now:                    opts.Now,
	}

	return &Service{
		cfg:   opts.Config,
		store: opts.Store,
		gen: query.New(query.Limits{
			MaxPerCandidate:     opts.Config.Query.MaxPerCandidate,
			MaxPerRun:           opts.Config.Query.MaxPerRun,
			MaxCitiesPerCountry: opts.Config.Query.MaxCitiesPerCountry,
			// The generator's own depth bound is deliberately loose: the service
			// drives expansion rounds itself so it can persist each round's
			// lineage before starting the next. A second, conflicting notion of
			// how deep a run goes would be a bug waiting to be found.
			MaxDepth: -1,
		}),
		baseRanker: rankerCfg,
		now:        opts.Now,
		log:        opts.Log,
		provs:      opts.Providers,
	}, nil
}

// FreshnessHalfLife is how long before a candidate's freshness score halves. A
// quarter is the working assumption that a company described accurately three
// months ago is roughly half as trustworthy as one described this morning.
const FreshnessHalfLife = 90 * 24 * time.Hour

// effectiveProfile applies the configured targeting defaults to a request
// profile.
//
// The request wins wherever it says something, so an API caller can override a
// deployment's defaults per run. The configuration fills in only what the
// request left blank, which means a configured language list applies to every
// run rather than being silently ignored because the profile omitted it.
func (s *Service) effectiveProfile(p query.Profile) query.Profile {
	if len(p.Languages) == 0 {
		p.Languages = s.cfg.Query.Languages
	}
	return p
}

// newRanker builds the ranker for one run. It is per-run rather than per-service
// because the targeting terms come from the profile, and two runs against
// different profiles must not share a scoring opinion.
func (s *Service) newRanker(p query.Profile) *ranking.Ranker {
	cfg := s.baseRanker
	cfg.Industries = p.Industries
	cfg.Keywords = p.Keywords
	cfg.Countries = p.Countries
	cfg.Exclude = p.Exclude
	return ranking.New(cfg)
}

// NewRun builds the runner and registers the providers. It is separate from
// New because the run ID scopes the lineage records, and the run ID does not
// exist until the run row is created.
//
// A provider is registered even when it is not ready. Readiness is the
// provider's own report, and a run's stats are how an operator finds out that
// the search key is missing; a provider that silently does not appear is a
// problem nobody ever investigates.
func (s *Service) NewRun(runID string) (*providers.Runner, error) {
	runner, err := providers.NewRunner(providers.RunnerConfig{
		RunID:            runID,
		Ledger:           lineage.NewLedger(),
		RunBudget:        s.cfg.Run.MaxProviderCalls,
		PerProviderCalls: s.cfg.Prov.PerProviderCalls,
		Concurrency:      s.cfg.Run.Concurrency,
		Breaker:          platformcircuit.DefaultConfig(),
		Now:              s.now,
	})
	if err != nil {
		return nil, err
	}
	names := s.enabledProviders()
	for _, p := range s.provs {
		if !names[allProviders] && !names[p.Name()] {
			continue
		}
		if err := runner.Register(p); err != nil {
			return nil, fmt.Errorf("service: register %s: %w", p.Name(), err)
		}
	}
	// A name in the enabled list that matches no provider is a configuration
	// error, not an empty string to ignore. Disabled names are exempt: excluding a
	// surface that happens not to be built is the point of excluding it.
	registered := map[string]bool{}
	for _, n := range runner.ProviderNames() {
		registered[n] = true
	}
	disabled := map[string]bool{}
	for _, n := range s.cfg.Prov.Disabled {
		disabled[n] = true
	}
	for _, want := range s.cfg.Prov.Enabled {
		if registered[want] || disabled[want] {
			continue
		}
		return nil, fmt.Errorf("%w: %s (registered: %s)", ErrUnknownProvider, want, strings.Join(runner.ProviderNames(), ", "))
	}
	return runner, nil
}

// toProviderQuery converts a generated query into the provider-facing shape.
//
// The two packages classify queries at different resolutions: the generator
// distinguishes a profile term from a localized one, while a provider only
// needs to know whether it has an opinion about the question. Mapping here
// rather than in either package keeps the generator free of the provider
// contract and lets a provider ignore kinds it cannot serve.
func toProviderQuery(q query.Query, limit int) providers.Query {
	return providers.Query{
		Text:     q.Text,
		Language: q.Language,
		Kind:     toProviderKind(q.Kind),
		Country:  q.Country,
		Limit:    limit,
	}
}

// toProviderKind collapses a generated kind onto the provider contract's
// coarser one. An unmapped kind becomes an empty kind, which no provider claims,
// so the query is skipped rather than sent to a surface that would misread it.
func toProviderKind(k query.Kind) providers.Kind {
	switch k {
	case query.KindDirect:
		return providers.KindName
	case query.KindIndustry, query.KindProfile, query.KindLocal:
		return providers.KindIndustry
	case query.KindCity, query.KindCountry:
		return providers.KindGeography
	case query.KindTechnology:
		return providers.KindTechnology
	case query.KindDirectory:
		return providers.KindDirectory
	case query.KindCompetitor:
		return providers.KindCompetitor
	default:
		return ""
	}
}

// enabledProviders resolves the enabled/disabled lists into a name set.
//
// An empty Enabled list means every provider, which is what the configuration
// documents and what makes a fresh deployment work with no configuration at all.
// Returning an empty set for that case would silently register nothing and
// produce a run that "succeeds" while discovering nothing.
func (s *Service) enabledProviders() map[string]bool {
	names := map[string]bool{}
	if len(s.cfg.Prov.Enabled) == 0 {
		names[allProviders] = true
	}
	for _, n := range s.cfg.Prov.Enabled {
		names[n] = true
	}
	for _, n := range s.cfg.Prov.Disabled {
		delete(names, n)
	}
	return names
}

// allProviders is the sentinel key meaning "no allowlist was given".
const allProviders = "\x00*"

// isEnabled reports whether a provider name is in scope for a run.
func (s *Service) isEnabled(name string) bool {
	names := s.enabledProviders()
	return names[allProviders] || names[name]
}

// Request describes one run.
type Request struct {
	// Profile drives the questions. Its root queries search the market for
	// companies matching the targeting, which is how a run discovers companies
	// the operator never heard of.
	Profile query.Profile
	// Seeds are the candidates the operator already knows about. They are the
	// starting frontier, and a run may consist of seeds alone.
	Seeds []candidate.Candidate
	// MaxDepth overrides the configured expansion depth when positive. Zero uses
	// the configuration.
	MaxDepth int
}

// Report is the outcome of a run.
// Report is the outcome of a run. The JSON tags are the API contract.
type Report struct {
	// RunID is the run this report describes.
	RunID string `json:"id"`
	// Status mirrors the stored run status.
	Status string `json:"status"`
	// Candidates is everything found, ranked, in rank order.
	Candidates []Scored `json:"candidates"`
	// Accepted and Rejected count the verdicts.
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
	// QueriesRun and QueriesPending count the questions asked and the ones left
	// unfinished. A non-zero Pending is the signal to resume.
	QueriesRun     int `json:"queries_run"`
	QueriesPending int `json:"queries_pending"`
	// ProviderCalls and ProviderFailures are the run's cost and health.
	ProviderCalls    int `json:"provider_calls"`
	ProviderFailures int `json:"provider_failures"`
	// BudgetSpent is how much of the call allowance was consumed.
	BudgetSpent int `json:"budget_spent"`
	// ProviderStats is the per-provider breakdown, for the run report.
	ProviderStats map[string]providers.Stats `json:"providers"`
	// StartedAt and FinishedAt bracket the run.
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// Error explains a failed run.
	Error string `json:"error,omitempty"`
}

// Scored is a ranked candidate as the run produced it.
type Scored struct {
	ID        string                   `json:"id"`
	Candidate candidate.Candidate      `json:"candidate"`
	Score     float64                  `json:"score"`
	Verdict   string                   `json:"verdict"`
	Explain   string                   `json:"explain,omitempty"`
	Factors   []persistence.RankFactor `json:"factors,omitempty"`
	Rank      int                      `json:"rank"`
}

// RunStatus values written to the store.
const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Run executes a discovery run end to end and records everything it did.
//
// A run that returns an error has still been written: the run row is closed as
// failed or cancelled and the lineage recorded so far is durable, so the
// operator can see how far it got and resume rather than start again.
func (s *Service) Run(ctx context.Context, req Request) (*Report, error) {
	started := s.now()
	if req.Profile.Name == "" && len(req.Seeds) == 0 {
		return nil, ErrNoSeeds
	}
	profile := s.effectiveProfile(req.Profile)

	depth := req.MaxDepth
	if depth <= 0 {
		depth = s.cfg.Run.MaxDepth
	}
	if depth < 0 {
		depth = 0
	}

	// The run row comes first so every subsequent write has a run ID to hang off.
	// A run that fails during start-up is still a run someone will ask about.
	runID, err := s.store.CreateRun(ctx, persistence.Run{
		ProfileName: req.Profile.Name,
		SeedsTotal:  len(req.Seeds),
		MaxDepth:    depth,
		BudgetLimit: s.cfg.Run.MaxProviderCalls,
		Config:      s.redactedRunConfig(req),
		StartedAt:   started,
	})
	if err != nil {
		return nil, err
	}

	report := &Report{RunID: runID, Status: StatusRunning, StartedAt: started, ProviderStats: map[string]providers.Stats{}}

	runner, err := s.NewRun(runID)
	if err != nil {
		return s.abort(ctx, report, fmt.Errorf("service: build runner: %w", err))
	}

	// The frontier is what the next round expands. Seeds start it; anything a
	// provider turns up with a domain we have not seen starts a new one.
	set, frontier := s.seed(req.Seeds)

	// deferred counts questions the run wanted to ask but could not, which is
	// what tells an operator a run is worth resuming.
	deferred := 0

	var errs []error
	for round := 0; round <= depth; round++ {
		if ctx.Err() != nil {
			return s.abort(ctx, report, ctx.Err())
		}
		calls := s.buildCalls(runner, profile, frontier, round, set.count())
		if len(calls) == 0 {
			s.log.Info("discovery: no queries left to ask",
				"run_id", runID, "round", round)
			break
		}
		// The budget is a run-level ceiling, so it is checked before asking
		// rather than after. Anything not asked because of it is deferred work,
		// not a failure.
		if limit := s.cfg.Run.MaxProviderCalls; limit > 0 {
			spent := runner.Stats().Succeeded + runner.Stats().Failed
			if spent >= limit {
				deferred += len(calls)
				s.log.Info("discovery: run budget reached",
					"run_id", runID, "round", round, "spent", spent, "limit", limit)
				break
			}
			if remaining := limit - spent; remaining < len(calls) {
				// Deferred work is what was not asked, which is the calls
				// dropped here rather than the ones kept. Counting the kept
				// ones would understate pending work and send a resuming run
				// out believing questions had been answered.
				deferred += len(calls) - remaining
				calls = calls[:remaining]
			}
		}

		s.log.Info("discovery: asking providers",
			"run_id", runID, "round", round, "queries", len(calls))
		fmt.Println("DBG round",round,"calls",len(calls),"frontier",len(frontier),"known",set.count())

		results := runner.Run(ctx, calls)
		newFrontier := s.absorb(runID, set, frontier, results)
		if err := s.persistAttempts(ctx, runID, runner); err != nil {
			// Lineage durability is not optional. A run whose results cannot be
			// recorded must not be reported as a success.
			errs = append(errs, err)
			return s.abort(ctx, report, err)
		}
		frontier = newFrontier
		if len(frontier) == 0 {
			s.log.Info("discovery: nothing new to expand",
				"run_id", runID, "round", round)
			break
		}
	}

	// Everything found, ranked and explained.
	all := set.values()
	decisions := s.newRanker(profile).Rank(all)
	var persistErr error
	report.Candidates, persistErr = s.persistRanked(ctx, runID, decisions)
	if persistErr != nil {
		// A run whose findings could not be written is a failed run. Reporting
		// it as complete would tell an operator the leads are in the database
		// when they are not, and the run would look successful to every
		// automated check in between.
		return s.abort(ctx, report, fmt.Errorf("store results: %w", persistErr))
	}
	report.Accepted, report.Rejected = countVerdicts(report.Candidates)
	report.QueriesRun = runner.Stats().Succeeded + runner.Stats().Failed
	report.ProviderCalls = report.QueriesRun
	for name, st := range runner.Stats().ByProvider {
		report.ProviderStats[name] = st
		report.ProviderFailures += st.Failures
	}
	report.BudgetSpent = report.ProviderCalls

	// Pending work is what a resume would pick up: questions the budget stopped
	// this run from asking, plus the ones a provider said it had more of. A run
	// that finished with pending work is a run that did not finish, and the
	// number here is how the operator tells the two apart.
	pending := deferred
	for _, st := range report.ProviderStats {
		pending += st.Truncated
	}
	report.QueriesPending = pending

	switch {
	case ctx.Err() != nil:
		report.Status = StatusCancelled
		report.Error = ctx.Err().Error()
		errs = append(errs, ctx.Err())
	default:
		report.Status = StatusCompleted
	}
	report.FinishedAt = s.now()

	if err := s.store.FinishRun(ctx, runID, persistence.RunResult{
		Status:           report.Status,
		Candidates:       len(report.Candidates),
		Accepted:         report.Accepted,
		Rejected:         report.Rejected,
		ProviderCalls:    report.ProviderCalls,
		ProviderFailures: report.ProviderFailures,
		QueriesRun:       report.QueriesRun,
		QueriesPending:   report.QueriesPending,
		BudgetSpent:      report.BudgetSpent,
		Error:            report.Error,
		FinishedAt:       report.FinishedAt,
	}); err != nil {
		return report, err
	}
	return report, errors.Join(errs...)
}

// abort closes the run as failed and returns the report alongside the error, so
// a caller still learns how far the run got.
func (s *Service) abort(ctx context.Context, report *Report, cause error) (*Report, error) {
	report.Status = StatusFailed
	report.Error = cause.Error()
	report.FinishedAt = s.now()
	// The run row is closed with a background context: the caller's may already
	// be cancelled, and a cancelled run still needs to be recorded as cancelled
	// or the operator has no way to see that it died.
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.store.FinishRun(bg, report.RunID, persistence.RunResult{
		Status:     report.Status,
		Candidates: len(report.Candidates),
		Error:      report.Error,
		FinishedAt: report.FinishedAt,
	}); err != nil {
		s.log.Error("discovery: could not record the failed run", "run_id", report.RunID, "error", err)
	}
	return report, cause
}

// candidateSet is the running collection of companies a run has found, keyed by
// domain where there is one.
//
// The key matters more than it looks: a company found twice under two spellings
// is one company, and treating it as two would double-count the sources that
// corroborated it, which is the strongest signal in the whole system.
type candidateSet struct {
	byKey map[string]*candidate.Candidate
	order []string
}

func newCandidateSet() *candidateSet {
	return &candidateSet{byKey: map[string]*candidate.Candidate{}}
}

func keyFor(c candidate.Candidate) string {
	if c.Domain != "" {
		return "d:" + c.Domain
	}
	return "n:" + strings.ToLower(strings.TrimSpace(c.Name))
}

// has reports whether the set already holds this company.
func (s *candidateSet) has(k string) bool {
	_, ok := s.byKey[k]
	return ok
}

func (s *candidateSet) add(c candidate.Candidate) (merged bool, known bool) {
	k := keyFor(c)
	if existing, ok := s.byKey[k]; ok {
		existing.Merge(c)
		return true, true
	}
	copied := c
	s.byKey[k] = &copied
	s.order = append(s.order, k)
	return true, false
}

// count is how many distinct companies the set holds.
func (s *candidateSet) count() int { return len(s.order) }

func (s *candidateSet) values() []candidate.Candidate {
	out := make([]candidate.Candidate, 0, len(s.order))
	for _, k := range s.order {
		if c, ok := s.byKey[k]; ok {
			out = append(out, *c)
		}
	}
	return out
}

// seed puts the starting candidates in place and returns them as the first
// frontier.
func (s *Service) seed(seeds []candidate.Candidate) (*candidateSet, []candidate.Candidate) {
	set := newCandidateSet()
	frontier := make([]candidate.Candidate, 0, len(seeds))
	for _, c := range seeds {
		now := s.now()
		if c.FirstSeen.IsZero() {
			c.FirstSeen = now
		}
		if c.LastSeen.IsZero() {
			c.LastSeen = now
		}
		if c.Status == "" {
			c.Status = candidate.StatusNew
		}
		_, known := set.add(c)
		if !known {
			frontier = append(frontier, c)
		}
	}
	return set, frontier
}

// buildCalls turns a round's frontier into provider calls, bounded by the
// configured limits.
//
// The bounding happens here rather than inside the runner because the runner
// enforces cost while this enforces relevance: a run that spends its whole
// budget on the broadest queries in round zero never reaches the specific ones
// that identify a good lead. Higher-priority queries are kept for this reason.
func (s *Service) buildCalls(runner *providers.Runner, profile query.Profile, frontier []candidate.Candidate, round, known int) []providers.Call {
	names := runner.ProviderNames()
	if len(names) == 0 {
		return nil
	}

	// Round zero asks about the market and about the companies the operator
	// already named. Later rounds ask about the companies found since.
	//
	// Seeds are searched in round zero deliberately: a run whose only input is a
	// list of known companies is the most common shape there is, and treating
	// the seeds as a frontier that waits for a market search would spend budget
	// looking for companies the operator never asked about.
	// Market discovery first, then questions about specific companies. The two
	// are kept as separate lists because which one to ask depends on how full the
	// run already is, and by the time that is known the ordering is too late to
	// fix.
	roots := s.gen.Root(profile)
	expansions := s.expandQueries(profile, frontier)

	queries := s.dedupQueries(append(append([]query.Query(nil), roots...), expansions...), round)

	maxQueries := s.cfg.Query.MaxPerRun
	if maxQueries <= 0 {
		maxQueries = len(queries)
	}
	if s.cfg.Run.MaxCandidates > 0 {
		// The ceiling bounds how many new companies the run may store. Once it is
		// reached, questions that could only turn up something new are worth
		// nothing, because the answers would be refused; questions about companies
		// the run already holds are still worth asking, because a second provider
		// confirming one is what decides whether a lead is worth calling.
		//
		// So the run stops looking for new companies and spends what it has left
		// corroborating the ones it has. Ordering the list so the expansion
		// questions come first is what makes that happen, and keeping at least one
		// question is what stops a full run from going silent while it still has
		// budget and reachable providers.
		if remaining := s.cfg.Run.MaxCandidates - known; remaining < len(queries) {
			ordered := s.dedupQueries(append(append([]query.Query(nil), expansions...), roots...), round)
			if remaining < 1 {
				remaining = 1
			}
			if remaining < len(ordered) {
				ordered = ordered[:remaining]
			}
			queries = ordered
		}
	}
	if len(queries) > maxQueries {
		queries = queries[:maxQueries]
	}

	var calls []providers.Call
	for _, name := range names {
		if !s.isEnabled(name) {
			continue
		}
		for _, q := range queries {
			calls = append(calls, providers.Call{Provider: name, Query: toProviderQuery(q, s.cfg.Run.MaxCandidatesPerQuery)})
		}
	}
	return calls
}

// expandQueries generates the queries for one round over the current frontier,
// capped per candidate so a large frontier cannot crowd out the tail.
func (s *Service) expandQueries(profile query.Profile, frontier []candidate.Candidate) []query.Query {
	limit := s.cfg.Query.MaxPerCandidate
	if limit <= 0 {
		limit = 8
	}
	// With more companies than the per-candidate allowance, take the most
	// recently seen first: a fresh sighting is a better expansion target than a
	// stale one.
	focus := frontier
	if len(focus) > limit {
		focus = append([]candidate.Candidate(nil), focus...)
		sort.SliceStable(focus, func(i, j int) bool {
			return focus[i].LastSeen.After(focus[j].LastSeen)
		})
		focus = focus[:limit]
	}
	var out []query.Query
	for _, c := range focus {
		out = append(out, s.gen.Expand(profile, c)...)
	}
	return out
}

// dedupQueries removes questions already asked in an earlier round, so the
// expansion stage does not re-ask what the root stage knew.
func (s *Service) dedupQueries(in []query.Query, round int) []query.Query {
	seen := map[string]bool{}
	out := in[:0]
	for _, q := range in {
		if q.Text == "" || seen[q.Text] {
			continue
		}
		seen[q.Text] = true
		q.Depth = round
		out = append(out, q)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].Text < out[j].Text
	})
	return out
}

// absorb merges a round's results into the set and returns the new frontier:
// the candidates that were not already known, which is what the next round
// expands.
func (s *Service) absorb(runID string, set *candidateSet, frontier []candidate.Candidate, results []providers.Result) []candidate.Candidate {
	known := map[string]bool{}
	for _, c := range frontier {
		known[keyFor(c)] = true
	}
	var fresh []candidate.Candidate
	perQuery := s.cfg.Run.MaxCandidatesPerQuery
	// The run's candidate ceiling is enforced here, on results, rather than by
	// trimming the query list. Bounding questions does not bound answers: a
	// single broad query can return more companies than the run is allowed to
	// keep, and the surplus has to be dropped here or not at all.
	maxTotal := s.cfg.Run.MaxCandidates

	for _, res := range results {
		now := s.now()
		limit := len(res.Candidates)
		if perQuery > 0 && limit > perQuery {
			limit = perQuery
		}
		for i, c := range res.Candidates {
			if i >= limit {
				break
			}
			// The ceiling bounds how many distinct companies a run may hold, not
			// how much it may learn. A candidate already in the set still carries
			// new evidence and a new independent source, and that is the part of
			// a second provider's answer that makes it worth paying for. So the
			// ceiling is enforced only against genuinely new companies, and it
			// skips rather than stops: a later result may already be known.
			alreadyKnown := set.has(keyFor(c))
			if maxTotal > 0 && !alreadyKnown && set.count() >= maxTotal {
				s.log.Info("discovery: candidate ceiling reached",
					"run_id", runID, "limit", maxTotal)
				continue
			}
			c.RunID = runID
			if c.FirstSeen.IsZero() {
				c.FirstSeen = now
			}
			if c.LastSeen.IsZero() {
				c.LastSeen = now
			}
			if c.Status == "" {
				c.Status = candidate.StatusNew
			}
			// Every candidate must be attributable to the question that found it.
			// A provider that forgot its evidence produces a candidate nobody can
			// explain, so the service supplies the attribution rather than
			// dropping the result.
			if len(c.Evidence) == 0 {
				c.Evidence = []candidate.Evidence{{
					Source:     sourceForProvider(res.Provider),
					Method:     candidate.MethodDerived,
					URL:        res.Query.Text,
					Query:      res.Query.Text,
					Snippet:    c.Name,
					ObservedAt: now,
				}}
			}
			if err := c.Validate(); err != nil {
				s.log.Debug("discovery: discarding an unusable candidate",
					"run_id", runID, "provider", res.Provider, "name", c.Name, "reason", err)
				continue
			}
			_, wasKnown := set.add(c)
			if !wasKnown {
				k := keyFor(c)
				if !known[k] {
					known[k] = true
					fresh = append(fresh, c)
				}
			}
		}
	}
	return fresh
}

// sourceForProvider maps a provider name to the evidence source it represents.
// The mapping is by name rather than by assertion because a provider is a plugin
// and nothing forces it to be one of the surfaces the candidate package
// enumerated; an unmapped provider still gets an attributable source, which is
// what matters for the audit trail.
func sourceForProvider(name string) candidate.Source {
	switch strings.ToLower(name) {
	case "seed", "seeds", "file", "inline":
		return candidate.SourceSeed
	case "search", "web", "serp":
		return candidate.SourceSearch
	case "certificate", "ct", "crt":
		return candidate.SourceCertificate
	case "rdap", "registry", "whois":
		return candidate.SourceRegistry
	case "sitemap":
		return candidate.SourceSitemap
	case "crawl", "crawler":
		return candidate.SourceDirectory
	default:
		return candidate.SourceExpansion
	}
}

// persistAttempts writes the run's lineage. It is called once per round so a
// crash mid-run leaves a resumable trail rather than nothing.
//
// The records come from the runner's ledger, not from re-deriving them from the
// results. The runner already classified every call once, and it is the only
// party that saw the durations and the per-call errors; a second classifier
// here disagreed with it, and a run whose stored lineage contradicted its own
// report was worse than no lineage at all. One classifier, one record.
func (s *Service) persistAttempts(ctx context.Context, runID string, runner *providers.Runner) error {
	for _, a := range runner.Ledger().Attempts() {
		a.RunID = runID
		if err := s.store.RecordAttempt(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// persistRanked stores every candidate with its score, verdict, and explanation,
// and returns them in rank order.
//
// Ranking output is stored rather than recomputed on read because the score a
// salesperson acted on has to be the score the engine produced at the time. A
// threshold tuned a week later must not silently rewrite last week's decisions.
func (s *Service) persistRanked(ctx context.Context, runID string, decisions []ranking.Decision) ([]Scored, error) {
	all := make([]candidate.Candidate, 0, len(decisions))
	for _, d := range decisions {
		all = append(all, d.Candidate)
	}
	stored, err := s.store.UpsertCandidates(ctx, runID, all)
	if err != nil {
		return nil, fmt.Errorf("candidates: %w", err)
	}
	byDomain := map[string]persistence.StoredCandidate{}
	for _, st := range stored {
		byDomain[keyFor(st.Candidate)] = st
	}

	scored := make([]Scored, 0, len(decisions))
	writes := make([]persistence.Scored, 0, len(decisions))
	for i, d := range decisions {
		sc := Scored{
			Candidate: d.Candidate,
			Score:     d.Score,
			Verdict:   d.Verdict.String(),
			Explain:   d.Explanation,
			Rank:      i + 1,
		}
		for _, f := range d.Factors {
			sc.Factors = append(sc.Factors, persistence.RankFactor{
				Name:         string(f.Reason),
				Label:        f.Note,
				Weight:       f.Weight,
				Score:        f.Contribution,
				Contribution: f.Contribution,
				Detail:       f.Detail,
			})
		}
		if st, ok := byDomain[keyFor(d.Candidate)]; ok {
			sc.ID = st.ID
			writes = append(writes, persistence.Scored{
				ID: st.ID, Score: d.Score, Verdict: sc.Verdict,
				Explain: d.Explanation, Factors: sc.Factors,
			})
		}
		scored = append(scored, sc)
	}
	if err := s.store.SaveRanking(ctx, writes); err != nil {
		return nil, fmt.Errorf("ranking: %w", err)
	}
	return scored, nil
}

// countVerdicts splits a run's output by verdict.
func countVerdicts(in []Scored) (accepted, rejected int) {
	for _, s := range in {
		switch s.Verdict {
		case "accept":
			accepted++
		case "reject":
			rejected++
		}
	}
	return accepted, rejected
}

// redactedRunConfig is the configuration stored with a run, with every secret
// removed. A run has to be reproducible from what is stored, and a stored
// secret is a secret in the database forever.
func (s *Service) redactedRunConfig(req Request) map[string]any {
	red := s.cfg.Redacted()
	out := map[string]any{
		"profile":    req.Profile.Name,
		"max_depth":  depthOr(s.cfg.Run.MaxDepth, req.MaxDepth),
		"industries": req.Profile.Industries,
		"countries":  req.Profile.Countries,
	}
	if len(req.Seeds) > 0 {
		out["seeds"] = len(req.Seeds)
	}
	if red.Run.MaxProviderCalls > 0 {
		out["max_provider_calls"] = red.Run.MaxProviderCalls
	}
	if red.Prov.PerProviderCalls > 0 {
		out["per_provider_calls"] = red.Prov.PerProviderCalls
	}
	return out
}

func depthOr(configured, override int) int {
	if override > 0 {
		return override
	}
	return configured
}

// Report lists stored runs, newest first.
func (s *Service) Report(ctx context.Context, limit int) ([]persistence.RunSummary, error) {
	return s.store.RecentRuns(ctx, limit)
}

// RunInfo reads one stored run back, including the configuration it ran with.
// It is how an operator answers "what did we actually do last Tuesday", which
// the run list alone cannot answer.
func (s *Service) RunInfo(ctx context.Context, id string) (persistence.Run, error) {
	return s.store.Run(ctx, id)
}

// Pending returns the questions a run left unfinished, so a caller can see
// whether resuming it is worth the budget before spending it.
func (s *Service) Pending(ctx context.Context, runID string) ([]lineage.Attempt, error) {
	return s.store.PendingAttempts(ctx, runID)
}

// Candidates lists stored candidates, filtered.
func (s *Service) Candidates(ctx context.Context, f persistence.CandidateFilter) ([]persistence.StoredCandidate, error) {
	return s.store.Candidates(ctx, f)
}

// Candidate reads one stored candidate.
func (s *Service) Candidate(ctx context.Context, id string) (persistence.StoredCandidate, error) {
	return s.store.Candidate(ctx, id)
}

// Attempts returns a stored run's lineage.
func (s *Service) Attempts(ctx context.Context, runID string) ([]lineage.Attempt, error) {
	return s.store.AttemptsFor(ctx, runID)
}
