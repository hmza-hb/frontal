package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
	"github.com/hmza-hb/lead-intelligence/discovery/persistence"
	"github.com/hmza-hb/lead-intelligence/discovery/providers"
)

// memStore is an in-memory persistence.Store. It exists so the service's
// orchestration can be tested without a database: what is under test here is
// the order of operations and the accounting, and those should not depend on
// Postgres being up.
type memStore struct {
	mu       sync.Mutex
	runs     map[string]*persistence.Run
	runOrder []string
	byDomain map[string]*memCandidate
	attempts map[string][]lineage.Attempt
	attKey   map[string]bool
	ranking  map[string]persistence.Scored
	failNext error
	// failWrites makes candidate and ranking writes fail, so a test can check
	// what a run reports when its results cannot be stored.
	failWrites error
	closed     bool
}

type memCandidate struct {
	id    string
	c     candidate.Candidate
	first string
	last  string
	ev    []candidate.Evidence
}

func newMemStore() *memStore {
	return &memStore{
		runs:     map[string]*persistence.Run{},
		byDomain: map[string]*memCandidate{},
		attempts: map[string][]lineage.Attempt{},
		attKey:   map[string]bool{},
		ranking:  map[string]persistence.Scored{},
	}
}

func (m *memStore) CreateRun(_ context.Context, r persistence.Run) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", persistence.ErrStoreClosed
	}
	id := fmt.Sprintf("run-%d", len(m.runOrder)+1)
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().UTC()
	}
	r.ID = id
	r.Status = "running"
	stored := r
	m.runs[id] = &stored
	m.runOrder = append(m.runOrder, id)
	return id, nil
}

func (m *memStore) FinishRun(_ context.Context, id string, res persistence.RunResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return persistence.ErrStoreClosed
	}
	if m.failNext != nil {
		err := m.failNext
		m.failNext = nil
		return err
	}
	r, ok := m.runs[id]
	if !ok {
		return fmt.Errorf("%w: run %s", persistence.ErrNotFound, id)
	}
	r.Status = res.Status
	r.Candidates = res.Candidates
	r.Accepted = res.Accepted
	r.Rejected = res.Rejected
	r.ProviderCalls = res.ProviderCalls
	r.ProviderFailures = res.ProviderFailures
	r.QueriesRun = res.QueriesRun
	r.QueriesPending = res.QueriesPending
	r.BudgetSpent = res.BudgetSpent
	r.Error = res.Error
	if !res.FinishedAt.IsZero() {
		r.FinishedAt = res.FinishedAt
	}
	return nil
}

func (m *memStore) Run(_ context.Context, id string) (persistence.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return persistence.Run{}, fmt.Errorf("%w: run %s", persistence.ErrNotFound, id)
	}
	return *r, nil
}

func (m *memStore) RecentRuns(_ context.Context, limit int) ([]persistence.RunSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []persistence.RunSummary
	for i := len(m.runOrder) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		r := m.runs[m.runOrder[i]]
		out = append(out, persistence.RunSummary{
			ID: r.ID, Status: r.Status, ProfileName: r.ProfileName,
			SeedsTotal: r.SeedsTotal, Candidates: r.Candidates, StartedAt: r.StartedAt,
		})
	}
	return out, nil
}

func (m *memStore) UpsertCandidates(_ context.Context, runID string, in []candidate.Candidate) ([]persistence.StoredCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, persistence.ErrStoreClosed
	}
	if m.failWrites != nil {
		return nil, m.failWrites
	}
	out := make([]persistence.StoredCandidate, 0, len(in))
	for _, c := range in {
		if err := c.Validate(); err != nil {
			continue
		}
		k := c.Domain
		existing, ok := m.byDomain[k]
		if ok {
			existing.c.Merge(c)
			existing.last = runID
		} else {
			existing = &memCandidate{id: k, c: c, first: runID, last: runID}
			m.byDomain[k] = existing
		}
		existing.ev = append(existing.ev, c.Evidence...)
		out = append(out, persistence.StoredCandidate{ID: existing.id, Candidate: existing.c})
	}
	return out, nil
}

func (m *memStore) Candidate(_ context.Context, id string) (persistence.StoredCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mc := range m.byDomain {
		if mc.id == id {
			return persistence.StoredCandidate{ID: mc.id, Candidate: mc.c}, nil
		}
	}
	return persistence.StoredCandidate{}, persistence.ErrNotFound
}

func (m *memStore) Candidates(_ context.Context, _ persistence.CandidateFilter) ([]persistence.StoredCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []persistence.StoredCandidate
	for _, mc := range m.byDomain {
		out = append(out, persistence.StoredCandidate{ID: mc.id, Candidate: mc.c})
	}
	return out, nil
}

func (m *memStore) SaveRanking(_ context.Context, in []persistence.Scored) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range in {
		m.ranking[s.ID] = s
	}
	return nil
}

func (m *memStore) RecordAttempt(_ context.Context, a lineage.Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return persistence.ErrStoreClosed
	}
	k := a.RunID + "\x00" + a.Provider + "\x00" + a.Query
	if m.attKey[k] {
		return nil
	}
	m.attKey[k] = true
	m.attempts[a.RunID] = append(m.attempts[a.RunID], a)
	return nil
}

func (m *memStore) AttemptsFor(_ context.Context, runID string) ([]lineage.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]lineage.Attempt(nil), m.attempts[runID]...), nil
}

func (m *memStore) PendingAttempts(ctx context.Context, runID string) ([]lineage.Attempt, error) {
	all, err := m.AttemptsFor(ctx, runID)
	if err != nil {
		return nil, err
	}
	var out []lineage.Attempt
	for _, a := range all {
		if a.Outcome.Exhausted() || a.Outcome.Resumable() {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *memStore) Close() { m.mu.Lock(); m.closed = true; m.mu.Unlock() }

// calls returns how many attempts were recorded, for assertions.
func (m *memStore) calls(runID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.attempts[runID])
}

// fakeProvider is a provider whose answers are scripted.
type fakeProvider struct {
	name     string
	mu       sync.Mutex
	seen     []providers.Query
	calls    int
	respond  func(q providers.Query, call int) ([]candidate.Candidate, string, bool, error)
	readyErr error
	// kinds, when set, is what the provider claims to serve. A provider that
	// claims nothing must not be sent every query.
	kinds []providers.Kind
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Ready() error { return f.readyErr }

func (f *fakeProvider) Kinds() []providers.Kind { return f.kinds }

func (f *fakeProvider) Search(ctx context.Context, q providers.Query) (providers.Result, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.seen = append(f.seen, q)
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return providers.Result{Provider: f.name, Query: q, Err: err}, err
	}
	if !providers.Applies(f, q.Kind) {
		// The runner should never have sent this. Recording it as an unsupported
		// query is the honest answer, and a test that trips this will say so.
		return providers.Result{Provider: f.name, Query: q, Err: providers.ErrUnsupportedQuery}, providers.ErrUnsupportedQuery
	}
	if f.respond == nil {
		return providers.Result{Provider: f.name, Query: q, Err: providers.ErrNoResults}, nil
	}
	cands, cursor, truncated, err := f.respond(q, n)
	return providers.Result{
		Provider: f.name, Query: q, Candidates: cands,
		Cursor: cursor, Truncated: truncated, Err: err,
	}, err
}

func (f *fakeProvider) queries() []providers.Query {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]providers.Query(nil), f.seen...)
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// candidateWith builds a plausible provider result: a company with evidence,
// because a candidate without evidence is one the service would reject.
func candidateWith(domain, name, industry, country string) candidate.Candidate {
	return candidate.Candidate{
		Name:     name,
		Domain:   domain,
		URL:      "https://" + domain + "/",
		Industry: industry,
		Country:  country,
		Evidence: []candidate.Evidence{{
			Source: candidate.SourceSearch, Method: candidate.MethodSearchResult,
			URL: "https://search.test/?q=" + strings.ReplaceAll(name, " ", "+"),
		}},
	}
}

// quietLog keeps test output readable. A run that logs at info level for every
// round would bury an assertion failure in noise.
func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

var errNoStore = errors.New("memstore: closed")
