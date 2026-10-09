package lineage

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

var at = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func produced(provider, query string) Attempt {
	return Attempt{
		RunID: "run-1", Provider: provider, Query: query, Language: "en",
		Outcome: OutcomeProduced, Candidates: 3, At: at, Duration: 200 * time.Millisecond,
	}
}

func TestRecordRejectsUnreproducibleAttempts(t *testing.T) {
	l := NewLedger()
	cases := []struct {
		why string
		a   Attempt
	}{
		{"no provider", Attempt{Query: "q", Outcome: OutcomeEmpty}},
		{"no query", Attempt{Provider: "p", Outcome: OutcomeEmpty}},
		{"no outcome", Attempt{Provider: "p", Query: "q"}},
		{"produced nothing", Attempt{Provider: "p", Query: "q", Outcome: OutcomeProduced}},
		{"empty but counted", Attempt{Provider: "p", Query: "q", Outcome: OutcomeEmpty, Candidates: 4}},
	}
	for _, tc := range cases {
		if err := l.Record(tc.a); err == nil {
			t.Errorf("%s: Record accepted an invalid attempt", tc.why)
		}
	}
	if l.Len() != 0 {
		t.Errorf("rejected attempts must not be stored, ledger has %d", l.Len())
	}
}

func TestRecordRefusesCredentials(t *testing.T) {
	// A provider that puts a key in its error message would otherwise write it
	// into durable storage and into every operator-facing report built from the
	// ledger. A rejected record is recoverable; a leaked key is not.
	l := NewLedger()
	secrets := []struct {
		why string
		a   Attempt
	}{
		{"key in the query", Attempt{Provider: "p", Query: "acme?api_key=sk-1234567890abcdef", Outcome: OutcomeEmpty}},
		{"bearer in the error", Attempt{Provider: "p", Query: "q", Outcome: OutcomeFailed, Error: "401 unauthorized: Bearer sk_live_abcdef123456"}},
		{"token in the error", Attempt{Provider: "p", Query: "q", Outcome: OutcomeFailed, Error: "bad access_token=xyz"}},
		{"long opaque blob in a detail", Attempt{Provider: "p", Query: "q", Outcome: OutcomeEmpty, Detail: "A9dKq2Zx8Lm4Np7Rt3Vw6Yb1Hc5Jf0Gs"}},
	}
	for _, tc := range secrets {
		err := l.Record(tc.a)
		if err == nil {
			t.Errorf("%s: Record accepted an attempt that may contain a credential", tc.why)
			continue
		}
		if !errors.Is(err, ErrRedacted) {
			t.Errorf("%s: got %v, want ErrRedacted", tc.why, err)
		}
	}
	if l.Len() != 0 {
		t.Errorf("nothing may be stored, ledger has %d", l.Len())
	}

	// A legitimate query must not be caught by the guard.
	if err := l.Record(produced("crt.sh", "%.acme.de")); err != nil {
		t.Errorf("a legitimate query was rejected: %v", err)
	}
}

func TestShouldRunDistinguishesResultsFromFailures(t *testing.T) {
	l := NewLedger()
	k := Key{Provider: "search", Query: "acme robots"}

	// Unseen work always runs.
	if run, _ := l.ShouldRun(k); !run {
		t.Error("an unrecorded query must run")
	}

	// A genuine empty result is a real answer and re-asking it is how a
	// provider's bill triples for no new information.
	if err := l.Record(Attempt{RunID: "run-1", Provider: k.Provider, Query: k.Query, Outcome: OutcomeEmpty}); err != nil {
		t.Fatal(err)
	}
	if run, _ := l.ShouldRun(k); run {
		t.Error("a query that genuinely came back empty must not be re-run")
	}

	// A paginated query resumes from where it stopped, not from the beginning.
	page := Attempt{
		RunID: "run-1", Provider: k.Provider, Query: k.Query, Language: "en",
		Outcome: OutcomeTruncated, Cursor: "page=3", At: at,
	}
	if err := l.Record(page); err != nil {
		t.Fatal(err)
	}
	run, cursor := l.ShouldRun(k)
	if !run || cursor != "page=3" {
		t.Errorf("a truncated query should resume from its cursor, got run=%v cursor=%q", run, cursor)
	}

	// A failure is retried, because transient errors and rate limits are the
	// common case and giving up on them silently loses coverage.
	failed := Attempt{RunID: "run-1", Provider: k.Provider, Query: k.Query, Outcome: OutcomeFailed, Error: "429 rate limited"}
	if err := l.Record(failed); err != nil {
		t.Fatal(err)
	}
	if run, cursor := l.ShouldRun(k); !run || cursor != "" {
		t.Errorf("a failed query should be retried from the start, got run=%v cursor=%q", run, cursor)
	}
}

func TestPendingListsOnlyResumableWork(t *testing.T) {
	l := NewLedger()
	must := func(a Attempt) {
		t.Helper()
		if err := l.Record(a); err != nil {
			t.Fatal(err)
		}
	}
	must(produced("search", "a"))
	must(Attempt{Provider: "search", Query: "b", Outcome: OutcomeEmpty})
	must(Attempt{Provider: "crt.sh", Query: "c", Outcome: OutcomeTruncated, Cursor: "offset=100"})
	must(Attempt{Provider: "rdap", Query: "d", Outcome: OutcomeBudgeted})
	must(Attempt{Provider: "news", Query: "e", Outcome: OutcomeFailed})

	got := l.Pending()
	if len(got) != 2 {
		t.Fatalf("Pending = %v, want the truncated and budgeted queries only", got)
	}
	// Sorted for determinism, so a resumed run replays in a stable order.
	if got[0].Provider != "crt.sh" || got[1].Provider != "rdap" {
		t.Errorf("Pending is not sorted: %v", got)
	}
}

func TestStatsCollapsesFlappingToTheLatestAttempt(t *testing.T) {
	l := NewLedger()
	k := Key{Provider: "search", Query: "acme"}
	rec := func(outcome Outcome, n int, d time.Duration) {
		t.Helper()
		if err := l.Record(Attempt{
			RunID: "run-1", Provider: k.Provider, Query: k.Query,
			Outcome: outcome, Candidates: n, Duration: d, At: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rec(OutcomeFailed, 0, time.Second)
	rec(OutcomeFailed, 0, time.Second)
	rec(OutcomeProduced, 7, 3*time.Second)

	s := l.Stats("run-1")
	// Three attempts on one query are one question, answered.
	if s.Queries != 1 {
		t.Errorf("Queries = %d, want the latest attempt per query collapsed to 1", s.Queries)
	}
	// But they are three attempts, and the flapping is what an operator needs to
	// see: a provider that needed three tries is a different risk from one that
	// answered first time.
	if s.TotalAttempts != 3 {
		t.Errorf("TotalAttempts = %d, want 3", s.TotalAttempts)
	}
	if s.Failed != 0 || s.Exhausted != 1 {
		t.Errorf("a query that eventually succeeded is coverage, got failed=%d exhausted=%d", s.Failed, s.Exhausted)
	}
	if s.Candidates != 7 {
		t.Errorf("Candidates = %d, want 7", s.Candidates)
	}
	// Failed attempts still cost time and that time is still spent.
	if got := s.ByProvider["search"].Attempts; got != 3 {
		t.Errorf("provider attempts = %d, want 3", got)
	}
	if got := s.ByProvider["search"].Duration; got != 5*time.Second {
		t.Errorf("provider time = %v, want every attempt's time counted even the failed ones", got)
	}
}

func TestSilentProviderIsDistinguishedFromAnExhaustedOne(t *testing.T) {
	// The whole point of the ledger. A provider whose key expired and a provider
	// that genuinely has no data both return zero candidates; only one of them
	// means the run's coverage claim is false.
	l := NewLedger()
	rec := func(a Attempt) {
		t.Helper()
		a.RunID = "run-1"
		if err := l.Record(a); err != nil {
			t.Fatal(err)
		}
	}
	// A provider that has gone silent: every query came back empty.
	for _, q := range []string{"a", "b", "c"} {
		rec(Attempt{Provider: "news", Query: q, Language: "en", Outcome: OutcomeEmpty, At: at})
	}
	// A provider that has no data for a market but works elsewhere.
	rec(Attempt{Provider: "search", Query: "de", Language: "de", Outcome: OutcomeEmpty, At: at})
	rec(produced("search", "acme robots"))

	s := l.Stats("run-1")
	if len(s.SilentProviders) != 1 || s.SilentProviders[0] != "news" {
		t.Errorf("SilentProviders = %v, want [news]", s.SilentProviders)
	}
	// Every empty query is counted against its language, so a language that only
	// ever comes back empty is visible as a gap rather than as a negative
	// result: three empty English queries from the silent provider, and one
	// empty German query from a provider that is merely out of data there.
	if s.EmptyByLanguage["en"] != 3 || s.EmptyByLanguage["de"] != 1 {
		t.Errorf("EmptyByLanguage = %v, want en=3 de=1", s.EmptyByLanguage)
	}
}

func TestFailedProviderIsNotReportedAsSilent(t *testing.T) {
	// A provider that is visibly erroring is already obviously broken. Reporting
	// it as silent too would bury the one thing an operator needs to act on.
	l := NewLedger()
	rec := func(a Attempt) {
		t.Helper()
		a.RunID = "run-1"
		if err := l.Record(a); err != nil {
			t.Fatal(err)
		}
	}
	rec(Attempt{Provider: "news", Query: "a", Outcome: OutcomeEmpty, At: at})
	rec(Attempt{Provider: "news", Query: "b", Outcome: OutcomeFailed, Error: "500", At: at})

	if s := l.Stats("run-1"); len(s.SilentProviders) != 0 {
		t.Errorf("a visibly failing provider should not also be reported as silent: %v", s.SilentProviders)
	}
}

func TestStatsAreScopedToARun(t *testing.T) {
	l := NewLedger()
	if err := l.Record(Attempt{RunID: "run-1", Provider: "search", Query: "a", Outcome: OutcomeProduced, Candidates: 5, At: at}); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(Attempt{RunID: "run-2", Provider: "search", Query: "a", Outcome: OutcomeProduced, Candidates: 2, At: at}); err != nil {
		t.Fatal(err)
	}

	if s := l.Stats("run-1"); s.Candidates != 5 {
		t.Errorf("run-1 candidates = %d, want 5", s.Candidates)
	}
	if s := l.Stats("run-2"); s.Candidates != 2 {
		t.Errorf("run-2 candidates = %d, want 2", s.Candidates)
	}
	// Unscoped, the two runs ask the same question, so it collapses to one query
	// with the latest outcome.
	all := l.Stats("")
	if all.Queries != 1 || all.TotalAttempts != 2 {
		t.Errorf("unscoped: queries=%d attempts=%d, want 1 and 2", all.Queries, all.TotalAttempts)
	}
	// Volume and time are not collapsed, so the unscoped candidate total is every
	// candidate the ledger holds, not just the last run's.
	if all.Candidates != 7 {
		t.Errorf("unscoped candidates = %d, want every attempt's 7", all.Candidates)
	}
}

func TestResetDropsOneRunOnly(t *testing.T) {
	l := NewLedger()
	if err := l.Record(produced("search", "a")); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(Attempt{RunID: "run-2", Provider: "search", Query: "b", Outcome: OutcomeProduced, Candidates: 1, At: at}); err != nil {
		t.Fatal(err)
	}
	if dropped := l.Reset("run-1"); dropped != 1 {
		t.Errorf("Reset dropped %d attempts, want 1", dropped)
	}
	if l.Len() != 1 {
		t.Fatalf("Len = %d, want the other run's attempt to survive", l.Len())
	}
	// The index must be rebuilt, or a rolled-back run's history would still
	// answer questions about the queries it recorded.
	if _, ok := l.Latest(Key{Provider: "search", Query: "a"}); ok {
		t.Error("a rolled-back attempt is still reachable through the index")
	}
	if run, _ := l.ShouldRun(Key{Provider: "search", Query: "a"}); !run {
		t.Error("a rolled-back query should be runnable again")
	}
	if run, _ := l.ShouldRun(Key{Provider: "search", Query: "b"}); run {
		t.Error("a surviving query should still be known as done")
	}
	_ = l
}

func TestConcurrentRecordAndQuery(t *testing.T) {
	l := NewLedger()
	var wg sync.WaitGroup
	const workers = 16
	const each = 40
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				a := produced("search", "query")
				a.RunID = "run-1"
				if err := l.Record(a); err != nil {
					t.Error(err)
					return
				}
				// The record above completed before this check, so the query is
				// answered and must not be re-queued. An inversion here would
				// make every goroutine exit after one record.
				if run, _ := l.ShouldRun(Key{Provider: "search", Query: "query"}); run {
					t.Error("a query that just produced results should not need re-running")
					return
				}
				_ = l.Stats("run-1")
				_ = l.Pending()
			}
		}(w)
	}
	wg.Wait()
	if l.Len() != workers*each {
		t.Errorf("Len = %d, want %d", l.Len(), workers*each)
	}
}

func TestHistoryIsAppendOnly(t *testing.T) {
	l := NewLedger()
	k := Key{Provider: "search", Query: "a"}
	for i, outcome := range []Outcome{OutcomeFailed, OutcomeBudgeted, OutcomeProduced} {
		a := produced("search", "a")
		a.Outcome = outcome
		a.Candidates = 0
		if outcome == OutcomeProduced {
			a.Candidates = 2
		}
		a.Detail = "attempt " + string(rune('0'+i))
		if err := l.Record(a); err != nil {
			t.Fatal(err)
		}
	}
	h := l.History(k)
	if len(h) != 3 {
		t.Fatalf("History has %d attempts, want 3", len(h))
	}
	// The flapping is the point: a later success does not erase the evidence
	// that the provider was struggling.
	if h[0].Outcome != OutcomeFailed || h[2].Outcome != OutcomeProduced {
		t.Errorf("history = %v, want the failures preserved in order", h)
	}
	latest, ok := l.Latest(k)
	if !ok || latest.Outcome != OutcomeProduced {
		t.Errorf("Latest = %v, want the produced attempt", latest.Outcome)
	}
}

func TestOutcomeClassification(t *testing.T) {
	cases := []struct {
		outcome                    Outcome
		exhausted, resumable, term bool
	}{
		{OutcomeProduced, true, false, false},
		{OutcomeEmpty, true, false, false},
		{OutcomeTruncated, false, true, false},
		{OutcomeBudgeted, false, true, false},
		{OutcomeFailed, false, false, true},
		{OutcomeSkipped, false, false, true},
	}
	for _, tc := range cases {
		if got := tc.outcome.Exhausted(); got != tc.exhausted {
			t.Errorf("%q.Exhausted() = %v, want %v", tc.outcome, got, tc.exhausted)
		}
		if got := tc.outcome.Resumable(); got != tc.resumable {
			t.Errorf("%q.Resumable() = %v, want %v", tc.outcome, got, tc.resumable)
		}
		if got := tc.outcome.Terminal(); got != tc.term {
			t.Errorf("%q.Terminal() = %v, want %v", tc.outcome, got, tc.term)
		}
	}
}

func TestErrorTextIsRecordedForDiagnosis(t *testing.T) {
	// The error text is what lets an operator diagnose a systematic failure from
	// the ledger alone, so it has to survive.
	l := NewLedger()
	const msg = "the upstream search index returned an empty result set for language de"
	if err := l.Record(Attempt{
		RunID: "run-1", Provider: "search", Query: "q", Outcome: OutcomeFailed, Error: msg, At: at,
	}); err != nil {
		t.Fatal(err)
	}
	a, _ := l.Latest(Key{Provider: "search", Query: "q"})
	if a.Error != msg {
		t.Errorf("Error = %q, want %q", a.Error, msg)
	}
}

func TestKeyIsStableAcrossLanguagesAndRuns(t *testing.T) {
	// The key must not include language or run, or a resumed run would not see
	// that the query was already answered.
	k1 := Key{Provider: "search", Query: "acme"}
	if k1.String() != (Key{Provider: "search", Query: "acme"}).String() {
		t.Error("Key is not stable")
	}
	// Two different queries must never collide into one key, including a query
	// that happens to contain a NUL.
	if (Key{Provider: "a", Query: "b"}).String() == (Key{Provider: "a\x00b", Query: ""}).String() {
		t.Error("distinct keys collided")
	}
	if !strings.Contains((Key{Provider: "a", Query: "b"}).String(), "a") {
		t.Error("key should identify the provider")
	}
}
