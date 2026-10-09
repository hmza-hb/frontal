// Package api exposes discovery over HTTP.
//
// The surface is deliberately small: start a run, watch runs, and read
// candidates. Discovery is an expensive batch job, so every endpoint here is a
// way to ask about work that already happened, plus one way to start more. There
// is no endpoint that mutates a candidate directly, because a candidate is a
// claim with evidence behind it and editing the claim out from under the
// evidence is how a database stops being trustworthy.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/persistence"
	"github.com/hmza-hb/lead-intelligence/discovery/query"
	"github.com/hmza-hb/lead-intelligence/discovery/service"
	"github.com/hmza-hb/lead-intelligence/platform/httpx"
)

// MaxBodyBytes bounds a request body. A run request is a targeting profile and a
// seed list, both of which are small; anything larger is a mistake or an attack.
const MaxBodyBytes = 4 << 20

// DefaultPageSize is how many rows a listing returns when the client does not ask.
const DefaultPageSize = 50

// API serves the discovery HTTP surface.
type API struct {
	svc *service.Service
	log *slog.Logger
	// runTimeout bounds a synchronous run. A run started through HTTP cannot
	// outlive the request that asked for it, so a caller wanting a long run
	// should use the CLI or a job queue instead of holding a connection open.
	runTimeout time.Duration
	// onRun is called when a run finishes, for a test to observe.
	onRun func(*service.Report)
}

// Options configures the API.
type Options struct {
	// Service is the orchestration under the API. Required.
	Service *service.Service
	// Log receives request failures. Nil uses the default logger.
	Log *slog.Logger
	// RunTimeout bounds a run started through this API. Zero means ten minutes.
	RunTimeout time.Duration
	// OnRun is called after each run completes. Used by tests.
	OnRun func(*service.Report)
}

// New builds an API.
func New(opts Options) (*API, error) {
	if opts.Service == nil {
		return nil, errors.New("api: a service is required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.RunTimeout <= 0 {
		opts.RunTimeout = 10 * time.Minute
	}
	return &API{svc: opts.Service, log: opts.Log, runTimeout: opts.RunTimeout, onRun: opts.OnRun}, nil
}

// Handler returns the routed handler with the standard middleware applied.
//
// Go 1.22's method-and-wildcard patterns do the routing, so there is no
// third-party router to keep current and every path is visible in one place.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", a.readyz)
	mux.HandleFunc("POST /v1/runs", a.startRun)
	mux.HandleFunc("GET /v1/runs", a.listRuns)
	mux.HandleFunc("GET /v1/runs/{id}", a.getRun)
	mux.HandleFunc("GET /v1/runs/{id}/attempts", a.listAttempts)
	mux.HandleFunc("GET /v1/candidates", a.listCandidates)
	mux.HandleFunc("GET /v1/candidates/{id}", a.getCandidate)

	// Go's mux answers an unsupported method with a plain-text 405, which would
	// make this the one endpoint in the API whose errors are not JSON. These
	// method-agnostic registrations catch those requests and answer in the same
	// shape as everything else, so a client can parse one error type throughout.
	mux.HandleFunc("/v1/runs", httpx.MethodNotAllowedHandler("GET, POST"))
	mux.HandleFunc("/v1/runs/{id}", httpx.MethodNotAllowedHandler("GET"))
	mux.HandleFunc("/v1/runs/{id}/attempts", httpx.MethodNotAllowedHandler("GET"))
	mux.HandleFunc("/v1/candidates", httpx.MethodNotAllowedHandler("GET"))
	mux.HandleFunc("/v1/candidates/{id}", httpx.MethodNotAllowedHandler("GET"))
	mux.HandleFunc("/", httpx.Handler(httpx.NotFoundHandler))

	return httpx.Chain(mux,
		httpx.Recover(a.log),
		httpx.RequestID("X-Request-ID"),
		httpx.LogRequests(a.log),
		httpx.SecurityHeaders,
		httpx.NoCacheMiddleware,
	)
}

// readyz reports whether the store answers, which is the one dependency that
// makes every other endpoint useless when it is down.
func (a *API) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if _, err := a.svc.Report(ctx, 1); err != nil {
		a.log.Error("discovery: readiness check failed", "error", err)
		httpx.Error(w, httpx.Unavailable("store is unreachable").WithCause(err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// startRunRequest is the body of POST /v1/runs.
type startRunRequest struct {
	// Profile drives the questions. A run with only seeds is valid.
	Profile *query.Profile `json:"profile"`
	// Seeds are companies the operator already knows about.
	Seeds []seedRequest `json:"seeds"`
	// MaxDepth overrides the configured expansion depth.
	MaxDepth int `json:"max_depth"`
}

// seedRequest is one operator-supplied company.
type seedRequest struct {
	Name         string   `json:"name"`
	Domain       string   `json:"domain"`
	Country      string   `json:"country"`
	Industry     string   `json:"industry"`
	EmployeeHint string   `json:"employee_hint"`
	Notes        string   `json:"notes"`
	Keywords     []string `json:"keywords"`
	Confidence   *float64 `json:"confidence"`
}

// toCandidate converts a seed request into a candidate.
//
// The evidence is the operator's own statement, which is a real source: a seed
// is someone telling the engine who to look for. Recording it as anything less
// would mean the strongest source in the system had no provenance.
func (s seedRequest) toCandidate(now time.Time) candidate.Candidate {
	desc := s.Notes
	c := candidate.Candidate{
		Name:         s.Name,
		Domain:       strings.ToLower(strings.TrimSpace(s.Domain)),
		Country:      strings.ToUpper(strings.TrimSpace(s.Country)),
		Industry:     s.Industry,
		EmployeeHint: s.EmployeeHint,
		Description:  desc,
		Keywords:     s.Keywords,
		Confidence:   0.5,
		FirstSeen:    now,
		LastSeen:     now,
		Status:       candidate.StatusNew,
	}
	if len(s.Domain) > 0 {
		c.URL = "https://" + c.Domain + "/"
	}
	c.Evidence = []candidate.Evidence{{
		Source:     candidate.SourceSeed,
		Method:     candidate.MethodSeedImport,
		Snippet:    strings.TrimSpace(s.Name + " " + desc),
		ObservedAt: now,
	}}
	return c
}

// startRun executes a discovery run synchronously.
//
// It is synchronous because a caller asking for a run usually wants the result
// and a run's cost is bounded by configuration. A caller that wants a long run
// should use the CLI, which is not held to a request timeout.
func (a *API) startRun(w http.ResponseWriter, r *http.Request) {
	var req startRunRequest
	if err := httpx.DecodeJSON(w, r, MaxBodyBytes, &req); err != nil {
		httpx.Error(w, httpx.BadRequest("malformed request body").WithCause(err))
		return
	}
	if req.Profile == nil && len(req.Seeds) == 0 {
		httpx.Error(w, httpx.BadRequest("a run needs a profile, seeds, or both"))
		return
	}

	now := time.Now().UTC()
	seeds := make([]candidate.Candidate, 0, len(req.Seeds))
	for i, s := range req.Seeds {
		if strings.TrimSpace(s.Name) == "" && strings.TrimSpace(s.Domain) == "" {
			httpx.Error(w, httpx.BadRequest("seed "+strconv.Itoa(i)+" has neither a name nor a domain"))
			return
		}
		seeds = append(seeds, s.toCandidate(now))
	}

	profile := query.Profile{}
	if req.Profile != nil {
		profile = *req.Profile
	}

	ctx, cancel := context.WithTimeout(r.Context(), a.runTimeout)
	defer cancel()
	report, err := a.svc.Run(ctx, service.Request{
		Profile:  profile,
		Seeds:    seeds,
		MaxDepth: req.MaxDepth,
	})
	if report != nil && a.onRun != nil {
		a.onRun(report)
	}
	if err != nil {
		// A run that failed after doing real work still has a report, and the
		// report is more useful to the caller than the error alone.
		if report != nil {
			httpx.JSON(w, http.StatusInternalServerError, runResponse{Run: report})
			return
		}
		a.fail(w, "start run", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, runResponse{Run: report})
}

// runResponse is what a completed or failed run looks like to a client.
type runResponse struct {
	Run *service.Report `json:"run"`
}

// candidateResponse is a candidate as the API returns it: the claim, the score,
// and the evidence that justifies it.
type candidateResponse struct {
	ID         string                   `json:"id"`
	Rank       int                      `json:"rank"`
	Name       string                   `json:"name"`
	Domain     string                   `json:"domain"`
	URL        string                   `json:"url"`
	Country    string                   `json:"country"`
	Industry   string                   `json:"industry"`
	Confidence float64                  `json:"confidence"`
	Score      float64                  `json:"score"`
	Verdict    string                   `json:"verdict"`
	Explain    string                   `json:"explain"`
	Factors    []persistence.RankFactor `json:"factors,omitempty"`
	Evidence   []evidenceResponse       `json:"evidence,omitempty"`
	FirstSeen  time.Time                `json:"first_seen"`
	LastSeen   time.Time                `json:"last_seen"`
}

// evidenceResponse is one observation, including the question that produced it.
type evidenceResponse struct {
	Source     string    `json:"source"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	Query      string    `json:"query"`
	Snippet    string    `json:"snippet"`
	ObservedAt time.Time `json:"observed_at"`
}

func toCandidateResponse(sc service.Scored) candidateResponse {
	out := candidateResponse{
		ID: sc.ID, Rank: sc.Rank, Name: sc.Candidate.Name, Domain: sc.Candidate.Domain,
		URL: sc.Candidate.URL, Country: sc.Candidate.Country,
		Industry: sc.Candidate.Industry, Confidence: sc.Candidate.Confidence,
		Score: sc.Score, Verdict: sc.Verdict, Explain: sc.Explain,
		Factors: sc.Factors, FirstSeen: sc.Candidate.FirstSeen,
		LastSeen: sc.Candidate.LastSeen,
	}
	for _, e := range sc.Candidate.Evidence {
		out.Evidence = append(out.Evidence, evidenceResponse{
			Source: string(e.Source), Method: string(e.Method), URL: e.URL,
			Query: e.Query, Snippet: e.Snippet, ObservedAt: e.ObservedAt,
		})
	}
	return out
}

func toCandidateResponseStored(st persistence.StoredCandidate) candidateResponse {
	out := candidateResponse{
		ID: st.ID, Name: st.Candidate.Name, Domain: st.Candidate.Domain,
		URL: st.Candidate.URL, Country: st.Candidate.Country,
		Industry: st.Candidate.Industry, Confidence: st.Candidate.Confidence,
		Score: st.Score, Verdict: st.Verdict, Explain: st.RankExplain,
		Factors: st.RankFactors, FirstSeen: st.Candidate.FirstSeen,
		LastSeen: st.Candidate.LastSeen,
	}
	for _, e := range st.Candidate.Evidence {
		out.Evidence = append(out.Evidence, evidenceResponse{
			Source: string(e.Source), Method: string(e.Method), URL: e.URL,
			Query: e.Query, Snippet: e.Snippet, ObservedAt: e.ObservedAt,
		})
	}
	return out
}

// listRuns returns stored runs, newest first.
func (a *API) listRuns(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	limit := intParam(r, "limit", DefaultPageSize)
	runs, err := a.svc.Report(ctx, limit)
	if err != nil {
		a.fail(w, "list runs", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"runs": runs, "count": len(runs)})
}

// getRun returns one run's detail, including its provider health, which is what
// an operator looks at after a run finds less than expected.
func (a *API) getRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httpx.Error(w, httpx.BadRequest("a run id is required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	run, err := a.svc.RunInfo(ctx, id)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			httpx.Error(w, httpx.NotFound("no such run"))
			return
		}
		a.fail(w, "get run", err)
		return
	}
	cands, err := a.svc.Candidates(ctx, persistence.CandidateFilter{RunID: id, Limit: MaxPageSize})
	if err != nil {
		a.fail(w, "run candidates", err)
		return
	}
	out := make([]candidateResponse, 0, len(cands))
	for _, c := range cands {
		out = append(out, toCandidateResponseStored(c))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"run": run, "candidates": out})
}

// MaxPageSize bounds a single page of results.
const MaxPageSize = 500

// listAttempts returns a run's lineage: every question asked, what came back,
// and what it cost. This is the endpoint that makes a surprising lead
// explainable.
func (a *API) listAttempts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httpx.Error(w, httpx.BadRequest("a run id is required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	attempts, err := a.svc.Attempts(ctx, id)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			httpx.Error(w, httpx.NotFound("no such run"))
			return
		}
		a.fail(w, "list attempts", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"run_id":   id,
		"attempts": attempts,
		"count":    len(attempts),
	})
}

// listCandidates returns stored candidates, filtered and paged.
//
// The filters are validated against fixed sets rather than interpolated. A
// filter value is operator input, and interpolating it into SQL is the oldest
// and most durable mistake available.
func (a *API) listCandidates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	f := persistence.CandidateFilter{
		Status:  r.URL.Query().Get("status"),
		Verdict: r.URL.Query().Get("verdict"),
		RunID:   r.URL.Query().Get("run_id"),
		Limit:   intParam(r, "limit", DefaultPageSize),
		Offset:  intParam(r, "offset", 0),
	}
	if f.Status != "" && !validStatus(f.Status) {
		httpx.Error(w, httpx.BadRequest("unknown status "+strconv.Quote(f.Status)))
		return
	}
	if f.Verdict != "" && !validVerdict(f.Verdict) {
		httpx.Error(w, httpx.BadRequest("unknown verdict "+strconv.Quote(f.Verdict)))
		return
	}
	cands, err := a.svc.Candidates(ctx, f)
	if err != nil {
		a.fail(w, "list candidates", err)
		return
	}
	out := make([]candidateResponse, 0, len(cands))
	for _, c := range cands {
		out = append(out, toCandidateResponseStored(c))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"candidates": out,
		"count":      len(out),
		"limit":      f.Limit,
		"offset":     f.Offset,
	})
}

// validStatus reports whether a status filter names a real lifecycle position.
func validStatus(s string) bool {
	switch candidate.Status(s) {
	case candidate.StatusNew, candidate.StatusAccepted, candidate.StatusRejected,
		candidate.StatusDuplicate, candidate.StatusError:
		return true
	}
	return false
}

// validVerdict reports whether a verdict filter names a real ranking outcome.
func validVerdict(v string) bool {
	switch v {
	case "accept", "review", "reject":
		return true
	}
	return false
}

// getCandidate returns one candidate with its evidence.
func (a *API) getCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httpx.Error(w, httpx.BadRequest("a candidate id is required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	c, err := a.svc.Candidate(ctx, id)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			httpx.Error(w, httpx.NotFound("no such candidate"))
			return
		}
		a.fail(w, "get candidate", err)
		return
	}
	httpx.JSON(w, http.StatusOK, toCandidateResponseStored(c))
}

// intParam reads a bounded integer query parameter.
func intParam(r *http.Request, name string, def int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def
	}
	if n > MaxPageSize {
		return MaxPageSize
	}
	return n
}

// fail logs the real error and returns a generic one. Internal error text can
// carry a DSN, a query, or a provider's response body, none of which belongs in
// an HTTP response to a caller who did not ask for it.
func (a *API) fail(w http.ResponseWriter, what string, err error) {
	a.log.Error("discovery: api request failed", "operation", what, "error", err)
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		httpx.Error(w, httpx.Timeout(what+" timed out").WithCause(err))
	case errors.Is(err, persistence.ErrNotFound):
		httpx.Error(w, httpx.NotFound("not found").WithCause(err))
	case errors.Is(err, service.ErrNoSeeds):
		httpx.Error(w, httpx.BadRequest("a run needs a profile, seeds, or both").WithCause(err))
	case errors.Is(err, service.ErrUnknownProvider):
		httpx.Error(w, httpx.BadRequest("an enabled provider is not registered").WithCause(err))
	default:
		httpx.Error(w, httpx.Internal(what+" failed").WithCause(err))
	}
}
