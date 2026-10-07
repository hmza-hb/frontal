# Upvista — Lead Intelligence Platform

An API-first system that turns an Ideal Customer Profile into a ranked list of
50–100 real companies, each backed by cited evidence, and a per-company dossier
of research grounded in what was actually found.

The defining constraint is **evidence, not inference**. Every claim carries the
URL it came from and the snippet that supports it. A company that cannot be
justified from a source does not appear in the output; it appears in the
rejected list with a reason. Nothing is invented, and no LLM is required for the
pipeline to work end to end.

The system is built as a set of **independent products**, not one application.
Every top-level directory is its own Go module with its own schema, tests and
public API, and can be sold on its own. See [ARCHITECTURE.md](ARCHITECTURE.md).

---

## Status

Honest state of the build. This table is the thing to read first.

| Module | What it does | State |
| --- | --- | --- |
| **platform** | config, Postgres, migrations, logging, metrics, HTTP plumbing, retry, rate limiting, circuit breakers | **Complete** — 80% test coverage |
| **crawler** | polite, restart-safe web crawler with durable storage | **Complete** — 71% coverage, ships as `crawld` |
| crawler/robotstxt | RFC 9309 parser: groups, wildcards, crawl-delay, sitemaps | **Complete** |
| discovery | seed expansion, ICP-driven candidate generation | Not started |
| extractor | text, contacts, entities, structured data from documents | Not started |
| research | per-company research briefs with citations | Not started |
| enrichment | firmographics, technographics, funding, hiring signals | Not started |
| entity-resolution | dedupe and merge companies across sources | Not started |
| knowledge-graph | companies, people and their relationships | Not started |
| qualification | ICP scoring, fit reasons, disqualifiers | Not started |
| personalization | per-recipient messaging and angle selection | Not started |
| lead-engine | composition root, REST API, pipeline runner | Not started |

The two finished modules are finished in the strong sense: their documented
guarantees are held by tests, and `crawler` has been verified against a real
Postgres, a real HTTP server and the real `crawld` binary. See
[crawler/README.md](crawler/README.md).

The pipeline is designed end to end and specified in full in
[ARCHITECTURE.md](ARCHITECTURE.md), but not yet implemented. No `make run` will
produce 50 leads today.

---

## Quick start

Requires Go 1.24+ and Postgres 16+.

```bash
make up                       # Postgres via docker compose
cp .env.example .env          # then edit DATABASE_URL

make verify                   # gofmt check, go vet, tests, per module
make build                    # -> ./bin/crawld
```

Run the crawler on its own:

```bash
export DATABASE_URL='postgres://localhost:5432/leads?sslmode=disable'
export CRAWLER_ALLOW_PRIVATE_HOSTS=true   # only for local targets

./bin/crawld -migrate-only                # apply the schema
./bin/crawld                              # serve on $HTTP_ADDR

curl -X POST localhost:8080/v1/crawl -H 'content-type: application/json' -d '{
  "seeds": ["https://example.com"],
  "follow_links": true,
  "max_pages": 50
}'
```

End to end, the smoke script builds the binary, starts a target site, crawls it,
and asserts on the results — the fastest way to see the whole thing work:

```bash
make crawl-smoke
```

---

## The pipeline

Once the remaining modules land, one `lead-engine run` executes:

```
  ICP ──▶ discovery ──▶ crawler ──▶ extractor ──▶ entity-resolution
                                                        │
                          qualification ◀── enrichment ─┤
                                 │                      │
                            personalization ◀── research
                                 │
                            knowledge-graph
                                                        │
                                                  50–100 leads
```

Each stage reads and writes Postgres, so any stage can be re-run in isolation
against stored inputs. Nothing is passed in memory between processes, which is
what makes a partial re-run cheap instead of a full pipeline restart.

The full contracts — the types crossing each boundary, the evidence model, the
quality bar for a lead — are in [ARCHITECTURE.md](ARCHITECTURE.md).

---

## Ground rules

These are constraints the code is written to enforce, not preferences.

- **No authentication, CAPTCHA or rate-limit bypass.** No search-engine
  result-page scraping. Every document comes from a URL the site itself serves.
- **robots.txt is obeyed.** Including failing closed when it is unreachable.
- **Every output claim is cited.** A claim without a source URL does not ship.
- **No LLM required.** Provider keys are optional. With none configured, the
  pipeline runs on public web data and deterministic heuristics, and says so.
- **The crawler is identifiable.** A user agent with a real contact address is
  the price of being crawled politely.
- **Private networks are off by default.** A crawler that can reach internal
  addresses is a request-forgery gadget.

---

## Layout

```
platform/     config, db, migrations, logging, metrics, http, retry, ratelimit
crawler/      the web crawler; also the crawld service in cmd/crawld
discovery/    candidate generation from an ICP
extractor/    documents -> text, contacts, entities, structured data
research/     per-company research with citations
enrichment/   firmographics and signals
entity-resolution/  dedupe and merge
knowledge-graph/    companies, people, relationships
qualification/      ICP scoring and reasons
personalization/    messaging per recipient
lead-engine/  composition root, REST API, CLI
go.work       developer convenience only; no module depends on it
```

`go.work` exists for local development. Every `go.mod` resolves its siblings by
version, so any module can be moved to its own repository and still build.

---

## Development

```bash
make help            # list every target
make test            # tests, per module
make test-race       # tests with the race detector
make cover           # coverage per module
make fmt-check       # fail on unformatted code
make verify          # fmt-check + vet + test
```

Postgres-backed tests skip rather than fail when no `TEST_DATABASE_URL` or
`DATABASE_URL` is set, so the pure-Go suite runs anywhere. Each test that needs
a database gets its own schema, so tests never collide and never depend on
ordering.
