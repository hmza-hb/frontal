-- Discovery: runs, candidates, evidence, and the lineage ledger.
--
-- The shape here is dictated by one requirement: a lead must always be
-- explainable. Every row that claims something about a company carries the
-- source, the method, the query, the URL, and the time it was observed, so a
-- human can walk from a ranked list back to the page that justified it.

-- A run is one execution of the discovery engine over a set of seeds. It is the
-- unit of rollback: a run that produced bad candidates is deleted without
-- touching history from previous runs.
CREATE TABLE IF NOT EXISTS discovery_runs (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    status        text        NOT NULL DEFAULT 'running',
    profile_name  text        NOT NULL DEFAULT '',
    seeds_total   integer     NOT NULL DEFAULT 0,
    candidates    integer     NOT NULL DEFAULT 0,
    accepted      integer     NOT NULL DEFAULT 0,
    rejected      integer     NOT NULL DEFAULT 0,
    provider_calls integer    NOT NULL DEFAULT 0,
    provider_failures integer NOT NULL DEFAULT 0,
    queries_run   integer     NOT NULL DEFAULT 0,
    queries_pending integer   NOT NULL DEFAULT 0,
    budget_spent  integer     NOT NULL DEFAULT 0,
    budget_limit  integer     NOT NULL DEFAULT 0,
    max_depth     integer     NOT NULL DEFAULT 0,
    error         text        NOT NULL DEFAULT '',
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    -- The full effective configuration, with secrets removed. A run has to be
    -- reproducible from what is stored, and an operator has to be able to see
    -- what the engine actually did rather than what the file said.
    config        jsonb       NOT NULL DEFAULT '{}'::jsonb,

    CONSTRAINT discovery_runs_status_valid
        CHECK (status IN ('running', 'completed', 'failed', 'cancelled')),
    CONSTRAINT discovery_runs_counts_valid
        CHECK (seeds_total >= 0 AND candidates >= 0 AND accepted >= 0
               AND rejected >= 0 AND provider_calls >= 0 AND provider_failures >= 0
               AND queries_run >= 0 AND queries_pending >= 0
               AND budget_spent >= 0 AND budget_limit >= 0 AND max_depth >= 0)
);

-- Newest first: the operations question this table answers is "what did we run
-- last, and did it work".
CREATE INDEX IF NOT EXISTS discovery_runs_started_at_idx
    ON discovery_runs (started_at DESC);
CREATE INDEX IF NOT EXISTS discovery_runs_status_idx
    ON discovery_runs (status, started_at DESC);

-- One row per company the engine has ever proposed.
--
-- The primary key is the engine's own id, not the domain, because a domain
-- changes hands: acme.test acquired last year and sold today is two different
-- companies, and collapsing them into one row would attach the old company's
-- evidence to the new owner.
CREATE TABLE IF NOT EXISTS discovery_candidates (
    id             text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name           text        NOT NULL DEFAULT '',
    -- The registrable domain when there is one. A company named by a source but
    -- with no website yet is a legitimate row, so this is nullable rather than
    -- the key.
    domain         text,
    url            text        NOT NULL DEFAULT '',
    country        text        NOT NULL DEFAULT '',
    industry       text        NOT NULL DEFAULT '',
    employee_hint  text        NOT NULL DEFAULT '',
    description    text        NOT NULL DEFAULT '',
    keywords       text[]      NOT NULL DEFAULT '{}',
    status         text        NOT NULL DEFAULT 'new',
    reason         text        NOT NULL DEFAULT '',
    confidence     real        NOT NULL DEFAULT 0,
    source_count   integer     NOT NULL DEFAULT 0,
    -- First and last observation, not a single timestamp. A candidate that
    -- reappears months later is worth revisiting, which is a fact a single
    -- updated-at column cannot express.
    first_seen     timestamptz NOT NULL DEFAULT now(),
    last_seen      timestamptz NOT NULL DEFAULT now(),
    -- The run that most recently touched it, and the run that first proposed it.
    first_run_id   text        NOT NULL DEFAULT '',
    last_run_id    text        NOT NULL DEFAULT '',
    -- Ranking output, stored so a list can be served without re-scoring, and so
    -- the score a user acted on is recoverable.
    score          real,
    verdict        text        NOT NULL DEFAULT '',
    rank_factors   jsonb       NOT NULL DEFAULT '[]'::jsonb,
    rank_explain   text        NOT NULL DEFAULT '',
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT discovery_candidates_confidence_valid CHECK (confidence >= 0 AND confidence <= 1),
    CONSTRAINT discovery_candidates_status_valid
        CHECK (status IN ('new', 'accepted', 'rejected', 'duplicate', 'error')),
    CONSTRAINT discovery_candidates_source_count_valid CHECK (source_count >= 0),
    CONSTRAINT discovery_candidates_seen_order_valid CHECK (last_seen >= first_seen)
);

-- The domain is what identity and ranking key on, so it is unique when present.
-- The partial index leaves name-only candidates unconstrained, which is what
-- makes "a company we know of but have no website for" storable at all.
CREATE UNIQUE INDEX IF NOT EXISTS discovery_candidates_domain_key
    ON discovery_candidates (domain) WHERE domain IS NOT NULL AND domain <> '';
CREATE INDEX IF NOT EXISTS discovery_candidates_last_seen_idx
    ON discovery_candidates (last_seen DESC);
CREATE INDEX IF NOT EXISTS discovery_candidates_status_score_idx
    ON discovery_candidates (status, score DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS discovery_candidates_last_run_idx
    ON discovery_candidates (last_run_id);

-- Every observation behind a candidate, one row each.
--
-- This table is append-only. An evidence row is a claim that a specific source
-- said something at a specific time, and deleting or rewriting one destroys the
-- audit trail that makes a ranking explainable. Corrections are made by adding
-- a new observation, never by editing an old one.
CREATE TABLE IF NOT EXISTS discovery_evidence (
    id           bigserial    PRIMARY KEY,
    candidate_id text         NOT NULL REFERENCES discovery_candidates (id) ON DELETE CASCADE,
    run_id       text         NOT NULL DEFAULT '',
    source       text         NOT NULL,
    method       text         NOT NULL,
    url          text         NOT NULL DEFAULT '',
    query        text         NOT NULL DEFAULT '',
    snippet      text         NOT NULL DEFAULT '',
    detail       text         NOT NULL DEFAULT '',
    observed_at  timestamptz NOT NULL DEFAULT now(),

    -- The source and method vocabularies are fixed here on purpose even though
    -- they are strings in Go: discovery is resumable, and a row whose vocabulary
    -- has drifted cannot be interpreted by a later run that predates the drift.
    CONSTRAINT discovery_evidence_source_valid
        CHECK (source IN ('seed', 'certificate', 'registry', 'directory', 'news',
                          'developer', 'search', 'sitemap', 'expansion')),
    CONSTRAINT discovery_evidence_method_valid
        CHECK (method IN ('seed_import', 'list_page', 'search_result', 'api_record',
                          'sitemap_entry', 'certificate', 'link', 'derived'))
);

CREATE INDEX IF NOT EXISTS discovery_evidence_candidate_idx
    ON discovery_evidence (candidate_id, observed_at DESC);

-- Idempotence. A resumed run re-reads pages it has already seen, and without
-- this it would append a second copy of every claim it collected the first
-- time round. The digest is over the claim's content rather than its identity
-- so that a genuinely repeated observation of the same fact collapses, while a
-- different fact from the same URL does not.
CREATE UNIQUE INDEX IF NOT EXISTS discovery_evidence_dedupe_key
    ON discovery_evidence (candidate_id, source, method,
                           md5(url || '|' || query || '|' || snippet));
CREATE INDEX IF NOT EXISTS discovery_evidence_run_idx
    ON discovery_evidence (run_id);
CREATE INDEX IF NOT EXISTS discovery_evidence_source_idx
    ON discovery_evidence (source, observed_at DESC);

-- The query/provider ledger, mirroring the lineage package exactly.
--
-- This exists in the database as well as in memory so a run survives a process
-- restart. Resumability is the whole point: a run that re-asks every answered
-- question after a crash costs real money and produces the same answers.
CREATE TABLE IF NOT EXISTS discovery_lineage (
    id          bigserial    PRIMARY KEY,
    run_id      text         NOT NULL DEFAULT '',
    provider    text         NOT NULL,
    query       text         NOT NULL,
    language    text         NOT NULL DEFAULT '',
    outcome     text         NOT NULL,
    candidates  integer      NOT NULL DEFAULT 0,
    cursor      text         NOT NULL DEFAULT '',
    error       text         NOT NULL DEFAULT '',
    duration_ms bigint       NOT NULL DEFAULT 0,
    detail      text         NOT NULL DEFAULT '',
    at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT discovery_lineage_outcome_valid
        CHECK (outcome IN ('produced', 'empty', 'failed', 'budgeted', 'truncated', 'skipped')),
    CONSTRAINT discovery_lineage_candidates_valid CHECK (candidates >= 0),
    CONSTRAINT discovery_lineage_duration_valid CHECK (duration_ms >= 0)
);

-- Pending work is rare and worth finding fast, so it gets a partial index
-- rather than a filter on the full history.
CREATE INDEX IF NOT EXISTS discovery_lineage_pending_idx
    ON discovery_lineage (run_id, provider, at DESC)
    WHERE outcome IN ('truncated', 'budgeted');
CREATE INDEX IF NOT EXISTS discovery_lineage_run_idx
    ON discovery_lineage (run_id, at DESC);
-- The resume key: one row per (run, provider, query). A repeated run updates
-- this row rather than accumulating one per attempt, so the table stays
-- proportional to the number of questions asked rather than to the number of
-- times they were asked.
CREATE UNIQUE INDEX IF NOT EXISTS discovery_lineage_key
    ON discovery_lineage (run_id, provider, query);
