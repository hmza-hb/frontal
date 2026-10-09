package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
	"github.com/hmza-hb/lead-intelligence/discovery/persistence"
	"github.com/hmza-hb/lead-intelligence/platform/db"
	"github.com/hmza-hb/lead-intelligence/platform/testutil"
)

// newStore returns a store on an isolated schema with discovery's migrations
// applied, skipping when no database is configured so `go test ./...` works on
// a machine without one.
func newStore(t *testing.T) *persistence.PostgresStore {
	t.Helper()
	pool := testutil.Postgres(t)
	res, err := db.Migrate(context.Background(), pool, []db.Source{persistence.MigrationSource()}, nil)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Logf("migrations: %d applied, %d skipped", len(res.Applied), len(res.Skipped))
	return persistence.NewPostgresStore(pool)
}

func seedCandidate() candidate.Candidate {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return candidate.Candidate{
		Name:         "Northwind Robotics",
		Domain:       "northwind.test",
		URL:          "https://northwind.test/",
		Country:      "US",
		Industry:     "Industrial Automation",
		EmployeeHint: "51-200",
		Description:  "Warehouse robotics integrator",
		Keywords:     []string{"northwind robotics", "nw robotics"},
		Confidence:   0.6,
		FirstSeen:    now,
		LastSeen:     now,
		Evidence: []candidate.Evidence{{
			Source:     candidate.SourceSeed,
			Method:     candidate.MethodSeedImport,
			URL:        "https://northwind.test/",
			Snippet:    "Northwind Robotics, US, industrial automation",
			ObservedAt: now,
		}},
	}
}

func TestMigrationAppliesCleanly(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	// The migration must be re-runnable: a deployment that restarts should not
	// have to know whether it got halfway last time.
	if _, err := db.Migrate(ctx, store.Pool(), []db.Source{persistence.MigrationSource()}, nil); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestRunLifecycle(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	id, err := store.CreateRun(ctx, persistence.Run{
		ProfileName: "us-warehouse-automation",
		SeedsTotal:  12,
		MaxDepth:    2,
		BudgetLimit: 500,
		Config:      map[string]any{"max_depth": 2, "providers": []string{"search"}},
		StartedAt:   time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if id == "" {
		t.Fatal("create run returned no id")
	}

	run, err := store.Run(ctx, id)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if run.Status != "running" {
		t.Errorf("status = %q, want running", run.Status)
	}
	if run.ProfileName != "us-warehouse-automation" || run.SeedsTotal != 12 {
		t.Errorf("run = %+v, want the profile and seed count it was created with", run)
	}
	if run.FinishedAt != (time.Time{}) {
		t.Errorf("finished_at = %v, want zero for a running run", run.FinishedAt)
	}
	if cfg, ok := run.Config["max_depth"]; !ok || cfg != float64(2) {
		t.Errorf("config = %v, want max_depth round-tripped as jsonb", run.Config)
	}

	if err := store.FinishRun(ctx, id, persistence.RunResult{
		Status:        "completed",
		Candidates:    4,
		Accepted:      3,
		Rejected:      1,
		ProviderCalls: 22,
		BudgetSpent:   22,
		QueriesRun:    9,
	}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	run, err = store.Run(ctx, id)
	if err != nil {
		t.Fatalf("read finished run: %v", err)
	}
	if run.Status != "completed" || run.Candidates != 4 || run.Accepted != 3 || run.BudgetSpent != 22 {
		t.Errorf("run = %+v, want the finished counters stored", run)
	}
	if run.FinishedAt.IsZero() {
		t.Error("finished_at is zero after finishing the run")
	}

	if _, err := store.Run(ctx, "does-not-exist"); !errors.Is(err, persistence.ErrNotFound) {
		t.Errorf("unknown run error = %v, want ErrNotFound", err)
	}
}

func TestFinishUnknownRunReportsNotFound(t *testing.T) {
	store := newStore(t)
	if err := store.FinishRun(context.Background(), "missing", persistence.RunResult{}); !errors.Is(err, persistence.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestRecentRunsNewestFirst(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	var ids []string
	for i := 0; i < 3; i++ {
		id, err := store.CreateRun(ctx, persistence.Run{ProfileName: "p", StartedAt: base.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatalf("create run %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	runs, err := store.RecentRuns(ctx, 0)
	if err != nil {
		t.Fatalf("recent runs: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3", len(runs))
	}
	// ids[2] was created last and must lead the list.
	for i, want := range []string{ids[2], ids[1], ids[0]} {
		if runs[i].ID != want {
			t.Errorf("runs[%d].ID = %s, want %s (newest first)", i, runs[i].ID, want)
		}
	}
	if !runs[0].StartedAt.After(runs[2].StartedAt) {
		t.Errorf("started_at not descending: %v then %v", runs[0].StartedAt, runs[2].StartedAt)
	}
}

func TestUpsertMergesByDomainAndAppendsEvidence(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	runID, err := store.CreateRun(ctx, persistence.Run{ProfileName: "p"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	first := seedCandidate()
	stored, err := store.UpsertCandidates(ctx, runID, []candidate.Candidate{first})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("got %d stored candidates, want 1", len(stored))
	}
	id := stored[0].ID
	if id == "" {
		t.Fatal("stored candidate has no id")
	}

	// The second run sees the same company from a different surface, with a
	// thinner record: no industry, no employee band. The merge must keep what the
	// first run learned rather than blanking it.
	second := candidate.Candidate{
		Name:    "Northwind Robotics Inc",
		Domain:  "northwind.test",
		URL:     "https://www.northwind.test/products",
		Country: "US",
		Evidence: []candidate.Evidence{{
			Source:     candidate.SourceSearch,
			Method:     candidate.MethodSearchResult,
			URL:        "https://search.test/results?q=northwind",
			Query:      "northwind robotics us",
			Snippet:    "Northwind Robotics | Industrial Automation",
			ObservedAt: time.Now().UTC(),
		}},
	}
	stored, err = store.UpsertCandidates(ctx, runID, []candidate.Candidate{second})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if stored[0].ID != id {
		t.Fatalf("merge produced a new row %s, want the existing %s", stored[0].ID, id)
	}

	got, err := store.Candidate(ctx, id)
	if err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	if got.Candidate.Industry != "Industrial Automation" {
		t.Errorf("industry = %q, want the first run's value to survive the merge", got.Candidate.Industry)
	}
	if got.Candidate.EmployeeHint != "51-200" {
		t.Errorf("employee_hint = %q, want the first run's value to survive the merge", got.Candidate.EmployeeHint)
	}
	if got.Candidate.URL != "https://www.northwind.test/products" {
		t.Errorf("url = %q, want the newer landing page", got.Candidate.URL)
	}
	if len(got.Candidate.Evidence) != 2 {
		t.Errorf("got %d evidence rows, want both observations", len(got.Candidate.Evidence))
	}
	if got.Candidate.SourceCount != 2 {
		t.Errorf("source_count = %d, want 2 distinct sources recomputed from evidence", got.Candidate.SourceCount)
	}
	if got.Candidate.Confidence < 0.6 {
		t.Errorf("confidence = %v, want the higher of the two observations", got.Candidate.Confidence)
	}
	if !got.Candidate.LastSeen.After(got.Candidate.FirstSeen) && !got.Candidate.LastSeen.Equal(got.Candidate.FirstSeen) {
		t.Errorf("last_seen %v before first_seen %v", got.Candidate.LastSeen, got.Candidate.FirstSeen)
	}
}

func TestUpsertDoesNotDuplicateRepeatedEvidence(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	runID, err := store.CreateRun(ctx, persistence.Run{ProfileName: "p"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	c := seedCandidate()
	// Three identical upserts stand in for a run that was resumed and replayed
	// its whole seed list.
	for i := 0; i < 3; i++ {
		got, err := store.UpsertCandidates(ctx, runID, []candidate.Candidate{c})
		if err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
		if len(got) != 1 {
			t.Fatalf("upsert %d stored %d candidates, want 1", i, len(got))
		}
	}

	list, err := store.Candidates(ctx, persistence.CandidateFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d candidates, want the three replays collapsed to one", len(list))
	}
	full, err := store.Candidate(ctx, list[0].ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(full.Candidate.Evidence) != 1 {
		t.Errorf("got %d evidence rows, want 1: a replay must not append a duplicate claim", len(full.Candidate.Evidence))
	}
}

func TestUpsertSkipsInvalidCandidateWithoutFailingTheBatch(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	runID, err := store.CreateRun(ctx, persistence.Run{ProfileName: "p"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	bad := candidate.Candidate{Name: "No Domain Ltd", URL: "https://nodomain.test/"}
	bad.Domain = "not a domain"
	good := seedCandidate()
	good.Domain = "second.test"

	stored, err := store.UpsertCandidates(ctx, runID, []candidate.Candidate{bad, good})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if len(stored) != 1 {
		t.Errorf("got %d stored candidates, want the invalid one skipped and the valid one kept", len(stored))
	}
}

func TestUpsertEmptyInputIsANoOp(t *testing.T) {
	store := newStore(t)
	got, err := store.UpsertCandidates(context.Background(), "run", nil)
	if err != nil || got != nil {
		t.Errorf("UpsertCandidates(nil) = %v, %v, want nil, nil", got, err)
	}
}

func TestCandidateFilter(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	runA, err := store.CreateRun(ctx, persistence.Run{ProfileName: "a"})
	if err != nil {
		t.Fatalf("create run a: %v", err)
	}
	runB, err := store.CreateRun(ctx, persistence.Run{ProfileName: "b"})
	if err != nil {
		t.Fatalf("create run b: %v", err)
	}

	acme := seedCandidate()
	acme.Domain = "acme.test"
	globex := seedCandidate()
	globex.Domain = "globex.test"
	if _, err := store.UpsertCandidates(ctx, runA, []candidate.Candidate{acme}); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if _, err := store.UpsertCandidates(ctx, runB, []candidate.Candidate{globex}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}

	scored := []persistence.Scored{
		{ID: "missing", Score: 1, Verdict: "accept"}, // unknown id, not an error
	}
	all, err := store.Candidates(ctx, persistence.CandidateFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d candidates, want 2", len(all))
	}
	if err := store.SaveRanking(ctx, scored); err != nil {
		t.Fatalf("save ranking for unknown id: %v", err)
	}

	byRun, err := store.Candidates(ctx, persistence.CandidateFilter{RunID: runA})
	if err != nil {
		t.Fatalf("list by run: %v", err)
	}
	if len(byRun) != 1 || byRun[0].Candidate.Domain != "acme.test" {
		t.Errorf("by run = %+v, want only the candidate last touched by run A", byRun)
	}

	byStatus, err := store.Candidates(ctx, persistence.CandidateFilter{Status: "new"})
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(byStatus) != 2 {
		t.Errorf("got %d candidates with status new, want 2", len(byStatus))
	}
	none, err := store.Candidates(ctx, persistence.CandidateFilter{Status: "accepted"})
	if err != nil {
		t.Fatalf("list by absent status: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("got %d candidates with status accepted, want none", len(none))
	}
}

func TestSaveRankingStoresExplainability(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	runID, _ := store.CreateRun(ctx, persistence.Run{ProfileName: "p"})
	stored, err := store.UpsertCandidates(ctx, runID, []candidate.Candidate{seedCandidate()})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("got %d stored candidates, want 1", len(stored))
	}

	err = store.SaveRanking(ctx, []persistence.Scored{{
		ID:      stored[0].ID,
		Score:   0.8125,
		Verdict: "accept",
		Explain: "exact industry match in a target country",
		Factors: []persistence.RankFactor{
			{Name: "industry", Label: "Industry fit", Weight: 0.4, Score: 1, Detail: "industrial automation"},
			{Name: "geography", Label: "Geography", Weight: 0.25, Score: 1, Detail: "US target"},
		},
	}})
	if err != nil {
		t.Fatalf("save ranking: %v", err)
	}

	got, err := store.Candidate(ctx, stored[0].ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.HasScore || got.Score != 0.8125 {
		t.Errorf("score = %v (has=%v), want 0.8125 stored", got.Score, got.HasScore)
	}
	if got.Verdict != "accept" {
		t.Errorf("verdict = %q, want accept", got.Verdict)
	}
	if len(got.RankFactors) != 2 {
		t.Fatalf("got %d rank factors, want 2 round-tripped through jsonb", len(got.RankFactors))
	}
	if got.RankFactors[0].Label != "Industry fit" || got.RankFactors[0].Detail != "industrial automation" {
		t.Errorf("factor = %+v, want the label and detail preserved", got.RankFactors[0])
	}
	if got.RankExplain == "" {
		t.Error("rank explanation was not stored")
	}
}

func TestUnrankedCandidateIsDistinctFromRankedZero(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	runID, _ := store.CreateRun(ctx, persistence.Run{ProfileName: "p"})
	stored, err := store.UpsertCandidates(ctx, runID, []candidate.Candidate{seedCandidate()})
	if err != nil || len(stored) != 1 {
		t.Fatalf("upsert = %v, %d stored, want 1 stored", err, len(stored))
	}

	got, err := store.Candidate(ctx, stored[0].ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.HasScore {
		t.Error("a candidate that has never been ranked reports a score of zero as if it were a decision")
	}
	if err := store.SaveRanking(ctx, []persistence.Scored{{ID: stored[0].ID, Score: 0, Verdict: "reject"}}); err != nil {
		t.Fatalf("save ranking: %v", err)
	}
	got, err = store.Candidate(ctx, stored[0].ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.HasScore || got.Score != 0 || got.Verdict != "reject" {
		t.Errorf("got score %v has=%v verdict %q, want a stored decision of zero", got.Score, got.HasScore, got.Verdict)
	}
}

func TestRecordAttemptIsIdempotentPerQuestion(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	runID, err := store.CreateRun(ctx, persistence.Run{ProfileName: "p"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	a := lineage.Attempt{
		RunID:      runID,
		Provider:   "search",
		Query:      "warehouse automation us",
		Language:   "en",
		Outcome:    lineage.OutcomeTruncated,
		Candidates: 10,
		Cursor:     "page=2",
		Duration:   1500 * time.Millisecond,
		At:         time.Now().UTC(),
	}
	if err := store.RecordAttempt(ctx, a); err != nil {
		t.Fatalf("record: %v", err)
	}
	// The same question asked again after a resume updates its row.
	a.Outcome = lineage.OutcomeProduced
	a.Candidates = 25
	a.Cursor = ""
	if err := store.RecordAttempt(ctx, a); err != nil {
		t.Fatalf("re-record: %v", err)
	}

	all, err := store.AttemptsFor(ctx, runID)
	if err != nil {
		t.Fatalf("attempts: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d attempts, want the repeat collapsed into one row", len(all))
	}
	if all[0].Outcome != lineage.OutcomeProduced || all[0].Candidates != 25 {
		t.Errorf("attempt = %+v, want the later observation to win", all[0])
	}
	if all[0].Duration != 1500*time.Millisecond {
		t.Errorf("duration = %v, want it round-tripped from milliseconds", all[0].Duration)
	}
}

func TestPendingAttemptsAreOnlyTheUnfinished(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	runID, _ := store.CreateRun(ctx, persistence.Run{ProfileName: "p"})

	base := time.Now().UTC()
	attempts := []lineage.Attempt{
		{RunID: runID, Provider: "search", Query: "q1", Outcome: lineage.OutcomeProduced, Candidates: 3, At: base},
		{RunID: runID, Provider: "search", Query: "q2", Outcome: lineage.OutcomeTruncated, Cursor: "p2", At: base},
		{RunID: runID, Provider: "rdap", Query: "q3", Outcome: lineage.OutcomeBudgeted, At: base},
		{RunID: runID, Provider: "rdap", Query: "q4", Outcome: lineage.OutcomeFailed, Error: "timeout", At: base},
		{RunID: runID, Provider: "certificate", Query: "q5", Outcome: lineage.OutcomeEmpty, At: base},
	}
	for _, a := range attempts {
		if err := store.RecordAttempt(ctx, a); err != nil {
			t.Fatalf("record %q: %v", a.Query, err)
		}
	}

	pending, err := store.PendingAttempts(ctx, runID)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("got %d pending attempts, want the truncated and budgeted ones only", len(pending))
	}
	queries := map[string]bool{pending[0].Query: true, pending[1].Query: true}
	if !queries["q2"] || !queries["q3"] {
		t.Errorf("pending queries = %v, want q2 and q3", queries)
	}
	// A failed attempt is finished: it is not retried by resuming, because the
	// runner's circuit breaker is what decides whether a provider deserves
	// another try.
	if queries["q4"] {
		t.Error("a failed attempt was reported as pending work")
	}
}

func TestAttemptsAreScopedToTheirRun(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	runA, err := store.CreateRun(ctx, persistence.Run{ProfileName: "a"})
	if err != nil {
		t.Fatalf("create run a: %v", err)
	}
	runB, err := store.CreateRun(ctx, persistence.Run{ProfileName: "b"})
	if err != nil {
		t.Fatalf("create run b: %v", err)
	}

	for _, id := range []string{runA, runB} {
		if err := store.RecordAttempt(ctx, lineage.Attempt{
			RunID: id, Provider: "search", Query: "same question", Outcome: lineage.OutcomeProduced,
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	got, err := store.AttemptsFor(ctx, runA)
	if err != nil {
		t.Fatalf("attempts: %v", err)
	}
	if len(got) != 1 || got[0].RunID != runA {
		t.Errorf("got %d attempts for run A, want 1: the identical question in run B must not collide", len(got))
	}
}

func TestClosedStoreRefusesWork(t *testing.T) {
	store := &persistence.PostgresStore{}
	ctx := context.Background()
	if _, err := store.CreateRun(ctx, persistence.Run{}); !errors.Is(err, persistence.ErrStoreClosed) {
		t.Errorf("CreateRun error = %v, want ErrStoreClosed", err)
	}
	if err := store.FinishRun(ctx, "x", persistence.RunResult{}); !errors.Is(err, persistence.ErrStoreClosed) {
		t.Errorf("FinishRun error = %v, want ErrStoreClosed", err)
	}
	if _, err := store.Run(ctx, "x"); !errors.Is(err, persistence.ErrStoreClosed) {
		t.Errorf("Run error = %v, want ErrStoreClosed", err)
	}
	if _, err := store.RecentRuns(ctx, 0); !errors.Is(err, persistence.ErrStoreClosed) {
		t.Errorf("RecentRuns error = %v, want ErrStoreClosed", err)
	}
	if _, err := store.UpsertCandidates(ctx, "r", []candidate.Candidate{{Domain: "x.test"}}); !errors.Is(err, persistence.ErrStoreClosed) {
		t.Errorf("UpsertCandidates error = %v, want ErrStoreClosed", err)
	}
	if err := store.SaveRanking(ctx, []persistence.Scored{{ID: "x"}}); !errors.Is(err, persistence.ErrStoreClosed) {
		t.Errorf("SaveRanking error = %v, want ErrStoreClosed", err)
	}
	if err := store.RecordAttempt(ctx, lineage.Attempt{}); !errors.Is(err, persistence.ErrStoreClosed) {
		t.Errorf("RecordAttempt error = %v, want ErrStoreClosed", err)
	}
	if _, err := store.AttemptsFor(ctx, "r"); !errors.Is(err, persistence.ErrStoreClosed) {
		t.Errorf("AttemptsFor error = %v, want ErrStoreClosed", err)
	}
	if _, err := store.PendingAttempts(ctx, "r"); !errors.Is(err, persistence.ErrStoreClosed) {
		t.Errorf("PendingAttempts error = %v, want ErrStoreClosed", err)
	}
	// Close on a store with no pool must not panic.
	store.Close()
}
