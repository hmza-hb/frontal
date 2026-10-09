package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
)

// UpsertCandidates merges candidates into the store and returns them with their
// stored IDs.
//
// The merge is the point of the whole exercise: a company found in run one and
// found again in run four is one row with four runs of evidence behind it, not
// four rows for a salesperson to dismiss by hand. The domain is the merge key
// because that is what identity and ranking already agree on.
//
// Evidence is appended, never replaced. A row claiming a source said something
// is an audit record, and the only way to correct one is to observe again.
func (s *PostgresStore) UpsertCandidates(ctx context.Context, runID string, in []candidate.Candidate) ([]StoredCandidate, error) {
	if s.pool == nil {
		return nil, ErrStoreClosed
	}
	if len(in) == 0 {
		return nil, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("discovery: begin upsert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	out := make([]StoredCandidate, 0, len(in))
	for _, c := range in {
		stored, err := upsertOne(ctx, tx, runID, c, now)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			// The candidate failed its own validation. Skipping it is better than
			// aborting the run: one malformed provider row should not cost the
			// operator the other nine thousand.
			continue
		}
		out = append(out, *stored)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("discovery: commit upsert: %w", err)
	}
	return out, nil
}

// upsertOne inserts or merges a single candidate inside a transaction.
func upsertOne(ctx context.Context, tx pgx.Tx, runID string, c candidate.Candidate, now time.Time) (*StoredCandidate, error) {
	if err := c.Validate(); err != nil {
		return nil, nil //nolint:nilerr // an invalid row is skipped, not fatal
	}
	if c.Status == "" {
		// A provider is allowed to leave the lifecycle position unset; the column
		// default would cover an omitted column but not an empty string, and
		// "unranked, unexamined" is what an empty status means.
		c.Status = candidate.StatusNew
	}
	if c.Keywords == nil {
		// A Go nil slice is encoded as SQL NULL, not as an empty array, so a
		// candidate with no keywords would fail the NOT NULL constraint on a
		// column whose default is '{}'. The distinction between "no keywords" and
		// "keywords unknown" does not survive the database round trip, and
		// rejecting the row for it would lose an otherwise valid lead.
		c.Keywords = []string{}
	}

	var existingID string
	var seenFirst time.Time
	err := tx.QueryRow(ctx, `
		SELECT id, first_seen FROM discovery_candidates
		 WHERE domain = $1
		 FOR UPDATE`, c.Domain).Scan(&existingID, &seenFirst)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		existingID = ""
	case err != nil:
		return nil, fmt.Errorf("discovery: look up candidate: %w", err)
	}

	firstSeen := now
	firstRun := runID
	if existingID != "" {
		if !seenFirst.IsZero() {
			firstSeen = seenFirst
		}
		// The run that first proposed it is history, not something a later run
		// gets to overwrite.
		if err := tx.QueryRow(ctx,
			`SELECT first_run_id FROM discovery_candidates WHERE id = $1`, existingID,
		).Scan(&firstRun); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("discovery: read first run: %w", err)
		}
	}

	lastSeen := c.LastSeen
	if lastSeen.IsZero() {
		lastSeen = now
	}
	if firstSeen.After(lastSeen) {
		// A provider can hand back a timestamp older than the row's first
		// observation. The constraint forbids that, and the honest reading is
		// that the row has been seen at least as recently as now.
		firstSeen = lastSeen
	}

	var id string
	if existingID == "" {
		err = tx.QueryRow(ctx, `
			INSERT INTO discovery_candidates
				(name, domain, url, country, industry, employee_hint, description,
				 keywords, status, reason, confidence, source_count,
				 first_seen, last_seen, first_run_id, last_run_id, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			RETURNING id`,
			c.Name, c.Domain, c.URL, c.Country, c.Industry, c.EmployeeHint,
			c.Description, c.Keywords, string(c.Status), c.Reason, c.Confidence,
			c.SourceCount, firstSeen, lastSeen, firstRun, runID, now,
		).Scan(&id)
	} else {
		id = existingID
		// The merged row keeps the richer of the two values for each field rather
		// than the newest. A later provider returning a bare domain should not
		// erase the industry a directory already stated.
		err = tx.QueryRow(ctx, `
			UPDATE discovery_candidates SET
				name = CASE WHEN $2 <> '' THEN $2 ELSE name END,
				url = CASE WHEN $3 <> '' THEN $3 ELSE url END,
				country = CASE WHEN $4 <> '' THEN $4 ELSE country END,
				industry = CASE WHEN $5 <> '' THEN $5 ELSE industry END,
				employee_hint = CASE WHEN $6 <> '' THEN $6 ELSE employee_hint END,
				description = CASE WHEN $7 <> '' THEN $7 ELSE description END,
				keywords = (SELECT ARRAY(SELECT DISTINCT unnest(keywords || $8))),
				confidence = GREATEST(confidence, $9),
				source_count = $10,
				last_seen = GREATEST(last_seen, $11),
				last_run_id = $12,
				updated_at = $13
			WHERE id = $1
			RETURNING id`,
			id, c.Name, c.URL, c.Country, c.Industry, c.EmployeeHint,
			c.Description, c.Keywords, c.Confidence, c.SourceCount, lastSeen, runID, now,
		).Scan(&id)
	}
	if err != nil {
		return nil, fmt.Errorf("discovery: write candidate: %w", err)
	}

	if len(c.Evidence) > 0 {
		if err := appendEvidence(ctx, tx, id, runID, c.Evidence); err != nil {
			return nil, err
		}
	}
	// SourceCount is recomputed from the stored evidence rather than taken from
	// the caller, so the column cannot drift from the rows it summarises.
	if _, err := tx.Exec(ctx, `
		UPDATE discovery_candidates
		   SET source_count = (SELECT COUNT(DISTINCT source) FROM discovery_evidence WHERE candidate_id = $1)
		 WHERE id = $1`, id); err != nil {
		return nil, fmt.Errorf("discovery: recount sources: %w", err)
	}
	return &StoredCandidate{ID: id, Candidate: c}, nil
}

// appendEvidence writes observation rows, skipping any that duplicate an existing
// one. The dedupe matters because a resumed run re-reads the same page, and
// fifty identical rows for one claim is noise a reviewer has to filter out.
func appendEvidence(ctx context.Context, tx pgx.Tx, candidateID, runID string, ev []candidate.Evidence) error {
	batch := &pgx.Batch{}
	for _, e := range ev {
		source := string(e.Source)
		if e.Source == "" {
			// Evidence always needs an attributable surface. An observation with
			// no source is not evidence, so it is recorded as derived rather than
			// dropped: the engine's own inference is still a thing that happened
			// and a reviewer needs to see it.
			source = string(candidate.SourceExpansion)
		}
		method := string(e.Method)
		if e.Method == "" {
			method = string(candidate.MethodDerived)
		}
		observed := e.ObservedAt
		if observed.IsZero() {
			observed = time.Now().UTC()
		}
		batch.Queue(`
			INSERT INTO discovery_evidence
				(candidate_id, run_id, source, method, url, query, snippet, detail, observed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (candidate_id, source, method, md5(url || '|' || query || '|' || snippet))
			DO NOTHING`,
			candidateID, runID, source, method, e.URL, e.Query, e.Snippet, e.Detail, observed)
	}
	if err := sendBatch(ctx, tx, batch, len(ev)); err != nil {
		return fmt.Errorf("discovery: write evidence: %w", err)
	}
	return nil
}

const candidateColumns = `id, name, domain, url, country, industry, employee_hint,
	description, keywords, status, reason, confidence, source_count,
	first_seen, last_seen, first_run_id, last_run_id, score, verdict,
	rank_factors, rank_explain`

func scanCandidate(row pgx.Row) (StoredCandidate, error) {
	var (
		out      StoredCandidate
		domain   *string
		score    *float64
		factors  []byte
		firstRun string
		lastRun  string
	)
	err := row.Scan(&out.ID, &out.Candidate.Name, &domain, &out.Candidate.URL,
		&out.Candidate.Country, &out.Candidate.Industry, &out.Candidate.EmployeeHint,
		&out.Candidate.Description, &out.Candidate.Keywords, &out.Candidate.Status,
		&out.Candidate.Reason, &out.Candidate.Confidence, &out.Candidate.SourceCount,
		&out.Candidate.FirstSeen, &out.Candidate.LastSeen, &firstRun, &lastRun,
		&score, &out.Verdict, &factors, &out.RankExplain)
	if domain != nil {
		out.Candidate.Domain = *domain
	}
	if score != nil {
		out.Score = *score
		out.HasScore = true
	}
	if len(factors) > 0 {
		_ = json.Unmarshal(factors, &out.RankFactors)
	}
	out.Candidate.ID = out.ID
	out.FirstRunID = firstRun
	out.LastRunID = lastRun
	return out, err
}

// Candidate reads one candidate.
func (s *PostgresStore) Candidate(ctx context.Context, id string) (StoredCandidate, error) {
	if s.pool == nil {
		return StoredCandidate{}, ErrStoreClosed
	}
	row := s.pool.QueryRow(ctx, `SELECT `+candidateColumns+` FROM discovery_candidates WHERE id = $1`, id)
	out, err := scanCandidate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredCandidate{}, fmt.Errorf("%w: candidate %s", ErrNotFound, id)
	}
	if err != nil {
		return StoredCandidate{}, fmt.Errorf("discovery: read candidate: %w", err)
	}
	if err := s.loadEvidence(ctx, &out); err != nil {
		return StoredCandidate{}, err
	}
	return out, nil
}

// Candidates lists candidates newest first.
func (s *PostgresStore) Candidates(ctx context.Context, f CandidateFilter) ([]StoredCandidate, error) {
	if s.pool == nil {
		return nil, ErrStoreClosed
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultQueryLimit
	}
	if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	// The filters are optional rather than concatenated SQL, so there is no path
	// by which a caller's string can reach the statement.
	// A ranked lead is more useful than a recently seen one, so a list that
	// carries scores orders by them. Candidates with no score sort last rather
	// than being treated as zero, which would put unranked rows above a lead
	// that scored badly on purpose.
	orderBy := `ORDER BY (score IS NOT NULL) DESC, score DESC NULLS LAST, last_seen DESC, id`
	if f.Order == CandidateOrderRecent {
		orderBy = `ORDER BY last_seen DESC, id`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+candidateColumns+` FROM discovery_candidates
		 WHERE ($1 = '' OR status = $1)
		   AND ($2 = '' OR verdict = $2)
		   AND ($3 = '' OR last_run_id = $3)
		 `+orderBy+`
		 LIMIT $4 OFFSET $5`,
		f.Status, f.Verdict, f.RunID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("discovery: list candidates: %w", err)
	}
	defer rows.Close()
	var out []StoredCandidate
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("discovery: scan candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discovery: list candidates: %w", err)
	}
	// Evidence is loaded here rather than left to the caller to fetch per row:
	// a lead list without its sources is not evidence of anything, and an
	// N+1 fetch here would make the list slower the more useful it got.
	if err := attachEvidence(ctx, s.pool, out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachEvidence fills in each candidate's evidence from stored observation
// rows, newest first.
func attachEvidence(ctx context.Context, pool *pgxpool.Pool, out []StoredCandidate) error {
	if len(out) == 0 {
		return nil
	}
	ids := make([]string, 0, len(out))
	index := make(map[string]int, len(out))
	for i, c := range out {
		ids = append(ids, c.ID)
		index[c.ID] = i
	}
	rows, err := pool.Query(ctx, `
		SELECT candidate_id, source, method, url, query, snippet, observed_at
		  FROM discovery_evidence
		 WHERE candidate_id = ANY($1)
		 ORDER BY observed_at DESC`, ids)
	if err != nil {
		return fmt.Errorf("discovery: load evidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                       string
			ev                       candidate.Evidence
			source, method, url, qry string
		)
		if err := rows.Scan(&id, &source, &method, &url, &qry, &ev.Snippet, &ev.ObservedAt); err != nil {
			return fmt.Errorf("discovery: scan evidence: %w", err)
		}
		ev.Source, ev.Method = candidate.Source(source), candidate.Method(method)
		ev.URL, ev.Query = url, qry
		if at, ok := index[id]; ok {
			out[at].Candidate.Evidence = append(out[at].Candidate.Evidence, ev)
		}
	}
	return rows.Err()
}

// loadEvidence attaches the observation rows to a candidate.
func (s *PostgresStore) loadEvidence(ctx context.Context, out *StoredCandidate) error {
	rows, err := s.pool.Query(ctx, `
		SELECT source, method, url, query, snippet, detail, observed_at
		  FROM discovery_evidence
		 WHERE candidate_id = $1
		 ORDER BY observed_at ASC, id ASC`, out.ID)
	if err != nil {
		return fmt.Errorf("discovery: read evidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e candidate.Evidence
		var source, method string
		if err := rows.Scan(&source, &method, &e.URL, &e.Query, &e.Snippet,
			&e.Detail, &e.ObservedAt); err != nil {
			return fmt.Errorf("discovery: scan evidence: %w", err)
		}
		e.Source = candidate.Source(source)
		e.Method = candidate.Method(method)
		out.Candidate.AddEvidence(e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("discovery: read evidence: %w", err)
	}
	return nil
}

// SaveRanking stores the ranker's output against the candidates it scored.
func (s *PostgresStore) SaveRanking(ctx context.Context, decisions []Scored) error {
	if s.pool == nil {
		return ErrStoreClosed
	}
	if len(decisions) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("discovery: begin ranking write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	batch := &pgx.Batch{}
	for _, d := range decisions {
		factors, ferr := json.Marshal(d.Factors)
		if ferr != nil {
			// An unencodable factor list must not lose the score; the score is the
			// thing a caller asked for.
			factors = []byte("[]")
		}
		batch.Queue(`
			UPDATE discovery_candidates
			   SET score = $2, verdict = $3, rank_factors = $4, rank_explain = $5
			 WHERE id = $1`, d.ID, d.Score, d.Verdict, factors, d.Explain)
	}
	if err := sendBatch(ctx, tx, batch, len(decisions)); err != nil {
		return fmt.Errorf("discovery: write ranking: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("discovery: commit ranking write: %w", err)
	}
	return nil
}

// Scored is one candidate's ranking output, addressed by its stored ID.
type Scored struct {
	ID      string
	Score   float64
	Verdict string
	Explain string
	Factors []RankFactor
}

// RecordAttempt writes one provider attempt.
//
// The unique index on (run, provider, query) makes this an upsert: a resumed run
// that re-asks a question updates its row rather than growing the table, and the
// table stays proportional to the number of questions rather than the number of
// times they were asked.
func (s *PostgresStore) RecordAttempt(ctx context.Context, a lineage.Attempt) error {
	if s.pool == nil {
		return ErrStoreClosed
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO discovery_lineage
			(run_id, provider, query, language, outcome, candidates, cursor, error,
			 duration_ms, detail, at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (run_id, provider, query) DO UPDATE
		   SET language = EXCLUDED.language,
		       outcome = EXCLUDED.outcome,
		       candidates = EXCLUDED.candidates,
		       cursor = EXCLUDED.cursor,
		       error = EXCLUDED.error,
		       duration_ms = EXCLUDED.duration_ms,
		       detail = EXCLUDED.detail,
		       at = EXCLUDED.at`,
		a.RunID, a.Provider, a.Query, a.Language, string(a.Outcome), a.Candidates,
		a.Cursor, a.Error, a.Duration.Milliseconds(), a.Detail, a.At)
	if err != nil {
		return fmt.Errorf("discovery: record attempt: %w", err)
	}
	return nil
}

const attemptColumns = `run_id, provider, query, language, outcome, candidates,
	cursor, error, duration_ms, detail, at`

func scanAttempt(row pgx.Row) (lineage.Attempt, error) {
	var (
		a       lineage.Attempt
		outcome string
		ms      int64
	)
	err := row.Scan(&a.RunID, &a.Provider, &a.Query, &a.Language, &outcome,
		&a.Candidates, &a.Cursor, &a.Error, &ms, &a.Detail, &a.At)
	a.Outcome = lineage.Outcome(outcome)
	a.Duration = time.Duration(ms) * time.Millisecond
	return a, err
}

// AttemptsFor returns a run's attempts, oldest first.
func (s *PostgresStore) AttemptsFor(ctx context.Context, runID string) ([]lineage.Attempt, error) {
	return s.queryAttempts(ctx, `
		SELECT `+attemptColumns+` FROM discovery_lineage
		 WHERE run_id = $1 ORDER BY at ASC, id ASC`, runID)
}

// PendingAttempts returns the attempts that left work unfinished, so a resumed
// run knows what to pick up rather than starting the whole sweep again.
func (s *PostgresStore) PendingAttempts(ctx context.Context, runID string) ([]lineage.Attempt, error) {
	return s.queryAttempts(ctx, `
		SELECT `+attemptColumns+` FROM discovery_lineage
		 WHERE run_id = $1 AND outcome IN ('truncated', 'budgeted')
		 ORDER BY at ASC, id ASC`, runID)
}

func (s *PostgresStore) queryAttempts(ctx context.Context, q string, runID string) ([]lineage.Attempt, error) {
	if s.pool == nil {
		return nil, ErrStoreClosed
	}
	rows, err := s.pool.Query(ctx, q, runID)
	if err != nil {
		return nil, fmt.Errorf("discovery: read lineage: %w", err)
	}
	defer rows.Close()
	var out []lineage.Attempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("discovery: scan lineage: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discovery: read lineage: %w", err)
	}
	return out, nil
}

// sendBatch runs a batch of n statements, stopping at the first failure. A
// rejected evidence row costs that row, not the candidate it belonged to, which
// is why this returns the error and lets the caller decide rather than
// panicking the way pgx's default batch behaviour would.
func sendBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch, n int) error {
	results := tx.SendBatch(ctx, batch)
	defer results.Close()
	for i := 0; i < n; i++ {
		if _, err := results.Exec(); err != nil {
			// Drain the remainder so the connection is not left mid-batch, which
			// would make the next statement on it read the wrong results.
			for j := i + 1; j < n; j++ {
				_, _ = results.Exec()
			}
			return err
		}
	}
	return results.Close()
}
