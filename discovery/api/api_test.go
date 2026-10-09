package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/api"
	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/config"
	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
	"github.com/hmza-hb/lead-intelligence/discovery/persistence"
	"github.com/hmza-hb/lead-intelligence/discovery/providers"
	"github.com/hmza-hb/lead-intelligence/discovery/service"
)

// store is a minimal in-memory persistence.Store for the API tests. The API is
// about HTTP shapes, so the store only has to be honest enough to catch a
// handler passing the wrong filter through.
type store struct {
	mu       sync.Mutex
	runs     map[string]persistence.Run
	order    []string
	cands    map[string]persistence.StoredCandidate
	attempts map[string][]lineage.Attempt
}

func newStore() *store {
	return &store{
		runs:     map[string]persistence.Run{},
		cands:    map[string]persistence.StoredCandidate{},
		attempts: map[string][]lineage.Attempt{},
	}
}

func (m *store) CreateRun(_ context.Context, r persistence.Run) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := "run-" + string(rune('a'+len(m.order)))
	r.ID = id
	r.Status = "completed"
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().UTC()
	}
	m.runs[id] = r
	m.order = append(m.order, id)
	return id, nil
}

func (m *store) FinishRun(_ context.Context, id string, res persistence.RunResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return persistence.ErrNotFound
	}
	r.Status = res.Status
	r.FinishedAt = res.FinishedAt
	m.runs[id] = r
	return nil
}

func (m *store) Run(_ context.Context, id string) (persistence.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return persistence.Run{}, persistence.ErrNotFound
	}
	return r, nil
}

func (m *store) RecentRuns(_ context.Context, limit int) ([]persistence.RunSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []persistence.RunSummary
	for i := len(m.order) - 1; i >= 0; i-- {
		if limit > 0 && len(out) >= limit {
			break
		}
		r := m.runs[m.order[i]]
		out = append(out, persistence.RunSummary{ID: r.ID, Status: r.Status, ProfileName: r.ProfileName})
	}
	return out, nil
}

func (m *store) UpsertCandidates(_ context.Context, runID string, in []candidate.Candidate) ([]persistence.StoredCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]persistence.StoredCandidate, 0, len(in))
	for i, c := range in {
		id := c.Domain
		if id == "" {
			id = "candidate-" + string(rune('a'+i))
		}
		c.ID = id
		st := persistence.StoredCandidate{ID: id, Candidate: c}
		m.cands[id] = st
		out = append(out, st)
	}
	return out, nil
}

func (m *store) Candidate(_ context.Context, id string) (persistence.StoredCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cands[id]
	if !ok {
		return persistence.StoredCandidate{}, persistence.ErrNotFound
	}
	return c, nil
}

func (m *store) Candidates(_ context.Context, f persistence.CandidateFilter) ([]persistence.StoredCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []persistence.StoredCandidate
	for _, c := range m.cands {
		if f.Status != "" && string(c.Candidate.Status) != f.Status {
			continue
		}
		if f.Verdict != "" && c.Verdict != f.Verdict {
			continue
		}
		out = append(out, c)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

func (m *store) SaveRanking(ctx context.Context, in []persistence.Scored) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range in {
		c, ok := m.cands[s.ID]
		if !ok {
			continue
		}
		c.Score = s.Score
		c.HasScore = true
		c.Verdict = s.Verdict
		c.RankExplain = s.Explain
		c.RankFactors = s.Factors
		m.cands[s.ID] = c
	}
	return nil
}

func (m *store) RecordAttempt(_ context.Context, a lineage.Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[a.RunID] = append(m.attempts[a.RunID], a)
	return nil
}

func (m *store) AttemptsFor(_ context.Context, runID string) ([]lineage.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempts[runID], nil
}

func (m *store) PendingAttempts(ctx context.Context, runID string) ([]lineage.Attempt, error) {
	return m.AttemptsFor(ctx, runID)
}

func (m *store) Close() {}

var allKinds = []providers.Kind{
	providers.KindIndustry, providers.KindTechnology, providers.KindGeography,
	providers.KindDirectory, providers.KindName, providers.KindCompetitor,
}

// stubProvider answers every query with one company.
type stubProvider struct {
	mu   sync.Mutex
	n    int
	name string
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Ready() error { return nil }

func (s *stubProvider) Kinds() []providers.Kind { return allKinds }

func (s *stubProvider) Search(_ context.Context, q providers.Query) (providers.Result, error) {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	return providers.Result{
		Provider: s.name, Query: q,
		Candidates: []candidate.Candidate{{
			Name: "Found Co", Domain: "found.test", URL: "https://found.test/",
			Industry: "Industrial Automation", Country: "US", Confidence: 0.8,
			Evidence: []candidate.Evidence{{
				Source: candidate.SourceSearch, Method: candidate.MethodSearchResult,
				URL: "https://search.test/?q=x", Query: q.Text, Snippet: "Found Co",
			}},
		}},
	}, nil
}

func newAPI(t *testing.T) (http.Handler, *store) {
	t.Helper()
	st := newStore()
	cfg := config.Default()
	cfg.Run.MaxDepth = 0
	cfg.Run.MaxProviderCalls = 20
	cfg.Rank.AcceptThreshold = 0.1
	svc, err := service.New(service.Options{
		Config:    cfg,
		Store:     st,
		Providers: []providers.Provider{&stubProvider{name: "search"}},
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	a, err := api.New(api.Options{
		Service: svc,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	return a.Handler(), st
}

func do(t *testing.T, h http.Handler, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// The raw body is read first: decoding it consumes the reader, and a test
	// that wants to assert on the text would otherwise see an empty string.
	raw := rec.Body.String()
	var out map[string]any
	if raw != "" {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&out); err != nil {
			t.Fatalf("%s %s: body is not JSON: %v (%s)", method, path, err, raw)
		}
	}
	return rec, out
}

func TestHealthz(t *testing.T) {
	h, _ := newAPI(t)
	rec, body := do(t, h, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["status"] != "ok" {
		t.Errorf("body = %v, want status ok", body)
	}
}

func TestReadyzReportsStoreHealth(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestStartRunWithProfile(t *testing.T) {
	h, st := newAPI(t)
	rec, body := do(t, h, http.MethodPost, "/v1/runs", `{
		"profile": {"name":"us-automation","industries":["Industrial Automation"],"countries":["US"]}
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	run, ok := body["run"].(map[string]any)
	if !ok {
		t.Fatalf("body = %v, want a run object", body)
	}
	if run["status"] != "completed" {
		t.Errorf("status = %v, want completed", run["status"])
	}
	if len(st.cands) == 0 {
		t.Error("the run stored no candidates")
	}
}

func TestStartRunWithSeeds(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodPost, "/v1/runs", `{
		"seeds": [{"name":"Acme Robotics","domain":"acme.test","country":"us","industry":"Robotics"}]
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestSeedEvidenceIsRecordedAsTheOperatorsOwnStatement(t *testing.T) {
	h, st := newAPI(t)
	do(t, h, http.MethodPost, "/v1/runs", `{
		"seeds": [{"name":"Acme Robotics","domain":"Acme.Test","country":"us","notes":"known customer"}]
	}`)
	got, ok := st.cands["acme.test"]
	if !ok {
		t.Fatalf("candidates = %v, want a normalised acme.test", keys(st.cands))
	}
	if got.Candidate.Country != "US" {
		t.Errorf("country = %q, want it normalised to upper case", got.Candidate.Country)
	}
	if len(got.Candidate.Evidence) != 1 {
		t.Fatalf("got %d evidence rows, want 1", len(got.Candidate.Evidence))
	}
	e := got.Candidate.Evidence[0]
	if e.Source != candidate.SourceSeed || e.Method != candidate.MethodSeedImport {
		t.Errorf("evidence = %+v, want a seed import the operator can trace", e)
	}
	if !strings.Contains(e.Snippet, "known customer") {
		t.Errorf("snippet = %q, want the operator's notes carried into it", e.Snippet)
	}
}

func TestStartRunRefusesAnEmptyRequest(t *testing.T) {
	h, _ := newAPI(t)
	rec, body := do(t, h, http.MethodPost, "/v1/runs", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if body["error"] == nil {
		t.Error("no error object in the response")
	}
}

func TestStartRunRefusesASeedWithNoIdentity(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodPost, "/v1/runs", `{"seeds":[{"country":"US"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a seed with neither a name nor a domain", rec.Code)
	}
}

func TestStartRunRejectsMalformedJSON(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodPost, "/v1/runs", `{"profile":`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestStartRunNamesTheOffendingSeed(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodPost, "/v1/runs", `{"seeds":[{"name":"ok"},{"country":"US"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	// Telling the caller which line is wrong saves a round trip.
	if !strings.Contains(rec.Body.String(), "seed 1") {
		t.Errorf("body = %s, want it to name the offending seed index", rec.Body.String())
	}
}

func TestListRuns(t *testing.T) {
	h, _ := newAPI(t)
	do(t, h, http.MethodPost, "/v1/runs", `{"profile":{"name":"p1","industries":["x"]}}`)
	do(t, h, http.MethodPost, "/v1/runs", `{"profile":{"name":"p2","industries":["y"]}}`)

	rec, body := do(t, h, http.MethodGet, "/v1/runs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	runs, ok := body["runs"].([]any)
	if !ok || len(runs) != 2 {
		t.Fatalf("runs = %v, want 2", body["runs"])
	}
	// Newest first: the run an operator just started is the one they are asking about.
	if runs[0].(map[string]any)["profile_name"] != "p2" {
		t.Errorf("first run = %v, want the newest", runs[0])
	}
}

func TestGetRunIncludesItsCandidates(t *testing.T) {
	h, _ := newAPI(t)
	_, body := do(t, h, http.MethodPost, "/v1/runs", `{
		"profile": {"name":"p","industries":["Industrial Automation"],"countries":["US"]}
	}`)
	id := body["run"].(map[string]any)["id"].(string)

	rec, got := do(t, h, http.MethodGet, "/v1/runs/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got["run"] == nil {
		t.Error("no run in the response")
	}
	cands, ok := got["candidates"].([]any)
	if !ok || len(cands) == 0 {
		t.Fatalf("candidates = %v, want the run's output", got["candidates"])
	}
}

func TestGetUnknownRunIs404(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodGet, "/v1/runs/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestListAttemptsIsTheExplainabilityEndpoint(t *testing.T) {
	h, st := newAPI(t)
	_, body := do(t, h, http.MethodPost, "/v1/runs", `{
		"profile": {"name":"p","industries":["Industrial Automation"],"countries":["US"]}
	}`)
	id := body["run"].(map[string]any)["id"].(string)

	rec, got := do(t, h, http.MethodGet, "/v1/runs/"+id+"/attempts", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	attempts, ok := got["attempts"].([]any)
	if !ok || len(attempts) == 0 {
		t.Fatalf("attempts = %v, want the run's lineage", got["attempts"])
	}
	first := attempts[0].(map[string]any)
	// Every field needed to answer "why did this company turn up".
	for _, field := range []string{"Provider", "Query", "Outcome", "At"} {
		if _, ok := first[field]; !ok {
			t.Errorf("attempt is missing %s: %v", field, first)
		}
	}
	if len(st.attempts[id]) == 0 {
		t.Error("the store recorded no attempts")
	}
}

func TestListCandidatesFiltersAreValidated(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodGet, "/v1/candidates?status=not-a-status", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status filter: got %d, want 400", rec.Code)
	}
	rec, _ = do(t, h, http.MethodGet, "/v1/candidates?verdict=maybe", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("verdict filter: got %d, want 400", rec.Code)
	}
	rec, _ = do(t, h, http.MethodGet, "/v1/candidates?status=new&verdict=accept", "")
	if rec.Code != http.StatusOK {
		t.Errorf("valid filters: got %d, want 200", rec.Code)
	}
}

func TestListCandidatesPagingIsBounded(t *testing.T) {
	h, _ := newAPI(t)
	do(t, h, http.MethodPost, "/v1/runs", `{"profile":{"name":"p","industries":["Industrial Automation"]}}`)

	rec, body := do(t, h, http.MethodGet, "/v1/candidates?limit=99999", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// An unbounded page is a denial of service with a query string.
	if got, ok := body["limit"].(json.Number); ok {
		if n, err := got.Int64(); err == nil && n > 500 {
			t.Errorf("limit = %s, want it clamped to the page maximum", got)
		}
	}
}

func TestFilterValuesCannotReachSQL(t *testing.T) {
	// A filter value is operator input. The store here never interpolates, and
	// the API must not either, so this asserts the API quotes what it echoes
	// and never treats a value as a clause.
	h, _ := newAPI(t)
	rec, body := do(t, h, http.MethodGet, "/v1/candidates?status=1%27%20OR%20%271%27%3D%271", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown status", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "OR '1'='1") && body["error"] == nil {
		t.Error("an injection attempt was reflected as a successful result")
	}
}

func TestGetCandidate(t *testing.T) {
	h, st := newAPI(t)
	do(t, h, http.MethodPost, "/v1/runs", `{"seeds":[{"name":"Acme","domain":"acme.test","country":"US"}]}`)
	if len(st.cands) == 0 {
		t.Fatal("no candidate stored")
	}
	rec, got := do(t, h, http.MethodGet, "/v1/candidates/acme.test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got["domain"] != "acme.test" {
		t.Errorf("domain = %v", got["domain"])
	}
	ev, ok := got["evidence"].([]any)
	if !ok || len(ev) == 0 {
		t.Error("the candidate came back with no evidence: it could not be explained")
	}
}

func TestGetUnknownCandidateIs404(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodGet, "/v1/candidates/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestResponsesCarrySecurityHeaders(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodGet, "/healthz", "")
	if rec.Header().Get("X-Content-Type-Options") == "" {
		t.Error("no nosniff header on an API response")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h, _ := newAPI(t)
	rec, _ := do(t, h, http.MethodDelete, "/v1/candidates/acme.test", "")
	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want a method rejection", rec.Code)
	}
}

func TestNewRequiresAService(t *testing.T) {
	if _, err := api.New(api.Options{}); err == nil {
		t.Fatal("an API with no service must be refused")
	}
}

func keys(m map[string]persistence.StoredCandidate) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
