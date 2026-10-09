// Package ranking decides which discovered candidates are worth a crawler's
// time, and explains every decision.
//
// The rule this package exists to enforce: a score is never a number without a
// reason. Every factor that contributes is returned as a Factor with its own
// weight and contribution, so an operator who sees a company rejected can read
// which signal was missing rather than guessing at a threshold. A ranking stage
// that emits a bare float is a ranking stage nobody can tune, and a threshold
// nobody understands is one nobody dares to change.
//
// The scoring itself is intentionally simple and additive. A learned model
// would score marginally better and be far worse to operate: it would need
// retraining, its decisions would not be explainable, and a bad prediction
// would be invisible. Discovery's job is to propose a few thousand plausible
// companies out of millions of mentions, and a transparent weighted sum with
// recorded contributions is the right tool for that.
package ranking

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/domain"
	"github.com/hmza-hb/lead-intelligence/discovery/identity"
)

// Reason explains why a candidate scored the way it did.
type Reason string

const (
	// ReasonStrongSource means the source is a high-quality one: an operator's
	// seed list, a certificate log, an official registry.
	ReasonStrongSource Reason = "strong_source"
	// ReasonCorroborated means more than one independent surface saw it.
	ReasonCorroborated Reason = "corroborated"
	// ReasonHasDomain means it has a website, which is what makes it reachable.
	ReasonHasDomain Reason = "has_domain"
	// ReasonNoDomain means the candidate is a name with nowhere to go yet.
	ReasonNoDomain Reason = "no_domain"
	// ReasonPlatformDomain means the domain is a user-content surface, so the
	// company found there is someone else's.
	ReasonPlatformDomain Reason = "platform_domain"
	// ReasonMatchesIndustry means the candidate sits in a target vertical.
	ReasonMatchesIndustry Reason = "matches_industry"
	// ReasonIndustryUnknown means no source stated an industry.
	ReasonIndustryUnknown Reason = "industry_unknown"
	// ReasonInTargetCountry means it is in a market being searched.
	ReasonInTargetCountry Reason = "in_target_country"
	// ReasonCountryMismatch means it is in a market nobody asked for.
	ReasonCountryMismatch Reason = "country_mismatch"
	// ReasonCountryUnknown means no source stated a country.
	ReasonCountryUnknown Reason = "country_unknown"
	// ReasonIDNDomain means an internationalised domain, which is a signal of a
	// company operating outside its home market.
	ReasonIDNDomain Reason = "idn_domain"
	// ReasonRecent means it was seen recently.
	ReasonRecent Reason = "recent"
	// ReasonStale means it has not been seen for a long time.
	ReasonStale Reason = "stale"
	// ReasonExcluded means the profile excludes it.
	ReasonExcluded Reason = "excluded"
	// ReasonExcludedHost means a configured host blocklist matched.
	ReasonExcludedHost Reason = "excluded_host"
	// ReasonTooManyOnDomain means the domain already has enough candidates
	// attributed to it, which is a sign of a directory rather than a company.
	ReasonTooManyOnDomain Reason = "too_many_on_domain"
)

// Factor is one scored component of a decision.
type Factor struct {
	// Reason identifies what was measured.
	Reason Reason
	// Detail is the specific observation, such as the domain or the country.
	Detail string
	// Weight is the factor's maximum contribution to the score, which is what an
	// operator tunes.
	Weight float64
	// Contribution is what the factor actually contributed, in [0,weight].
	// Recording it separately from Weight is what makes a partially satisfied
	// factor explainable rather than a pass or a fail.
	Contribution float64
	// Note explains the contribution in a sentence.
	Note string
}

// Decision is the full outcome for one candidate.
type Decision struct {
	// Candidate is the candidate as supplied.
	Candidate candidate.Candidate
	// Score is the final score in [0,1].
	Score float64
	// Verdict is what the engine should do with the candidate.
	Verdict Verdict
	// Factors are every scored component, sorted by absolute contribution so the
	// reason for the score is at the top.
	Factors []Factor
	// Explanation is a one-line human summary of the decision.
	Explanation string
	// Rank is the candidate's position in the run, assigned by Rank.
	Rank int
}

// Verdict is the decision on a candidate.
type Verdict int

const (
	// VerdictReject drops the candidate. It is still recorded with its score and
	// reasons, because a run that quietly discards companies cannot be debugged.
	VerdictReject Verdict = iota
	// VerdictReview keeps the candidate below the accept threshold but above the
	// review band, so an operator can see what is nearly good enough.
	VerdictReview
	// VerdictAccept queues the candidate for downstream work.
	VerdictAccept
)

func (v Verdict) String() string {
	switch v {
	case VerdictAccept:
		return "accept"
	case VerdictReview:
		return "review"
	default:
		return "reject"
	}
}

// Weights are the tunable factors. They sum to 1 in the default set, so the score
// is directly readable as "this fraction of the available evidence".
type Weights struct {
	// Source is the weight of the best source that saw the candidate.
	Source float64
	// Corroboration rewards agreement between independent sources, with
	// diminishing returns, because the third source saying the same thing adds
	// far less than the second.
	Corroboration float64
	// Domain is the weight of having a reachable website.
	Domain float64
	// Industry is the weight of sitting in a target vertical.
	Industry float64
	// Geography is the weight of being in a market being searched.
	Geography float64
	// Freshness decays with time since the candidate was last seen.
	Freshness float64
	// Penalty is subtracted for a platform domain, an exclusion, or a crowded
	// domain.
	Penalty float64
}

// DefaultWeights are the shipped tuning. They are a starting point with a
// documented reasoning, not a set of magic numbers.
func DefaultWeights() Weights {
	return Weights{
		Source:        0.30,
		Corroboration: 0.20,
		Domain:        0.15,
		Industry:      0.20,
		Geography:     0.10,
		Freshness:     0.05,
		// A penalty is not a weight to fill: it is subtracted, so it is not
		// part of the sum and a candidate can be driven below zero by enough
		// disqualifying signals.
		Penalty: 0.60,
	}
}

// Config configures a Ranker.
type Config struct {
	// Weights overrides the default weights field by field; a zero field takes
	// the default, so a caller can tune one factor without restating the rest.
	Weights Weights
	// AcceptThreshold is the score at or above which a candidate is accepted.
	AcceptThreshold float64
	// ReviewBand is the score at or above which a candidate is kept for review.
	ReviewBand float64
	// MaxCandidatesPerDomain bounds how many candidates one domain may
	// contribute, so a directory cannot flood the output.
	MaxCandidatesPerDomain int
	// Industries and Keywords are the target verticals, matched case-insensitively
	// against a candidate's industry and keywords.
	Industries []string
	Keywords   []string
	// Countries are the target markets. Empty means any country is acceptable.
	Countries []string
	// Exclude are terms that disqualify a candidate by name, industry or
	// keyword. This is how a competitor or a customer is kept out of the output.
	Exclude []string
	// ExcludeHosts are registrable domains to drop outright.
	ExcludeHosts []string
	// Freshness is the half-life of the freshness score. A candidate seen one
	// half-life ago scores half the freshness weight.
	Freshness time.Duration
	// Now is the clock, injectable so freshness is testable without sleeping.
	Now func() time.Time
}

// Ranker scores candidates.
type Ranker struct {
	cfg Config
}

// New returns a Ranker.
func New(cfg Config) *Ranker {
	d := DefaultWeights()
	w := cfg.Weights
	if w.Source <= 0 {
		w.Source = d.Source
	}
	if w.Corroboration <= 0 {
		w.Corroboration = d.Corroboration
	}
	if w.Domain <= 0 {
		w.Domain = d.Domain
	}
	if w.Industry <= 0 {
		w.Industry = d.Industry
	}
	if w.Geography <= 0 {
		w.Geography = d.Geography
	}
	if w.Freshness <= 0 {
		w.Freshness = d.Freshness
	}
	if w.Penalty <= 0 {
		w.Penalty = d.Penalty
	}
	if cfg.AcceptThreshold <= 0 {
		cfg.AcceptThreshold = 0.55
	}
	if cfg.ReviewBand <= 0 {
		cfg.ReviewBand = 0.35
	}
	if cfg.MaxCandidatesPerDomain <= 0 {
		cfg.MaxCandidatesPerDomain = 5
	}
	if cfg.Freshness <= 0 {
		cfg.Freshness = 90 * 24 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Ranker{cfg: Config{
		Weights:                w,
		AcceptThreshold:        cfg.AcceptThreshold,
		ReviewBand:             cfg.ReviewBand,
		MaxCandidatesPerDomain: cfg.MaxCandidatesPerDomain,
		Industries:             cleanTerms(cfg.Industries),
		Keywords:               cleanTerms(cfg.Keywords),
		Countries:              normalizeCountries(cfg.Countries),
		Exclude:                cleanTerms(cfg.Exclude),
		ExcludeHosts:           normalizeHosts(cfg.ExcludeHosts),
		Freshness:              cfg.Freshness,
		Now:                    cfg.Now,
	}}
}

// Config returns the effective configuration.
func (r *Ranker) Config() Config { return r.cfg }

// Score evaluates one candidate and returns the decision. It does not consider
// what other candidates exist, so a score depends only on the candidate itself
// and the run's configuration. Rank applies the cross-candidate rules.
func (r *Ranker) Score(c candidate.Candidate) Decision {
	d := Decision{Candidate: c}
	var factors []Factor
	add := func(reason Reason, detail string, weight, contribution float64, note string) {
		factors = append(factors, Factor{
			Reason:       reason,
			Detail:       detail,
			Weight:       weight,
			Contribution: contribution,
			Note:         note,
		})
	}

	w := r.cfg.Weights

	// Source strength. The best source wins: a candidate seen by a seed list and
	// a search snippet is as trustworthy as the seed list, and averaging would
	// wrongly pull it down.
	best := bestSource(c)
	sourceScore := 0.0
	if best != "" {
		sourceScore = sourceWeight(best)
		add(ReasonStrongSource, string(best), w.Source, w.Source*sourceScore,
			"source "+string(best)+" is worth "+percent(sourceScore)+" of the source weight")
	} else {
		add(ReasonStrongSource, "none", w.Source, 0, "no source recorded, so the claim is unsupported")
	}

	// Corroboration with diminishing returns. Two sources agreeing is strong
	// evidence; ten sources agreeing is barely more, because ten mentions of a
	// popular company prove nothing new.
	corroboration := 0.0
	switch {
	case c.SourceCount <= 1:
		add(ReasonCorroborated, fmtInt(c.SourceCount), w.Corroboration, 0,
			"seen by one surface, so there is no agreement to weigh")
	default:
		// 1 - 0.5^(n-1): 2 sources 0.5, 3 sources 0.75, 4 sources 0.875.
		corroboration = 1 - math.Pow(0.5, float64(c.SourceCount-1))
		add(ReasonCorroborated, fmtInt(c.SourceCount), w.Corroboration, w.Corroboration*corroboration,
			fmtInt(c.SourceCount)+" independent surfaces agree, worth "+percent(corroboration)+" of the corroboration weight")
	}

	// Reachability. A company with no website cannot be crawled, contacted or
	// verified, and a name alone is not a lead.
	domainScore := 0.0
	registrable := ""
	switch d, err := domain.FromHost(c.Domain); {
	case c.Domain == "":
		add(ReasonNoDomain, "", w.Domain, 0, "no website yet, so the company cannot be crawled or verified")
	case err != nil || d.Registrable == "":
		add(ReasonHasDomain, c.Domain, w.Domain, 0,
			"the domain could not be parsed into a usable host, so it is not reachable")
	default:
		registrable = d.Registrable
		if d.IsPlatform {
			// A company "found on" medium.com is not medium.com. The candidate
			// keeps a small score so it is visible in review, but it can never
			// reach the accept threshold on this factor alone.
			domainScore = 0.1
			add(ReasonPlatformDomain, registrable, w.Domain, w.Domain*domainScore,
				registrable+" is a "+platformKind(registrable)+" surface, so the company mentioned there is not "+registrable)
		} else {
			domainScore = 1
			add(ReasonHasDomain, registrable, w.Domain, w.Domain,
				registrable+" looks like a company website")
		}
	}

	// Vertical fit.
	industryScore, industryNote := r.industryFit(c)
	if industryScore > 0 {
		add(ReasonMatchesIndustry, c.Industry, w.Industry, w.Industry*industryScore, industryNote)
	} else {
		reason := ReasonIndustryUnknown
		if c.Industry != "" {
			reason = ReasonMatchesIndustry
		}
		add(reason, c.Industry, w.Industry, 0, industryNote)
	}

	// Geography.
	geoScore, geoReason, geoDetail, geoNote := r.geography(c)
	if geoScore > 0 {
		add(geoReason, geoDetail, w.Geography, w.Geography*geoScore, geoNote)
	} else {
		add(geoReason, geoDetail, w.Geography, 0, geoNote)
	}

	// Freshness.
	freshnessScore, freshnessNote := r.freshness(c)
	add(ReasonRecent, c.LastSeen.Format(time.RFC3339), w.Freshness, w.Freshness*freshnessScore, freshnessNote)
	if freshnessScore <= 0 {
		factors = append(factors, Factor{Reason: ReasonStale, Detail: c.LastSeen.Format(time.RFC3339), Note: freshnessNote})
	}

	// Exclusions and penalties.
	if hit, term := matchesAny(c, r.cfg.Exclude); hit {
		add(ReasonExcluded, term, w.Penalty, -w.Penalty, "excluded term "+term+" matches this candidate")
	}
	if registrable != "" {
		for _, blocked := range r.cfg.ExcludeHosts {
			if registrable == blocked {
				add(ReasonExcludedHost, blocked, w.Penalty, -w.Penalty, "host "+blocked+" is on the blocklist")
				break
			}
		}
	}

	// IDN is a small positive: an internationalised domain is a company serving a
	// market other than where it is registered.
	if d, err := domain.FromHost(c.Domain); err == nil && d.IsIDN {
		add(ReasonIDNDomain, registrable, 0, 0,
			registrable+" is an internationalised domain, which is a weak signal of cross-border activity")
	}

	score := sourceScore*w.Source + corroboration*w.Corroboration +
		domainScore*w.Domain + industryScore*w.Industry +
		geoScore*w.Geography + freshnessScore*w.Freshness

	for _, f := range factors {
		if f.Contribution < 0 {
			score += f.Contribution
		}
	}

	// A company with nowhere to go cannot be accepted no matter how well
	// attested it is. This is a gate rather than a weight because no amount of
	// corroboration makes an unreachable company a lead.
	if c.Domain == "" {
		score = math.Min(score, r.cfg.AcceptThreshold-0.01)
	}

	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	d.Score = round(score)
	d.Factors = sortFactors(factors)
	d.Verdict = r.verdict(d.Score, c)
	d.Explanation = explain(d)
	return d
}

// industryFit scores how well a candidate's stated industry or keywords match the
// target verticals.
func (r *Ranker) industryFit(c candidate.Candidate) (float64, string) {
	if len(r.cfg.Industries) == 0 && len(r.cfg.Keywords) == 0 {
		// No verticals configured means the operator has not expressed a
		// preference, so this factor must not silently penalise everything.
		return 0.5, "no target industries configured, so this factor is neutral"
	}
	if c.Industry == "" && len(c.Keywords) == 0 {
		return 0, "no source stated an industry or keyword, so the fit is unknown"
	}
	if c.Industry != "" {
		if score, term, ok := bestTermMatch(c.Industry, r.cfg.Industries); ok {
			return score, "industry " + c.Industry + " matches target vertical " + term
		}
	}
	for _, kw := range c.Keywords {
		if score, term, ok := bestTermMatch(kw, r.cfg.Industries); ok {
			// A keyword match is weaker than a stated industry, because a
			// keyword is something a source happened to mention.
			return 0.8 * score, "keyword " + kw + " matches target vertical " + term
		}
	}
	for _, kw := range c.Keywords {
		if score, term, ok := bestTermMatch(kw, r.cfg.Keywords); ok {
			return 0.5 * score, "keyword " + kw + " matches target keyword " + term
		}
	}
	return 0, "industry " + quoteOrNone(c.Industry) + " is outside the target verticals"
}

// geography scores whether a candidate is in a market being searched. A country
// implied by a ccTLD is treated as a hint at half weight, never as a fact: a
// .de domain says the domain is registered in Germany, not that the company is
// there.
func (r *Ranker) geography(c candidate.Candidate) (float64, Reason, string, string) {
	if len(r.cfg.Countries) == 0 {
		return 0.5, ReasonCountryUnknown, c.Country, "no target countries configured, so this factor is neutral"
	}
	if c.Country != "" {
		for _, want := range r.cfg.Countries {
			if c.Country == want {
				return 1, ReasonInTargetCountry, c.Country, "stated country " + c.Country + " is a target market"
			}
		}
		return 0, ReasonCountryMismatch, c.Country, "stated country " + c.Country + " is not a target market"
	}
	if d, err := domain.FromHost(c.Domain); err == nil && d.Country != "" {
		for _, want := range r.cfg.Countries {
			if d.Country == want {
				return 0.5, ReasonInTargetCountry, d.Country,
					"domain suffix hints at " + d.Country + ", which is a target market, at half weight because a ccTLD identifies a domain rather than a headquarters"
			}
		}
		return 0, ReasonCountryMismatch, d.Country,
			"domain suffix hints at " + d.Country + ", which is not a target market"
	}
	return 0, ReasonCountryUnknown, "", "no country stated and the domain suffix identifies none"
}

// freshness decays with the age of the last observation. A company that was
// mentioned two years ago and not since has probably closed or changed hands,
// and paying a provider to rediscover it is wasted money.
func (r *Ranker) freshness(c candidate.Candidate) (float64, string) {
	seen := c.LastSeen
	if seen.IsZero() {
		seen = c.FirstSeen
	}
	if seen.IsZero() {
		return 0.5, "no observation time recorded, so freshness is treated as unknown"
	}
	age := r.cfg.Now().Sub(seen)
	if age < 0 {
		// A timestamp in the future means a provider's clock is wrong, not that
		// the candidate is infinitely fresh.
		return 1, "observation time is in the future, likely a provider clock skew, so freshness is not penalised"
	}
	half := r.cfg.Freshness
	if half <= 0 {
		return 1, "freshness disabled"
	}
	score := math.Pow(0.5, age.Seconds()/half.Seconds())
	return score, "last seen " + age.Round(24*time.Hour).String() + " ago, " + percent(score) + " of the freshness weight"
}

func (r *Ranker) verdict(score float64, c candidate.Candidate) Verdict {
	if c.Domain == "" {
		// A name with no website is never accepted: there is nothing for the
		// crawler or the outreach stage to act on. It is kept for review so the
		// expansion stage can still work on it.
		return VerdictReview
	}
	switch {
	case score >= r.cfg.AcceptThreshold:
		return VerdictAccept
	case score >= r.cfg.ReviewBand:
		return VerdictReview
	default:
		return VerdictReject
	}
}

// Rank scores every candidate, applies the cross-candidate rules, and returns
// the decisions in rank order.
//
// The two cross-candidate rules are what stop one pathological source from
// dominating a run: a cap on how many candidates one domain may contribute, and
// identity clustering so the same company is not returned five times.
func (r *Ranker) Rank(candidates []candidate.Candidate) []Decision {
	decisions := make([]Decision, 0, len(candidates))
	for _, c := range candidates {
		decisions = append(decisions, r.Score(c))
	}

	decisions = r.capPerDomain(decisions)
	decisions = r.dedupe(decisions)
	sort.SliceStable(decisions, func(i, j int) bool {
		if decisions[i].Score != decisions[j].Score {
			return decisions[i].Score > decisions[j].Score
		}
		// A deterministic tiebreak: the same input always produces the same
		// order, which a resumed run depends on.
		return tiebreakKey(decisions[i].Candidate) < tiebreakKey(decisions[j].Candidate)
	})
	for i := range decisions {
		decisions[i].Rank = i + 1
	}
	return decisions
}

// capPerDomain rejects the lowest-scoring candidates once a domain has
// contributed its maximum. A directory listing that returns forty companies on
// one domain is a symptom of a bad provider response, not forty leads.
func (r *Ranker) capPerDomain(in []Decision) []Decision {
	byDomain := map[string]int{}
	out := make([]Decision, 0, len(in))
	for _, d := range in {
		if d.Candidate.Domain == "" {
			out = append(out, d)
			continue
		}
		reg := registrableOf(d.Candidate.Domain)
		if reg == "" {
			out = append(out, d)
			continue
		}
		if byDomain[reg] >= r.cfg.MaxCandidatesPerDomain {
			d.Verdict = VerdictReject
			d.Factors = append(d.Factors, Factor{
				Reason:       ReasonTooManyOnDomain,
				Detail:       reg,
				Weight:       r.cfg.Weights.Penalty,
				Contribution: -r.cfg.Weights.Penalty,
				Note: "domain " + reg + " already contributed " +
					strconv.Itoa(r.cfg.MaxCandidatesPerDomain) + " candidates, which is the per-domain cap",
			})
			d.Score = round(math.Max(0, d.Score-r.cfg.Weights.Penalty))
			d.Explanation = explain(d)
		}
		byDomain[reg]++
		out = append(out, d)
	}
	return out
}

// dedupe collapses candidates that identity says are the same company, keeping
// the highest scoring one and marking the rest as duplicates.
func (r *Ranker) dedupe(in []Decision) []Decision {
	comparator := identity.NewComparator(identity.DefaultThresholds())
	// Best first, so the survivor of a duplicate group is the strongest record.
	ordered := make([]Decision, len(in))
	copy(ordered, in)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Score > ordered[j].Score
	})

	var out []Decision
	kept := make([]candidate.Candidate, 0, len(ordered))
	for _, d := range ordered {
		isDuplicate := false
		for _, k := range kept {
			if comparator.Compare(k, d.Candidate).Same() {
				isDuplicate = true
				d.Verdict = VerdictReject
				d.Factors = append(d.Factors, Factor{
					Reason:       ReasonCorroborated,
					Detail:       d.Candidate.Domain,
					Note:         "the same company as " + k.Name + " or " + k.Domain + ", which scored higher",
					Weight:       0,
					Contribution: 0,
				})
				d.Explanation = explain(d)
				break
			}
		}
		if isDuplicate {
			continue
		}
		kept = append(kept, d.Candidate)
		out = append(out, d)
	}
	return out
}

// sourceWeight is the trust a surface carries. These are the whole point of
// keeping discovery's confidence in the provider rather than in the engine: a
// certificate log proves a domain exists, and an operator's seed list is a
// statement of intent, and no amount of corroboration makes them equivalent.
func sourceWeight(s candidate.Source) float64 {
	switch s {
	case candidate.SourceSeed:
		return 1.0
	case candidate.SourceRegistry:
		return 0.9
	case candidate.SourceDirectory:
		return 0.7
	case candidate.SourceSearch:
		return 0.6
	case candidate.SourceNews:
		return 0.5
	case candidate.SourceCertificate:
		return 0.4
	case candidate.SourceDeveloper:
		return 0.4
	case candidate.SourceSitemap:
		return 0.3
	case candidate.SourceExpansion:
		return 0.3
	default:
		return 0.2
	}
}

// bestSource returns the most trusted source that saw the candidate. The best
// one wins outright rather than being averaged with the rest, because a
// candidate seen by both a seed list and a search snippet is exactly as
// trustworthy as the seed list, and averaging would wrongly pull it down.
func bestSource(c candidate.Candidate) candidate.Source {
	best := candidate.Source("")
	bestWeight := -1.0
	for _, e := range c.Evidence {
		if weight := sourceWeight(e.Source); weight > bestWeight {
			best, bestWeight = e.Source, weight
		}
	}
	if bestWeight < 0 {
		return ""
	}
	return best
}

func platformKind(registrable string) string {
	if kind, ok := domain.IsPlatformDomain(registrable); ok {
		return kind
	}
	return "user-content"
}

// Registrable is exposed because a caller filtering results needs the same key
// the scorer used, and two implementations of that key would drift.
func Registrable(d string) string { return registrableOf(d) }

func registrableOf(d string) string {
	if strings.TrimSpace(d) == "" {
		return ""
	}
	parsed, err := domain.FromHost(d)
	if err != nil {
		return ""
	}
	return parsed.Registrable
}

// sortFactors puts the most consequential factors first so the explanation
// starts with what actually mattered.
func sortFactors(in []Factor) []Factor {
	out := make([]Factor, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := abs(out[i].Contribution), abs(out[j].Contribution)
		if ai != aj {
			return ai > aj
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// explain renders the decision for a human, naming the verdict and the single
// most relevant factor. A number with no sentence attached cannot be acted on.
//
// For an accepted candidate the strongest positive factor is what justifies it.
// For one that was not accepted, the operator's question is "what stopped this",
// so the disqualifying factor is named instead: a platform domain, a missing
// website, a market nobody asked for, an exclusion, or a penalty. Naming the
// largest positive contributor on a rejected candidate would point the operator
// at the one thing that was working.
func explain(d Decision) string {
	chosen, found := decisiveFactor(d)
	name := d.Candidate.Name
	if name == "" {
		name = d.Candidate.Domain
	}
	if name == "" {
		name = "unnamed candidate"
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteString(": score ")
	b.WriteString(formatScore(d.Score))
	b.WriteString(" (")
	b.WriteString(d.Verdict.String())
	b.WriteString(")")
	if found && chosen.Note != "" {
		b.WriteString(" because ")
		b.WriteString(chosen.Note)
	}
	return b.String()
}

// decisiveFactor picks the factor an operator most needs to see.
func decisiveFactor(d Decision) (Factor, bool) {
	var bestDisqualifier, bestPositive Factor
	foundDisqualifier, foundPositive := false, false
	for _, f := range d.Factors {
		if disqualifies(f.Reason) {
			if !foundDisqualifier || abs(f.Contribution) > abs(bestDisqualifier.Contribution) {
				bestDisqualifier, foundDisqualifier = f, true
			}
			continue
		}
		if f.Contribution > 0 && (!foundPositive || f.Contribution > bestPositive.Contribution) {
			bestPositive, foundPositive = f, true
		}
	}
	if d.Verdict == VerdictAccept {
		return bestPositive, foundPositive
	}
	if foundDisqualifier {
		return bestDisqualifier, true
	}
	return bestPositive, foundPositive
}

// disqualifies reports whether a reason is grounds for not accepting a candidate.
func disqualifies(r Reason) bool {
	switch r {
	case ReasonNoDomain, ReasonPlatformDomain, ReasonCountryMismatch,
		ReasonExcluded, ReasonExcludedHost, ReasonTooManyOnDomain,
		ReasonIndustryUnknown:
		return true
	default:
		return false
	}
}

// bestTermMatch returns the best partial-match score between a candidate term and
// a configured term, in [0,1]. A substring match is the common case — a
// directory saying "Industrial Robotics & Automation" should match a target of
// "industrial robotics" — so it is allowed but never scores full marks.
func bestTermMatch(term string, targets []string) (float64, string, bool) {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return 0, "", false
	}
	best, bestScore := "", 0.0
	for _, t := range targets {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		score := 0.0
		switch {
		case t == term:
			score = 1
		case strings.Contains(term, t):
			score = 0.85
		case strings.Contains(t, term):
			score = 0.75
		}
		if score > bestScore {
			best, bestScore = t, score
		}
	}
	if bestScore <= 0 {
		return 0, "", false
	}
	return bestScore, best, true
}

func matchesAny(c candidate.Candidate, terms []string) (bool, string) {
	haystacks := []string{c.Name, c.Industry}
	haystacks = append(haystacks, c.Keywords...)
	for _, t := range terms {
		lt := strings.ToLower(t)
		for _, h := range haystacks {
			if h == "" {
				continue
			}
			if strings.Contains(strings.ToLower(h), lt) {
				return true, t
			}
		}
	}
	return false, ""
}

func cleanTerms(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// normalizeCountries upper-cases and de-duplicates ISO 3166-1 alpha-2 codes.
// A length check is not enough: "X1" is two characters and is not a country, so
// a mistyped code would silently exclude every candidate in that market.
func normalizeCountries(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToUpper(strings.TrimSpace(s))
		if len(s) != 2 || !isASCIILetters(s) || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func isASCIILetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

func normalizeHosts(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		reg := registrableOf(s)
		if reg == "" || seen[reg] {
			continue
		}
		seen[reg] = true
		out = append(out, reg)
	}
	return out
}

func tiebreakKey(c candidate.Candidate) string {
	if c.Domain != "" {
		return "0" + registrableOf(c.Domain) + "|" + strings.ToLower(c.Name)
	}
	return "1" + strings.ToLower(c.Name)
}

func round(f float64) float64 { return math.Round(f*10000) / 10000 }

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// percent renders a fraction the way the explanations read it: "82%", "0%".
func percent(f float64) string { return strconv.Itoa(int(math.Round(f*100))) + "%" }

func formatScore(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

func fmtInt(n int) string { return strconv.Itoa(n) }

func quoteOrNone(s string) string {
	if s == "" {
		return "unstated"
	}
	return s
}
