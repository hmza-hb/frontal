// Package lineage records what discovery did during a run: which query went to
// which provider, what came back, and what it cost.
//
// # Why this is not a graph
//
// The architecture assigns general Node/Edge graphs, traversal, and JSON-LD or
// DOT export to the knowledge-graph module. Discovery deliberately does not
// reimplement that. What discovery needs is a ledger, not a graph, because the
// questions it must answer are all enumerations rather than traversals:
//
//   - Which queries have we already run, so a resumed run does not repeat them?
//   - Which paginated query stopped part-way, and where did it stop?
//   - Which providers cost us time and money?
//   - Which combinations of provider, language and market returned nothing?
//
// That last question is the reason the package exists at all. A provider that
// has silently stopped working — an expired API key, a changed response schema,
// a silent rate limit — is indistinguishable from a provider that genuinely has
// no matches, unless the outcome is recorded. Both return zero candidates. One
// of them means "this market is exhausted" and the other means "this run's
// results are a lie", and only one of those should let a run finish reporting
// success.
//
// # Append-only
//
// Attempts are never rewritten. A retried query appends a second attempt, so the
// ledger shows the flapping as well as the result. Overwriting the first
// outcome would hide exactly the provider instability an operator needs to see.
package lineage

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Outcome is what happened to one query on one provider.
type Outcome string

const (
	// OutcomeProduced means the provider returned candidates.
	OutcomeProduced Outcome = "produced"
	// OutcomeEmpty means the provider was asked successfully and genuinely had
	// no matches. This is a real result: it means that surface is exhausted for
	// that query.
	OutcomeEmpty Outcome = "empty"
	// OutcomeFailed means the provider was asked and errored. The error text is
	// recorded so a systematic failure can be diagnosed from the ledger alone.
	OutcomeFailed Outcome = "failed"
	// OutcomeBudgeted means the run stopped the query because a budget ran out.
	// This is neither a result nor a failure, and a resumed run must retry it.
	OutcomeBudgeted Outcome = "budgeted"
	// OutcomeTruncated means the provider had more to give and the query stopped
	// at a page boundary. The cursor records where.
	OutcomeTruncated Outcome = "truncated"
	// OutcomeSkipped means the query was not attempted, usually because a
	// previous attempt in this run already established the answer.
	OutcomeSkipped Outcome = "skipped"
)

// Exhausted reports whether an outcome is a trustworthy statement that the
// surface has nothing more to give. Only these may suppress a retry or count as
// coverage.
func (o Outcome) Exhausted() bool {
	return o == OutcomeProduced || o == OutcomeEmpty
}

// Resumable reports whether a later run should pick the query back up.
func (o Outcome) Resumable() bool {
	return o == OutcomeTruncated || o == OutcomeBudgeted
}

// Terminal reports whether the outcome means the query will never do better,
// so retrying it is pure cost.
func (o Outcome) Terminal() bool {
	return o == OutcomeFailed || o == OutcomeSkipped
}

// Attempt is one attempt at one query against one provider.
type Attempt struct {
	// RunID scopes the attempt so a failed run can be rolled back without
	// touching another run's history.
	RunID string
	// Provider is the surface, matching candidate.Source.
	Provider string
	// Query is the verbatim query text, so an attempt is reproducible.
	Query string
	// Language is the language the query is written in. It is recorded
	// separately because an empty result in a language discovery cannot
	// actually search is not coverage, it is a gap.
	Language string
	// Outcome is what happened.
	Outcome Outcome
	// Candidates is how many candidates the attempt produced.
	Candidates int
	// Cursor is where a truncated or paginated query stopped, opaque to
	// everything except the provider that set it.
	Cursor string
	// Error is the provider's error text, if any. It must never contain
	// credentials; callers are responsible for redacting before recording.
	Error string
	// Duration is the provider's wall-clock time, used for cost accounting.
	Duration time.Duration
	// At is when the attempt finished.
	At time.Time
	// Detail is free-form provider context such as a page number.
	Detail string
}

// Key identifies the logical unit of work: one query against one provider. The
// run and language are deliberately excluded, because a resumed run reuses the
// same key and must see the earlier attempts.
type Key struct {
	Provider string
	Query    string
}

func (k Key) String() string { return k.Provider + "\x00" + k.Query }

// Stats summarises a set of attempts.
type Stats struct {
	// Queries is the number of distinct provider/query pairs, each counted once
	// at its latest outcome. Repeated attempts at one question are flapping, not
	// additional coverage.
	Queries int
	// TotalAttempts is every attempt recorded, including retries. This is the
	// number that grows when a provider is struggling.
	TotalAttempts int
	// Candidates is the total number of candidates produced.
	Candidates int
	// Exhausted is the number of distinct queries that reached a trustworthy
	// conclusion.
	Exhausted int
	// Pending is the number of distinct queries that stopped early and are
	// resumable.
	Pending int
	// Failed is the number of distinct queries that failed.
	Failed int
	// ByProvider maps a provider to its attempts, candidates, and time.
	ByProvider map[string]ProviderStats
	// SilentProviders are providers where every attempt returned nothing. That
	// is the signature of a provider that has stopped working, and it is
	// reported separately from a provider that simply has no data for a market,
	// because only one of those is a bug.
	SilentProviders []string
	// EmptyByLanguage maps a language to the queries that were searched in it
	// and returned nothing, so a language the deployment cannot actually search
	// is visible as a gap rather than as a negative result.
	EmptyByLanguage map[string]int
}

// ProviderStats is one provider's contribution to a run.
type ProviderStats struct {
	// Queries is the distinct questions asked of this provider.
	Queries int
	// Attempts is every attempt, including retries.
	Attempts int
	// Candidates is the total number of candidates produced.
	Candidates int
	// Failed is the number of queries whose latest outcome is a failure.
	Failed int
	// Duration is the total provider time spent, including on failed attempts.
	// Cost accounting must include failures: a provider that burns ten minutes
	// returning errors has cost ten minutes whether or not it returned a row.
	Duration time.Duration
}

// Ledger is the append-only record of a run. It is safe for concurrent use,
// because providers fan out in parallel and a run that serialised its ledger
// would serialise itself.
type Ledger struct {
	mu       sync.RWMutex
	attempts []Attempt
	index    map[Key][]int
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{index: map[Key][]int{}}
}

// ErrRedacted is returned when an attempt would record something unsafe.
var ErrRedacted = errors.New("lineage: attempt text looks like it contains a credential")

// Record appends an attempt. It returns an error rather than storing a record
// that cannot be reproduced or that might leak a secret into durable storage.
func (l *Ledger) Record(a Attempt) error {
	if strings.TrimSpace(a.Provider) == "" {
		return errors.New("lineage: attempt has no provider")
	}
	if strings.TrimSpace(a.Query) == "" {
		return errors.New("lineage: attempt has no query")
	}
	if a.Outcome == "" {
		return errors.New("lineage: attempt has no outcome")
	}
	// A query or error string is written verbatim into durable storage and into
	// operator-facing reports. A provider that puts an API key in its error
	// message is a provider that will leak the key into the ledger, and a
	// leaked key is a much worse problem than a rejected record.
	for _, field := range []string{a.Query, a.Error, a.Detail, a.Cursor} {
		if looksLikeCredential(field) {
			return fmt.Errorf("%w: provider %s", ErrRedacted, a.Provider)
		}
	}
	if a.At.IsZero() {
		a.At = time.Now().UTC()
	}
	if a.Outcome == OutcomeProduced && a.Candidates == 0 {
		// "Produced" with a count of zero is a caller bug, and accepting it
		// would poison the coverage report: the query would be counted as
		// exhausted while contributing nothing.
		return errors.New("lineage: attempt claims to have produced results but counted none")
	}
	if a.Outcome != OutcomeProduced && a.Candidates != 0 {
		return fmt.Errorf("lineage: attempt with outcome %q counted %d candidates", a.Outcome, a.Candidates)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	k := Key{Provider: a.Provider, Query: a.Query}
	l.index[k] = append(l.index[k], len(l.attempts))
	l.attempts = append(l.attempts, a)
	return nil
}

// History returns every attempt for a key, in the order they happened.
func (l *Ledger) History(k Key) []Attempt {
	l.mu.RLock()
	defer l.mu.RUnlock()
	idx := l.index[k]
	out := make([]Attempt, 0, len(idx))
	for _, i := range idx {
		out = append(out, l.attempts[i])
	}
	return out
}

// Latest returns the most recent attempt for a key.
func (l *Ledger) Latest(k Key) (Attempt, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	idx := l.index[k]
	if len(idx) == 0 {
		return Attempt{}, false
	}
	return l.attempts[idx[len(idx)-1]], true
}

// ShouldRun reports whether a query against a provider still needs work, and
// returns the cursor to resume from.
//
// The rule is deliberately conservative about failure. A query that failed
// once is retried, because transient network errors and rate limits are the
// common case and giving up on them silently loses coverage. It is equally
// deliberate about success: a query that genuinely came back empty is never
// retried, because re-asking a question that has been answered is how a
// provider's API bill triples for no new information.
func (l *Ledger) ShouldRun(k Key) (run bool, cursor string) {
	a, ok := l.Latest(k)
	if !ok {
		return true, ""
	}
	switch {
	case a.Outcome.Resumable():
		return true, a.Cursor
	case a.Outcome.Exhausted():
		return false, ""
	default:
		// Failed or skipped: worth another attempt.
		return true, ""
	}
}

// Pending returns the work a resumed run must pick up, sorted for determinism.
func (l *Ledger) Pending() []Key {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []Key
	for k := range l.index {
		latest := l.attempts[l.index[k][len(l.index[k])-1]]
		if latest.Outcome.Resumable() {
			out = append(out, k)
		}
	}
	sortKeys(out)
	return out
}

// Attempts returns every attempt in the ledger in the order recorded.
func (l *Ledger) Attempts() []Attempt {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Attempt, len(l.attempts))
	copy(out, l.attempts)
	return out
}

// Len returns the number of attempts.
func (l *Ledger) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.attempts)
}

// Stats summarises the ledger.
//
// Outcomes are collapsed to the latest attempt per key, so a provider that
// flapped five times and then succeeded counts as coverage rather than as five
// failures. "Latest" is the last attempt recorded, not the one with the newest
// timestamp, because the ledger is append-only and a resumed run may replay an
// older attempt after a newer one.
//
// Cost is not collapsed: every attempt's duration counts, because the time a
// provider spent failing was still spent.
//
// Passing an empty runID scopes the summary to the whole ledger.
func (l *Ledger) Stats(runID string) Stats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	s := Stats{
		ByProvider:      map[string]ProviderStats{},
		EmptyByLanguage: map[string]int{},
		SilentProviders: []string{},
	}
	latest := map[Key]Attempt{}
	for _, a := range l.attempts {
		if runID != "" && a.RunID != runID {
			continue
		}
		// Time and volume accumulate on every attempt.
		ps := s.ByProvider[a.Provider]
		ps.Attempts++
		ps.Duration += a.Duration
		ps.Candidates += a.Candidates
		s.ByProvider[a.Provider] = ps
		s.TotalAttempts++
		s.Candidates += a.Candidates

		latest[Key{Provider: a.Provider, Query: a.Query}] = a
	}

	for _, a := range latest {
		s.Queries++
		ps := s.ByProvider[a.Provider]
		ps.Queries++
		switch {
		case a.Outcome.Exhausted():
			s.Exhausted++
			if a.Candidates == 0 {
				s.EmptyByLanguage[a.Language]++
			}
		case a.Outcome.Resumable():
			s.Pending++
		case a.Outcome.Terminal():
			s.Failed++
			ps.Failed++
		}
		s.ByProvider[a.Provider] = ps
	}

	// A provider is silent only if it answered every question and returned
	// nothing at all. A provider that returned nothing for one market but
	// produced candidates elsewhere is working, and reporting it as broken would
	// bury the one provider that is actually broken.
	for p, ps := range s.ByProvider {
		if ps.Queries > 0 && ps.Candidates == 0 && ps.Failed == 0 {
			s.SilentProviders = append(s.SilentProviders, p)
		}
	}
	sort.Strings(s.SilentProviders)
	return s
}

// Reset drops a run's attempts, for rolling back a run that failed. Attempts from
// other runs are preserved, because a run's history is not the ledger's to
// discard: a failed run must not erase the evidence that a provider was broken
// last week.
func (l *Ledger) Reset(runID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.attempts[:0]
	dropped := 0
	for _, a := range l.attempts {
		if a.RunID == runID {
			dropped++
			continue
		}
		kept = append(kept, a)
	}
	l.attempts = kept
	l.index = map[Key][]int{}
	for i, a := range l.attempts {
		k := Key{Provider: a.Provider, Query: a.Query}
		l.index[k] = append(l.index[k], i)
	}
	return dropped
}

// looksLikeCredential is a cheap guard against the common ways an API key ends up
// in a log line. It is deliberately not a complete detector, because the
// complete solution is not storing the string, and this is the last line before
// it would be.
func looksLikeCredential(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	for _, marker := range []string{
		"api_key=", "apikey=", "api-key=", "access_token=", "token=",
		"bearer ", "authorization:", "password=", "secret=", "client_secret=",
		"sig=", "signature=", "x-api-key",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	// A credential-shaped token: long, and mixing upper case, lower case and
	// digits. Natural-language queries and domains do not look like this, and
	// API keys essentially always do.
	//
	// This is a guard, not a guarantee. A heuristic cannot reliably tell a key
	// from a long identifier, so the real protection is that callers redact
	// before recording; this exists to catch the common case at the last line
	// before it would reach durable storage.
	for _, field := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && r != '_' && r != '-'
	}) {
		if len(field) >= 24 && hasUpper(field) && hasLower(field) && hasDigit(field) {
			return true
		}
	}
	return false
}

func hasUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

func hasLower(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			return true
		}
	}
	return false
}

func hasDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}

func sortKeys(keys []Key) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Provider != keys[j].Provider {
			return keys[i].Provider < keys[j].Provider
		}
		return keys[i].Query < keys[j].Query
	})
}
