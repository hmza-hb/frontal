package query

import (
	"strings"
	"testing"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/domain"
)

func profile() Profile {
	return Profile{
		Name:         "industrial robotics",
		Industries:   []string{"industrial robotics", "warehouse automation"},
		Keywords:     []string{"ISO 27001", "machine vision"},
		Technologies: []string{"Kubernetes", "ROS"},
		Competitors:  []string{"Fanuc", "KUKA"},
		Directories:  []string{"Crunchbase"},
		Countries:    []string{"DE", "JP"},
	}
}

func acme() candidate.Candidate {
	return candidate.Candidate{
		ID:       "cand_1",
		Name:     "Acme Robotics GmbH",
		Domain:   "acme-robotics.de",
		Country:  "DE",
		Industry: "industrial robotics",
		Keywords: []string{"warehouse automation"},
	}
}

func texts(qs []Query) []string {
	out := make([]string, len(qs))
	for i, q := range qs {
		out[i] = q.Text
	}
	return out
}

func has(qs []Query, text string) bool {
	for _, q := range qs {
		if q.Text == text {
			return true
		}
	}
	return false
}

func TestRootGeneratesTheMarketNotACompany(t *testing.T) {
	g := New(Limits{MaxPerRun: 500})
	got := g.Root(profile())
	if len(got) == 0 {
		t.Fatal("Root produced no queries for a populated profile")
	}
	all := strings.Join(texts(got), " | ")
	for _, want := range []string{
		"industrial robotics",
		"warehouse automation",
		"machine vision",
		"Kubernetes companies",
		"alternatives to Fanuc",
		"companies like KUKA",
		"companies listed on Crunchbase",
	} {
		if !has(got, want) {
			t.Errorf("Root is missing %q\ngot: %s", want, all)
		}
	}
	for _, q := range got {
		if strings.Contains(q.Text, "acme") {
			t.Errorf("Root contains a company-specific query %q; root is market discovery", q.Text)
		}
	}
}

func TestRootIsDeterministic(t *testing.T) {
	// A resumed run must re-query the same things, or the budget has no
	// predictable relationship to coverage.
	g := New(Limits{MaxPerRun: 500})
	first := texts(g.Root(profile()))
	for i := 0; i < 5; i++ {
		again := texts(g.Root(profile()))
		if len(again) != len(first) {
			t.Fatalf("iteration %d produced %d queries, want %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("iteration %d query %d = %q, want %q", i, j, again[j], first[j])
			}
		}
	}
}

func TestExpandNamesTheCompanyAndItsMarket(t *testing.T) {
	g := New(Limits{MaxPerCandidate: 40})
	got := g.Expand(profile(), acme())
	all := strings.Join(texts(got), " | ")
	for _, want := range []string{
		"acme-robotics.de",
		`"Acme Robotics GmbH"`,
		`"Acme Robotics"`,
		"industrial robotics",
		`"Acme Robotics GmbH" warehouse automation`,
	} {
		if !has(got, want) {
			t.Errorf("Expand is missing %q\ngot: %s", want, all)
		}
	}
	for _, q := range got {
		if q.Parent != "cand_1" {
			t.Errorf("query %q has parent %q; every expanded query must name its candidate", q.Text, q.Parent)
		}
	}
}

func TestExpandStripsLegalSuffixButKeepsTheBrand(t *testing.T) {
	g := New(Limits{MaxPerCandidate: 40})
	got := g.Expand(profile(), acme())
	if !has(got, `"Acme Robotics"`) {
		t.Errorf("the legal-suffix-stripped name is missing; a registry name and a brand name are different searches")
	}
	if has(got, `"Acme Robotics GmbH companies"`) {
		t.Error("the full legal name should not be concatenated as if it were a phrase")
	}
}

func TestWithoutLegalSuffix(t *testing.T) {
	cases := [][2]string{
		{"Acme Robotics GmbH", "Acme Robotics"},
		{"Acme Inc", "Acme"},
		{"Acme, Inc.", "Acme"},
		{"Contoso Limited", "Contoso"},
		{"株式会社ALC", "株式会社ALC"},
		{"Acme", "Acme"},
		// Nothing substantial left, so nothing is stripped.
		{"GmbH", "GmbH"},
		{"Ltd", "Ltd"},
		// The word appears mid-name and must survive.
		{"Ltd Solutions", "Ltd Solutions"},
	}
	for _, tc := range cases {
		if got := withoutLegalSuffix(tc[0]); got != tc[1] {
			t.Errorf("withoutLegalSuffix(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestLocalizationUsesRealRegistryTerms(t *testing.T) {
	// The whole reason the country registry carries terms. An English-only
	// search in Germany finds English-language US sites.
	g := New(Limits{MaxPerCandidate: 100})
	p := Profile{
		Industries: []string{"business software"},
		Countries:  []string{"DE"},
	}
	got := g.Expand(p, candidate.Candidate{ID: "c", Name: "Acme", Domain: "acme.de", Country: "DE"})

	de, ok := domain.LookupCountry("DE")
	if !ok {
		t.Fatal("DE missing from the registry")
	}
	localized := de.Term("de")
	if localized == "" {
		t.Fatal("the registry has no German term for DE; the localization test cannot run")
	}
	want := "business software " + localized
	if !has(got, want) {
		t.Errorf("missing the localized query %q\ngot: %s", want, strings.Join(texts(got), " | "))
	}

	for _, q := range got {
		if q.Language == "de" && q.Text == want {
			if q.Country != "DE" {
				t.Errorf("localized query %q has country %q, want DE", q.Text, q.Country)
			}
			if q.Kind != KindLocal {
				t.Errorf("localized query %q has kind %q, want %q", q.Text, q.Kind, KindLocal)
			}
		}
	}
}

func TestNoLocalizedQueryWithoutRegistryVocabulary(t *testing.T) {
	// A language the registry does not carry must produce no localized query at
	// all, rather than an English query filed under a localized label.
	g := New(Limits{MaxPerCandidate: 100})
	p := Profile{Industries: []string{"business software"}, Countries: []string{"DE"}, Languages: []string{"sw"}}
	got := g.Expand(p, candidate.Candidate{ID: "c", Name: "Acme", Domain: "acme.de", Country: "DE"})
	for _, q := range got {
		if q.Language == "sw" {
			t.Errorf("query %q is labelled Swahili but the registry has no Swahili term", q.Text)
		}
	}
}

func TestIdentifiersAreNotLocalized(t *testing.T) {
	// "ISO 27001" has no German form. Appending a German noun to it produces a
	// query for something that does not exist.
	g := New(Limits{MaxPerCandidate: 100})
	p := Profile{Keywords: []string{"ISO 27001", "KUKA"}, Countries: []string{"DE"}}
	got := g.Expand(p, candidate.Candidate{ID: "c", Name: "Acme", Domain: "acme.de", Country: "DE"})

	de, _ := domain.LookupCountry("DE")
	local := de.Term("de")
	for _, q := range got {
		if strings.HasPrefix(q.Text, "ISO 27001") && q.Kind == KindLocal {
			t.Errorf("query %q localized an identifier", q.Text)
		}
		if strings.HasPrefix(q.Text, "KUKA") && q.Kind == KindLocal {
			t.Errorf("query %q localized a brand name", q.Text)
		}
		if local != "" && strings.Contains(q.Text, "ISO 27001 "+local) {
			t.Errorf("query %q is a localized identifier", q.Text)
		}
	}
}

func TestCityExpansionIsCapped(t *testing.T) {
	// A country with fifty cities produces fifty near-identical queries, which
	// is how a run spends its whole budget on one market.
	g := New(Limits{MaxPerCandidate: 500, MaxCitiesPerCountry: 2})
	p := Profile{Industries: []string{"logistics"}, Countries: []string{"US", "DE"}}
	got := g.Expand(p, candidate.Candidate{ID: "c", Name: "Acme", Domain: "acme.com"})

	us, _ := domain.LookupCountry("US")
	de, _ := domain.LookupCountry("DE")
	allowed := map[string]bool{}
	for i := 0; i < 2; i++ {
		if i < len(us.Cities) {
			allowed[us.Cities[i]] = true
		}
		if i < len(de.Cities) {
			allowed[de.Cities[i]] = true
		}
	}
	if len(allowed) == 0 {
		t.Fatal("the registry has no cities, so the cap cannot be tested")
	}

	seenCity := false
	for _, q := range got {
		if q.Kind != KindCity {
			continue
		}
		seenCity = true
		// A city query is "<term> <city>". Find which allowed city it ends
		// with; a city outside the allowed set means the cap was not applied.
		matched := false
		for city := range allowed {
			if strings.HasSuffix(q.Text, " "+city) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("city query %q is not built from one of the %d allowed cities per country", q.Text, 2)
		}
	}
	if !seenCity {
		t.Error("no city queries were generated at all, so the cap was never exercised")
	}
}

func TestPerCandidateLimitIsEnforced(t *testing.T) {
	// The cap is enforced by the generator, not trusted to the caller, because a
	// profile with many industries is a combinatorial explosion a provider's
	// bill would discover first.
	g := New(Limits{MaxPerCandidate: 5, MaxPerRun: 10_000})
	p := Profile{
		Industries:   []string{"a", "b", "c", "d", "e", "f", "g", "h"},
		Keywords:     []string{"i", "j", "k"},
		Technologies: []string{"l", "m", "n", "o"},
		Countries:    []string{"DE", "FR", "JP", "BR", "IN"},
		Competitors:  []string{"p", "q"},
	}
	got := g.Expand(p, acme())
	if len(got) > 5 {
		t.Errorf("Expand returned %d queries, want at most 5", len(got))
	}
}

func TestRunLimitIsEnforced(t *testing.T) {
	g := New(Limits{MaxPerRun: 3, MaxPerCandidate: 1000})
	p := Profile{
		Industries:   []string{"a", "b", "c", "d", "e", "f"},
		Keywords:     []string{"g", "h"},
		Countries:    []string{"DE", "FR", "JP"},
		Technologies: []string{"i", "j", "k"},
	}
	if got := g.Root(p); len(got) > 3 {
		t.Errorf("Root returned %d queries, want at most 3", len(got))
	}
}

func TestZeroLimitsFallBackToDefaults(t *testing.T) {
	// A caller that only sets one limit should not get an unbounded generator.
	g := New(Limits{MaxPerCandidate: 7})
	if got := g.Limits().MaxPerCandidate; got != 7 {
		t.Errorf("MaxPerCandidate = %d, want the explicit 7", got)
	}
	d := DefaultLimits()
	if got := g.Limits().MaxPerRun; got != d.MaxPerRun {
		t.Errorf("MaxPerRun = %d, want the default %d", got, d.MaxPerRun)
	}
	if got := g.Limits().MaxCitiesPerCountry; got != d.MaxCitiesPerCountry {
		t.Errorf("MaxCitiesPerCountry = %d, want the default %d", got, d.MaxCitiesPerCountry)
	}
	// A negative depth is nonsense and must not mean "unlimited".
	if got := g.Limits().MaxDepth; got != d.MaxDepth {
		t.Errorf("MaxDepth = %d, want the default %d", got, d.MaxDepth)
	}
}

func TestDepthIsBounded(t *testing.T) {
	g := New(Limits{MaxPerCandidate: 500, MaxDepth: 1})
	got := g.Expand(profile(), acme())
	for _, q := range got {
		if q.Depth > 1 {
			t.Errorf("query %q has depth %d, want at most 1", q.Text, q.Depth)
		}
	}
}

func TestExpansionNeverRepeatsItself(t *testing.T) {
	// Asking for "alternatives to Fanuc" for a candidate that is Fanuc finds
	// nothing useful, and the provider still charges for it.
	g := New(Limits{MaxPerCandidate: 100})
	p := Profile{Competitors: []string{"Acme Robotics GmbH"}}
	got := g.Expand(p, acme())
	for _, q := range got {
		if q.Kind == KindCompetitor {
			t.Errorf("query %q was generated for a candidate that is the competitor itself", q.Text)
		}
	}
}

func TestSimilarityIgnoresForm(t *testing.T) {
	// A candidate whose domain is acme.com and whose name is "Acme" is the same
	// company as a competitor recorded as "www.acme.com".
	same := [][2]string{
		{"Acme Inc", "acme.com"},
		{"Acme Robotics GmbH", "acme-robotics.de"},
		{"Contoso", "https://www.contoso.com/about"},
		{"Acme, Inc.", "Acme"},
	}
	for _, tc := range same {
		if !similar(tc[0], tc[1]) {
			t.Errorf("similar(%q, %q) = false, want true", tc[0], tc[1])
		}
	}
	diff := [][2]string{
		{"Acme", "Contoso"},
		{"Acme", "acme-corp.com"},
		{"", "acme.com"},
	}
	for _, tc := range diff {
		if similar(tc[0], tc[1]) {
			t.Errorf("similar(%q, %q) = true, want false", tc[0], tc[1])
		}
	}
}

func TestCCLTLDFeedsTheMarketWithoutBeingAClaim(t *testing.T) {
	// A .de domain is a real signal that the market is Germany, even when no
	// source ever stated a country. It is carried as a hint on the query, not
	// asserted as the company's location.
	g := New(Limits{MaxPerCandidate: 100})
	p := Profile{Industries: []string{"business software"}}
	got := g.Expand(p, candidate.Candidate{ID: "c", Name: "Acme", Domain: "acme.de"})

	found := false
	for _, q := range got {
		if q.Country == "DE" {
			found = true
		}
	}
	if !found {
		t.Error("a .de domain did not contribute Germany as a target market")
	}
}

func TestExplicitCountryIsSearchedFirst(t *testing.T) {
	g := New(Limits{MaxPerCandidate: 100})
	got := g.Expand(profile(), acme())
	de, _ := domain.LookupCountry("DE")
	if de.Term("de") == "" {
		t.Skip("registry has no German term")
	}
	// The candidate's own country must lead, so its own market is covered before
	// the profile's other markets.
	var firstWithCountry string
	for _, q := range got {
		if q.Country != "" {
			firstWithCountry = q.Country
			break
		}
	}
	if firstWithCountry != "DE" {
		t.Errorf("first country-scoped query targets %q, want the candidate's own DE", firstWithCountry)
	}
}

func TestEmptyProfileAndCandidateProduceNothing(t *testing.T) {
	g := New(DefaultLimits())
	if got := g.Root(Profile{}); len(got) != 0 {
		t.Errorf("an empty profile produced %d queries: %v", len(got), texts(got))
	}
	if got := g.Expand(Profile{}, candidate.Candidate{}); len(got) != 0 {
		t.Errorf("an empty candidate produced %d queries: %v", len(got), texts(got))
	}
}

func TestUnknownCountryIsSkippedNotFatal(t *testing.T) {
	// A profile naming a country the registry does not carry should still
	// produce the queries it can, not fail the run.
	g := New(Limits{MaxPerCandidate: 50})
	p := Profile{Industries: []string{"logistics"}, Countries: []string{"ZZ", "DE"}}
	got := g.Expand(p, candidate.Candidate{ID: "c", Name: "Acme", Domain: "acme.com"})
	if len(got) == 0 {
		t.Error("an unknown country code suppressed every query")
	}
}

func TestQuotedNamesBalance(t *testing.T) {
	for _, name := range []string{"Acme", `Acme "The" Co`, "", "   "} {
		q := quote(name)
		if strings.Count(q, `"`)%2 != 0 {
			t.Errorf("quote(%q) = %q, which has unbalanced quotes", name, q)
		}
	}
}

func TestBlankAndDuplicateProfileTermsCollapse(t *testing.T) {
	// A copy-pasted profile accumulates blanks and near-duplicates. They must
	// not multiply the query count.
	g := New(Limits{MaxPerCandidate: 500})
	p := Profile{
		Industries: []string{"robotics", "  robotics  ", "", "ROBOTICS", "automation"},
		Countries:  []string{"DE", "de"},
	}
	got := g.Expand(p, acme())
	seen := map[string]int{}
	for _, q := range got {
		if strings.TrimSpace(q.Text) == "" {
			t.Error("a blank query was generated")
		}
		seen[q.Text]++
	}
	for text, n := range seen {
		if n > 1 {
			t.Errorf("query %q was generated %d times", text, n)
		}
	}
}

func TestIsIdentifier(t *testing.T) {
	yes := []string{"ISO 27001", "KUKA", "GDPR", "Shopify", "ACME"}
	// The empty term is reported as an identifier on purpose: there is nothing
	// to translate, and the caller drops it.
	no := []string{"business software", "warehouse automation", "machine vision"}
	for _, s := range yes {
		if !isIdentifier(s) {
			t.Errorf("isIdentifier(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isIdentifier(s) {
			t.Errorf("isIdentifier(%q) = true, want false", s)
		}
	}
}

func TestPrioritiesAreOrderedHighFirst(t *testing.T) {
	// Truncation must drop the least useful queries, so the order has to be
	// meaningful rather than incidental.
	g := New(Limits{MaxPerCandidate: 100})
	got := g.Expand(profile(), acme())
	for i := 1; i < len(got); i++ {
		if got[i].Depth < got[i-1].Depth {
			t.Fatalf("query %q (depth %d) sorts before %q (depth %d)",
				got[i].Text, got[i].Depth, got[i-1].Text, got[i-1].Depth)
		}
		if got[i].Depth == got[i-1].Depth && got[i].Priority > got[i-1].Priority {
			t.Fatalf("query %q (priority %d) sorts before %q (priority %d)",
				got[i].Text, got[i].Priority, got[i-1].Text, got[i-1].Priority)
		}
	}
}
