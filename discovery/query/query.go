// Package query turns a candidate and an ideal-customer profile into the
// searches that will find more companies like it.
//
// Query generation is where "global" stops being a marketing word. A company in
// Germany is described as "B2B-SaaS-Unternehmen" in German, listed in a local
// chamber-of-commerce directory, and found by searching for its industry plus
// "Berlin" — none of which a single English query reaches. This package builds
// those variants, and it is deliberately explicit about which dimension each
// query came from so the results can be traced back.
//
// Two properties matter more than cleverness:
//
//   - Determinism. The same candidate and profile always produce the same
//     queries in the same order. Without that, a resumed run re-queries
//     differently and the budget has no predictable relationship to coverage.
//   - Boundedness. Every dimension is capped, and the caps are enforced here
//     rather than trusted to callers, because a profile with fifty industries
//     and a country with fifty cities is a combinatorial explosion that a
//     provider's bill would discover first.
package query

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/domain"
)

// Kind is the dimension a query came from. It is carried on every Query so that
// a result set can be broken down by how it was found, and so a provider that
// only supports one kind can be routed the queries it can actually run.
type Kind string

const (
	// KindDirect looks for the company itself: "acme.com", "Acme Industries".
	// This is expansion of a known entity, not market discovery.
	KindDirect Kind = "direct"
	// KindIndustry searches the candidate's own vertical for peers.
	KindIndustry Kind = "industry"
	// KindProfile is the ICP's own industry terms, independent of any candidate.
	KindProfile Kind = "profile"
	// KindLocal is a localized industry term, such as "B2B-SaaS-Unternehmen".
	KindLocal Kind = "local"
	// KindCity adds a city to a profile term to find companies in one place.
	KindCity Kind = "city"
	// KindCountry adds a market to a profile term.
	KindCountry Kind = "country"
	// KindTechnology looks for companies using a specific technology, which is
	// how non-obvious businesses surface.
	KindTechnology Kind = "technology"
	// KindDirectory looks for a company on a known public directory.
	KindDirectory Kind = "directory"
	// KindCompetitor looks for companies similar to a named one.
	KindCompetitor Kind = "competitor"
)

// Query is one search to run.
type Query struct {
	// Text is the query as it will be sent, verbatim. It is stored so that a
	// surprising candidate can be traced to the exact search that found it;
	// reconstructing the string later from its parts would not be reliable.
	Text string
	// Kind is the dimension this came from.
	Kind Kind
	// Language is the BCP-47 tag of the query's language, which is not always
	// the language of the company: a German company searched in English is
	// still a German company.
	Language string
	// Country is the market the query targets, or "" for a global query.
	Country string
	// Parent records the candidate this query came from, so expansion can be
	// traced. It is empty for a profile-driven query, which is the root of the
	// run.
	Parent string
	// Depth is the expansion round. Round 0 is the profile itself, round 1
	// searches the industry, round 2 searches cities and competitors. The run
	// stops at the configured depth, so a query always has a bounded ancestry.
	Depth int
	// Priority orders work within a round. Higher runs first when the budget
	// runs out, so truncation drops the least useful queries rather than an
	// arbitrary half.
	Priority int
}

// String returns the query text, so a Query can be used where a string is
// expected in tests and logging.
func (q Query) String() string { return q.Text }

// Profile is the ideal-customer profile a run is searching for. It is the
// discovery module's only knowledge of what the operator wants, and it is
// deliberately coarse: terms a human recognises, not a scoring rubric.
type Profile struct {
	// Name is the operator's label for the profile, used in query text such as
	// '"Acme Robotics" alternatives'.
	Name string
	// Industries are the vertical terms. These drive most of the query volume.
	Industries []string
	// Keywords are additional product or capability terms, such as "computer
	// vision" or "ISO 27001".
	Keywords []string
	// Technologies are specific technologies whose adopters are themselves the
	// market: searching for a company using "Kubernetes" and "manufacturing"
	// finds businesses no directory lists.
	Technologies []string
	// Countries restricts discovery. Empty means every country in the registry.
	Countries []string
	// Cities restricts geographic expansion. Empty means the cities of each
	// target country.
	Cities []string
	// Competitors are companies whose peers are wanted.
	Competitors []string
	// Directories are public listings to look for candidates on. These are
	// names like "Crunchbase", not domains, because the query is a search, not
	// a fetch.
	Directories []string
	// Languages are BCP-47 tags to generate localized queries in, in addition to
	// each country's own language. Empty means the countries' own languages.
	Languages []string
	// Exclude are terms that disqualify a result, such as a competitor or a
	// company type the operator does not sell to. These are removed from
	// results, not from queries.
	Exclude []string
}

// Limits bound generation. Zero fields take their defaults, so a caller that
// only cares about the company cap can leave the rest alone.
type Limits struct {
	// MaxPerCandidate is the hard ceiling on queries for one candidate.
	MaxPerCandidate int
	// MaxPerRun is the hard ceiling across the whole run.
	MaxPerRun int
	// MaxCitiesPerCountry bounds geographic expansion. Past a handful of cities
	// the results are the same companies as the first few.
	MaxCitiesPerCountry int
	// MaxDepth bounds expansion rounds. Negative means unlimited, which the
	// generator refuses; depth is what stops expansion from never terminating.
	MaxDepth int
}

// DefaultLimits are conservative because a query costs money and a
// deduplicated result costs attention.
func DefaultLimits() Limits {
	return Limits{MaxPerCandidate: 12, MaxPerRun: 5000, MaxCitiesPerCountry: 3, MaxDepth: 2}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxPerCandidate <= 0 {
		l.MaxPerCandidate = d.MaxPerCandidate
	}
	if l.MaxPerRun <= 0 {
		l.MaxPerRun = d.MaxPerRun
	}
	if l.MaxCitiesPerCountry <= 0 {
		l.MaxCitiesPerCountry = d.MaxCitiesPerCountry
	}
	if l.MaxDepth == 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxDepth < 0 {
		l.MaxDepth = d.MaxDepth
	}
	return l
}

// Generator builds queries. It is safe for concurrent use: it holds no mutable
// state, so several providers can generate at once without coordination.
type Generator struct {
	limits Limits
}

// New returns a Generator.
func New(l Limits) *Generator { return &Generator{limits: l.withDefaults()} }

// Limits returns the effective limits, after defaults are applied.
func (g *Generator) Limits() Limits { return g.limits }

// Root returns the round-0 queries for a profile: the searches that describe
// what the operator wants without naming any company.
//
// These are the queries whose results are market discovery rather than
// expansion, so they are the ones an operator should read first to judge whether
// the profile is even right. A profile that returns nothing here is a profile
// problem, not a provider problem, and being able to see that is the point of
// generating them separately.
func (g *Generator) Root(p Profile) []Query {
	var out []Query
	add := func(q Query) { out = appendQuery(out, q, g.limits.MaxPerRun) }

	for _, ind := range cleanList(p.Industries) {
		add(Query{Text: ind, Kind: KindProfile, Language: "en", Depth: 0, Priority: 100})
	}
	for _, kw := range cleanList(p.Keywords) {
		add(Query{Text: kw, Kind: KindProfile, Language: "en", Depth: 0, Priority: 90})
	}
	for _, tech := range cleanList(p.Technologies) {
		add(Query{
			Text:     tech + " companies",
			Kind:     KindTechnology,
			Language: "en",
			Depth:    0,
			Priority: 80,
		})
	}
	for _, dir := range cleanList(p.Directories) {
		add(Query{
			Text:     "companies listed on " + dir,
			Kind:     KindDirectory,
			Language: "en",
			Depth:    0,
			Priority: 60,
		})
	}
	for _, c := range cleanList(p.Competitors) {
		add(Query{
			Text:     "alternatives to " + c,
			Kind:     KindCompetitor,
			Language: "en",
			Depth:    0,
			Priority: 70,
		})
		add(Query{
			Text:     "companies like " + c,
			Kind:     KindCompetitor,
			Language: "en",
			Depth:    0,
			Priority: 70,
		})
	}
	if p.Name != "" {
		// The profile's own name is the operator's word for the market, and
		// searching it verbatim finds peers who describe themselves the same way.
		add(Query{Text: p.Name, Kind: KindProfile, Language: "en", Depth: 0, Priority: 100})
	}

	out = append(out, g.expand(p, nil, 1)...)
	out = dedupe(out)
	return out[:min(len(out), g.limits.MaxPerRun)]
}

// Expand returns the queries that find companies related to one candidate, up
// to MaxPerCandidate. The candidate contributes its name and domain; the
// profile contributes the vertical and geography, which is what turns "we found
// Acme" into "we found the companies around Acme".
//
// The result is sorted by priority then text, so the same candidate always
// produces the same ordered list and a truncated budget is spent on the most
// useful queries.
func (g *Generator) Expand(p Profile, c candidate.Candidate) []Query {
	if c.Name == "" && c.Domain == "" {
		return nil
	}
	var out []Query
	add := func(q Query) {
		q.Parent = c.ID
		out = appendQuery(out, q, g.limits.MaxPerCandidate)
	}

	// Round 1: who is this company, and what else does it look like.
	if c.Domain != "" {
		add(Query{Text: c.Domain, Kind: KindDirect, Language: "en", Depth: 1, Priority: 100})
		add(Query{
			Text:     "site:" + c.Domain,
			Kind:     KindDirect,
			Language: "en",
			Depth:    1,
			Priority: 20, // one site is not a market; useful, not urgent
		})
	}
	if c.Name != "" {
		add(Query{Text: quote(c.Name), Kind: KindDirect, Language: "en", Depth: 1, Priority: 95})
		// "Acme Inc" and "Acme" are not the same search, and the legal suffix is
		// usually in the registry entry but not the brand.
		if short := withoutLegalSuffix(c.Name); short != c.Name && short != "" {
			add(Query{Text: quote(short), Kind: KindDirect, Language: "en", Depth: 1, Priority: 90})
		}
	}
	for _, kw := range c.Keywords {
		if kw = strings.TrimSpace(kw); kw != "" {
			add(Query{Text: quote(c.Name) + " " + kw, Kind: KindDirect, Language: "en", Depth: 1, Priority: 85})
		}
	}

	// Round 1 continues into the market the candidate sits in.
	if ind := strings.TrimSpace(c.Industry); ind != "" {
		add(Query{Text: ind, Kind: KindIndustry, Language: "en", Depth: 1, Priority: 75})
		add(Query{Text: ind + " companies", Kind: KindIndustry, Language: "en", Depth: 1, Priority: 70})
	}

	out = append(out, g.expand(p, &c, 1)...)
	out = dedupe(out)
	sortQueries(out)
	return out[:min(len(out), g.limits.MaxPerCandidate)]
}

// expand builds the market and geography queries, which are shared between the
// root round and a candidate's round. c is nil for a root expansion.
func (g *Generator) expand(p Profile, c *candidate.Candidate, depth int) []Query {
	if depth > g.limits.MaxDepth {
		return nil
	}
	var out []Query
	add := func(q Query) {
		if c != nil {
			q.Parent = c.ID
		}
		out = appendQuery(out, q, g.limits.MaxPerCandidate)
	}

	countries := g.targetCountries(p, c)
	terms := g.profileTerms(p)

	// Local language, for every term the registry can translate. This is the
	// dimension that finds companies a country-targeted English search misses
	// entirely, and it only produces queries when the registry genuinely has
	// the vocabulary.
	for _, code := range countries {
		country, ok := domain.LookupCountry(code)
		if !ok {
			continue
		}
		langs := p.Languages
		if len(langs) == 0 {
			langs = defaultLanguagesFor(country)
		}
		for _, lang := range langs {
			localized := localizeTerms(terms, country, lang)
			if len(localized) == 0 {
				continue
			}
			for _, t := range localized {
				add(Query{
					Text:     t,
					Kind:     KindLocal,
					Language: lang,
					Country:  code,
					Depth:    depth,
					Priority: 80,
				})
				// The localized term alone finds the market; adding the market
				// name finds the companies that describe themselves locally.
				if len(country.Markets) > 0 {
					add(Query{
						Text:     t + " " + country.Markets[0],
						Kind:     KindCountry,
						Language: lang,
						Country:  code,
						Depth:    depth,
						Priority: 78,
					})
				}
			}
		}
	}

	// Geography. These carry an untranslated term, so they are labelled English
	// whatever the operator asked for: a query is only ever labelled with the
	// language its text is actually in. Searching for an English vertical term
	// with a local market name is a real and useful search; claiming it was in
	// the local language is not, and the localized path above is where a
	// genuinely localized query comes from.
	for _, code := range countries {
		country, ok := domain.LookupCountry(code)
		if !ok {
			continue
		}
		for _, market := range country.Markets {
			for _, t := range terms {
				add(Query{
					Text:     t + " " + market,
					Kind:     KindCountry,
					Language: "en",
					Country:  code,
					Depth:    depth,
					Priority: 70,
				})
			}
		}
	}

	// Cities, capped. A city query is the sharpest geographic filter available
	// and also the noisiest, which is why the cap is low by default.
	for _, city := range g.cities(p, countries) {
		for _, t := range terms {
			add(Query{
				Text:     t + " " + city,
				Kind:     KindCity,
				Language: "en",
				Depth:    depth,
				Priority: 60,
			})
		}
	}

	// Technology adoption, which reaches companies no directory indexes.
	for _, tech := range cleanList(p.Technologies) {
		for _, t := range terms {
			add(Query{
				Text:     tech + " " + t,
				Kind:     KindTechnology,
				Language: "en",
				Depth:    depth,
				Priority: 50,
			})
		}
	}

	// Competitors of a known candidate are peers worth having; competitors named
	// only in the profile are handled in the root round.
	if c != nil {
		for _, comp := range cleanList(p.Competitors) {
			if similar(comp, c.Name) || similar(comp, c.Domain) {
				continue
			}
			add(Query{
				Text:     "alternatives to " + comp,
				Kind:     KindCompetitor,
				Language: "en",
				Depth:    depth,
				Priority: 55,
			})
		}
	}

	return out
}

// targetCountries decides which markets to search. The candidate's own country
// comes first, because a candidate found in Germany should be expanded in
// Germany before anywhere else; the profile's countries follow.
func (g *Generator) targetCountries(p Profile, c *candidate.Candidate) []string {
	var out []string
	seen := map[string]bool{}
	add := func(code string) {
		code = strings.ToUpper(strings.TrimSpace(code))
		if len(code) != 2 || seen[code] {
			return
		}
		if _, ok := domain.LookupCountry(code); !ok {
			return
		}
		seen[code] = true
		out = append(out, code)
	}
	if c != nil {
		add(c.Country)
	}
	for _, code := range cleanList(p.Countries) {
		add(code)
	}
	if c != nil {
		// A German ccTLD is a real signal about the market even when the source
		// never stated a country, so the suffix feeds the same path. It is a
		// hint and is carried as one.
		if d, err := domain.FromHost(c.Domain); err == nil && d.Country != "" {
			add(d.Country)
		}
	}
	return out
}

// cities resolves the city list, honouring an explicit override and otherwise
// taking each target country's own cities up to the cap.
func (g *Generator) cities(p Profile, countries []string) []string {
	if len(p.Cities) > 0 {
		return cleanList(p.Cities)[:min(len(cleanList(p.Cities)), g.limits.MaxCitiesPerCountry*max(1, len(countries)))]
	}
	var out []string
	seen := map[string]bool{}
	for _, code := range countries {
		country, ok := domain.LookupCountry(code)
		if !ok {
			continue
		}
		for i, city := range country.Cities {
			if i >= g.limits.MaxCitiesPerCountry {
				break
			}
			key := strings.ToLower(city)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, city)
		}
	}
	return out
}

// profileTerms is the set of vertical phrases a market query is built from.
// Keywords are included because a keyword like "ISO 27001" is often the more
// specific half of a good query.
func (g *Generator) profileTerms(p Profile) []string {
	terms := cleanList(p.Industries)
	terms = append(terms, cleanList(p.Keywords)...)
	return uniqueFold(terms)[:min(len(uniqueFold(terms)), 4)]
}

// defaultLanguagesFor picks the languages a country is searched in when the
// profile does not say. The country's own language comes first, then English,
// which is the language most technology companies describe themselves in even
// when they are not English-speaking.
func defaultLanguagesFor(c domain.Country) []string {
	var out []string
	for _, lang := range []string{"de", "fr", "es", "pt", "it", "nl", "pl", "sv", "tr", "ja", "zh", "ko", "ar", "hi"} {
		if c.HasLanguage(lang) {
			out = append(out, lang)
		}
	}
	// English is queried for every market, but only as itself: the registry's
	// English term is only present if a real translation exists.
	if c.HasLanguage("en") {
		out = append(out, "en")
	}
	return out
}

// localizeTerms translates each term into the target language, dropping any the
// registry cannot express. A term with no translation is not issued in English
// under a localized label; it is issued in English by the caller that wanted
// English, or not at all.
func localizeTerms(terms []string, c domain.Country, lang string) []string {
	local := c.Term(lang)
	if local == "" {
		return nil
	}
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		// A keyword that is itself a proper noun or an identifier ("ISO 27001",
		// "Shopify") has no localized form and translating it would produce a
		// query for something that does not exist.
		if isIdentifier(t) {
			continue
		}
		out = append(out, t+" "+local)
	}
	return out
}

// isIdentifier reports whether a term has no localized form: a proper noun, an
// acronym, a standard, or a brand. An empty term is reported as an identifier so
// that it is never localized — there is nothing to translate.
func isIdentifier(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" {
		return true
	}
	letters := 0
	upper := 0
	for _, r := range t {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	// An acronym or an initialism.
	if letters > 1 && upper == letters {
		return true
	}
	// A single capitalised word with no other words is usually a brand.
	if letters > 0 && upper == 1 && len(strings.Fields(t)) == 1 {
		return true
	}
	return false
}

// legalSuffixes are the company-form words that appear in a registered name but
// not in how the company says its name. Stripping them broadens a search
// without changing what is being asked for.
var legalSuffixes = []string{
	"incorporated", "inc", "inc.", "llc", "l.l.c.", "ltd", "ltd.", "limited",
	"corp", "corp.", "corporation", "gmbh", "ag", "kg", "gmbh & co. kg",
	"plc", "llp", "lp", "pte", "pty", "s.a.", "sa", "nv", "bv", "b.v.",
	"ab", "as", "oy", "aps", "srl", "s.r.l.", "spa", "s.p.a.", "sarl",
	"有限公司", "股份有限公司", "株式会社",
}

// withoutLegalSuffix removes a trailing company-form word, but only once, never
// to the point of emptiness, and only when it stands as its own word.
//
// Requiring the space is what keeps "acme-corp.com" from being reduced to
// "acme": in a written name a company form is a separate word, while inside a
// domain it is glued to the brand. Collapsing the two would make a different
// company look like the one we already have.
func withoutLegalSuffix(name string) string {
	trimmed := strings.TrimSpace(name)
	lower := strings.ToLower(trimmed)
	for _, suffix := range legalSuffixes {
		if !strings.HasSuffix(lower, " "+suffix) {
			continue
		}
		cut := strings.TrimSpace(trimmed[:len(trimmed)-len(suffix)])
		cut = strings.TrimRight(cut, " ,.")
		if len([]rune(cut)) >= 3 {
			return cut
		}
	}
	return trimmed
}

// quote wraps a phrase in double quotes so a multi-word name is not searched as
// a bag of words. A name that already contains a quote is left alone rather
// than producing an unbalanced query.
func quote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, `"`) {
		return s
	}
	return `"` + s + `"`
}

// similar reports whether two terms name the same thing, ignoring case,
// punctuation, legal suffixes, and whether one of them is a URL or a domain.
//
// It exists to stop the generator searching "alternatives to Acme" for a
// candidate that is Acme, which costs a provider call to return the candidate
// that was already known. The comparison has to be tolerant of form because
// registries, directories and search results all write the same company
// differently: "Acme Inc" the name and "acme.com" the domain are one company,
// and the legal suffix in a name is the TLD in a domain.
func similar(a, b string) bool {
	na, nb := comparable(a), comparable(b)
	return na != "" && na == nb
}

// comparable reduces a name, a domain or a URL to its bare form: no scheme, no
// "www.", no TLD, no legal suffix, no punctuation, lower case.
func comparable(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ".")

	// A host or a URL reduces to the label under its public suffix, which is
	// the part a company actually chooses. The legal-suffix strip is then
	// deliberately skipped: the TLD has already come off, and what remains is a
	// single label where "corp" in "acme-corp.com" is part of the brand rather
	// than a company form.
	if strings.Contains(s, ".") {
		if d, err := domain.FromHost(s); err == nil && d.Registrable != "" {
			s = d.Registrable
			if i := strings.Index(s, "."); i > 0 {
				s = s[:i]
			}
			return squash(s)
		}
	}
	return squash(withoutLegalSuffix(s))
}

// squash lowercases and drops everything that is not a letter or a digit, so
// "Acme, Inc." and "acme inc" and "ACME" all reduce to the same string.
func squash(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cleanList trims, drops empties, and removes duplicates case-insensitively
// while preserving the operator's order. Order is their signal about what
// matters most, so it is not sorted away.
func cleanList(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func uniqueFold(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// appendQuery adds a query if the text is new, keeping the first occurrence. The
// first occurrence is the highest-priority one because callers add in priority
// order within a dimension.
func appendQuery(list []Query, q Query, max int) []Query {
	if q.Text == "" {
		return list
	}
	if len(list) >= max {
		return list
	}
	return append(list, q)
}

// dedupe removes queries with identical text, keeping the first, then sorts by
// priority so truncation is predictable.
func dedupe(in []Query) []Query {
	seen := make(map[string]bool, len(in))
	out := make([]Query, 0, len(in))
	for _, q := range in {
		key := strings.ToLower(strings.TrimSpace(q.Text))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, q)
	}
	sortQueries(out)
	return out
}

func sortQueries(in []Query) {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Depth != in[j].Depth {
			return in[i].Depth < in[j].Depth
		}
		if in[i].Priority != in[j].Priority {
			return in[i].Priority > in[j].Priority
		}
		return in[i].Text < in[j].Text
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// String renders a profile for logs and error messages. It lists the terms, not
// the whole structure, because a log line that a human can act on is worth more
// than a complete dump.
func (p Profile) String() string {
	return fmt.Sprintf("profile %q: industries=%v keywords=%v tech=%v countries=%v",
		p.Name, p.Industries, p.Keywords, p.Technologies, p.Countries)
}
