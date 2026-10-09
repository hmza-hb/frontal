# Architecture

The Lead Intelligence Platform is a **composition of independent engineering
products**, not one application. Each top-level directory is a separate Go
module with its own `go.mod`, its own database migrations, its own tests, its
own README, and a public API small enough that it can be lifted into its own
repository and sold on its own.

Nothing in this document is aspirational: every contract listed here is
implemented, and every module's tests exercise its own contract without
reaching into its neighbours' internals.

---

## 1. Why modules and not packages

A single `internal/` tree is faster to write and impossible to sell. The cost
we accept instead is discipline:

* **Producer owns the type.** A data structure is declared by the module that
  *creates* it and consumed by everyone else. `crawler.Document` is crawler's.
  Nobody redefines it. There is no `common` or `shared` package — the moment one
  appears, coupling has started.
* **Consumer owns the interface.** Go's idiom, applied strictly. If
  `discovery` needs to fetch a sitemap, `discovery` declares
  `type Fetcher interface { FetchPage(ctx, url) (crawler.Document, error) }`.
  It does not import crawler's service wiring, only the contract.
* **No hidden coupling.** The only module every other module may import is
  `platform`, and it imports nothing back. Module-to-module imports are
  declared in the table below and are acyclic by construction.
* **No workspace-specific `replace`.** `go.work` exists for developer
  convenience only. Every `go.mod` resolves its siblings by version, so
  `git mv crawler ../crawler-repo && go build ./...` works.

---

## 2. Module dependency graph

```
                       ┌──────────────┐
                       │   platform   │  config, db, migrations, logging,
                       │  (kernel)    │  metrics, http plumbing, retry,
                       └──┬────────┬──┘  rate limiting, circuit breakers
             ┌────────────┘        └────────────┐
             │                                  │
      ┌──────┴──────┐  ┌──────────┐  ┌──────────┴───────┐
      │   crawler   │  │   ...    │  │ entity-resolution│
      └──────┬──────┘  └────┬─────┘  └──────────────────┘
             │              │
      ┌──────┴──────┐       ├───────────────┐
      │  discovery  │       │ enrichment    │
      └──────┬──────┘       └───────┬───────┘
             │                      │
      ┌──────┴──────┐       ┌───────┴───────┐
      │  extractor  │       │    research   │
      └──────┬──────┘       └───────┬───────┘
             │                      │
             │              ┌───────┴────────┐
             │              │knowledge-graph │
             │              └───────┬────────┘
             └──────────┬───────────┘
                        │
              ┌─────────┴──────────┐
              │   qualification    │
              └─────────┬──────────┘
                        │
              ┌─────────┴──────────┐
              │  personalization  │
              └─────────┬──────────┘
                        │
              ┌─────────┴──────────┐
              │    lead-engine     │  composition root: pipeline, memory,
              └────────────────────┘  CLI, REST API
```

Legal imports, exhaustively:

| Module | May import |
| --- | --- |
| `platform` | *nothing from this repo* |
| `crawler` | `platform` |
| `entity-resolution` | `platform` |
| `knowledge-graph` | `platform` |
| `enrichment` | `platform` |
| `discovery` | `platform`, `crawler` |
| `extractor` | `platform`, `crawler` |
| `research` | `platform`, `extractor`, `enrichment`, `knowledge-graph` |
| `qualification` | `platform`, `extractor`, `research`, `knowledge-graph` |
| `personalization` | `platform`, `research`, `qualification` |
| `lead-engine` | all of the above |

`go vet ./...` and a CI grep over imports keep this honest.

---

## 3. Module catalogue

Every module answers the same four questions: what it owns, what it refuses to
own, what its public API is, and what it would need to stand alone.

### `platform` — shared kernel

**Owns:** configuration, connection pooling, the migration engine, structured
logging, metrics, HTTP server plumbing (health, readiness, JSON errors,
timeouts, panic recovery, request IDs), retry with jitter, per-key token-bucket
rate limiting, circuit breakers, content hashing, and test helpers.

**Refuses:** knowing what a lead is.

**Standalone value:** an opinionated Go service kernel. Publishing it removes
~2k lines from every future product.

### `crawler` — a crawling API

**Owns:** `Document` (the canonical fetched-page type), the frontier queue,
robots.txt compliance (RFC 9309), per-host politeness, conditional GET,
content-type and size policy, link and sitemap discovery, incremental
re-crawl decisions, fetch budget accounting, and a document store with raw-body
TTL retention.

**Public API:** `crawler.New(Config, ...Option) *Crawler`, `Crawler.Crawl(ctx,
Spec) (Result, error)`, `Fetcher`, `Frontier`, `Store`. Plus `cmd/crawld`, a
standalone HTTP service (`POST /v1/crawl`, `GET /v1/documents/{id}`,
`GET /v1/hosts/{host}/robots`).

**Standalone value:** a polite, resumable, horizontally-scalable crawler
service. This is the product that becomes "Crawld".

### `discovery` — candidate generation

**Owns:** `Candidate` (a seed URL plus why it might matter), the `Source`
interface, and a provider registry with budgets and health tracking.

**Sources shipped:** public web seeds, sitemap expansion via the crawler,
certificate transparency (`crt.sh`), RDAP/registration data, CSV/JSON seed
files, and HTTP search-provider adapters that stay inert without an API key.
No source can bypass authentication, CAPTCHAs, rate limits, or access
controls; a source that needs credentials is a source you were given
credentials for.

**Refuses:** deciding whether a candidate is good. It proposes; qualification
disposes.

### `extractor` — structured facts with provenance

**Owns:** `CompanyFacts`, `PersonFacts`, `Provenance`, and the extractors that
produce them from `crawler.Document`: JSON-LD, microdata, OpenGraph, semantic
`<title>`/`<h1>`/`<meta>`, mail/phone/social discovery, tech-stack hints from
public asset URLs, and boilerplate-stripped main text for downstream AI.

**Rule:** no extracted fact exists without provenance — source URL, selector or
JSON path, extraction time, content hash, and a confidence. A fact that cannot
be traced to bytes is not extracted, it is invented.

### `entity-resolution` — "are these the same thing?"

**Owns:** blocking strategies (domain, exact key, name, email), the similarity
model, and cluster assignment with an auditable decision (`auto-merge`,
`review`, `reject`) plus the signals that produced it.

**Standalone value:** a general record-linkage engine with an evidence trail.

### `knowledge-graph` — the evidence-backed graph

**Owns:** `Node`, `Edge`, typed edge vocabulary, run-scoped subgraphs,
neighbourhood traversal, and export to JSON, JSON-LD, and DOT.

**Rule:** every edge carries the evidence IDs that justify it. A graph that
cannot answer "why is this edge here?" is not this module's output.

### `enrichment` — provider fan-out

**Owns:** the `Provider` interface, the budgeted fan-out orchestrator, per
provider circuit breakers, TTL caching keyed on content hash, and provenance
for provider-returned facts.

**Providers shipped:** RDAP/domain registration, DNS (MX, TXT, SPF), certificate
transparency, and a null provider. Commercial vendors plug in behind `Provider`
without touching callers.

### `research` — from facts to findings

**Owns:** the research depth ladder (`screen` → `standard` → `deep`), the cost
and time budget, and `Finding` — a classified, evidence-backed statement such
as a pain point, a buying signal, a tech-stack fact, or a hiring signal.

**Standalone value:** bounded, evidence-grounded research that hands an LLM
structured context instead of a pile of HTML.

### `qualification` — ICP in, ranked explainable leads out

**Owns:** `ICP` (the normalised form of "what do we sell and to whom"),
`Component` scoring with weights and rationales, hard disqualification gates,
and the ranker that blends fit score with novelty and signal freshness.

**Rule:** every score decomposes into components, and every component cites
evidence. "Why was this lead selected?" is a function call, not an essay.

### `personalization` — evidence-gated messaging

**Owns:** `Brief` (structured research for one lead), the `Composer`
interface, the deterministic template composer, and guardrails that reject any
drafted claim not backed by an evidence ID.

**Standalone value:** compliant, auditable outreach generation. The LLM
composer is an alternative implementation of the same interface.

### `lead-engine` — composition root

**Owns:** the pipeline and its checkpoints, the `Lead` artefact (the thing the
customer actually pays for), the run ledger, and the memory that decides
*new / changed / unchanged / already contacted* so repeat runs surface new
opportunities instead of the same 50 rows.

**Refuses:** reimplementing anything. It only wires the modules together and
owns cross-module concerns.

---

## 4. The data philosophy, mechanically enforced

```
Discover → Fetch → Extract → Normalize → Keep evidence → Discard bytes
```

1. **Never store the web.** Raw bodies live in `crawler_documents` with a
   `retain_until` timestamp (default 72h). After that only the hash, metadata,
   and extracted facts remain.
2. **Content hash is the primary key of everything.** Documents dedupe on
   `sha256(normalised body)`. Leads fingerprint on
   `hash(company facts ‖ person facts ‖ evidence set)`. If the fingerprint is
   unchanged, nothing downstream re-runs.
3. **Cheap filters first, deep research last.** `screen` costs one fetch.
   `deep` costs N fetches plus provider spend, and is only granted to
   candidates that already passed the score threshold.
4. **Budgets are enforced in code, not in documentation.** Page counts, bytes,
   wall-clock, provider calls, and LLM tokens are all counters that the
   pipeline refuses to exceed.
5. **Every write is scoped to a run.** Retention drops all but the newest N
   runs' raw data while keeping the durable entity graph.

---

## 5. Evidence model

One shape is used end to end, declared by `extractor` and reused by everyone:

```go
type Provenance struct {
    SourceURL   string    // where the bytes came from
    Selector    string    // CSS selector or JSON path within the page
    Extractor   string    // which rule produced it
    ExtractedAt time.Time
    ContentHash string    // ties back to crawler.Document
    Confidence  float64   // rule-level prior, not a vibe
    Snippet     string    // the exact text, so a human can verify in one click
}
```

Nothing in the pipeline is allowed to make a claim that cannot be rendered as
`(claim, evidenceID)` pairs. Personalization enforces this at the last step.

---

## 6. Standalone extraction: the exact recipe

Each module is designed to leave. To publish `crawler` on its own:

```bash
git mv crawler ../crawld
cd ../crawld
sed -i 's|github.com/hmza-hb/lead-intelligence/platform|github.com/hmza-hb/platform|' go.mod
# go mod tidy, tag v0.1.0, push
```

`crawler` depends on `platform` for logging, metrics, retry, rate limiting, and
the DB pool. Extracting `platform` first (it is a leaf with no repo imports) is
therefore the first move, not the crawler.

Module paths use `github.com/hmza-hb/lead-intelligence/<module>`, which is
forward-compatible with a future repository rename. The rename is one `sed` and
is called out in each module README.

---

## 7. What is deliberately not built yet

| Deferred | Why | Where it lands |
| --- | --- | --- |
| LLM-backed researcher and email writer | needs a model budget and eval set; the deterministic path must be trustworthy first | `research/llm`, `personalization/llm` behind existing ports |
| Commercial lead-data vendors | contracts and ToS review per vendor | `enrichment.Provider` implementations |
| Authenticated crawling | needs an explicit compliance model | separate module, opt-in only |
| Multi-tenant isolation | one ICP account is enough to prove the model | `platform` migrations, tenant column |
| Distributed scheduler | `SKIP LOCKED` frontier + run checkpoints already allow N workers | `lead-engine` worker pool |

Each is a plug-in point that already exists in the code, not a redesign.

---

## 8. Non-goals and safety rails

* No bypassing authentication, CAPTCHAs, rate limits, paywalls, or robots.txt.
  The crawler honours `robots.txt` by default and has no code path to disable
  enforcement other than an explicit config flag recorded in run metadata.
* No bulk personal-data harvesting. Person facts come from pages a person
  published about themselves; the system prefers business contact routes and
  records provenance for every contact detail.
* No scraping of search engine result pages. Search is reached only through
  provider APIs the operator holds keys for.
* Personalization never invents a fact. Guardrails reject the draft instead of
  hedging.
