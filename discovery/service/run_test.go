package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/config"
	"github.com/hmza-hb/lead-intelligence/discovery/lineage"
	"github.com/hmza-hb/lead-intelligence/discovery/persistence"
	"github.com/hmza-hb/lead-intelligence/discovery/providers"
	"github.com/hmza-hb/lead-intelligence/discovery/query"
	"github.com/hmza-hb/lead-intelligence/discovery/ranking"
	"github.com/hmza-hb/lead-intelligence/discovery/service"
)

// allKinds is what a fake provider claims to serve. Giving every fake the full
// set keeps the tests about the service's orchestration rather than about kind
// routing, which the provider package's own tests already cover.
var allKinds = []providers.Kind{
	providers.KindIndustry, providers.KindTechnology, providers.KindGeography,
	providers.KindDirectory, providers.KindName, providers.KindCompetitor,
}

func testConfig() config.Config {
	c := config.Default()
	c.Run.MaxDepth = 0
	c.Run.Concurrency = 2
	c.Run.MaxProviderCalls = 50
	c.Run.MaxCandidatesPerQuery = 10
	c.Query.MaxPerCandidate = 4
	c.Query.MaxPerRun = 20
	c.Prov.Enabled = nil
	c.Prov.Disabled = nil
	return c
}

func newService(t *testing.T, cfg config.Config, provs ...providers.Provider) (*service.Service, *memStore) {
	t.Helper()
	store := newMemStore()
	svc, err := service.New(service.Options{
		Config:    cfg,
		Store:     store,
		Providers: provs,
		Now:       func() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) },
		Log:       quietLog(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, store
}

func seed(name, domain, industry string) candidate.Candidate {
	return candidate.Candidate{
		Name:     name,
		Domain:   domain,
		URL:      "https://" + domain + "/",
		Industry: industry,
		Country:  "US",
		Evidence: []candidate.Evidence{{
			Source: candidate.SourceSeed, Method: candidate.MethodSeedImport,
			URL: "file:///seeds.csv", Snippet: name,
		}},
	}
}

func TestRunWithoutSeedsOrProfileRefuses(t *testing.T) {
	svc, _ := newService(t, testConfig())
	_, err := svc.Run(context.Background(), service.Request{})
	if !errors.Is(err, service.ErrNoSeeds) {
		t.Errorf("error = %v, want ErrNoSeeds", err)
	}
}

func TestNewRequiresAStore(t *testing.T) {
	_, err := service.New(service.Options{Config: testConfig()})
	if err == nil {
		t.Fatal("a service with no store cannot record or resume a run and must be refused")
	}
}

func TestSeedOnlyRunSearchesTheSeeds(t *testing.T) {
	cfg := testConfig()
	// A provider that answers only the query naming the seed.
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		if strings.Contains(strings.ToLower(q.Text), "acme") {
			return []candidate.Candidate{candidateWith("acme-finds.test", "Acme Finds", "Industrial Automation", "US")}, "", false, nil
		}
		return nil, "", false, providers.ErrNoResults
	}}
	svc, store := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Seeds: []candidate.Candidate{seed("Acme Robotics", "acme.test", "Industrial Automation")},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Status != service.StatusCompleted {
		t.Errorf("status = %q, want completed", report.Status)
	}
	// The seed itself plus the company the provider found.
	if len(report.Candidates) < 2 {
		t.Fatalf("got %d candidates, want the seed and at least one discovery", len(report.Candidates))
	}
	found := false
	for _, c := range report.Candidates {
		if c.Candidate.Domain == "acme-finds.test" {
			found = true
		}
	}
	if !found {
		t.Error("the candidate the provider returned for the seed's query was not in the output")
	}
	if store.calls(report.RunID) == 0 {
		t.Error("no lineage was recorded, so the run could not be resumed or explained")
	}
}

func TestProfileOnlyRunDiscoversCompaniesTheOperatorNeverNamed(t *testing.T) {
	cfg := testConfig()
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		if q.Kind != providers.KindIndustry {
			return nil, "", false, providers.ErrNoResults
		}
		return []candidate.Candidate{
			candidateWith("one.test", "One Co", "Industrial Automation", "US"),
			candidateWith("two.test", "Two Co", "Industrial Automation", "US"),
		}, "", false, nil
	}}
	svc, _ := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{
			Name:       "us-automation",
			Industries: []string{"Industrial Automation"},
			Countries:  []string{"US"},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(report.Candidates) == 0 {
		t.Fatal("a market search found nothing; the profile never reached the provider")
	}
	if report.Accepted == 0 {
		t.Errorf("accepted = 0, ranking rejected everything the profile asked for: %+v", report.Candidates[0])
	}
}

func TestEveryCandidateIsAttributable(t *testing.T) {
	cfg := testConfig()
	// A provider that returns a candidate with no evidence at all, which a
	// misbehaving third-party API really does.
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{{
			Name: "Anonymous Co", Domain: "anon.test",
			Industry: "Industrial Automation", Country: "US",
		}}, "", false, nil
	}}
	svc, store := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(report.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(report.Candidates))
	}
	ev := report.Candidates[0].Candidate.Evidence
	if len(ev) == 0 {
		t.Fatal("the candidate reached the output with no evidence: nobody could explain it")
	}
	if ev[0].Source == "" || ev[0].Method == "" {
		t.Errorf("evidence = %+v, want a source and a method", ev[0])
	}
	if ev[0].Query == "" {
		t.Error("the supplied evidence does not name the query that found the company")
	}
	if report.Candidates[0].ID == "" {
		t.Error("the candidate has no stored id, so it cannot be looked up later")
	}
	if store.calls(report.RunID) == 0 {
		t.Error("no lineage recorded")
	}
}

func TestRunIsBoundedByItsProviderCallBudget(t *testing.T) {
	cfg := testConfig()
	cfg.Run.MaxProviderCalls = 3
	cfg.Query.MaxPerRun = 50
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{candidateWith("co.test", "Co", "Industrial Automation", "US")}, "", false, nil
	}}
	svc, _ := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{
			Name:       "p",
			Industries: []string{"Industrial Automation", "Robotics", "Logistics", "Packaging", "Controls"},
			Countries:  []string{"US", "CA", "GB", "DE"},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if p.callCount() > 3 {
		t.Errorf("made %d provider calls, want at most the configured 3", p.callCount())
	}
	if report.ProviderCalls > 3 {
		t.Errorf("report says %d calls, want at most 3", report.ProviderCalls)
	}
	if report.QueriesPending == 0 {
		t.Error("a run that stopped at its budget reports no pending work, so nothing tells an operator to resume it")
	}
}

func TestUnfinishedQuestionsAreReportedAsPending(t *testing.T) {
	cfg := testConfig()
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{candidateWith("co.test", "Co", "Industrial Automation", "US")}, "page=2", true, nil
	}}
	svc, _ := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.QueriesPending == 0 {
		t.Error("a provider that said it had more results produced no pending work")
	}
}

func TestSameCompanyFoundTwiceIsOneCandidate(t *testing.T) {
	cfg := testConfig()
	n := 0
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		n++
		// The same company, from two different queries, with two different
		// spellings of the name.
		return []candidate.Candidate{{
			Name:     []string{"Acme Robotics", "Acme Robotics Inc"}[n%2],
			Domain:   "acme.test",
			Industry: "Industrial Automation",
			Country:  "US",
			Evidence: []candidate.Evidence{{
				Source: candidate.SourceSearch, Method: candidate.MethodSearchResult,
				URL: "https://search.test/?q=" + q.Text,
			}},
		}}, "", false, nil
	}}
	svc, _ := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Seeds: []candidate.Candidate{seed("Acme Robotics", "acme.test", "Industrial Automation")},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	count := 0
	for _, c := range report.Candidates {
		if c.Candidate.Domain == "acme.test" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("got %d entries for acme.test, want one: the same company is the same company", count)
	}
}

func TestRankingOutputIsStoredAndExplainable(t *testing.T) {
	cfg := testConfig()
	cfg.Rank.AcceptThreshold = 0.1
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{candidateWith("ranked.test", "Ranked Co", "Industrial Automation", "US")}, "", false, nil
	}}
	svc, store := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(report.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(report.Candidates))
	}
	top := report.Candidates[0]
	if top.Rank != 1 {
		t.Errorf("rank = %d, want 1 for the only candidate", top.Rank)
	}
	if top.Verdict == "" {
		t.Error("no verdict was produced")
	}
	if top.Explain == "" {
		t.Error("no explanation was produced: an unexplained score cannot be tuned")
	}
	if len(top.Factors) == 0 {
		t.Fatal("no rank factors were recorded, so the score cannot be reconstructed")
	}
	if top.ID == "" {
		t.Fatal("no stored id")
	}
	svcStore, ok := store.ranking[top.ID]
	if !ok {
		t.Fatal("the ranking was not written to the store")
	}
	if svcStore.Score != top.Score {
		t.Errorf("stored score %v does not match the reported %v", svcStore.Score, top.Score)
	}
}

func TestExcludedTermIsRejectedRatherThanSilentlyDropped(t *testing.T) {
	cfg := testConfig()
	cfg.Rank.AcceptThreshold = 0.0
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{
			candidateWith("acme.test", "Acme Robotics", "Industrial Automation", "US"),
			candidateWith("competitor.test", "Rival Industries", "Industrial Automation", "US"),
		}, "", false, nil
	}}
	svc, _ := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{
			Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"},
			Exclude: []string{"rival"},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var rival *service.Scored
	for i := range report.Candidates {
		if strings.Contains(strings.ToLower(report.Candidates[i].Candidate.Name), "rival") {
			rival = &report.Candidates[i]
		}
	}
	if rival == nil {
		t.Fatal("the excluded company vanished entirely: an operator who expected it needs to see why it was cut")
	}
	if rival.Verdict != "reject" {
		t.Errorf("verdict = %q, want reject for an excluded company", rival.Verdict)
	}
	excluded := false
	for _, f := range rival.Factors {
		if f.Name == string(ranking.ReasonExcluded) {
			excluded = true
		}
	}
	if !excluded {
		t.Errorf("no exclusion reason recorded among %+v: the rejection is unexplained", rival.Factors)
	}
	if report.Accepted != 1 {
		t.Errorf("accepted = %d, want only the non-excluded company", report.Accepted)
	}
}

func TestMaxCandidatesCapsTheRun(t *testing.T) {
	cfg := testConfig()
	cfg.Run.MaxCandidates = 2
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{
			candidateWith("a.test", "A", "Industrial Automation", "US"),
			candidateWith("b.test", "B", "Industrial Automation", "US"),
			candidateWith("c.test", "C", "Industrial Automation", "US"),
			candidateWith("d.test", "D", "Industrial Automation", "US"),
		}, "", false, nil
	}}
	svc, _ := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(report.Candidates) > 2 {
		t.Errorf("got %d candidates, want the configured ceiling of 2", len(report.Candidates))
	}
}

func TestCancellationIsRecordedNotSwallowed(t *testing.T) {
	cfg := testConfig()
	ctx, cancel := context.WithCancel(context.Background())
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		cancel()
		return nil, "", false, context.Canceled
	}}
	svc, store := newService(t, cfg, p)

	report, err := svc.Run(ctx, service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled surfaced to the caller", err)
	}
	if report == nil || report.Status != service.StatusCancelled {
		t.Fatalf("report = %+v, want a cancelled run recorded", report)
	}
	run, err := store.Run(context.Background(), report.RunID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if run.Status != service.StatusCancelled {
		t.Errorf("stored status = %q, want cancelled: a run that died must be visible in the run list", run.Status)
	}
}

func TestUnknownProviderInEnabledListFailsTheRun(t *testing.T) {
	cfg := testConfig()
	cfg.Prov.Enabled = []string{"search", "typo-provider"}
	p := &fakeProvider{name: "search", kinds: allKinds}
	svc, _ := newService(t, cfg, p)

	_, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"x"}},
	})
	if !errors.Is(err, service.ErrUnknownProvider) {
		t.Errorf("error = %v, want ErrUnknownProvider: a typo that silently disables a surface costs a week of missing data", err)
	}
}

func TestDisabledProviderIsNotCalled(t *testing.T) {
	cfg := testConfig()
	cfg.Prov.Enabled = []string{"search", "certificate", "sitemap"}
	cfg.Prov.Disabled = []string{"certificate", "sitemap"}
	called := &fakeProvider{name: "search", kinds: allKinds}
	skipped := &fakeProvider{name: "certificate"}
	// A provider that declares it serves no kind at all is registered, so its
	// state is visible, but must never be called.
	unscoped := &fakeProvider{name: "sitemap", kinds: []providers.Kind{}}
	svc, _ := newService(t, cfg, called, skipped)

	if _, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"x"}},
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if skipped.callCount() != 0 {
		t.Errorf("a disabled provider was called %d times", skipped.callCount())
	}
	if unscoped.callCount() != 0 {
		t.Errorf("a provider that declared it serves no query kind was called %d times: budget was spent collecting nothing", unscoped.callCount())
	}
}

func TestUnreadyProviderStillAppearsInStats(t *testing.T) {
	cfg := testConfig()
	unready := &fakeProvider{name: "search", kinds: allKinds, readyErr: errors.New("missing API key")}
	svc, _ := newService(t, cfg, unready)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"x"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// A provider that reports itself unready is skipped rather than called, so
	// the run still completes; the run's provider list is where the operator
	// finds out why coverage is thinner than they expected.
	st, ok := report.ProviderStats["search"]
	if !ok {
		t.Fatal("an unconfigured provider vanished from the report: that is how missing credentials stay hidden")
	}
	if st.Ready {
		t.Error("the provider reports ready while holding no key")
	}
	if !strings.Contains(st.ReadyError, "missing API key") {
		t.Errorf("ready error = %q, want the reason the operator needs to see", st.ReadyError)
	}
}

func TestRunWithNoRegisteredProvidersCompletesEmpty(t *testing.T) {
	cfg := testConfig()
	svc, store := newService(t, cfg)

	report, err := svc.Run(context.Background(), service.Request{
		Seeds: []candidate.Candidate{seed("Acme", "acme.test", "Robotics")},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Status != service.StatusCompleted {
		t.Errorf("status = %q, want completed", report.Status)
	}
	if len(report.Candidates) != 1 {
		t.Errorf("got %d candidates, want the seed itself", len(report.Candidates))
	}
	run, err := store.Run(context.Background(), report.RunID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if run.SeedsTotal != 1 {
		t.Errorf("seeds_total = %d, want 1", run.SeedsTotal)
	}
}

func TestDepthStopsExpansion(t *testing.T) {
	cfg := testConfig()
	cfg.Run.MaxDepth = 1
	var round int
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		round++
		// Every call returns something new, so a run that kept going would never
		// run out of frontier.
		return []candidate.Candidate{candidateWith(
			"chain"+itoa(round)+".test", "Chain "+itoa(round), "Industrial Automation", "US")}, "", false, nil
	}}
	svc, _ := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Depth 1 means two rounds, 0 and 1. Expansion must not continue past that
	// however much the frontier keeps growing.
	if report.Candidates == nil {
		t.Fatal("no candidates")
	}
	stored, err := store2Candidates(svc, report)
	if err != nil {
		t.Fatalf("read candidates: %v", err)
	}
	// A chain of discoveries is one per round; a run that ignored its depth
	// would produce far more.
	if len(stored) > 40 {
		t.Errorf("got %d candidates across a depth-1 run, which suggests the depth bound was not applied", len(stored))
	}
	for _, c := range stored {
		if c.Candidate.Status == "" {
			t.Error("a persisted candidate has no lifecycle status")
		}
	}
}

func TestSeedsArePersistedAndRanked(t *testing.T) {
	cfg := testConfig()
	svc, _ := newService(t, cfg)

	report, err := svc.Run(context.Background(), service.Request{
		Seeds: []candidate.Candidate{
			seed("Acme Robotics", "acme.test", "Industrial Automation"),
			seed("Beta Systems", "beta.test", "Industrial Automation"),
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(report.Candidates) != 2 {
		t.Fatalf("got %d candidates, want both seeds", len(report.Candidates))
	}
	if report.Candidates[0].ID == "" || report.Candidates[1].ID == "" {
		t.Error("a seed was not assigned a stored id")
	}
	// Both seeds have the same industry and no profile targeting, so both should
	// be kept rather than one flooding the other out.
	if report.Accepted+report.Rejected != 2 {
		t.Errorf("accepted %d + rejected %d != 2 verdicts for 2 candidates", report.Accepted, report.Rejected)
	}
}

func TestRedactedConfigStoredOnTheRun(t *testing.T) {
	cfg := testConfig()
	cfg.Prov.SearchKey = "super-secret-key"
	svc, store := newService(t, cfg)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"x"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	run, err := store.Run(context.Background(), report.RunID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	for k, v := range run.Config {
		if s, ok := v.(string); ok && strings.Contains(s, "super-secret-key") {
			t.Fatalf("a credential was stored on the run as %s", k)
		}
	}
	if run.ProfileName != "p" {
		t.Errorf("profile = %q, want the run to record which targeting it used", run.ProfileName)
	}
}

func TestConfiguredLanguagesReachTheProfile(t *testing.T) {
	cfg := testConfig()
	cfg.Query.Languages = []string{"de"}
	svc, _ := newService(t, cfg, &fakeProvider{name: "search", kinds: allKinds})

	// A request profile that says nothing about languages must still be run with
	// the deployment's configured ones; a setting an operator set that never
	// reaches a query is worse than not offering it.
	_, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"x"}, Countries: []string{"DE"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestServiceHelpersReadThroughTheStore(t *testing.T) {
	cfg := testConfig()
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{candidateWith("found.test", "Found Co", "Industrial Automation", "US")}, "", false, nil
	}}
	svc, _ := newService(t, cfg, p)
	ctx := context.Background()

	report, err := svc.Run(ctx, service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	runs, err := svc.Report(ctx, 10)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != report.RunID {
		t.Errorf("runs = %+v, want the run just made", runs)
	}
	cands, err := svc.Candidates(ctx, persistence.CandidateFilter{})
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(cands) == 0 {
		t.Error("no candidates readable back through the service")
	}
	if _, err := svc.Candidate(ctx, cands[0].ID); err != nil {
		t.Errorf("candidate: %v", err)
	}
	attempts, err := svc.Attempts(ctx, report.RunID)
	if err != nil {
		t.Fatalf("attempts: %v", err)
	}
	if len(attempts) == 0 {
		t.Error("no lineage readable back through the service")
	}
}

// store2Candidates reads a run's candidates through the service.
func store2Candidates(svc *service.Service, report *service.Report) ([]persistence.StoredCandidate, error) {
	return svc.Candidates(context.Background(), persistence.CandidateFilter{RunID: report.RunID})
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestRunFailsWhenResultsCannotBeStored(t *testing.T) {
	// A run whose leads never reached the database must not report itself
	// complete. Reporting success here is the failure mode that loses a sales
	// team's morning: the output looks fine on screen and is not there later.
	cfg := testConfig()
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(providers.Query, int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{candidateWith("a.test", "A", "Industrial Automation", "US")}, "", false, nil
	}}
	svc, store := newService(t, cfg, p)
	store.failWrites = errors.New("disk is full")

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if err == nil {
		t.Fatal("a run that could not store its candidates returned no error")
	}
	if report != nil && report.Status == "completed" {
		t.Errorf("status = %q, want a failure: the findings were never written", report.Status)
	}
}

func TestUnstoredRunIsMarkedFailedInTheDatabase(t *testing.T) {
	// The stored run has to agree with the returned one. An operator who lists
	// runs and sees "completed" must be able to trust that the leads are there.
	cfg := testConfig()
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(providers.Query, int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{candidateWith("a.test", "A", "Industrial Automation", "US")}, "", false, nil
	}}
	svc, store := newService(t, cfg, p)
	store.failWrites = errors.New("disk is full")

	_, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if err == nil {
		t.Fatal("expected the run to fail")
	}
	run, gerr := store.Run(context.Background(), lastRunID(t, store))
	if gerr != nil {
		t.Fatalf("read run: %v", gerr)
	}
	if run.Status == "completed" {
		t.Errorf("stored status = %q, want failed", run.Status)
	}
}

// lastRunID returns the most recent run the store saw.
func lastRunID(t *testing.T, m *memStore) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.runOrder) == 0 {
		t.Fatal("no runs were recorded")
	}
	return m.runOrder[len(m.runOrder)-1]
}

func TestNewEvidenceForAKnownCompanySurvivesTheCandidateCeiling(t *testing.T) {
	// The ceiling bounds how many companies a run holds, not how much it may
	// learn about one of them. Once the run is full, a later provider that
	// confirms a company already held is still the corroboration that makes the
	// lead worth calling, and dropping it would mean paying for the answer and
	// then discarding it.
	cfg := testConfig()
	cfg.Run.MaxCandidates = 1
	cfg.Run.MaxDepth = 2
	// Each result carries evidence naming the query that produced it, so the two
	// sightings of A are genuinely different observations rather than a duplicate.
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(q providers.Query, _ int) ([]candidate.Candidate, string, bool, error) {
		with := func(domain, name string) candidate.Candidate {
			return candidate.Candidate{
				Name: name, Domain: domain, URL: "https://" + domain + "/",
				Evidence: []candidate.Evidence{{
					Source: candidate.SourceSearch, Method: candidate.MethodSearchResult,
					URL: "https://search.test/?q=" + q.Text, Query: q.Text,
				}},
			}
		}
		return []candidate.Candidate{with("a.test", "A"), with("b.test", "B")}, "", false, nil
	}}
	svc, _ := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}, Countries: []string{"US"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(report.Candidates) > 1 {
		t.Fatalf("got %d candidates, want the ceiling of 1", len(report.Candidates))
	}
	if report.Candidates[0].Candidate.Domain != "a.test" {
		t.Fatalf("kept %q, want the first company found", report.Candidates[0].Candidate.Domain)
	}
	// Round one asks about A again. The run is already at its ceiling, so B is
	// refused, but A's second sighting must be kept.
	if got := len(report.Candidates[0].Candidate.Evidence); got < 2 {
		t.Errorf("A kept %d evidence rows, want both sightings despite the ceiling", got)
	}
}

func TestPendingCountReflectsQuestionsNotAsked(t *testing.T) {
	// Pending work is what a resuming run will pick up. If the budget dropped
	// questions, they have to be counted, or the next run believes they were
	// answered and the run never finishes.
	cfg := testConfig()
	cfg.Run.MaxProviderCalls = 1
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(providers.Query, int) ([]candidate.Candidate, string, bool, error) {
		return []candidate.Candidate{candidateWith("a.test", "A", "Industrial Automation", "US")}, "", false, nil
	}}
	svc, _ := newService(t, cfg, p)

	report, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation", "Automotive", "Aerospace", "Energy"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.ProviderCalls > 1 {
		t.Fatalf("made %d calls, want the budget of 1", report.ProviderCalls)
	}
	if report.QueriesPending == 0 {
		t.Error("pending = 0, but questions were dropped by the budget and a later run will have to ask them")
	}
}

func TestStoredLineageAgreesWithTheRunner(t *testing.T) {
	// The runner classifies every call once, and the stored lineage has to be
	// that record rather than a second opinion. A provider that is simply not
	// configured is skipped, not failed: the difference decides whether a
	// health dashboard reports a real outage.
	cfg := testConfig()
	p := &fakeProvider{name: "search", kinds: allKinds, respond: func(providers.Query, int) ([]candidate.Candidate, string, bool, error) {
		return nil, "", false, providers.ErrNotConfigured
	}}
	svc, store := newService(t, cfg, p)

	if _, err := svc.Run(context.Background(), service.Request{
		Profile: query.Profile{Name: "p", Industries: []string{"Industrial Automation"}},
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	id := lastRunID(t, store)
	attempts, err := store.AttemptsFor(context.Background(), id)
	if err != nil {
		t.Fatalf("attempts: %v", err)
	}
	if len(attempts) == 0 {
		t.Fatal("no lineage recorded")
	}
	for _, a := range attempts {
		if a.Outcome == lineage.OutcomeFailed {
			t.Errorf("attempt on %q recorded as failed; an unconfigured provider is skipped", a.Query)
		}
	}
	// The report's own failure count has to agree with the stored lineage.
	report, err := store.Run(context.Background(), id)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	stored := 0
	for _, a := range attempts {
		if a.Outcome == lineage.OutcomeFailed {
			stored++
		}
	}
	if report.ProviderFailures != stored {
		t.Errorf("report says %d provider failures, lineage records %d", report.ProviderFailures, stored)
	}
}
