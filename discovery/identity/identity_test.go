package identity

import (
	"strings"
	"testing"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
)

func ev(source candidate.Source, method candidate.Method, detail string) candidate.Evidence {
	return candidate.Evidence{Source: source, Method: method, Detail: detail}
}

func TestSameDomainAlwaysMerges(t *testing.T) {
	// One registrable domain is the company, whatever the names say. A company
	// that lists two names is one company with an inconsistency.
	c := NewComparator(Thresholds{})
	cmp := c.Compare(
		candidate.Candidate{Name: "Acme Robotics", Domain: "acme.com", Country: "US"},
		candidate.Candidate{Name: "Acme Robotics GmbH", Domain: "www.acme.com", Country: "DE", Industry: "aerospace"},
	)
	if !cmp.Same() {
		t.Fatalf("same domain did not merge: %+v", cmp)
	}
	if cmp.Confidence < 0.9 {
		t.Errorf("Confidence = %v, want a near-certain merge", cmp.Confidence)
	}
	if !strings.Contains(cmp.Explanation, "acme.com") {
		t.Errorf("Explanation %q should name the shared domain", cmp.Explanation)
	}
}

func TestDifferentDomainsNeverMergeOnNameAlone(t *testing.T) {
	// The most expensive false merge in lead generation. Two companies can
	// legitimately have the same name in different markets.
	c := NewComparator(Thresholds{})
	cases := []struct {
		why  string
		name string
		a, b string
	}{
		{"same name, same industry, same country", "Acme Robotics", "acme-berlin.de", "acme-austin.com"},
		{"identical names", "Acme", "acme-berlin.de", "acme-austin.com"},
		{"fuzzy names with corroboration", "Acme Robotics GmbH", "acme-berlin.de", "acme-robotics-austin.com"},
	}
	for _, tc := range cases {
		cmp := c.Compare(
			candidate.Candidate{
				Name: tc.name, Domain: tc.a, Industry: "robotics",
				Country: "DE", SourceCount: 3,
			},
			candidate.Candidate{
				Name: tc.name, Domain: tc.b, Industry: "robotics",
				Country: "DE", SourceCount: 3,
			},
		)
		if cmp.Same() {
			t.Errorf("%s: two different domains merged on a name alone (%v)\n%v",
				tc.name, cmp.Confidence, cmp.Explanation)
		}
		if !contains(cmp.Conflict, SignalConflictingDomain) {
			t.Errorf("%s: the domain conflict was not recorded: %+v", tc.name, cmp)
		}
	}
}

func TestNameOnlyCandidateResolvesAgainstADomainCandidate(t *testing.T) {
	// This is the case the expansion stage creates, and refusing it would leave
	// every name-only seed permanently unresolved.
	c := NewComparator(Thresholds{})
	cmp := c.Compare(
		candidate.Candidate{Name: "Acme Robotics", Domain: "acme.com", Country: "US", Industry: "robotics", SourceCount: 2},
		candidate.Candidate{Name: "Acme Robotics", Country: "US", Industry: "robotics", SourceCount: 2},
	)
	if !cmp.Same() {
		t.Errorf("a name-only record should resolve against its domain record: %v\n%v",
			cmp.Confidence, cmp.Explanation)
	}
}

func TestLegalFormWordsDoNotBlockAMerge(t *testing.T) {
	c := NewComparator(Thresholds{})
	pairs := [][2]string{
		{"Acme Inc", "Acme Incorporated"},
		{"Acme GmbH", "Acme AG"},
		{"Contoso Limited", "Contoso Ltd"},
		{"Acme, Inc.", "Acme"},
		{"The Acme Company", "Acme"},
	}
	for _, p := range pairs {
		cmp := c.Compare(
			candidate.Candidate{Name: p[0]},
			candidate.Candidate{Name: p[1]},
		)
		if !cmp.Same() {
			t.Errorf("%q and %q should be the same name (confidence %v): %v",
				p[0], p[1], cmp.Confidence, cmp.Explanation)
		}
	}
}

func TestWordOrderDoesNotMatterButExtraWordsDo(t *testing.T) {
	c := NewComparator(Thresholds{})
	same := c.Compare(
		candidate.Candidate{Name: "Acme Industrial Systems"},
		candidate.Candidate{Name: "Industrial Systems Acme"},
	)
	if !same.Same() {
		t.Errorf("reordered tokens should be the same name: %v", same.Explanation)
	}

	diff := c.Compare(
		candidate.Candidate{Name: "Acme"},
		candidate.Candidate{Name: "Acme Industrial Systems Holdings International"},
	)
	if diff.Same() {
		t.Errorf("a short name must not absorb a long unrelated one: %v", diff.Explanation)
	}
}

func TestSharedOwnerLinksDifferentDomains(t *testing.T) {
	// The one legitimate reason two different registrable domains are one
	// candidate: a stated group relationship.
	c := NewComparator(Thresholds{})
	// A registry record that states a group owner is the one legitimate reason
	// two different registrable domains are one candidate.
	grouped := candidate.Candidate{
		Name: "Acme Cloud", Domain: "acmecloud.com", SourceCount: 1,
		Evidence: []candidate.Evidence{ev(candidate.SourceRegistry, candidate.MethodAPIRecord, "group=acme-group")},
	}
	other := candidate.Candidate{
		Name: "Acme Analytics", Domain: "acmeanalytics.io", SourceCount: 1,
		Evidence: []candidate.Evidence{ev(candidate.SourceRegistry, candidate.MethodAPIRecord, "group=acme-group")},
	}
	cmp := c.Compare(grouped, other)
	if !contains(cmp.Match, SignalSharedOwner) {
		t.Fatalf("a stated group owner was not detected: %+v", cmp)
	}
	if !cmp.Same() {
		t.Errorf("a stated group owner should link two domains: %v\n%v", cmp.Confidence, cmp.Explanation)
	}

	// Without the shared statement, two domains and similar names must stay
	// apart even though both come from the same registry.
	noGroup := c.Compare(
		candidate.Candidate{
			Name: "Acme Cloud", Domain: "acmecloud.com", SourceCount: 1,
			Evidence: []candidate.Evidence{ev(candidate.SourceRegistry, candidate.MethodAPIRecord, "")},
		},
		candidate.Candidate{
			Name: "Acme Analytics", Domain: "acmeanalytics.io", SourceCount: 1,
			Evidence: []candidate.Evidence{ev(candidate.SourceRegistry, candidate.MethodAPIRecord, "")},
		},
	)
	if noGroup.Same() {
		t.Errorf("two domains merged without a stated relationship: %v", noGroup.Explanation)
	}
}

func TestSharedHostIsWeakerThanASharedDomain(t *testing.T) {
	// A corporate parent often runs many domains, and a shared host is common on
	// shared hosting, so neither should merge as readily as one domain.
	c := NewComparator(Thresholds{})
	sameHost := c.Compare(
		candidate.Candidate{Name: "Acme", URL: "https://shop.acme.com/x"},
		candidate.Candidate{Name: "Acme", URL: "https://blog.acme.com/y"},
	)
	if !sameHost.Same() {
		t.Errorf("two URLs on one registrable domain should merge: %v\n%v",
			sameHost.Confidence, sameHost.Explanation)
	}
	if sameHost.Confidence >= c.Thresholds().Merge+0.0001 {
		t.Errorf("Confidence = %v should sit below a plain domain match", sameHost.Confidence)
	}
}

func TestConflictingCountryBlocksANameMerge(t *testing.T) {
	c := NewComparator(Thresholds{})
	cmp := c.Compare(
		candidate.Candidate{Name: "Acme", Country: "US", Industry: "robotics"},
		candidate.Candidate{Name: "Acme", Country: "JP", Industry: "robotics"},
	)
	if cmp.Same() {
		t.Error("two same-named companies in different countries must not merge")
	}
	if !contains(cmp.Conflict, SignalConflictingCountry) {
		t.Errorf("the country conflict was not recorded: %+v", cmp)
	}
}

func TestVerdictsAreOrderedAndExplainable(t *testing.T) {
	c := NewComparator(Thresholds{})
	// Distinct.
	distinct := c.Compare(
		candidate.Candidate{Name: "Acme", Domain: "acme.de", Country: "DE"},
		candidate.Candidate{Name: "Contoso", Domain: "contoso.com", Country: "US"},
	)
	if distinct.Verdict != VerdictDistinct {
		t.Errorf("Verdict = %v, want distinct", distinct.Verdict)
	}
	if distinct.Explanation == "" {
		t.Error("a distinct verdict must still be explainable")
	}
	// Related: same name, no domain on one side, different countries.
	related := c.Compare(
		candidate.Candidate{Name: "Acme", Country: "US"},
		candidate.Candidate{Name: "Acme", Country: "JP"},
	)
	if related.Verdict == VerdictDuplicate {
		t.Error("a name match across two countries must not be a duplicate")
	}
	if related.Explanation == "" {
		t.Error("a related verdict must be explainable")
	}
}

func TestEveryComparisonProducesAnExplanation(t *testing.T) {
	c := NewComparator(Thresholds{})
	pairs := [][2]candidate.Candidate{
		{{Name: "Acme", Domain: "acme.com"}, {Name: "Acme", Domain: "acme.com"}},
		{{Name: "Acme", Domain: "acme.com"}, {Name: "Other", Domain: "other.com"}},
		{{Name: "Acme"}, {Name: "Acme Inc"}},
		{{}, {Name: "Acme"}},
		{{Name: "Acme", URL: "https://acme.com"}, {Name: "Acme", URL: "https://sub.acme.com"}},
	}
	for _, p := range pairs {
		cmp := c.Compare(p[0], p[1])
		if strings.TrimSpace(cmp.Explanation) == "" {
			t.Errorf("comparison %+v vs %+v produced no explanation", p[0], p[1])
		}
		if cmp.Confidence < 0 || cmp.Confidence > 1 {
			t.Errorf("confidence %v is out of range", cmp.Confidence)
		}
	}
}

func TestEmptyCandidatesDoNotPanic(t *testing.T) {
	c := NewComparator(Thresholds{})
	for _, p := range [][2]candidate.Candidate{
		{{}, {}},
		{{Name: "Acme"}, {}},
		{{Domain: "acme.com"}, {Name: "Acme"}},
	} {
		cmp := c.Compare(p[0], p[1])
		if cmp.Verdict == VerdictDuplicate {
			t.Errorf("an empty candidate merged with %+v: %v", p[1], cmp.Explanation)
		}
	}
}

func TestThresholdsAreTunable(t *testing.T) {
	strict := NewComparator(Thresholds{Merge: 0.99, Related: 0.95})
	loose := NewComparator(Thresholds{Merge: 0.5, Related: 0.4})
	a := candidate.Candidate{Name: "Acme", Country: "US", Industry: "robotics"}
	b := candidate.Candidate{Name: "Acme", Country: "US", Industry: "robotics"}

	if strict.Compare(a, b).Same() {
		t.Error("a strict comparator should not merge on a name alone")
	}
	if !loose.Compare(a, b).Same() {
		t.Error("a loose comparator should merge on a name alone")
	}
	if got := NewComparator(Thresholds{}).Thresholds(); got != DefaultThresholds() {
		t.Errorf("zero thresholds did not fall back to the defaults: %+v", got)
	}
}

func TestClusterGroupsDuplicatesAndKeepsDistinctSeparate(t *testing.T) {
	c := NewComparator(Thresholds{})
	in := []candidate.Candidate{
		{Name: "Acme Robotics", Domain: "acme.com", Country: "US", SourceCount: 1},
		{Name: "Acme Robotics GmbH", Domain: "www.acme.com", Country: "DE", SourceCount: 1},
		{Name: "Contoso", Domain: "contoso.com", Country: "US", SourceCount: 1},
		{Name: "Contoso Ltd", Domain: "contoso.co.uk", Country: "GB", SourceCount: 1},
		{Name: "Fabrikam", Domain: "fabrikam.de", Country: "DE", SourceCount: 1},
	}
	// Four distinct companies: Acme (seen twice), contoso.com, contoso.co.uk,
	// and Fabrikam. Contoso's two domains are deliberately different companies.
	groups := c.Cluster(in)
	if len(groups) != 4 {
		t.Fatalf("got %d groups, want 4: %+v", len(groups), groups)
	}
	sizes := map[string]int{}
	for _, g := range groups {
		sizes[g.Representative.Domain] = g.Size()
	}
	if sizes["acme.com"] != 2 {
		t.Errorf("acme group size = %d, want 2", sizes["acme.com"])
	}
	if sizes["fabrikam.de"] != 1 {
		t.Errorf("fabrikam group size = %d, want 1", sizes["fabrikam.de"])
	}
	if sizes["contoso.com"] != 1 {
		t.Errorf("contoso.com and contoso.co.uk must stay separate; they are different companies")
	}
}

func TestClusterIsDeterministic(t *testing.T) {
	c := NewComparator(Thresholds{})
	in := []candidate.Candidate{
		{Name: "Acme", Domain: "acme.com"},
		{Name: "Acme GmbH", Domain: "acme.com"},
		{Name: "Contoso", Domain: "contoso.com"},
	}
	first := c.Cluster(in)
	for i := 0; i < 3; i++ {
		again := c.Cluster(in)
		if len(again) != len(first) {
			t.Fatalf("clustering is not deterministic: %d vs %d groups", len(again), len(first))
		}
		for j := range first {
			if first[j].Representative.Domain != again[j].Representative.Domain {
				t.Fatalf("group %d representative changed between runs", j)
			}
		}
	}
}

func TestMergeProducesOneRecord(t *testing.T) {
	group := Group{
		Representative: candidate.Candidate{
			Name: "Acme Robotics GmbH", Domain: "acme.com", Country: "DE",
			Evidence: []candidate.Evidence{ev(candidate.SourceSeed, candidate.MethodSeedImport, "")},
		},
		Members: []candidate.Candidate{
			{
				Name: "Acme", Industry: "robotics", SourceCount: 2,
				Evidence: []candidate.Evidence{ev(candidate.SourceDirectory, candidate.MethodListPage, "")},
			},
			{
				URL: "https://acme.com/about", SourceCount: 1,
				Evidence: []candidate.Evidence{ev(candidate.SourceDirectory, candidate.MethodAPIRecord, "")},
			},
		},
	}
	merged := Merge(group)
	if merged.Domain != "acme.com" {
		t.Errorf("Domain = %q, want the representative's domain kept", merged.Domain)
	}
	if merged.Industry != "robotics" {
		t.Errorf("Industry = %q, want the member's industry merged in", merged.Industry)
	}
	if merged.URL != "https://acme.com/about" {
		t.Errorf("URL = %q, want the member's URL merged in", merged.URL)
	}
	if merged.SourceCount < 2 {
		t.Errorf("SourceCount = %d, want corroboration from both sources", merged.SourceCount)
	}
	if len(merged.Evidence) != 3 {
		t.Errorf("Evidence has %d rows, want all three observations", len(merged.Evidence))
	}
}

func TestNormalizeNameDropsCompanyForms(t *testing.T) {
	cases := [][2]string{
		{"Acme, Inc.", "acme"},
		{"Acme GmbH", "acme"},
		{"The Acme Company", "acme"},
		{"Acme Robotics", "acme robotics"},
	}
	for _, tc := range cases {
		if got := normalizeName(tc[0]); got != tc[1] {
			t.Errorf("normalizeName(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestNameSimilarity(t *testing.T) {
	high := [][2]string{
		{"Acme Robotics", "Acme Robotics"},
		{"Acme Robotics", "Acme Robotic"},
		{"Acme Robotics", "acme robotics gmbh"},
		{"Industrial Systems Acme", "Acme Industrial Systems"},
	}
	for _, p := range high {
		if got := nameSimilarity(p[0], p[1]); got < 0.9 {
			t.Errorf("nameSimilarity(%q, %q) = %v, want at least 0.9", p[0], p[1], got)
		}
	}
	low := [][2]string{
		{"Acme", "Contoso"},
		{"Acme", "Fabrikam"},
		{"Acme Robotics", "Global Logistics Holdings"},
		{"", "Acme"},
		{"Acme", ""},
	}
	for _, p := range low {
		if got := nameSimilarity(p[0], p[1]); got >= 0.9 {
			t.Errorf("nameSimilarity(%q, %q) = %v, want below 0.9", p[0], p[1], got)
		}
	}
}

func TestSortedSignalsAreStable(t *testing.T) {
	c := NewComparator(Thresholds{})
	cmp := c.Compare(
		candidate.Candidate{Name: "Acme", Domain: "acme.com", Country: "US", Industry: "a"},
		candidate.Candidate{Name: "Other", Domain: "acme.com", Country: "US", Industry: "b"},
	)
	m1, c1 := cmp.SortedSignals()
	m2, c2 := cmp.SortedSignals()
	if len(m1) != len(m2) || len(c1) != len(c2) {
		t.Fatal("SortedSignals is not stable")
	}
	for i := range m1 {
		if m1[i] != m2[i] {
			t.Fatal("SortedSignals ordering changed between calls")
		}
	}
	for i := 1; i < len(m1); i++ {
		if m1[i] < m1[i-1] {
			t.Error("match signals are not sorted")
		}
	}
	for i := 1; i < len(c1); i++ {
		if c1[i] < c1[i-1] {
			t.Error("conflict signals are not sorted")
		}
	}
}

func TestVerdictString(t *testing.T) {
	for v, want := range map[Verdict]string{
		VerdictDistinct:  "distinct",
		VerdictDuplicate: "duplicate",
		VerdictRelated:   "related",
	} {
		if got := v.String(); got != want {
			t.Errorf("Verdict(%d).String() = %q, want %q", v, got, want)
		}
	}
}

func TestSharedPlatformDomainIsNotACompany(t *testing.T) {
	// A platform domain names the venue, not the company. github.com/torvalds
	// and github.com/golang are two unrelated projects that happen to be hosted
	// in the same place, and merging them would delete one from the results.
	c := NewComparator(Thresholds{})
	cases := []struct{ a, b string }{
		{"github.com/torvalds/linux", "github.com/golang/go"},
		{"linkedin.com/in/ada", "linkedin.com/in/grace"},
		{"medium.com/@acme", "medium.com/@globex"},
	}
	for _, tc := range cases {
		cmp := c.Compare(
			candidate.Candidate{Name: "Linux Kernel", Domain: tc.a, Industry: "developer tools"},
			candidate.Candidate{Name: "The Go Programming Language", Domain: tc.b, Industry: "developer tools"},
		)
		if cmp.Same() {
			t.Errorf("%s and %s merged on a shared platform domain (%v)\n%v",
				tc.a, tc.b, cmp.Confidence, cmp.Explanation)
		}
	}

	// The same reasoning must not weaken identity for a real company domain:
	// a genuine shared domain is still the strongest signal available.
	real := c.Compare(
		candidate.Candidate{Name: "Acme Robotics", Domain: "acme.com"},
		candidate.Candidate{Name: "Acme Robotics GmbH", Domain: "shop.acme.com"},
	)
	if !real.Same() {
		t.Errorf("a real shared domain must still merge: %v\n%v", real.Confidence, real.Explanation)
	}
}

func TestPlatformDomainFallsThroughToNameSignals(t *testing.T) {
	// On a platform domain the domain tells us nothing, so the name has to carry
	// the decision. The same project named identically on one platform is the
	// same project.
	c := NewComparator(Thresholds{})
	cmp := c.Compare(
		candidate.Candidate{Name: "Acme Robotics", Domain: "github.com/acme/robotics", Industry: "robotics"},
		candidate.Candidate{Name: "Acme Robotics", Domain: "github.com/acme-robotics", Industry: "robotics"},
	)
	if !cmp.Same() {
		t.Errorf("identical names on a platform domain should still merge: %v\n%v", cmp.Confidence, cmp.Explanation)
	}
}
