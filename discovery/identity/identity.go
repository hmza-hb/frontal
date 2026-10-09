// Package identity produces the signals that decide whether two candidates are
// the same company, and records the evidence for that decision.
//
// This is not entity resolution. Resolving "Acme Robotics GmbH" with
// "Acme Robotics" and "Acme Robotics Ltd" to one real-world entity across
// countries, languages and years is a research problem, and the architecture
// gives it its own module for a reason. What discovery needs is narrower and
// more urgent: when a provider returns the same company twice, stop persisting
// it twice, and when two candidates are different companies, never collapse
// them.
//
// That asymmetry is the design. Every function here returns a confidence and a
// list of reasons, never a bare boolean, because a merge decision that cannot be
// explained cannot be audited, and a deduplication that silently drops a real
// company is worse than a duplicate. The two failure modes are not symmetric: a
// duplicate costs a little attention, a wrongly merged candidate costs a real
// lead forever.
package identity

import (
	"sort"
	"strings"
	"unicode"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/domain"
)

// Signal is one reason a pair of candidates might be the same company.
type Signal string

const (
	// SignalSameDomain is the strongest signal available: two candidates with
	// the same registrable domain are the same company unless one of them is
	// lying about its name.
	SignalSameDomain Signal = "same_domain"
	// SignalSameHost is a weaker variant: a shared subdomain or host name,
	// which may be a shared hosting or a corporate parent.
	SignalSameHost Signal = "same_host"
	// SignalSameName is an exact match after normalization.
	SignalSameName Signal = "same_name"
	// SignalFuzzyName is a high name similarity, such as a missing legal
	// suffix or a transliteration.
	SignalFuzzyName Signal = "fuzzy_name"
	// SignalSameCountry is supporting, never sufficient: two companies in
	// Germany are not one company.
	SignalSameCountry Signal = "same_country"
	// SignalSameIndustry is supporting, never sufficient.
	SignalSameIndustry Signal = "same_industry"
	// SignalCorroborated means independent sources saw both.
	SignalCorroborated Signal = "corroborated"
	// SignalSharedOwner means a source stated that both belong to the same
	// group, which is the one signal that legitimately links two different
	// registrable domains.
	SignalSharedOwner Signal = "shared_owner"
	// SignalConflictingCountry means the two candidates claim different
	// countries, which blocks a merge unless a domain proves otherwise.
	SignalConflictingCountry Signal = "conflicting_country"
	// SignalConflictingIndustry means the two candidates claim different
	// industries. A company that changed sector is common enough that this only
	// reduces confidence slightly.
	SignalConflictingIndustry Signal = "conflicting_industry"
	// SignalConflictingDomain means both have domains and they differ, which is
	// the main reason not to merge on a name alone.
	SignalConflictingDomain Signal = "conflicting_domain"
)

// Verdict is the outcome of comparing two candidates.
type Verdict int

const (
	// VerdictDistinct means the candidates should stay separate. This is the
	// default, because wrongly merging two companies is the expensive mistake.
	VerdictDistinct Verdict = iota
	// VerdictDuplicate means the candidates are the same company and one should
	// be merged into the other.
	VerdictDuplicate
	// VerdictRelated means they are probably the same company but a human
	// should decide. This verdict exists so a genuinely ambiguous case is
	// recorded rather than forced either way.
	VerdictRelated
)

func (v Verdict) String() string {
	switch v {
	case VerdictDuplicate:
		return "duplicate"
	case VerdictRelated:
		return "related"
	default:
		return "distinct"
	}
}

// Comparison is the full explanation of a verdict.
type Comparison struct {
	Verdict Verdict
	// Confidence is how strongly the signals point one way, in [0,1]. It is the
	// evidence behind the verdict, not a probability that the verdict is right.
	Confidence float64
	// Match lists the signals that support merging.
	Match []Signal
	// Conflict lists the signals that argue against merging.
	Conflict []Signal
	// Explanation is a human-readable summary an operator can read in a log or
	// an API response without interpreting signals.
	Explanation string
}

// Same reports whether the comparison supports collapsing the pair.
func (c Comparison) Same() bool { return c.Verdict == VerdictDuplicate }

// Thresholds are the decision boundaries, exposed so an operator can tune them
// and so a test can pin the behaviour at each boundary.
type Thresholds struct {
	// Merge is the confidence at or above which two candidates are merged. It
	// is high on purpose.
	Merge float64
	// Related is the confidence at or above which they are marked related for
	// review. Below it they are distinct.
	Related float64
}

// DefaultThresholds are deliberately conservative. A duplicate costs a little
// attention downstream; a wrongly merged candidate loses a real company from the
// pipeline with nothing downstream able to notice.
func DefaultThresholds() Thresholds { return Thresholds{Merge: 0.85, Related: 0.6} }

// Comparator decides whether two candidates are the same company.
type Comparator struct {
	thresholds Thresholds
}

// NewComparator returns a Comparator. Zero thresholds take the defaults.
func NewComparator(t Thresholds) *Comparator {
	d := DefaultThresholds()
	if t.Merge <= 0 {
		t.Merge = d.Merge
	}
	if t.Related <= 0 {
		t.Related = d.Related
	}
	return &Comparator{thresholds: t}
}

// Thresholds returns the effective thresholds.
func (c *Comparator) Thresholds() Thresholds { return c.thresholds }

// Compare explains whether two candidates are the same company.
//
// The order of reasoning matters and is deliberate:
//
//  1. A shared registrable domain settles it. A company that lists two
//     different names on one domain is one company with an inconsistency, not
//     two companies.
//  2. Two different registrable domains never merge on a name alone, whatever
//     the name similarity. "Acme Robotics" in Berlin and "Acme Robotics" in
//     Austin are two companies, and this is the most common false merge in lead
//     generation.
//  3. A candidate with no domain can merge on a name, because there is nothing
//     to contradict it. This is the case the expansion stage creates, and
//     refusing it would leave every name-only seed unresolvable.
func (c *Comparator) Compare(a, b candidate.Candidate) Comparison {
	var cmp Comparison

	aDomain := registrable(a.Domain)
	bDomain := registrable(b.Domain)

	switch {
	case aDomain != "" && aDomain == bDomain && isPlatformDomain(aDomain):
		// A shared platform domain is not a shared company. github.com/torvalds
		// and github.com/golang are two unrelated companies that happen to be
		// hosted in the same place, and merging them would lose one of them
		// entirely. The domain is the hosting venue here, not the identity, so
		// the candidates fall through to name and industry signals.
	case aDomain != "" && aDomain == bDomain:
		cmp.Match = append(cmp.Match, SignalSameDomain)
	case aDomain != "" && bDomain != "":
		// Both have domains and they differ. This is decisive on its own.
		cmp.Conflict = append(cmp.Conflict, SignalConflictingDomain)
	}

	// A shared host is a weaker form of the same idea and matters when one side
	// is a subdomain-only record.
	if aDomain == "" || bDomain == "" {
		if _, ok := sharedHost(a, b); ok {
			cmp.Match = append(cmp.Match, SignalSameHost)
		}
	}

	if a.Name != "" && b.Name != "" {
		// An identical set of words is an identical name even when the order
		// differs, so it belongs in the exact tier. Scoring "Industrial Systems
		// Acme" as merely similar to "Acme Industrial Systems" would cap it
		// below the merge threshold and leave two spellings of one name as two
		// candidates.
		switch {
		case normalizeName(a.Name) == normalizeName(b.Name) || sortedTokens(a.Name) == sortedTokens(b.Name):
			cmp.Match = append(cmp.Match, SignalSameName)
		case nameSimilarity(a.Name, b.Name) >= 0.9:
			cmp.Match = append(cmp.Match, SignalFuzzyName)
		}
	}

	if a.Country != "" && b.Country != "" {
		if a.Country == b.Country {
			cmp.Match = append(cmp.Match, SignalSameCountry)
		} else {
			cmp.Conflict = append(cmp.Conflict, SignalConflictingCountry)
		}
	}
	if a.Industry != "" && b.Industry != "" {
		if normalizeName(a.Industry) == normalizeName(b.Industry) {
			cmp.Match = append(cmp.Match, SignalSameIndustry)
		} else {
			cmp.Conflict = append(cmp.Conflict, SignalConflictingIndustry)
		}
	}
	if overlapping(a.SourceCount, b.SourceCount) {
		cmp.Match = append(cmp.Match, SignalCorroborated)
	}
	// A company group is the one legitimate reason two different domains are
	// one candidate, so it is checked last and can outweigh a domain conflict.
	if sharedOwner(a, b) {
		cmp.Match = append(cmp.Match, SignalSharedOwner)
	}

	cmp.Confidence = c.score(cmp)
	cmp.Verdict = c.verdict(cmp)
	cmp.Explanation = explain(cmp, a, b)
	return cmp
}

// score weighs the signals. A domain match dominates everything, a name match
// carries a name-only candidate, and the supporting signals only add confidence
// to a merge that is already plausible.
func (c *Comparator) score(cmp Comparison) float64 {
	has := func(s Signal) bool { return contains(cmp.Match, s) }
	conflicts := len(cmp.Conflict)

	switch {
	case has(SignalSameDomain):
		// One domain is the company. Conflicting countries or industries here
		// are inconsistencies in the source data, not evidence of two
		// companies, so they reduce confidence only slightly.
		score := 0.99
		if has(SignalConflictingCountry) {
			score -= 0.05
		}
		if has(SignalConflictingIndustry) {
			score -= 0.03
		}
		return clamp(score)

	case has(SignalSameHost):
		// Shared hosting is common enough to be a real false-positive source, so
		// this sits below the merge threshold unless corroborated.
		score := 0.7
		if has(SignalSameName) {
			score += 0.15
		}
		if has(SignalCorroborated) {
			score += 0.05
		}
		return clamp(score - penalty(conflicts))

	case has(SignalSharedOwner):
		// A stated group relationship overrides a domain difference, but not
		// silently: a conflicting country still costs confidence.
		score := 0.88
		if has(SignalConflictingCountry) {
			score -= 0.1
		}
		return clamp(score)

	case has(SignalFuzzyName):
		// A fuzzy name is weaker than an exact one even with no conflict at
		// all: near-identical names are how two different firms with similar
		// names end up on one record. It is capped below the merge threshold
		// unless the only conflicts are a country or an industry, which a single
		// company can legitimately disagree with itself about.
		score := 0.55
		if !contains(cmp.Conflict, SignalConflictingDomain) {
			score = 0.7
		}
		if has(SignalSameCountry) {
			score += 0.12
		}
		if has(SignalSameIndustry) {
			score += 0.08
		}
		if has(SignalCorroborated) {
			score += 0.1
		}
		// A name is never on its own an identity, so a fuzzy match stays below
		// the merge threshold and is recorded as related for review.
		return clamp(min(score, 0.84) - penalty(conflicts))

	case has(SignalSameName):
		score := 0.86
		if has(SignalSameCountry) {
			score += 0.05
		}
		if has(SignalSameIndustry) {
			score += 0.03
		}
		if has(SignalCorroborated) {
			score += 0.03
		}
		if contains(cmp.Conflict, SignalConflictingDomain) {
			// The cap exists for exactly this case: two companies that state
			// different domains are different companies however well the names
			// line up, so a name never clears the threshold here.
			score = min(score, 0.84)
		}
		return clamp(score - penalty(conflicts))

	default:
		return 0
	}
}

func (c *Comparator) verdict(cmp Comparison) Verdict {
	switch {
	case cmp.Confidence >= c.thresholds.Merge:
		return VerdictDuplicate
	case cmp.Confidence >= c.thresholds.Related:
		return VerdictRelated
	default:
		return VerdictDistinct
	}
}

// penalty reduces confidence for each conflicting signal, so a pair with a
// domain conflict and a country conflict is clearly weaker than one with either.
func penalty(conflicts int) float64 { return 0.08 * float64(conflicts) }

func clamp(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func contains(list []Signal, s Signal) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// overlapping reports whether two candidates were seen by at least one common
// source, which is weak evidence that they are the same company rather than
// two companies that happen to appear in the same directory.
func overlapping(a, b int) bool { return a > 0 && b > 0 }

// sharedOwner reports whether a source stated that both candidates belong to the
// same group. It is a deliberate, recorded assertion rather than an inference.
func sharedOwner(a, b candidate.Candidate) bool {
	owners := map[string]struct{}{}
	for _, c := range []candidate.Candidate{a, b} {
		for _, e := range c.Evidence {
			detail := strings.ToLower(e.Detail)
			if strings.Contains(detail, "parent=") || strings.Contains(detail, "group=") {
				owners[detail] = struct{}{}
			}
		}
	}
	return len(owners) == 1
}

// sharedHost reports whether two candidates sit under the same registrable
// domain when at least one of them has only a host.
func sharedHost(a, b candidate.Candidate) (string, bool) {
	ha, hb := hostOf(a), hostOf(b)
	if ha == "" || hb == "" {
		return "", false
	}
	da, err1 := domain.FromHost(ha)
	db, err2 := domain.FromHost(hb)
	if err1 != nil || err2 != nil {
		return "", false
	}
	if da.Registrable == "" || db.Registrable == "" {
		return "", false
	}
	if da.Registrable == db.Registrable {
		return da.Registrable, true
	}
	return "", false
}

// hostOf returns the best host available for a candidate, preferring an explicit
// domain over one implied by a URL.
func hostOf(c candidate.Candidate) string {
	if c.Domain != "" {
		return c.Domain
	}
	if c.URL != "" {
		return domain.Normalize(c.URL)
	}
	return ""
}

// isPlatformDomain reports whether a domain is a user-content surface, where the
// domain names the venue rather than the company.
func isPlatformDomain(registrable string) bool {
	_, ok := domain.IsPlatformDomain(registrable)
	return ok
}

// registrable reduces a domain to the eTLD+1, which is the identity. Comparing
// normalized hosts instead would make "sub.example.com" and "other.example.com"
// look like two different companies and record a false conflict.
func registrable(d string) string {
	if strings.TrimSpace(d) == "" {
		return ""
	}
	parsed, err := domain.FromHost(d)
	if err != nil {
		return ""
	}
	return parsed.Registrable
}

// legalFormWords are company-form words removed before comparing names. They are
// matched as whole words only, so "Acme Corp Solutions" and "Acme Corporation
// Solutions" are the same name while "acme-corp.com" is untouched.
var legalFormWords = map[string]bool{
	"inc": true, "incorporated": true, "llc": true, "l l c": true,
	"ltd": true, "limited": true, "corp": true, "corporation": true,
	"co": true, "company": true, "gmbh": true, "ag": true, "kg": true,
	"plc": true, "llp": true, "lp": true, "pte": true, "pty": true,
	"sa": true, "nv": true, "bv": true, "ab": true, "as": true, "oy": true,
	"aps": true, "srl": true, "spa": true, "sarl": true, "the": true,
}

// normalizeName reduces a company name to a comparable form: lower case, no
// punctuation, no company-form words, no spaces.
func normalizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if unicode.IsSpace(r) {
			b.WriteRune(' ')
		}
	}
	fields := strings.Fields(b.String())
	kept := fields[:0]
	for _, f := range fields {
		if !legalFormWords[f] {
			kept = append(kept, f)
		}
	}
	return strings.Join(kept, " ")
}

// nameSimilarity scores two company names in [0,1].
//
// It is the Dice coefficient over character bigrams of the sorted token form,
// which gets the three properties a company-name comparison needs:
//
//   - Order-insensitive. "Acme Industrial Systems" and "Industrial Systems
//     Acme" are the same name, and a plain edit distance scores them as
//     completely different.
//   - Typo-tolerant. "Acme Robotics" and "Acme Robotic" differ by one
//     character and stay above the fuzzy threshold.
//   - Containment-resistant. "Acme" is a prefix of "Acme Industrial Systems",
//     but a bigram coefficient scores the pair low because the longer name has
//     many bigrams the shorter one does not. A token-overlap score would call
//     that a perfect match, which is how a short name absorbs every longer name
//     that starts with the same word.
//
// Company-form words are dropped first, so "Acme GmbH" and "Acme AG" are
// compared as "acme" and "acme".
func nameSimilarity(a, b string) float64 {
	na, nb := sortedTokens(a), sortedTokens(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1
	}
	ba, bb := bigrams(na), bigrams(nb)
	if len(ba) == 0 || len(bb) == 0 {
		return 0
	}
	shared := 0
	for g := range ba {
		if bb[g] > 0 {
			shared += minInt(ba[g], bb[g])
		}
	}
	return 2 * float64(shared) / float64(len(ba)+len(bb))
}

// sortedTokens reduces a name to lower-case tokens with company forms removed,
// sorted and joined, so that word order and punctuation cannot affect the score.
func sortedTokens(name string) string {
	fields := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	kept := fields[:0]
	for _, f := range fields {
		if !legalFormWords[f] {
			kept = append(kept, f)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	sort.Strings(kept)
	return strings.Join(kept, " ")
}

func bigrams(s string) map[string]int {
	out := map[string]int{}
	r := []rune(s)
	for i := 0; i+1 < len(r); i++ {
		out[string(r[i:i+2])]++
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// explain renders a verdict for a human. It is built from the signals rather
// than from the score, because "confidence 0.83" does not tell an operator
// whether to trust the decision.
func explain(cmp Comparison, a, b candidate.Candidate) string {
	names := []string{trimOr(a.Name, a.Domain), trimOr(b.Name, b.Domain)}
	if contains(cmp.Match, SignalSameDomain) {
		return "same registrable domain " + registrable(a.Domain)
	}
	if contains(cmp.Match, SignalSharedOwner) {
		return names[0] + " and " + names[1] + " share a stated group owner"
	}
	if contains(cmp.Match, SignalSameHost) {
		return names[0] + " and " + names[1] + " share a host under one registrable domain"
	}

	parts := []string{}
	switch {
	case contains(cmp.Match, SignalSameName):
		parts = append(parts, "identical names")
	case contains(cmp.Match, SignalFuzzyName):
		parts = append(parts, "near-identical names")
	}
	if len(parts) == 0 {
		return names[0] + " and " + names[1] + " share no strong signal"
	}
	if contains(cmp.Match, SignalSameCountry) {
		parts = append(parts, "same country")
	}
	if contains(cmp.Match, SignalSameIndustry) {
		parts = append(parts, "same industry")
	}
	if contains(cmp.Match, SignalCorroborated) {
		parts = append(parts, "seen by a common source")
	}
	if contains(cmp.Conflict, SignalConflictingDomain) {
		parts = append(parts, "but different domains "+
			registrable(a.Domain)+" and "+registrable(b.Domain))
	}
	if contains(cmp.Conflict, SignalConflictingCountry) {
		parts = append(parts, "but different countries "+a.Country+" and "+b.Country)
	}
	return strings.Join(parts, ", ")
}

func trimOr(name, fallback string) string {
	if s := strings.TrimSpace(name); s != "" {
		return s
	}
	if s := strings.TrimSpace(fallback); s != "" {
		return s
	}
	return "unnamed"
}

// Cluster groups candidates that are the same company, keeping the first
// occurrence as the representative and returning the rest as its members.
//
// Grouping rather than pairwise comparison is what makes this usable at volume:
// a provider returning the same company from five sources is one group, and
// comparing every pair is quadratic in exactly the case that matters most. The
// representative is the first in the input order, which keeps the merge
// deterministic and lets a caller put its most trusted candidates first.
func (c *Comparator) Cluster(candidates []candidate.Candidate) []Group {
	var groups []Group
	for _, cand := range candidates {
		placed := false
		for i := range groups {
			// Comparing against the representative is enough because a group
			// is formed transitively through the representative, and a
			// transitive closure through one representative is what a
			// single-linkage cluster is.
			if c.Compare(groups[i].Representative, cand).Same() {
				groups[i].Members = append(groups[i].Members, cand)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, Group{Representative: cand})
		}
	}
	return groups
}

// Group is a set of candidates judged to be one company.
type Group struct {
	Representative candidate.Candidate
	Members        []candidate.Candidate
}

// Size is the number of candidates in the group including the representative.
func (g Group) Size() int { return len(g.Members) + 1 }

// Merge folds a group into one candidate using the per-field merge rules in the
// candidate package, so merging here and merging in the store produce the same
// record.
func Merge(g Group) candidate.Candidate {
	out := g.Representative
	for _, m := range g.Members {
		out.Merge(m)
	}
	return out
}

// SortedSignals returns the match and conflict signals in a stable order, for
// deterministic API responses and log lines.
func (c Comparison) SortedSignals() (match, conflict []Signal) {
	match = append(match, c.Match...)
	conflict = append(conflict, c.Conflict...)
	sort.Slice(match, func(i, j int) bool { return match[i] < match[j] })
	sort.Slice(conflict, func(i, j int) bool { return conflict[i] < conflict[j] })
	return match, conflict
}
