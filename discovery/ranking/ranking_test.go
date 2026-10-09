package ranking

import (
	"strings"
	"testing"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
)

var base = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func cfg(t *testing.T) Config {
	t.Helper()
	return Config{
		Industries:             []string{"industrial robotics", "logistics software"},
		Keywords:               []string{"warehouse automation", "fleet tracking"},
		Countries:              []string{"DE", "GB", "PL"},
		Exclude:                []string{"acme rival"},
		ExcludeHosts:           []string{"blocked.northwind-industries.de"},
		MaxCandidatesPerDomain: 5,
		Freshness:              365 * 24 * time.Hour,
		Now:                    func() time.Time { return base },
	}
}

// cand builds a healthy candidate that clears the accept threshold on its own, so
// that each test can degrade exactly one factor and observe the effect.
func cand(t *testing.T, mutate func(*candidate.Candidate)) candidate.Candidate {
	t.Helper()
	c := candidate.Candidate{
		Name:      "Northwind Robotics GmbH",
		Domain:    "northwind-robotics.de",
		Industry:  "Industrial Robotics",
		Country:   "DE",
		Keywords:  []string{"warehouse automation"},
		FirstSeen: base.Add(-30 * 24 * time.Hour),
		LastSeen:  base.Add(-2 * 24 * time.Hour),
	}
	c.AddEvidence(candidate.Evidence{
		Source:     candidate.SourceSearch,
		Method:     candidate.MethodSearchResult,
		URL:        "https://example.org/dir/northwind",
		Query:      "industrial robotics germany",
		ObservedAt: base.Add(-2 * 24 * time.Hour),
	})
	if mutate != nil {
		mutate(&c)
	}
	return c
}

func TestScoreExplainsEveryFactor(t *testing.T) {
	r := New(cfg(t))
	d := r.Score(cand(t, nil))

	if d.Verdict != VerdictAccept {
		t.Fatalf("healthy candidate should be accepted, got %v at %v (%s)", d.Verdict, d.Score, d.Explanation)
	}
	if d.Score < 0.6 {
		t.Fatalf("healthy candidate scored only %v: %s", d.Score, d.Explanation)
	}
	if d.Explanation == "" {
		t.Fatal("a decision without an explanation cannot be acted on")
	}

	// Every factor must carry a weight or a note, otherwise it is dead weight in
	// the output that an operator will try to interpret.
	if len(d.Factors) < 5 {
		t.Fatalf("expected several factors, got %d: %+v", len(d.Factors), d.Factors)
	}
	for _, f := range d.Factors {
		if f.Note == "" && f.Contribution == 0 && f.Reason != ReasonIDNDomain {
			t.Errorf("factor %v has neither a note nor a contribution: %+v", f.Reason, f)
		}
	}

	// Factors must be ordered by consequence so the top of the list is what
	// actually decided the score.
	for i := 1; i < len(d.Factors); i++ {
		if abs(d.Factors[i-1].Contribution) < abs(d.Factors[i].Contribution) {
			t.Errorf("factors out of order at %d: %v(%v) then %v(%v)",
				i, d.Factors[i-1].Reason, d.Factors[i-1].Contribution,
				d.Factors[i].Reason, d.Factors[i].Contribution)
		}
	}

	// The total must equal the sum of the contributions, or the score is not
	// actually derived from the explanation it ships with.
	var sum float64
	for _, f := range d.Factors {
		sum += f.Contribution
	}
	if diff := sum - d.Score; diff > 0.011 || diff < -0.011 {
		t.Errorf("score %v does not match the sum of its factors %v", d.Score, round(sum))
	}
}

func TestMissingDomainIsNeverAccepted(t *testing.T) {
	r := New(cfg(t))
	// Even a maximally attested, perfectly-fitting company with no website is
	// not a lead: there is nothing to crawl, verify or contact.
	c := cand(t, func(c *candidate.Candidate) {
		c.Domain = ""
		c.Country = "DE"
		for i := range c.Evidence {
			c.Evidence[i].Source = candidate.SourceSeed
		}
	})
	d := r.Score(c)
	if d.Verdict == VerdictAccept {
		t.Fatalf("a candidate with no domain must never be accepted, got %v at %v", d.Score, d.Explanation)
	}
	if d.Score >= r.cfg.AcceptThreshold {
		t.Fatalf("score %v should have been capped below the accept threshold %v", d.Score, r.cfg.AcceptThreshold)
	}
	var sawNoDomain bool
	for _, f := range d.Factors {
		if f.Reason == ReasonNoDomain && f.Note != "" {
			sawNoDomain = true
		}
	}
	if !sawNoDomain {
		t.Error("the missing domain must be called out in the factors")
	}
}

func TestPlatformDomainIsPenalised(t *testing.T) {
	r := New(cfg(t))
	good := r.Score(cand(t, nil))
	onPlatform := r.Score(cand(t, func(c *candidate.Candidate) {
		c.Domain = "acme-robotics.medium.com"
	}))
	if onPlatform.Score >= good.Score {
		t.Errorf("a company found on a platform domain scored %v, at or above the %v for a real website",
			onPlatform.Score, good.Score)
	}
	if onPlatform.Verdict == VerdictAccept {
		t.Errorf("a company on a platform domain should not be accepted, got %v", onPlatform.Score)
	}
	var sawPlatform bool
	for _, f := range onPlatform.Factors {
		if f.Reason == ReasonPlatformDomain {
			sawPlatform = true
		}
	}
	if !sawPlatform {
		t.Error("the platform domain must be called out in the factors")
	}
}

func TestCorroborationHasDiminishingReturns(t *testing.T) {
	r := New(cfg(t))
	one := cand(t, nil)

	two := cand(t, func(c *candidate.Candidate) {
		c.AddEvidence(candidate.Evidence{
			Source:     candidate.SourceRegistry,
			Method:     candidate.MethodAPIRecord,
			URL:        "https://northwind-robotics.de/impressum",
			ObservedAt: base.Add(-3 * 24 * time.Hour),
		})
	})
	five := cand(t, func(c *candidate.Candidate) {
		for i, src := range []candidate.Source{
			candidate.SourceRegistry,
			candidate.SourceDirectory,
			candidate.SourceNews,
			candidate.SourceCertificate,
		} {
			c.AddEvidence(candidate.Evidence{
				Source:     src,
				Method:     candidate.MethodDerived,
				URL:        "https://source.example/" + string(rune('a'+i)),
				ObservedAt: base.Add(-3 * 24 * time.Hour),
			})
		}
	})

	s1 := r.Score(one).Score
	s2 := r.Score(two).Score
	s5 := r.Score(five).Score

	if !(s1 < s2 && s2 < s5) {
		t.Fatalf("corroboration should increase the score: 1=%v 2=%v 5=%v", s1, s2, s5)
	}
	// The whole point of diminishing returns: the fourth and fifth source add
	// far less than the second.
	gainFirst := s2 - s1
	gainRest := s5 - s2
	if gainRest >= gainFirst {
		t.Errorf("corroboration is not diminishing: +1 source gained %v, +3 more gained %v", gainFirst, gainRest)
	}
}

func TestIndustryAndGeographyGates(t *testing.T) {
	r := New(cfg(t))

	match := r.Score(cand(t, nil))
	mismatch := r.Score(cand(t, func(c *candidate.Candidate) {
		c.Industry = "Baking Supplies"
		c.Keywords = []string{"sourdough"}
		c.Country = "FR"
	}))
	if mismatch.Score >= match.Score {
		t.Errorf("out-of-vertical out-of-market candidate scored %v, at or above the %v for the target fit",
			mismatch.Score, match.Score)
	}
	if mismatch.Verdict == VerdictAccept {
		t.Errorf("a candidate outside every target should not be accepted, got %v", mismatch.Score)
	}
	for _, want := range []Reason{ReasonCountryMismatch} {
		var found bool
		for _, f := range mismatch.Factors {
			if f.Reason == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected reason %v in %+v", want, mismatch.Factors)
		}
	}

	// A candidate that matches a vertical only loosely must not get full marks.
	partial := r.Score(cand(t, func(c *candidate.Candidate) {
		c.Industry = "Industrial Robotics and Automation Systems"
	}))
	if partial.Score >= match.Score {
		t.Errorf("a fuzzy industry match should score below an exact one: %v vs %v", partial.Score, match.Score)
	}
}

func TestCCTLDIsAHintNotAFact(t *testing.T) {
	r := New(cfg(t))
	// No stated country, but a .de domain, in a run targeting Germany. A ccTLD
	// identifies a domain's registration, not a headquarters, so it must count
	// for less than a stated country.
	hint := r.Score(cand(t, func(c *candidate.Candidate) { c.Country = "" }))
	stated := r.Score(cand(t, nil))
	if hint.Score >= stated.Score {
		t.Errorf("a ccTLD hint (%v) must not score as high as a stated country (%v)", hint.Score, stated.Score)
	}

	// A .com in a Germany-only run gets no credit from the suffix at all.
	none := r.Score(cand(t, func(c *candidate.Candidate) {
		c.Country = ""
		c.Domain = "northwind-robotics.com"
	}))
	if none.Score >= hint.Score {
		t.Errorf("a suffix identifying no country (%v) should score below a matching suffix (%v)", none.Score, hint.Score)
	}
}

func TestFreshnessDecays(t *testing.T) {
	r := New(cfg(t))
	fresh := r.Score(cand(t, nil))
	stale := r.Score(cand(t, func(c *candidate.Candidate) {
		c.FirstSeen = base.Add(-5 * 365 * 24 * time.Hour)
		c.LastSeen = base.Add(-4 * 365 * 24 * time.Hour)
	}))
	if stale.Score >= fresh.Score {
		t.Errorf("a four-year-old candidate (%v) should not score above a fresh one (%v)", stale.Score, fresh.Score)
	}

	// A future timestamp is provider clock skew, not infinite freshness, and it
	// must not be treated as an error.
	skewed := r.Score(cand(t, func(c *candidate.Candidate) {
		c.LastSeen = base.Add(72 * time.Hour)
	}))
	if skewed.Verdict != VerdictAccept {
		t.Errorf("clock skew should not cost a candidate its verdict: %v at %v", skewed.Verdict, skewed.Score)
	}
}

func TestExclusionsBeatAStrongScore(t *testing.T) {
	r := New(cfg(t))
	clean := r.Score(cand(t, nil))
	if clean.Verdict != VerdictAccept {
		t.Fatalf("the control candidate should be accepted, got %v at %v", clean.Verdict, clean.Score)
	}
	excluded := r.Score(cand(t, func(c *candidate.Candidate) {
		c.Keywords = []string{"acme rival distribution"}
	}))
	if excluded.Verdict == VerdictAccept {
		t.Errorf("an excluded candidate must not be accepted, got %v at %v", excluded.Score, excluded.Explanation)
	}
	blocked := r.Score(cand(t, func(c *candidate.Candidate) {
		c.Domain = "blocked.northwind-industries.de"
	}))
	if blocked.Verdict == VerdictAccept {
		t.Errorf("a blocklisted host must not be accepted, got %v at %v", blocked.Score, blocked.Explanation)
	}
	// Both penalties must cost the same configured amount off the same baseline.
	// Comparing two differently-configured candidates instead would prove
	// nothing about how decisive a penalty is.
	penalty := r.cfg.Weights.Penalty
	for _, tc := range []struct {
		what string
		got  float64
	}{{"term exclusion", excluded.Score}, {"host blocklist", blocked.Score}} {
		if cost := clean.Score - tc.got; cost < penalty-0.011 {
			t.Errorf("a %s should cost about %v, but cost %v", tc.what, penalty, cost)
		}
	}
}

func TestPerDomainCap(t *testing.T) {
	c := cfg(t)
	c.MaxCandidatesPerDomain = 3
	r := New(c)

	// Twelve unrelated companies that all happen to be hosted on one platform
	// domain. Identity correctly keeps them distinct, which is exactly the case
	// where a per-domain cap has to do the work.
	var in []candidate.Candidate
	for i := 0; i < 12; i++ {
		in = append(in, cand(t, func(cc *candidate.Candidate) {
			cc.Name = "Independent Project " + string(rune('A'+i))
			cc.Domain = "independent-project-" + string(rune('a'+i)) + ".github.com"
			cc.Industry = "Logistics Software"
		}))
	}
	out := r.Rank(in)
	if len(out) != 12 {
		t.Fatalf("capping must reject, not drop: got %d of %d", len(out), len(in))
	}
	var aboveCap int
	var sawCap bool
	for _, d := range out {
		for _, f := range d.Factors {
			if f.Reason == ReasonTooManyOnDomain {
				sawCap = true
				if d.Verdict == VerdictAccept {
					aboveCap++
				}
			}
		}
	}
	if aboveCap > 0 {
		t.Errorf("%d capped candidates were still accepted", aboveCap)
	}
	if !sawCap {
		t.Error("capped candidates must be told they were capped")
	}
}

func TestRankDeduplicatesAndIsDeterministic(t *testing.T) {
	r := New(cfg(t))
	// The same company stated two ways: once with a domain, once from a
	// different surface. Both are valid records; only one should be returned.
	a := cand(t, nil)
	b := cand(t, func(c *candidate.Candidate) {
		c.AddEvidence(candidate.Evidence{
			Source:     candidate.SourceDirectory,
			Method:     candidate.MethodListPage,
			URL:        "https://directory.example/northwind",
			ObservedAt: base.Add(-24 * time.Hour),
		})
	})
	other := cand(t, func(c *candidate.Candidate) {
		c.Name = "Fabrikam Logistics Ltd"
		c.Domain = "fabrikam-logistics.co.uk"
		c.Country = "GB"
	})

	out := r.Rank([]candidate.Candidate{a, b, other})
	if len(out) != 2 {
		t.Fatalf("expected the two duplicates of Northwind to collapse into one, got %d: %+v", len(out), out)
	}
	// The survivor must be the better-attested record.
	if !strings.Contains(out[0].Candidate.Name, "Northwind") {
		t.Errorf("the higher-scoring record should survive dedupe, got %q", out[0].Candidate.Name)
	}
	for i, d := range out {
		if d.Rank != i+1 {
			t.Errorf("rank %d is %d", i, d.Rank)
		}
	}

	// A resumed run must be able to rely on the ordering.
	again := r.Rank([]candidate.Candidate{a, b, other})
	for i := range out {
		if again[i].Candidate.ID != out[i].Candidate.ID {
			t.Fatalf("rank order is not deterministic at %d: %v then %v", i, out[i].Candidate.Name, again[i].Candidate.Name)
		}
	}
}

func TestScoreIsAlwaysInRange(t *testing.T) {
	r := New(cfg(t))
	cases := []candidate.Candidate{
		{},
		{Name: "no domain", Country: "ZZ"},
		{Name: "Excluded Co", Industry: "acme rival", Country: "DE"},
		{Name: "Many Keywords", Domain: "a.de", Industry: "logistics software", Country: "DE", Keywords: []string{"fleet tracking"}},
		{Name: "Weird", Domain: "not a domain", LastSeen: base.Add(-10000 * time.Hour)},
		{Name: "IDN", Domain: "münchen-industrie.de", Country: "DE", Industry: "industrial robotics"},
	}
	for i, c := range cases {
		c.AddEvidence(candidate.Evidence{
			Source:     candidate.SourceSeed,
			Method:     candidate.MethodSeedImport,
			URL:        "https://seed.example/" + string(rune('a'+i)),
			ObservedAt: base,
		})
		d := r.Score(c)
		if d.Score < 0 || d.Score > 1 {
			t.Errorf("case %d: score %v out of range", i, d.Score)
		}
		if d.Explanation == "" {
			t.Errorf("case %d: no explanation", i)
		}
	}
}

func TestPenaltyCanDriveScoreToZero(t *testing.T) {
	r := New(cfg(t))
	d := r.Score(cand(t, func(c *candidate.Candidate) {
		c.Name = "Acme Rival"
		c.Domain = "blocked.example.com"
		c.Industry = "acme rival"
		c.Country = "DE"
	}))
	if d.Score < 0 {
		t.Fatalf("score %v went negative; the clamp is not applied", d.Score)
	}
	if d.Verdict != VerdictReject {
		t.Errorf("a doubly-excluded candidate should be rejected, got %v at %v", d.Verdict, d.Score)
	}
}

func TestNoConfigurationMeansNeutralNotPunishing(t *testing.T) {
	// An operator who has configured no countries and no verticals has expressed
	// no preference. The neutral score must not be mistaken for a mismatch.
	r := New(Config{Now: func() time.Time { return base }})
	d := r.Score(cand(t, func(c *candidate.Candidate) {
		c.Country = "FR"
		c.Industry = "Baking Supplies"
	}))
	for _, f := range d.Factors {
		if f.Reason == ReasonCountryMismatch || f.Reason == ReasonIndustryUnknown {
			t.Errorf("unconfigured targets produced a mismatch reason: %+v", f)
		}
	}
}

func TestConfigNormalisation(t *testing.T) {
	r := New(Config{
		Countries:    []string{" de ", "DE", "d", "x1", "gb"},
		ExcludeHosts: []string{"SUB.Blocked.northwind.de", "blocked.northwind.de", "nonsense"},
		Now:          func() time.Time { return base },
	})
	got := r.Config()
	if len(got.Countries) != 2 {
		t.Errorf("countries should normalise to 2 unique codes, got %v", got.Countries)
	}
	if got.Countries[0] != "DE" || got.Countries[1] != "GB" {
		t.Errorf("countries = %v", got.Countries)
	}
	// Subdomains of a blocked host must reduce to the same key, or a blocklist
	// entry silently stops matching.
	if len(got.ExcludeHosts) != 2 || got.ExcludeHosts[0] != "northwind.de" {
		t.Errorf("a blocklist entry must reduce to its registrable domain so a subdomain entry still matches, got %v", got.ExcludeHosts)
	}
	if got.Weights != DefaultWeights() {
		t.Errorf("unset weights should fall back to the defaults, got %+v", got.Weights)
	}
	if got.AcceptThreshold <= 0 || got.ReviewBand <= 0 || got.Freshness <= 0 || got.Now == nil {
		t.Errorf("defaults not applied: %+v", got)
	}
}

func TestPartialWeightOverride(t *testing.T) {
	r := New(Config{Weights: Weights{Source: 0.5}, Now: func() time.Time { return base }})
	w := r.Config().Weights
	if w.Source != 0.5 {
		t.Errorf("the stated weight should be kept, got %v", w.Source)
	}
	d := DefaultWeights()
	if w.Domain != d.Domain || w.Industry != d.Industry || w.Geography != d.Geography {
		t.Errorf("unset weights should keep their defaults, got %+v", w)
	}
}

func TestExplanationsNameTheRealReason(t *testing.T) {
	r := New(cfg(t))
	// The most consequential thing about this candidate is that it is on a
	// platform domain, and the explanation should say so.
	d := r.Score(cand(t, func(c *candidate.Candidate) {
		c.Domain = "northwind.medium.com"
		c.Industry = "Baking Supplies"
		c.Country = "FR"
	}))
	if !strings.Contains(d.Explanation, "medium.com") {
		t.Errorf("explanation should name the platform domain, got %q", d.Explanation)
	}
	if !strings.Contains(d.Explanation, d.Verdict.String()) {
		t.Errorf("explanation should name the verdict, got %q", d.Explanation)
	}
}
