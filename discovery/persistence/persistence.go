// Package persistence stores discovery's output durably.
//
// Every module owns its own migrations and the lead-engine CLI passes them all
// to db.Migrate together, so discovery's schema ships with discovery rather than
// in a central place.
package persistence

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
	"github.com/hmza-hb/lead-intelligence/platform/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// MigrationSource returns discovery's migrations for db.Migrate.
func MigrationSource() db.Source {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		// Unreachable: the directory is embedded, so a failure here means the
		// binary was built wrong rather than that the environment is bad.
		panic("discovery: embedded migrations are missing: " + err.Error())
	}
	return db.Source{Module: "discovery", FS: sub}
}

// Store errors.
var (
	// ErrNotFound is returned when a run or candidate ID is unknown.
	ErrNotFound = errors.New("discovery: not found")
	// ErrStoreClosed is returned once the store has been closed.
	ErrStoreClosed = errors.New("discovery: store is closed")
)

// Store is what discovery writes its output to. It is an interface so the
// service can be exercised without a database, and so this module can be used
// with an in-memory implementation by a caller that does not want one.
type Store interface {
	// CreateRun records a started run and returns its ID.
	CreateRun(ctx context.Context, r Run) (string, error)
	// FinishRun records the outcome of a run.
	FinishRun(ctx context.Context, id string, r RunResult) error
	// Run reads a run back.
	Run(ctx context.Context, id string) (Run, error)
	// RecentRuns lists runs newest first, for the operations view.
	RecentRuns(ctx context.Context, limit int) ([]RunSummary, error)
	// UpsertCandidates merges candidates into the store and returns them with the
	// IDs the store assigned.
	//
	// Merging rather than inserting is what makes a second run useful: the same
	// domain discovered again is one row with a longer evidence trail, not a
	// duplicate for a salesperson to dismiss by hand.
	UpsertCandidates(ctx context.Context, runID string, in []candidate.Candidate) ([]StoredCandidate, error)
	// Candidate reads one candidate with its evidence.
	Candidate(ctx context.Context, id string) (StoredCandidate, error)
	// Candidates lists candidates, newest first, filtered by status and verdict
	// when either is non-empty.
	Candidates(ctx context.Context, f CandidateFilter) ([]StoredCandidate, error)
	// SaveRanking stores the ranker's output against the candidates it scored.
	SaveRanking(ctx context.Context, decisions []Scored) error
	// RecordAttempt writes one provider attempt to the lineage table.
	RecordAttempt(ctx context.Context, a lineage.Attempt) error
	// AttemptsFor returns the attempts recorded for a run, oldest first.
	AttemptsFor(ctx context.Context, runID string) ([]lineage.Attempt, error)
	// PendingAttempts returns the attempts that left work unfinished, so a resumed
	// run knows what to pick up.
	PendingAttempts(ctx context.Context, runID string) ([]lineage.Attempt, error)
	// Close releases the store.
	Close()
}

// Run is a discovery run. The same struct is used to create one and to read one
// back, so a caller does not have to learn two shapes for the same row.
type Run struct {
	// ID is assigned by the store on create.
	ID string `json:"id"`
	// Status is the lifecycle position: running, completed, failed, or cancelled.
	Status string `json:"status"`
	// ProfileName is the targeting profile the run used, for the operations view.
	ProfileName string `json:"profile_name"`
	// SeedsTotal is how many seeds the run started from.
	SeedsTotal int `json:"seeds_total"`
	// MaxDepth is how many expansion rounds the engine was allowed.
	MaxDepth int `json:"max_depth"`
	// BudgetLimit is the run's total provider call ceiling.
	BudgetLimit int `json:"budget_limit"`
	// Config is the effective configuration with secrets removed. It is stored so
	// the run can be explained and reproduced later.
	Config map[string]any `json:"config"`
	// The counters below are zero on create and filled in when the run finishes.
	Candidates       int `json:"candidates"`
	Accepted         int `json:"accepted"`
	Rejected         int `json:"rejected"`
	ProviderCalls    int `json:"provider_calls"`
	ProviderFailures int `json:"provider_failures"`
	QueriesRun       int `json:"queries_run"`
	QueriesPending   int `json:"queries_pending"`
	BudgetSpent      int `json:"budget_spent"`
	// Error explains a failed or cancelled run.
	Error string `json:"error,omitempty"`
	// StartedAt defaults to now.
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is set when the run ends.
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// RunResult is what a finished run reports.
type RunResult struct {
	Status           string    `json:"status"`
	Candidates       int       `json:"candidates"`
	Accepted         int       `json:"accepted"`
	Rejected         int       `json:"rejected"`
	ProviderCalls    int       `json:"provider_calls"`
	ProviderFailures int       `json:"provider_failures"`
	QueriesRun       int       `json:"queries_run"`
	QueriesPending   int       `json:"queries_pending"`
	BudgetSpent      int       `json:"budget_spent"`
	Error            string    `json:"error,omitempty"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
}

// RunSummary is one row in the run list.
//
// Every field carries a JSON tag because these structs go straight out over the
// API. A struct without them serialises as Go field names, and an API that
// returns "ProfileName" where the rest of it returns "profile_name" is a
// contract nobody can write a client against.
type RunSummary struct {
	ID            string    `json:"id"`
	Status        string    `json:"status"`
	ProfileName   string    `json:"profile_name"`
	SeedsTotal    int       `json:"seeds_total"`
	Candidates    int       `json:"candidates"`
	Accepted      int       `json:"accepted"`
	Rejected      int       `json:"rejected"`
	ProviderCalls int       `json:"provider_calls"`
	BudgetSpent   int       `json:"budget_spent"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// StoredCandidate is a candidate as the store holds it.
type StoredCandidate struct {
	ID        string `json:"id"`
	Candidate candidate.Candidate
	// FirstRunID and LastRunID are the runs that first proposed and most recently
	// touched this candidate. Both are needed: the first tells a human when the
	// company entered the system, the last tells them whether it is still there.
	FirstRunID string `json:"first_run_id"`
	LastRunID  string `json:"last_run_id"`
	// Score and Verdict are the stored ranking output. HasScore distinguishes "not
	// ranked" from "ranked zero", which are very different states for a
	// salesperson looking at a list.
	Score       float64      `json:"score"`
	HasScore    bool         `json:"has_score"`
	Verdict     string       `json:"verdict,omitempty"`
	RankFactors []RankFactor `json:"rank_factors,omitempty"`
	RankExplain string       `json:"rank_explain,omitempty"`
}

// RankFactor is one explained contribution to a candidate's score, stored so a
// stored score can be explained without re-running the ranker.
type RankFactor struct {
	// Name identifies the factor, such as industry or geography.
	Name string `json:"name"`
	// Label is the human sentence explaining it.
	Label string `json:"label"`
	// Weight is the factor's maximum possible contribution, which is what an
	// operator tunes.
	Weight float64 `json:"weight"`
	// Score is what the factor actually contributed, in [0,weight]. It is kept
	// separate from Weight so a partially satisfied factor stays explainable
	// instead of collapsing to a pass or a fail.
	Score        float64 `json:"score"`
	Contribution float64 `json:"contribution"`
	// Detail is the specific observation, such as the matched industry term.
	Detail string `json:"detail"`
}

// CandidateFilter narrows a candidate listing.
type CandidateFilter struct {
	// Status filters by lifecycle status when non-empty.
	Status string
	// Verdict filters by ranking verdict when non-empty.
	Verdict string
	// RunID restricts to candidates a run touched when non-empty.
	RunID string
	// Limit caps the rows returned. Zero means DefaultQueryLimit.
	Limit int
	// Offset pages through the result set.
	Offset int
	// Order selects the sort. Zero means ranked-first, which is what a lead
	// list wants; CandidateOrderRecent is for operational views that care when
	// a row last changed.
	Order CandidateOrder
}

// CandidateOrder selects how a candidate list is sorted.
type CandidateOrder int

const (
	// CandidateOrderRanked sorts by score descending, unranked last.
	CandidateOrderRanked CandidateOrder = iota
	// CandidateOrderRecent sorts by last observation, newest first.
	CandidateOrderRecent
)

// DefaultQueryLimit is the page size used when a filter does not set one.
const DefaultQueryLimit = 50

// MaxQueryLimit bounds a single page, so a client cannot ask for the whole
// table and take the service down with it.
const MaxQueryLimit = 500

// PostgresStore is the durable Store.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore wraps a pool. The pool is not owned by the store: the caller
// closes it, so one pool can be shared across modules.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Pool exposes the underlying pool for callers that need their own queries.
func (s *PostgresStore) Pool() *pgxpool.Pool { return s.pool }

// Close releases the pool. It satisfies Store so a caller can defer it.
func (s *PostgresStore) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// CreateRun records a started run.
func (s *PostgresStore) CreateRun(ctx context.Context, r Run) (string, error) {
	if s.pool == nil {
		return "", ErrStoreClosed
	}
	started := r.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	cfg := r.Config
	if cfg == nil {
		// A nil map marshals to SQL NULL, and the column is NOT NULL. The
		// default only applies to an omitted column, so the empty case has to be
		// made explicit here.
		cfg = map[string]any{}
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO discovery_runs
			(profile_name, seeds_total, max_depth, budget_limit, config, status, started_at)
		VALUES ($1, $2, $3, $4, $5, 'running', $6)
		RETURNING id`,
		r.ProfileName, r.SeedsTotal, r.MaxDepth, r.BudgetLimit, cfg, started,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("discovery: create run: %w", err)
	}
	return id, nil
}

// FinishRun records the outcome. finished defaults to now.
func (s *PostgresStore) FinishRun(ctx context.Context, id string, r RunResult) error {
	if s.pool == nil {
		return ErrStoreClosed
	}
	finished := r.FinishedAt
	if finished.IsZero() {
		finished = time.Now().UTC()
	}
	status := r.Status
	if status == "" {
		status = "completed"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE discovery_runs
		   SET status = $2, candidates = $3, accepted = $4, rejected = $5,
		       provider_calls = $6, provider_failures = $7, queries_run = $8,
		       queries_pending = $9, budget_spent = $10, error = $11, finished_at = $12
		 WHERE id = $1`,
		id, status, r.Candidates, r.Accepted, r.Rejected, r.ProviderCalls,
		r.ProviderFailures, r.QueriesRun, r.QueriesPending, r.BudgetSpent,
		r.Error, finished)
	if err != nil {
		return fmt.Errorf("discovery: finish run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: run %s", ErrNotFound, id)
	}
	return nil
}

const runColumns = `id, status, profile_name, seeds_total, candidates, accepted, rejected,
	provider_calls, provider_failures, queries_run, queries_pending, budget_spent,
	budget_limit, max_depth, error, started_at, finished_at, config`

func scanRun(row pgx.Row) (Run, error) {
	var (
		r        Run
		status   string
		cfg      map[string]any
		finished *time.Time
		errText  string
	)
	err := row.Scan(&r.ID, &status, &r.ProfileName, &r.SeedsTotal, &r.Candidates,
		&r.Accepted, &r.Rejected, &r.ProviderCalls, &r.ProviderFailures,
		&r.QueriesRun, &r.QueriesPending, &r.BudgetSpent, &r.BudgetLimit,
		&r.MaxDepth, &errText, &r.StartedAt, &finished, &cfg)
	r.Status = status
	r.Error = errText
	if finished != nil {
		r.FinishedAt = *finished
	}
	r.Config = cfg
	return r, err
}

// Run reads one run.
func (s *PostgresStore) Run(ctx context.Context, id string) (Run, error) {
	if s.pool == nil {
		return Run{}, ErrStoreClosed
	}
	row := s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM discovery_runs WHERE id = $1`, id)
	r, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, fmt.Errorf("%w: run %s", ErrNotFound, id)
	}
	if err != nil {
		return Run{}, fmt.Errorf("discovery: read run: %w", err)
	}
	return r, nil
}

// RecentRuns lists runs newest first.
func (s *PostgresStore) RecentRuns(ctx context.Context, limit int) ([]RunSummary, error) {
	if s.pool == nil {
		return nil, ErrStoreClosed
	}
	if limit <= 0 || limit > MaxQueryLimit {
		limit = DefaultQueryLimit
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+runColumns+` FROM discovery_runs ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("discovery: list runs: %w", err)
	}
	defer rows.Close()
	var out []RunSummary
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("discovery: scan run: %w", err)
		}
		out = append(out, RunSummary{
			ID: r.ID, Status: r.Status, ProfileName: r.ProfileName,
			SeedsTotal: r.SeedsTotal, Candidates: r.Candidates, Accepted: r.Accepted,
			Rejected: r.Rejected, ProviderCalls: r.ProviderCalls, BudgetSpent: r.BudgetSpent,
			StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Error: r.Error,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discovery: list runs: %w", err)
	}
	return out, nil
}
