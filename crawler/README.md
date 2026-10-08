# crawler

A polite, restart-safe web crawler. It takes a set of seeds, walks the links it
finds under a budget, and stores every document it keeps in Postgres so that a
second visit to the same URL costs a conditional request instead of a download.

It exists both as a library (the pipeline in `lead-engine` uses it directly) and
as a service (`cmd/crawld`).

## What it guarantees

These are the properties the tests actually hold, not aspirations.

**robots.txt is obeyed.** Every URL is checked against the site's rules before a
request goes out. Disallowed paths are never fetched, `Crawl-delay` raises the
per-host rate, and a `404` means "no restrictions" (per RFC 9309) while any other
error fails closed — an unreachable robots.txt does not become a licence to
crawl. Rules are cached per host for the life of the process.

**A second crawl is cheap.** Stored validators (ETag, Last-Modified) are replayed
as `If-None-Match` / `If-Modified-Since`. A `304` updates the fetch timestamp
and leaves the existing copy alone. Re-crawling an unchanged site transfers no
bodies at all.

**Crawls cannot loop.** The frontier remembers every key it has *completed* for
the run, not just what is queued. Without that, `A → B → A` re-fetches both
pages forever. With it, a page is fetched at most once per run, and a repeated
link costs one cheap `304`.

**Budgets are hard limits.** Page count, total bytes, per-host page count and
wall-clock duration are all reserved atomically before a worker starts, so N
concurrent workers cannot collectively overshoot a budget of 1. Reaching a
budget is a normal outcome reported in `stopped_by`, not an error.

**A failure never aborts a run.** A dead host, a 500, a TLS error: recorded in
`errors`, counted, and the crawl continues. Only a cancelled context stops it.

**It will not become a request-forgery gadget.** Loopback, link-local, private
and other non-global addresses are refused before connecting, checked against
every address a hostname resolves to, and re-checked on each redirect hop. Set
`CRAWLER_ALLOW_PRIVATE_HOSTS=true` only for local development; the service logs a
warning when it is on.

## Layout

| File | Responsibility |
| --- | --- |
| `doc.go` | `Document`, `Link`, `Request`, outcomes, status helpers |
| `config.go` | Configuration, validation, content policy, research depths |
| `url.go` | Normalisation, dedupe keys, SSRF guards, registrable domain |
| `links.go` | HTML link and sitemap extraction |
| `fetch.go` | The polite HTTP client: robots, retries, conditional GET, limits |
| `frontier.go` | `Frontier` contract, `MemoryFrontier`, leases and backoff |
| `store.go` | `Store` contract, `MemoryStore`, typed errors |
| `crawl.go` | The worker pool, budgets and run accounting |
| `pgfrontier.go` | Durable `Frontier` with `SKIP LOCKED` leasing |
| `pgstore.go` | Durable `Store` and the embedded migration source |
| `migrations/` | `0001_crawler.sql` — documents, runs, frontier, seen keys |

Two implementations of each of `Frontier` and `Store` exist on purpose: the
in-memory ones make tests fast and hermetic, the Postgres ones make a crawl
survive a restart and be shareable between processes.

## Using it as a library

```go
cfg := crawler.DefaultConfig()          // conservative: 1 req/s per host
fetch, err := crawler.NewHTTPFetcher(cfg, crawler.Options{Store: store})
loop, err := crawler.New(fetch, frontier, store, cfg, crawler.Options{})

res, err := loop.Crawl(ctx, crawler.CrawlOptions{
    Seeds:       []string{"https://example.com"},
    Sitemaps:    []string{"https://example.com/sitemap.xml"},
    Depth:       crawler.DepthStandard,
    FollowLinks: true,
    Budget:      crawler.Budget{MaxPages: 200, MaxDuration: 5 * time.Minute},
})
fmt.Println(res.Fetched, res.NotModified, res.Stats.StoppedBy)
```

Every item placed on a durable frontier carries the run ID. A `LinkFilter` or
`OnDocument` hook lets the caller filter or observe without forking the crawler.

## Running the service

```bash
createdb leads
export DATABASE_URL='postgres://localhost/leads?sslmode=disable'
export HTTP_ADDR=127.0.0.1:8080

go run ./crawler/cmd/crawld                 # migrates, then serves
go run ./crawler/cmd/crawld -migrate-only   # migrate as a release step, then exit
```

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/v1/crawl` | Run a bounded crawl, return the run result |
| `POST` | `/v1/fetch` | Fetch one URL politely |
| `GET` | `/v1/documents/{id}` | Fetch a stored document by ID |
| `GET` | `/v1/documents?url=` | Fetch the last stored copy of a URL |
| `GET` | `/v1/hosts/{host}/robots` | The rules cached for a host (never fetches) |
| `GET` | `/v1/stats?run_id=` | Frontier depth and run statistics |
| `GET` | `/healthz` `/readyz` `/version` `/metrics` | Probes and metrics |

```bash
curl -X POST localhost:8080/v1/crawl -H 'content-type: application/json' -d '{
  "seeds": ["https://example.com"],
  "sitemaps": ["https://example.com/sitemap.xml"],
  "follow_links": true,
  "max_pages": 200,
  "max_duration": "5m"
}'
```

A few deliberate choices in that API:

- A budget stopping the crawl is `200`, not an error. A client that retried would
  redo work that already succeeded.
- `POST /v1/fetch` returns `200` even when the target returned 404. The caller
  asked what is at that URL and got a truthful answer; the outcome, status and
  reason say what it was.
- A failed fetch always carries a `reason`. A failure with no explanation is not
  an answer.
- `GET /v1/documents` does not return bodies. A document can be megabytes, and a
  caller that wants text belongs in the extractor.
- `/v1/hosts/{host}/robots` only reports *cached* rules. It must not become a way
  to make the crawler fetch a robots.txt for an arbitrary host on demand.

### Configuration

`DATABASE_URL` is required. `HTTP_ADDR`, `LOG_LEVEL` and `LOG_FORMAT` work both
unprefixed and with the `UPVISTA_` prefix; the prefixed form wins if both are
set. Crawler tuning uses `CRAWLER_*` — see `.env.example` at the repository root
for the full list and the reasoning behind the conservative defaults.

## Tests

```bash
export TEST_DATABASE_URL='postgres://localhost/leads?sslmode=disable'
go test -race ./...
```

The Postgres-backed tests are skipped, not failed, when no database is
configured, so the pure-Go suite runs anywhere. `testutil.Postgres` gives each
test its own schema, so tests never collide or need cleanup ordering.

Coverage is 71% of statements, with the untested remainder mostly being
Postgres error paths and the backoff timing that is already covered by injected
clocks elsewhere.

## Known limits

- **Crawl depth is hop count, not page count.** A wide site can hit `MaxPages`
  before reaching a page three links in. `DepthDeep` exists for that case.
- **The durable frontier is a queue, not a scheduler.** A crawl that is
  interrupted mid-run leaves its leased items visible again after the lease
  expires, so restarting `Crawl` with the same run ID resumes rather than
  restarting. The API does not expose resume yet.
- **`/v1/crawl` runs the crawl inside the request.** That is right for a bounded
  run and wrong for a large one; the request context cancels the crawl if the
  client disconnects, which is the safe failure but does mean a long crawl needs
  a client that stays connected.
