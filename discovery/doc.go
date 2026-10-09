// Package discovery turns an Ideal Customer Profile into a large, diverse,
// deduplicated set of real candidate companies, each carrying the provenance
// that explains why it was found.
//
// The engine answers "where could this company be found?", not "is this a good
// lead?" — qualification owns the second question. Discovery's contract is that
// every candidate can explain itself: which provider surfaced it, under which
// query, from which URL, with what confidence.
//
// # Pipeline
//
// The packages below compose in dependency order, each usable and testable on its
// own:
//
//	config      What this deployment is allowed to do, and what it costs.
//	domain      Canonical hosts, registrable domains, platform surfaces, country hints.
//	query       Deterministic, bounded query text from the profile, with language provenance.
//	providers   Bounded fan-out to real surfaces, each with its own budget and health.
//	lineage     What each query did, on which provider, and what it cost.
//	candidate   The claim: a company plus every observation supporting it.
//	identity    Whether two claims describe the same company, and why.
//	ranking     Which claims are worth a crawler's time, with a factor for each point.
//	persistence Run-scoped durable storage.
//	service     Orchestration: budget, concurrency, and resumability across the above.
//
// # Boundaries this module will not cross
//
// Discovery proposes; qualification disposes. It does not decide whether a
// candidate is a good lead, extract facts from a page, or enrich a record. Those
// belong to extractor, enrichment, and qualification.
//
// Discovery does not implement a general entity graph. Node and edge modelling,
// traversal, and JSON-LD or DOT export are knowledge-graph's, and duplicating
// them here would produce two graphs that drift apart. What discovery records is
// lineage — which query reached which provider and produced what — because that
// is what coverage and resumability need.
//
// Every source is treated as hostile. A provider response is untrusted input that
// may be arbitrarily large, malformed, or wrong, and no source may bypass
// authentication, CAPTCHAs, rate limits, robots restrictions, or access
// controls. A source that needs credentials is a source you were given
// credentials for.
package discovery
